package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	appconfig "github.com/flavorplus/onboardd/internal/config"
)

func TestOptionsFromRenderedConfiguration(t *testing.T) {
	configured := appconfig.Defaults()
	configured.Product.Name = "InkyPi"
	configured.Product.DeviceName = "Kitchen Display"
	configured.Branding.Text.Title = "Set up {{ .DeviceName }}"
	rendered, err := appconfig.RenderTemplates(
		configured,
		appconfig.Identity{DeviceID: "AB12CD34", Hostname: "inkypi"},
	)
	if err != nil {
		t.Fatal(err)
	}
	options, err := OptionsFromConfig(rendered, "inkypi")
	if err != nil {
		t.Fatal(err)
	}
	if options.Branding.ProductName != "InkyPi" || options.Branding.Title != "Set up Kitchen Display" {
		t.Fatalf("branding = %+v", options.Branding)
	}
}

func TestAppearanceIsPublicAndExcludedFromAPISetup(t *testing.T) {
	branding := Branding{
		ProductName:     "InkyPi",
		DeviceName:      "Kitchen Display",
		Title:           "Set up Kitchen Display",
		Subtitle:        "Choose a connection.",
		PrimaryColor:    "#123456",
		BackgroundColor: "#f1f2f3",
	}
	api, _, _ := newTestAPIWithOptions(t, Options{Branding: branding})
	handler, err := NewHandler(api, fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("setup")},
	}, Options{Branding: branding})
	if err != nil {
		t.Fatal(err)
	}
	appearanceRequest := httptest.NewRequest(http.MethodGet, testOrigin+appearanceURL, nil)
	appearanceResponse := httptest.NewRecorder()
	handler.ServeHTTP(appearanceResponse, appearanceRequest)
	for _, expected := range []string{
		`"product_name":"InkyPi"`,
		`"device_name":"Kitchen Display"`,
		`"primary_color":"#123456"`,
	} {
		if !strings.Contains(appearanceResponse.Body.String(), expected) {
			t.Errorf("public appearance does not contain %q: %s", expected, appearanceResponse.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodGet, testOrigin+"/api/v1/setup", nil)
	response := httptest.NewRecorder()
	serveAPI(t, api, response, request)

	for _, moved := range []string{
		`"branding"`,
		`"product_name"`,
		`"device_name"`,
		`"primary_color"`,
		`"logo_url"`,
	} {
		if strings.Contains(response.Body.String(), moved) {
			t.Errorf("private setup response still contains moved resource %q: %s", moved, response.Body.String())
		}
	}
}

func TestAPISetupIncludesBrowserSafeHandoff(t *testing.T) {
	info := Handoff{
		SetupURL:                  "http://inkypi.local:18080/",
		Application:               &ApplicationHandoff{Label: "Open InkyPi", URL: "http://inkypi.local/"},
		HealthCheckURL:            "http://127.0.0.1/health",
		ShowStandaloneCredentials: true,
	}
	api, _, _ := newTestAPIWithOptions(t, Options{
		Branding:      defaultBranding(),
		Handoff:       &info,
		HealthChecker: fixedReadiness(true),
	})
	request := httptest.NewRequest(http.MethodGet, testOrigin+"/api/v1/setup", nil)
	response := httptest.NewRecorder()
	serveAPI(t, api, response, request)

	body := response.Body.String()
	for _, expected := range []string{
		`"setup_url":"http://inkypi.local:18080/"`,
		`"label":"Open InkyPi"`,
		`"url":"http://inkypi.local/"`,
		`"ready":true`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("response does not contain %q: %s", expected, body)
		}
	}
	for _, serverOnly := range []string{"health", "show_standalone_credentials"} {
		if strings.Contains(body, serverOnly) {
			t.Errorf("response exposes server-only handoff field %q: %s", serverOnly, body)
		}
	}
}

func TestAPISetupGatesUnhealthyApplicationURL(t *testing.T) {
	info := Handoff{
		SetupURL:       "http://inkypi.local:18080/",
		Application:    &ApplicationHandoff{Label: "Open InkyPi", URL: "http://inkypi.local/"},
		HealthCheckURL: "http://127.0.0.1/health",
	}
	api, _, _ := newTestAPIWithOptions(t, Options{
		Branding:      defaultBranding(),
		Handoff:       &info,
		HealthChecker: fixedReadiness(false),
	})
	request := httptest.NewRequest(http.MethodGet, testOrigin+"/api/v1/setup", nil)
	response := httptest.NewRecorder()
	serveAPI(t, api, response, request)

	body := response.Body.String()
	if !strings.Contains(body, `"application":{"label":"Open InkyPi","ready":false}`) {
		t.Fatalf("response does not contain gated application: %s", body)
	}
	if strings.Contains(body, "http://inkypi.local/\"") || strings.Contains(body, "health") {
		t.Fatalf("response exposed an unavailable destination or health policy: %s", body)
	}
}

func TestAPISetupExposesStandaloneHandoffBeforeTransition(t *testing.T) {
	info := Handoff{
		SetupURL:                  "http://inkypi.local:18080/",
		Standalone:                &StandaloneHandoff{SSID: "InkyPi-AB12CD34", Password: "private-password"},
		ShowStandaloneCredentials: true,
	}
	api, _, _ := newTestAPIWithOptions(t, Options{Branding: defaultBranding(), Handoff: &info})

	request := httptest.NewRequest(http.MethodGet, testOrigin+"/api/v1/setup", nil)
	response := httptest.NewRecorder()
	serveAPI(t, api, response, request)
	for _, expected := range []string{
		`"standalone":{"ssid":"InkyPi-AB12CD34","password":"private-password"}`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("setup response does not contain %q: %s", expected, response.Body.String())
		}
	}
}

func TestAPISetupHonorsStandaloneCredentialPolicy(t *testing.T) {
	info := Handoff{
		SetupURL:   "http://inkypi.local:18080/",
		Standalone: &StandaloneHandoff{SSID: "InkyPi-AB12CD34", Password: "private-password"},
	}
	api, _, _ := newTestAPIWithOptions(t, Options{Branding: defaultBranding(), Handoff: &info})
	request := httptest.NewRequest(http.MethodGet, testOrigin+"/api/v1/setup", nil)
	response := httptest.NewRecorder()
	serveAPI(t, api, response, request)

	body := response.Body.String()
	if !strings.Contains(body, `"standalone":{"ssid":"InkyPi-AB12CD34"}`) ||
		strings.Contains(body, "private-password") {
		t.Fatalf("credential policy response = %s", body)
	}
}

func TestConfiguredLogoIsServedWithRestrictedPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logo.svg")
	if err := os.WriteFile(path, []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20"><circle cx="10" cy="10" r="8" fill="#123456"/></svg>`), 0o600); err != nil {
		t.Fatal(err)
	}
	logo, err := loadLogo(path)
	if err != nil {
		t.Fatal(err)
	}
	api, _, _ := newTestAPI(t)
	handler, err := NewHandler(api, fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("setup")},
	}, Options{Branding: defaultBranding(), Logo: logo})
	if err != nil {
		t.Fatal(err)
	}

	appearanceRequest := httptest.NewRequest(http.MethodGet, testOrigin+appearanceURL, nil)
	appearanceResponse := httptest.NewRecorder()
	handler.ServeHTTP(appearanceResponse, appearanceRequest)
	if !strings.Contains(appearanceResponse.Body.String(), `"logo_url":"`+logoURL+`"`) {
		t.Fatalf("appearance response = %s", appearanceResponse.Body.String())
	}

	logoRequest := httptest.NewRequest(http.MethodGet, testOrigin+logoURL, nil)
	logoResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoResponse, logoRequest)
	if logoResponse.Code != http.StatusOK || logoResponse.Header().Get("Content-Type") != "image/svg+xml" {
		t.Fatalf("logo response = %d %q", logoResponse.Code, logoResponse.Header())
	}
	if logoResponse.Header().Get("Content-Security-Policy") == "" ||
		logoResponse.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
		t.Fatalf("logo security headers = %q", logoResponse.Header())
	}
}

func TestLoadLogoRejectsActiveSVG(t *testing.T) {
	for _, svg := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><image href="https://example.com/logo.png"/></svg>`,
	} {
		path := filepath.Join(t.TempDir(), "logo.svg")
		if err := os.WriteFile(path, []byte(svg), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadLogo(path); err == nil {
			t.Fatalf("loadLogo() accepted %s", svg)
		}
	}
}

func TestLoadLogoRejectsCorruptRasterImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logo.png")
	data := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 504)...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLogo(path); err == nil || !strings.Contains(err.Error(), "decode branding logo") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewAPIRejectsInvalidBranding(t *testing.T) {
	_, _, service := newTestAPI(t)
	_, err := NewAPI(
		service,
		testOrigin,
		Authentication{Password: testAdminPassword},
		Options{Branding: Branding{ProductName: "Device"}},
	)
	if err == nil || !strings.Contains(err.Error(), "device names") {
		t.Fatalf("error = %v", err)
	}
}

type fixedReadiness bool

func (ready fixedReadiness) Ready(context.Context, string) bool {
	return bool(ready)
}

func TestHandoffFromConfig(t *testing.T) {
	configured := appconfig.Defaults()
	configured.Handoff.ApplicationLabel = "Open player"
	configured.Handoff.ApplicationURL = "http://lobby-display.local/"
	configured.Handoff.HealthCheckURL = "http://127.0.0.1/health"
	configured.Handoff.ShowStandaloneCredentials = true
	configured.Portal.ListenerPort = 19000

	info, err := handoffFromConfig(configured, "Lobby-Display")
	if err != nil {
		t.Fatal(err)
	}
	if info.SetupURL != "http://lobby-display.local:19000/" || info.Application == nil ||
		info.Application.Label != "Open player" || info.HealthCheckURL != "http://127.0.0.1/health" ||
		!info.ShowStandaloneCredentials {
		t.Fatalf("handoff = %+v", info)
	}
	if info.Standalone == nil || info.Standalone.SSID != configured.Network.Standalone.SSID {
		t.Fatalf("standalone = %+v", info.Standalone)
	}
	if _, err := handoffFromConfig(configured, "display.local"); err == nil {
		t.Fatal("handoffFromConfig accepted a multi-label Avahi hostname")
	}
}

func TestHealthChecker(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		ready  bool
	}{
		{name: "ready", status: http.StatusNoContent, ready: true},
		{name: "starting", status: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			checker := &healthChecker{client: &http.Client{Transport: roundTripFunc(
				func(request *http.Request) (*http.Response, error) {
					if request.Header.Get("User-Agent") != "onboardd-health/1" {
						t.Errorf("User-Agent = %q", request.Header.Get("User-Agent"))
					}
					return &http.Response{
						StatusCode: test.status,
						Body:       io.NopCloser(strings.NewReader("")),
						Header:     make(http.Header),
						Request:    request,
					}, nil
				},
			)}}
			if got := checker.Ready(context.Background(), "http://application.test/health"); got != test.ready {
				t.Fatalf("Ready() = %t, want %t", got, test.ready)
			}
		})
	}

	checker := &healthChecker{client: &http.Client{Transport: roundTripFunc(
		func(*http.Request) (*http.Response, error) {
			return nil, errors.New("connection refused")
		},
	)}}
	if checker.Ready(context.Background(), "http://application.test/health") {
		t.Fatal("Ready() accepted a failed connection")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
