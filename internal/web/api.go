// Package web provides the product-facing setup HTTP API.
package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/flavorplus/onboardd/internal/setup"
)

const (
	apiBodyLimit      = 4096
	csrfHeader        = "X-Onboardd-CSRF"
	sessionCookieName = "onboardd_session"
	failedLoginDelay  = 350 * time.Millisecond
	minAdminPassword  = 12
	maxAdminPassword  = 256
)

// Authentication contains the administrator secret used to unlock the setup API.
type Authentication struct {
	Password string
}

// ValidateAdminPassword checks the runtime password policy without retaining the
// secret. The password itself belongs in a root-only file, never in TOML.
func ValidateAdminPassword(password string) error {
	length := len([]byte(password))
	if length < minAdminPassword || length > maxAdminPassword {
		return fmt.Errorf("admin password must be between %d and %d bytes", minAdminPassword, maxAdminPassword)
	}
	if strings.ContainsAny(password, "\r\n\x00") {
		return errors.New("admin password must not contain line breaks or NUL bytes")
	}
	return nil
}

type setupService interface {
	Bootstrap(context.Context) (setup.Bootstrap, error)
	Networks(context.Context) ([]setup.Network, error)
	KnownNetworks(context.Context) ([]setup.KnownNetwork, error)
	ForgetKnownNetwork(context.Context, string) error
	StartConnection(setup.ConnectionRequest) (setup.Operation, error)
	StartKnownNetwork(context.Context, string) (setup.Operation, error)
	StartStandalone() (setup.Operation, error)
	BeginOperation(string) bool
	CancelPendingOperation(string) bool
	Operation(string) (setup.Operation, bool)
}

// API is the versioned setup JSON surface.
type API struct {
	service         setupService
	canonicalOrigin string
	csrfToken       string
	passwordDigest  [sha256.Size]byte
	sessionToken    string
	handoff         *Handoff
	healthChecker   readinessChecker
	mux             *http.ServeMux
}

// NewAPI validates the portal origin and creates an isolated v1 handler.
func NewAPI(
	service setupService,
	canonicalOrigin string,
	authentication Authentication,
	options ...Options,
) (*API, error) {
	if service == nil {
		return nil, errors.New("setup service is required")
	}
	if err := ValidateAdminPassword(authentication.Password); err != nil {
		return nil, err
	}
	origin, err := normalizeOrigin(canonicalOrigin)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveOptions(options)
	if err != nil {
		return nil, err
	}
	csrfToken, err := randomToken("setup CSRF")
	if err != nil {
		return nil, err
	}
	sessionToken, err := randomToken("setup session")
	if err != nil {
		return nil, err
	}
	api := &API{
		service:         service,
		canonicalOrigin: origin,
		csrfToken:       csrfToken,
		passwordDigest:  sha256.Sum256([]byte(authentication.Password)),
		sessionToken:    sessionToken,
		handoff:         resolved.handoff,
		healthChecker:   resolved.healthChecker,
		mux:             http.NewServeMux(),
	}
	api.mux.HandleFunc("POST /api/v1/session", api.postSession)
	api.mux.HandleFunc("GET /api/v1/setup", api.getSetup)
	api.mux.HandleFunc("GET /api/v1/networks", api.getNetworks)
	api.mux.HandleFunc("GET /api/v1/known-networks", api.getKnownNetworks)
	api.mux.HandleFunc("DELETE /api/v1/known-networks/{uuid}", api.deleteKnownNetwork)
	api.mux.HandleFunc("POST /api/v1/known-networks/{uuid}/connect", api.postKnownNetwork)
	api.mux.HandleFunc("POST /api/v1/connections", api.postConnection)
	api.mux.HandleFunc("POST /api/v1/standalone", api.postStandalone)
	api.mux.HandleFunc("GET /api/v1/operations/{id}", api.getOperation)
	api.mux.HandleFunc("/api/", api.notFound)
	return api, nil
}

func randomToken(label string) (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate %s token: %w", label, err)
	}
	return hex.EncodeToString(value), nil
}

func (api *API) notFound(response http.ResponseWriter, _ *http.Request) {
	writeError(response, http.StatusNotFound, setup.Failure{
		Code:    "api_not_found",
		Message: "This setup API route does not exist.",
	})
}

// ServeHTTP applies response security policy to every API route.
func (api *API) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Referrer-Policy", "no-referrer")
	if request.Method != http.MethodPost || request.URL.Path != "/api/v1/session" {
		if !api.allowSession(response, request) {
			return
		}
	}
	api.mux.ServeHTTP(response, request)
}

type sessionPayload struct {
	Password string `json:"password"`
}

func (api *API) postSession(response http.ResponseWriter, request *http.Request) {
	if !api.allowOrigin(response, request) {
		return
	}
	var payload sessionPayload
	if !decodeJSON(response, request, &payload) {
		return
	}
	provided := sha256.Sum256([]byte(payload.Password))
	if subtle.ConstantTimeCompare(provided[:], api.passwordDigest[:]) != 1 {
		timer := time.NewTimer(failedLoginDelay)
		defer timer.Stop()
		select {
		case <-request.Context().Done():
			return
		case <-timer.C:
		}
		writeError(response, http.StatusUnauthorized, setup.Failure{
			Code:    "authentication_failed",
			Message: "The administrator password is incorrect.",
		})
		return
	}
	http.SetCookie(response, &http.Cookie{
		Name:     sessionCookieName,
		Value:    api.sessionToken,
		Path:     "/api/v1/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(response, http.StatusOK, struct {
		Authenticated bool `json:"authenticated"`
	}{Authenticated: true})
}

func (api *API) allowSession(response http.ResponseWriter, request *http.Request) bool {
	cookie, err := request.Cookie(sessionCookieName)
	if err == nil && len(cookie.Value) == len(api.sessionToken) && subtle.ConstantTimeCompare(
		[]byte(cookie.Value),
		[]byte(api.sessionToken),
	) == 1 {
		return true
	}
	writeError(response, http.StatusUnauthorized, setup.Failure{
		Code:    "authentication_required",
		Message: "Enter the administrator password to continue.",
	})
	return false
}

type setupResponse struct {
	setup.Bootstrap
	CSRFToken string           `json:"csrf_token"`
	Handoff   *handoffResponse `json:"handoff,omitempty"`
}

type handoffResponse struct {
	SetupURL    string               `json:"setup_url"`
	Application *applicationResponse `json:"application,omitempty"`
	Standalone  *standaloneResponse  `json:"standalone,omitempty"`
}

type applicationResponse struct {
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
	Ready bool   `json:"ready"`
}

type standaloneResponse struct {
	SSID     string `json:"ssid"`
	Password string `json:"password,omitempty"`
}

func (api *API) getSetup(response http.ResponseWriter, request *http.Request) {
	bootstrap, err := api.service.Bootstrap(request.Context())
	if err != nil {
		writeInternalError(response)
		return
	}
	writeJSON(response, http.StatusOK, setupResponse{
		Bootstrap: bootstrap,
		CSRFToken: api.csrfToken,
		Handoff: browserHandoff(
			request.Context(),
			api.handoff,
			api.healthChecker,
		),
	})
}

func browserHandoff(
	ctx context.Context,
	info *Handoff,
	checker readinessChecker,
) *handoffResponse {
	if info == nil {
		return nil
	}
	response := &handoffResponse{SetupURL: info.SetupURL}
	if info.Application != nil {
		ready := info.HealthCheckURL == "" || (checker != nil && checker.Ready(ctx, info.HealthCheckURL))
		response.Application = &applicationResponse{
			Label: info.Application.Label,
			Ready: ready,
		}
		if ready {
			response.Application.URL = info.Application.URL
		}
	}
	if info.Standalone != nil {
		response.Standalone = &standaloneResponse{
			SSID: info.Standalone.SSID,
		}
		if info.ShowStandaloneCredentials {
			response.Standalone.Password = info.Standalone.Password
		}
	}
	return response
}

func (api *API) getNetworks(response http.ResponseWriter, request *http.Request) {
	networks, err := api.service.Networks(request.Context())
	if err != nil {
		writeServiceError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, struct {
		Networks []setup.Network `json:"networks"`
	}{Networks: networks})
}

func (api *API) getKnownNetworks(response http.ResponseWriter, request *http.Request) {
	known, err := api.service.KnownNetworks(request.Context())
	if err != nil {
		writeServiceError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, struct {
		Networks []setup.KnownNetwork `json:"networks"`
	}{Networks: known})
}

func (api *API) deleteKnownNetwork(response http.ResponseWriter, request *http.Request) {
	if !api.allowMutation(response, request) {
		return
	}
	uuid := strings.ToLower(request.PathValue("uuid"))
	if !validProfileUUID(uuid) {
		writeError(response, http.StatusBadRequest, setup.Failure{
			Code:    "invalid_profile_id",
			Message: "The saved network identifier is invalid.",
		})
		return
	}
	if err := api.service.ForgetKnownNetwork(request.Context(), uuid); err != nil {
		writeServiceError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, struct {
		Forgotten string `json:"forgotten"`
	}{Forgotten: uuid})
}

func (api *API) postKnownNetwork(response http.ResponseWriter, request *http.Request) {
	if !api.allowMutation(response, request) {
		return
	}
	uuid := strings.ToLower(request.PathValue("uuid"))
	if !validProfileUUID(uuid) {
		writeError(response, http.StatusBadRequest, setup.Failure{
			Code:    "invalid_profile_id",
			Message: "The saved network identifier is invalid.",
		})
		return
	}
	operation, err := api.service.StartKnownNetwork(request.Context(), uuid)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	if err := api.acceptOperation(response, operation); err != nil {
		api.service.CancelPendingOperation(operation.ID)
		return
	}
	api.service.BeginOperation(operation.ID)
}

type connectionPayload struct {
	SSID     string `json:"ssid"`
	Password string `json:"password"`
	Open     bool   `json:"open"`
	Hidden   bool   `json:"hidden"`
}

func (api *API) postConnection(response http.ResponseWriter, request *http.Request) {
	if !api.allowMutation(response, request) {
		return
	}
	var payload connectionPayload
	if !decodeJSON(response, request, &payload) {
		return
	}
	if failure := validateConnectionPayload(payload); failure != nil {
		writeError(response, http.StatusBadRequest, *failure)
		return
	}
	operation, err := api.service.StartConnection(setup.ConnectionRequest{
		SSID:     payload.SSID,
		Password: payload.Password,
		Open:     payload.Open,
		Hidden:   payload.Hidden,
	})
	if err != nil {
		writeServiceError(response, err)
		return
	}
	if err := api.acceptOperation(response, operation); err != nil {
		api.service.CancelPendingOperation(operation.ID)
		return
	}
	api.service.BeginOperation(operation.ID)
}

func (api *API) acceptOperation(response http.ResponseWriter, operation setup.Operation) error {
	payload, err := json.Marshal(struct {
		Operation setup.Operation `json:"operation"`
	}{Operation: operation})
	if err != nil {
		return fmt.Errorf("encode accepted setup operation: %w", err)
	}
	payload = append(payload, '\n')
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(http.StatusAccepted)
	written, err := response.Write(payload)
	if err != nil {
		return fmt.Errorf("write accepted setup operation: %w", err)
	}
	if written != len(payload) {
		return fmt.Errorf("write accepted setup operation: %w", io.ErrShortWrite)
	}
	if err := http.NewResponseController(response).Flush(); err != nil {
		return fmt.Errorf("flush accepted setup operation: %w", err)
	}
	return nil
}

type standalonePayload struct {
	Confirm bool `json:"confirm"`
}

func (api *API) postStandalone(response http.ResponseWriter, request *http.Request) {
	if !api.allowMutation(response, request) {
		return
	}
	var payload standalonePayload
	if !decodeJSON(response, request, &payload) {
		return
	}
	if !payload.Confirm {
		writeError(response, http.StatusBadRequest, setup.Failure{
			Code:    "confirmation_required",
			Message: "Confirm standalone mode before continuing.",
		})
		return
	}
	operation, err := api.service.StartStandalone()
	if err != nil {
		writeServiceError(response, err)
		return
	}
	if err := api.acceptOperation(response, operation); err != nil {
		api.service.CancelPendingOperation(operation.ID)
		return
	}
	api.service.BeginOperation(operation.ID)
}

func (api *API) getOperation(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	operation, ok := api.service.Operation(id)
	if !ok {
		writeError(response, http.StatusNotFound, setup.Failure{
			Code:    "operation_not_found",
			Message: "This setup operation is no longer available.",
		})
		return
	}
	writeJSON(response, http.StatusOK, struct {
		Operation setup.Operation `json:"operation"`
	}{Operation: operation})
}

func (api *API) allowMutation(response http.ResponseWriter, request *http.Request) bool {
	provided := request.Header.Get(csrfHeader)
	if len(provided) != len(api.csrfToken) || subtle.ConstantTimeCompare(
		[]byte(provided),
		[]byte(api.csrfToken),
	) != 1 {
		writeError(response, http.StatusForbidden, setup.Failure{
			Code:    "request_not_allowed",
			Message: "Refresh the setup page and try again.",
		})
		return false
	}
	return api.allowOrigin(response, request)
}

func (api *API) allowOrigin(response http.ResponseWriter, request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	normalized, err := normalizeOrigin(origin)
	if err == nil && (normalized == api.canonicalOrigin || normalized == requestOrigin(request)) {
		return true
	}
	writeError(response, http.StatusForbidden, setup.Failure{
		Code:    "request_not_allowed",
		Message: "Refresh the setup page and try again.",
	})
	return false
}

func requestOrigin(request *http.Request) string {
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + strings.ToLower(request.Host)
}

func decodeJSON(response http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(response, http.StatusUnsupportedMediaType, setup.Failure{
			Code:    "json_required",
			Message: "The request must use JSON.",
		})
		return false
	}
	request.Body = http.MaxBytesReader(response, request.Body, apiBodyLimit)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(response, http.StatusRequestEntityTooLarge, setup.Failure{
				Code:    "request_too_large",
				Message: "The request is too large.",
			})
			return false
		}
		writeInvalidJSON(response)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeInvalidJSON(response)
		return false
	}
	return true
}

func validateConnectionPayload(payload connectionPayload) *setup.Failure {
	ssidLength := len([]byte(payload.SSID))
	if ssidLength == 0 || ssidLength > 32 {
		return &setup.Failure{
			Code:    "invalid_network_name",
			Message: "Enter a Wi-Fi network name between 1 and 32 characters.",
		}
	}
	if payload.Open {
		if payload.Password != "" {
			return &setup.Failure{
				Code:    "unexpected_password",
				Message: "An open network does not use a password.",
			}
		}
		return nil
	}
	if !validPSK(payload.Password) {
		return &setup.Failure{
			Code:    "invalid_password",
			Message: "Enter a Wi-Fi password between 8 and 63 characters.",
		}
	}
	return nil
}

func validPSK(password string) bool {
	length := len(password)
	if length >= 8 && length <= 63 {
		return true
	}
	if length != 64 {
		return false
	}
	for _, character := range password {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return false
		}
	}
	return true
}

func validProfileUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' ||
		value[18] != '-' || value[23] != '-' {
		return false
	}
	plain := strings.ReplaceAll(value, "-", "")
	_, err := hex.DecodeString(plain)
	return err == nil
}

func normalizeOrigin(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("canonical origin must be an absolute HTTP URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return "", errors.New("canonical origin must not include credentials, path, query, or fragment")
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), nil
}

func writeServiceError(response http.ResponseWriter, err error) {
	var conflict *setup.ConflictError
	if errors.As(err, &conflict) {
		writeJSON(response, http.StatusConflict, struct {
			Error     setup.Failure   `json:"error"`
			Operation setup.Operation `json:"operation"`
		}{
			Error: setup.Failure{
				Code:    "operation_in_progress",
				Message: "Another network change is already in progress.",
			},
			Operation: conflict.Operation,
		})
		return
	}
	var public *setup.PublicError
	if errors.As(err, &public) {
		status := http.StatusBadRequest
		switch public.Failure.Code {
		case "mode_unavailable", "network_read_only":
			status = http.StatusForbidden
		case "known_network_not_found":
			status = http.StatusNotFound
		case "active_network":
			status = http.StatusConflict
		case "profile_change_in_progress":
			status = http.StatusConflict
		}
		writeError(response, status, public.Failure)
		return
	}
	writeInternalError(response)
}

func writeInvalidJSON(response http.ResponseWriter) {
	writeError(response, http.StatusBadRequest, setup.Failure{
		Code:    "invalid_json",
		Message: "The request could not be understood.",
	})
}

func writeInternalError(response http.ResponseWriter) {
	writeError(response, http.StatusInternalServerError, setup.Failure{
		Code:    "internal_failure",
		Message: "Setup is temporarily unavailable. Please try again.",
	})
}

func writeError(response http.ResponseWriter, status int, failure setup.Failure) {
	writeJSON(response, status, struct {
		Error setup.Failure `json:"error"`
	}{Error: failure})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
