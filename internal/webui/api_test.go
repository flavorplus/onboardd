package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flavorplus/onboardd/internal/setup"
)

const (
	testOrigin        = "http://10.42.0.1"
	testAdminPassword = "correct-admin-password"
)

func TestAPIRequiresAuthenticationForEveryPrivateRoute(t *testing.T) {
	api, _, _ := newTestAPI(t)
	for _, test := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/v1/setup"},
		{method: http.MethodGet, path: "/api/v1/networks"},
		{method: http.MethodGet, path: "/api/v1/known-networks"},
		{method: http.MethodDelete, path: "/api/v1/known-networks/example"},
		{method: http.MethodPost, path: "/api/v1/known-networks/example/connect"},
		{method: http.MethodPost, path: "/api/v1/connections"},
		{method: http.MethodPost, path: "/api/v1/standalone"},
		{method: http.MethodGet, path: "/api/v1/operations/example"},
		{method: http.MethodGet, path: "/api/v1/missing"},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, testOrigin+test.path, nil)
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized ||
				!strings.Contains(response.Body.String(), `"code":"authentication_required"`) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAPILoginCreatesPrivateBrowserSession(t *testing.T) {
	api, _, _ := newTestAPI(t)
	request := jsonRequest(
		t,
		http.MethodPost,
		testOrigin+"/api/v1/session",
		`{"password":"`+testAdminPassword+`"}`,
		"",
	)
	request.Header.Set("Origin", testOrigin)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("login response = %d %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %#v", cookies)
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookieName || cookie.Value == "" || cookie.Path != "/api/v1/" ||
		!cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Secure {
		t.Fatalf("session cookie = %#v", cookie)
	}

	setupRequest := httptest.NewRequest(http.MethodGet, testOrigin+"/api/v1/setup", nil)
	setupRequest.AddCookie(cookie)
	setupResponse := httptest.NewRecorder()
	api.ServeHTTP(setupResponse, setupRequest)
	if setupResponse.Code != http.StatusOK {
		t.Fatalf("authenticated setup response = %d %s", setupResponse.Code, setupResponse.Body.String())
	}
}

func TestAPILoginRejectsInvalidPasswordAndOrigin(t *testing.T) {
	for _, test := range []struct {
		name       string
		password   string
		origin     string
		wantStatus int
	}{
		{name: "incorrect password", password: "incorrect-password", origin: testOrigin, wantStatus: http.StatusUnauthorized},
		{name: "foreign origin", password: testAdminPassword, origin: "https://attacker.example", wantStatus: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			api, _, _ := newTestAPI(t)
			request := jsonRequest(
				t,
				http.MethodPost,
				testOrigin+"/api/v1/session",
				`{"password":"`+test.password+`"}`,
				"",
			)
			request.Header.Set("Origin", test.origin)
			response := httptest.NewRecorder()
			api.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestNewAPIRejectsWeakAdminPassword(t *testing.T) {
	_, _, service := newTestAPI(t)
	_, err := NewAPI(service, testOrigin, Authentication{Password: "short"})
	if err == nil || !strings.Contains(err.Error(), "between 12 and 256") {
		t.Fatalf("error = %v", err)
	}
}

func TestAPISetupBootstrap(t *testing.T) {
	api, _, _ := newTestAPI(t)
	request := httptest.NewRequest(http.MethodGet, testOrigin+"/api/v1/setup", nil)
	response := httptest.NewRecorder()

	serveAPI(t, api, response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	var body struct {
		Capabilities setup.Capabilities `json:"capabilities"`
		CurrentMode  setup.Mode         `json:"current_mode"`
		CSRFToken    string             `json:"csrf_token"`
	}
	decodeResponse(t, response, &body)
	if !body.Capabilities.Network || !body.Capabilities.Standalone ||
		body.CurrentMode != setup.ModeSetup || len(body.CSRFToken) != 64 {
		t.Fatalf("body = %#v", body)
	}
}

func TestAPIConnectionMutationAndOperation(t *testing.T) {
	api, backend, _ := newTestAPI(t)
	token := bootstrapToken(t, api)
	request := jsonRequest(
		t,
		http.MethodPost,
		testOrigin+"/api/v1/connections",
		`{"ssid":"Office","password":"private-password","open":false,"hidden":false}`,
		token,
	)
	response := httptest.NewRecorder()

	serveAPI(t, api, response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "private-password") {
		t.Fatal("password leaked in response")
	}
	var accepted struct {
		Operation setup.Operation `json:"operation"`
	}
	decodeResponse(t, response, &accepted)
	if accepted.Operation.ID == "" || accepted.Operation.Network != "Office" {
		t.Fatalf("operation = %#v", accepted.Operation)
	}

	close(backend.release)
	waitForAPIState(t, api, accepted.Operation.ID, setup.OperationSucceeded)
	backend.mu.Lock()
	connection := backend.connection
	backend.mu.Unlock()
	if connection.Password != "private-password" {
		t.Fatalf("backend connection = %#v", connection)
	}
}

func TestAPIDoesNotBeginOperationWhenAcceptedResponseCannotFlush(t *testing.T) {
	api, backend, service := newTestAPI(t)
	request := jsonRequest(
		t,
		http.MethodPost,
		testOrigin+"/api/v1/connections",
		`{"ssid":"Office","password":"private-password"}`,
		bootstrapToken(t, api),
	)
	response := &flushErrorResponseWriter{
		ResponseRecorder: httptest.NewRecorder(),
		err:              errors.New("client disconnected"),
	}

	serveAPI(t, api, response, request)

	backend.mu.Lock()
	started := backend.started
	backend.mu.Unlock()
	if started {
		t.Fatal("backend started after accepted response flush failed")
	}
	var accepted struct {
		Operation setup.Operation `json:"operation"`
	}
	decodeResponse(t, response.ResponseRecorder, &accepted)
	operation, ok := service.Operation(accepted.Operation.ID)
	if !ok || operation.State != setup.OperationFailed || operation.Failure == nil ||
		operation.Failure.Code != "operation_interrupted" {
		t.Fatalf("operation = %#v, exists = %t", operation, ok)
	}
	if _, err := service.StartConnection(setup.ConnectionRequest{SSID: "Other"}); err != nil {
		t.Fatalf("next StartConnection() error = %v", err)
	}
}

func TestAPIDoesNotBeginOperationWhenAcceptedResponseCannotWrite(t *testing.T) {
	api, backend, service := newTestAPI(t)
	request := jsonRequest(
		t,
		http.MethodPost,
		testOrigin+"/api/v1/connections",
		`{"ssid":"Office","password":"private-password"}`,
		bootstrapToken(t, api),
	)
	response := &writeErrorResponseWriter{
		ResponseRecorder: httptest.NewRecorder(),
		err:              errors.New("client disconnected"),
	}

	serveAPI(t, api, response, request)

	backend.mu.Lock()
	started := backend.started
	backend.mu.Unlock()
	if started {
		t.Fatal("backend started after accepted response write failed")
	}
	bootstrap, err := service.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.Operation == nil || bootstrap.Operation.State != setup.OperationFailed ||
		bootstrap.Operation.Failure == nil ||
		bootstrap.Operation.Failure.Code != "operation_interrupted" {
		t.Fatalf("latest operation = %#v", bootstrap.Operation)
	}
}

func TestAPIConnectionPreservesOpenAndHiddenChoices(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   string
		open   bool
		hidden bool
	}{
		{
			name: "visible open network",
			body: `{"ssid":"Guest","password":"","open":true,"hidden":false}`,
			open: true,
		},
		{
			name:   "hidden protected network",
			body:   `{"ssid":"Hidden Office","password":"private-password","open":false,"hidden":true}`,
			hidden: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			api, backend, _ := newTestAPI(t)
			request := jsonRequest(
				t,
				http.MethodPost,
				testOrigin+"/api/v1/connections",
				test.body,
				bootstrapToken(t, api),
			)
			response := httptest.NewRecorder()

			serveAPI(t, api, response, request)

			if response.Code != http.StatusAccepted {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var accepted struct {
				Operation setup.Operation `json:"operation"`
			}
			decodeResponse(t, response, &accepted)
			close(backend.release)
			waitForAPIState(t, api, accepted.Operation.ID, setup.OperationSucceeded)
			backend.mu.Lock()
			connection := backend.connection
			backend.mu.Unlock()
			if connection.Open != test.open || connection.Hidden != test.hidden {
				t.Fatalf("connection = %#v, want open=%t hidden=%t", connection, test.open, test.hidden)
			}
		})
	}
}

func TestAPIListsAndForgetsKnownNetwork(t *testing.T) {
	api, backend, _ := newTestAPI(t)
	uuid := "329cdb0f-d696-4f63-a17e-84ac66582f43"
	backend.knownNetworks = []setup.KnownNetwork{
		{UUID: uuid, SSID: "Office", Managed: true, CanForget: true},
		{UUID: "b01a1c10-ce1e-40e7-9fe2-7ebcf30a43c7", SSID: "System", Automatic: true},
	}

	listRequest := httptest.NewRequest(http.MethodGet, testOrigin+"/api/v1/known-networks", nil)
	listResponse := httptest.NewRecorder()
	serveAPI(t, api, listResponse, listRequest)
	if listResponse.Code != http.StatusOK ||
		!strings.Contains(listResponse.Body.String(), `"managed_by_onboardd":true`) {
		t.Fatalf("list response = %d %s", listResponse.Code, listResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(
		http.MethodDelete,
		testOrigin+"/api/v1/known-networks/"+uuid,
		nil,
	)
	deleteRequest.Header.Set(csrfHeader, bootstrapToken(t, api))
	deleteResponse := httptest.NewRecorder()
	serveAPI(t, api, deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete response = %d %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if backend.forgottenUUID != uuid {
		t.Fatalf("forgotten uuid = %q", backend.forgottenUUID)
	}
}

func TestAPIConnectsKnownNetwork(t *testing.T) {
	api, backend, _ := newTestAPI(t)
	uuid := "0a3aeac5-3e46-4f46-b9b0-99b2f83d4cb1"
	backend.knownNetworks = []setup.KnownNetwork{{
		UUID: uuid, SSID: "Workshop", Managed: true, CanConnect: true, CanForget: true,
	}}
	request := httptest.NewRequest(
		http.MethodPost,
		testOrigin+"/api/v1/known-networks/"+uuid+"/connect",
		nil,
	)
	request.Header.Set(csrfHeader, bootstrapToken(t, api))
	response := httptest.NewRecorder()

	serveAPI(t, api, response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var accepted struct {
		Operation setup.Operation `json:"operation"`
	}
	decodeResponse(t, response, &accepted)
	if accepted.Operation.Network != "Workshop" {
		t.Fatalf("operation = %#v", accepted.Operation)
	}
	close(backend.release)
	waitForAPIState(t, api, accepted.Operation.ID, setup.OperationSucceeded)
	if backend.knownUUID != uuid {
		t.Fatalf("known network UUID = %q", backend.knownUUID)
	}
}

func TestAPIProtectsKnownNetworkActivation(t *testing.T) {
	const uuid = "0a3aeac5-3e46-4f46-b9b0-99b2f83d4cb1"
	for _, test := range []struct {
		name       string
		uuid       string
		token      bool
		backendErr error
		wantStatus int
	}{
		{name: "missing CSRF token", uuid: uuid, wantStatus: http.StatusForbidden},
		{name: "invalid uuid", uuid: "not-a-uuid", token: true, wantStatus: http.StatusBadRequest},
		{
			name: "system profile", uuid: uuid, token: true,
			backendErr: setup.NewPublicError("network_read_only", "read only"),
			wantStatus: http.StatusForbidden,
		},
		{
			name: "active profile", uuid: uuid, token: true,
			backendErr: setup.NewPublicError("active_network", "active"),
			wantStatus: http.StatusConflict,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			api, backend, _ := newTestAPI(t)
			backend.networkErr = test.backendErr
			request := httptest.NewRequest(
				http.MethodPost,
				testOrigin+"/api/v1/known-networks/"+test.uuid+"/connect",
				nil,
			)
			if test.token {
				request.Header.Set(csrfHeader, bootstrapToken(t, api))
			}
			response := httptest.NewRecorder()
			serveAPI(t, api, response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAPIProtectsKnownNetworkDeletion(t *testing.T) {
	for _, test := range []struct {
		name       string
		uuid       string
		token      bool
		backendErr error
		wantStatus int
	}{
		{
			name: "missing CSRF token", uuid: "329cdb0f-d696-4f63-a17e-84ac66582f43",
			wantStatus: http.StatusForbidden,
		},
		{name: "invalid uuid", uuid: "not-a-uuid", token: true, wantStatus: http.StatusBadRequest},
		{
			name: "system profile", uuid: "329cdb0f-d696-4f63-a17e-84ac66582f43", token: true,
			backendErr: setup.NewPublicError("network_read_only", "read only"),
			wantStatus: http.StatusForbidden,
		},
		{
			name: "active profile", uuid: "329cdb0f-d696-4f63-a17e-84ac66582f43", token: true,
			backendErr: setup.NewPublicError("active_network", "active"),
			wantStatus: http.StatusConflict,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			api, backend, _ := newTestAPI(t)
			backend.forgetErr = test.backendErr
			request := httptest.NewRequest(
				http.MethodDelete,
				testOrigin+"/api/v1/known-networks/"+test.uuid,
				nil,
			)
			if test.token {
				request.Header.Set(csrfHeader, bootstrapToken(t, api))
			}
			response := httptest.NewRecorder()
			serveAPI(t, api, response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAPIMutationProtection(t *testing.T) {
	for _, test := range []struct {
		name       string
		token      string
		origin     string
		wantStatus int
	}{
		{name: "missing token", origin: testOrigin, wantStatus: http.StatusForbidden},
		{name: "wrong origin", token: "TOKEN", origin: "https://attacker.example", wantStatus: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			api, _, _ := newTestAPI(t)
			token := test.token
			if token == "TOKEN" {
				token = bootstrapToken(t, api)
			}
			request := jsonRequest(
				t,
				http.MethodPost,
				testOrigin+"/api/v1/standalone",
				`{"confirm":true}`,
				token,
			)
			request.Header.Set("Origin", test.origin)
			response := httptest.NewRecorder()
			serveAPI(t, api, response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAPIMutationAllowsDirectListenerOrigin(t *testing.T) {
	api, backend, _ := newTestAPI(t)
	token := bootstrapToken(t, api)
	request := jsonRequest(
		t,
		http.MethodPost,
		"http://192.0.2.10:18080/api/v1/standalone",
		`{"confirm":true}`,
		token,
	)
	request.Header.Set("Origin", "http://192.0.2.10:18080")
	response := httptest.NewRecorder()

	serveAPI(t, api, response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	close(backend.release)
}

func TestAPIRejectsUnknownJSONAndLargeBodies(t *testing.T) {
	for _, test := range []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "unknown field", body: `{"confirm":true,"surprise":1}`, wantStatus: http.StatusBadRequest},
		{name: "too large", body: `{"confirm":true,"padding":"` + strings.Repeat("x", apiBodyLimit) + `"}`, wantStatus: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			api, _, _ := newTestAPI(t)
			request := jsonRequest(
				t,
				http.MethodPost,
				testOrigin+"/api/v1/standalone",
				test.body,
				bootstrapToken(t, api),
			)
			response := httptest.NewRecorder()
			serveAPI(t, api, response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAPIReportsOperationConflict(t *testing.T) {
	api, backend, _ := newTestAPI(t)
	token := bootstrapToken(t, api)
	firstRequest := jsonRequest(
		t,
		http.MethodPost,
		testOrigin+"/api/v1/standalone",
		`{"confirm":true}`,
		token,
	)
	firstResponse := httptest.NewRecorder()
	serveAPI(t, api, firstResponse, firstRequest)
	if firstResponse.Code != http.StatusAccepted {
		t.Fatalf("first status = %d", firstResponse.Code)
	}

	secondRequest := jsonRequest(
		t,
		http.MethodPost,
		testOrigin+"/api/v1/standalone",
		`{"confirm":true}`,
		token,
	)
	secondResponse := httptest.NewRecorder()
	serveAPI(t, api, secondResponse, secondRequest)
	if secondResponse.Code != http.StatusConflict ||
		!strings.Contains(secondResponse.Body.String(), "operation_in_progress") {
		t.Fatalf("second status = %d, body = %s", secondResponse.Code, secondResponse.Body.String())
	}
	close(backend.release)
}

func TestAPIHidesBackendErrors(t *testing.T) {
	api, backend, _ := newTestAPI(t)
	backend.networkErr = errors.New("private NetworkManager path")
	request := httptest.NewRequest(http.MethodGet, testOrigin+"/api/v1/networks", nil)
	response := httptest.NewRecorder()
	serveAPI(t, api, response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "NetworkManager") {
		t.Fatalf("backend error leaked: %s", response.Body.String())
	}
}

func TestAPIUnknownRouteReturnsJSON(t *testing.T) {
	for _, test := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/v1/missing"},
		{method: http.MethodPatch, path: "/api/v1/setup"},
	} {
		api, _, _ := newTestAPI(t)
		request := httptest.NewRequest(test.method, testOrigin+test.path, nil)
		response := httptest.NewRecorder()

		serveAPI(t, api, response, request)

		if response.Code != http.StatusNotFound ||
			response.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
			!strings.Contains(response.Body.String(), `"code":"api_not_found"`) {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
	}
}

func newTestAPI(t *testing.T) (*API, *fakeAPIBackend, *setup.Service) {
	return newTestAPIWithOptions(t)
}

func newTestAPIWithOptions(t *testing.T, options ...Options) (*API, *fakeAPIBackend, *setup.Service) {
	t.Helper()
	backend := &fakeAPIBackend{release: make(chan struct{})}
	service, err := setup.NewService(
		context.Background(),
		backend,
		setup.Capabilities{Network: true, Standalone: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	api, err := NewAPI(
		service,
		testOrigin,
		Authentication{Password: testAdminPassword},
		options...,
	)
	if err != nil {
		t.Fatal(err)
	}
	return api, backend, service
}

func serveAPI(t *testing.T, api *API, response http.ResponseWriter, request *http.Request) {
	t.Helper()
	request.AddCookie(&http.Cookie{
		Name:  sessionCookieName,
		Value: api.sessionToken,
		Path:  "/api/v1/",
	})
	api.ServeHTTP(response, request)
}

func bootstrapToken(t *testing.T, api *API) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, testOrigin+"/api/v1/setup", nil)
	response := httptest.NewRecorder()
	serveAPI(t, api, response, request)
	var body struct {
		CSRFToken string `json:"csrf_token"`
	}
	decodeResponse(t, response, &body)
	return body.CSRFToken
}

func jsonRequest(t *testing.T, method, target, body, token string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set(csrfHeader, token)
	}
	return request
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), destination); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

func waitForAPIState(t *testing.T, api *API, id string, state setup.OperationState) setup.Operation {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		request := httptest.NewRequest(http.MethodGet, testOrigin+"/api/v1/operations/"+id, nil)
		response := httptest.NewRecorder()
		serveAPI(t, api, response, request)
		var body struct {
			Operation setup.Operation `json:"operation"`
		}
		decodeResponse(t, response, &body)
		if body.Operation.State == state {
			return body.Operation
		}
		select {
		case <-deadline.C:
			t.Fatalf("operation state = %s, want %s", body.Operation.State, state)
		case <-ticker.C:
		}
	}
}

type fakeAPIBackend struct {
	mu            sync.Mutex
	mode          setup.Mode
	connection    setup.ConnectionRequest
	knownNetworks []setup.KnownNetwork
	forgottenUUID string
	knownUUID     string
	forgetErr     error
	networkErr    error
	release       chan struct{}
	started       bool
}

type flushErrorResponseWriter struct {
	*httptest.ResponseRecorder
	err error
}

func (response *flushErrorResponseWriter) FlushError() error { return response.err }

type writeErrorResponseWriter struct {
	*httptest.ResponseRecorder
	err error
}

func (response *writeErrorResponseWriter) Write([]byte) (int, error) {
	return 0, response.err
}

func (backend *fakeAPIBackend) CurrentMode(context.Context) (setup.Mode, error) {
	if backend.mode != "" {
		return backend.mode, nil
	}
	return setup.ModeSetup, nil
}

func (backend *fakeAPIBackend) Networks(context.Context) ([]setup.Network, error) {
	return []setup.Network{{SSID: "Office", Security: "protected", Strength: 80}}, backend.networkErr
}

func (backend *fakeAPIBackend) KnownNetworks(context.Context) ([]setup.KnownNetwork, error) {
	return append([]setup.KnownNetwork(nil), backend.knownNetworks...), backend.networkErr
}

func (backend *fakeAPIBackend) ForgetKnownNetwork(_ context.Context, uuid string) error {
	backend.forgottenUUID = uuid
	return backend.forgetErr
}

func (backend *fakeAPIBackend) Connect(_ context.Context, request setup.ConnectionRequest) error {
	backend.mu.Lock()
	backend.connection = request
	backend.started = true
	backend.mu.Unlock()
	<-backend.release
	return nil
}

func (backend *fakeAPIBackend) ConnectKnownNetwork(_ context.Context, uuid string) error {
	backend.mu.Lock()
	backend.knownUUID = uuid
	backend.started = true
	backend.mu.Unlock()
	<-backend.release
	return nil
}

func (backend *fakeAPIBackend) Standalone(context.Context) error {
	<-backend.release
	return nil
}
