package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	appconfig "github.com/flavorplus/onboardd/internal/config"
)

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
