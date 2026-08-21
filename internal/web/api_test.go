package web

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

const testOrigin = "http://10.42.0.1"

func TestAPISetupBootstrap(t *testing.T) {
	api, _, _ := newTestAPI(t)
	request := httptest.NewRequest(http.MethodGet, testOrigin+"/api/v1/setup", nil)
	response := httptest.NewRecorder()

	api.ServeHTTP(response, request)

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

	api.ServeHTTP(response, request)

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

			api.ServeHTTP(response, request)

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
			api.ServeHTTP(response, request)
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

	api.ServeHTTP(response, request)

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
			api.ServeHTTP(response, request)
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
	api.ServeHTTP(firstResponse, firstRequest)
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
	api.ServeHTTP(secondResponse, secondRequest)
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
	api.ServeHTTP(response, request)
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

		api.ServeHTTP(response, request)

		if response.Code != http.StatusNotFound ||
			response.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
			!strings.Contains(response.Body.String(), `"code":"api_not_found"`) {
			t.Fatalf("response = %d %q", response.Code, response.Body.String())
		}
	}
}

func newTestAPI(t *testing.T) (*API, *fakeAPIBackend, *setup.Service) {
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
	api, err := NewAPI(service, testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	return api, backend, service
}

func bootstrapToken(t *testing.T, api *API) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, testOrigin+"/api/v1/setup", nil)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, request)
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
		api.ServeHTTP(response, request)
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
	mu         sync.Mutex
	connection setup.ConnectionRequest
	networkErr error
	release    chan struct{}
}

func (backend *fakeAPIBackend) CurrentMode(context.Context) (setup.Mode, error) {
	return setup.ModeSetup, nil
}

func (backend *fakeAPIBackend) Networks(context.Context) ([]setup.Network, error) {
	return []setup.Network{{SSID: "Office", Security: "protected", Strength: 80}}, backend.networkErr
}

func (backend *fakeAPIBackend) Connect(_ context.Context, request setup.ConnectionRequest) error {
	backend.mu.Lock()
	backend.connection = request
	backend.mu.Unlock()
	<-backend.release
	return nil
}

func (backend *fakeAPIBackend) Standalone(context.Context) error {
	<-backend.release
	return nil
}
