package handoff

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHealthCheckerAcceptsOnlySuccessfulResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		ready  bool
	}{
		{name: "ready", status: http.StatusNoContent, ready: true},
		{name: "starting", status: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			checker := &HealthChecker{client: &http.Client{Transport: roundTripFunc(
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
}

func TestHealthCheckerTreatsConnectionFailureAsNotReady(t *testing.T) {
	checker := &HealthChecker{client: &http.Client{Transport: roundTripFunc(
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
