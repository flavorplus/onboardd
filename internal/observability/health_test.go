package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flavorplus/onboardd/internal/appliance"
)

func TestHealthTracksReadinessRecoveryAndShutdown(t *testing.T) {
	health := NewHealth()

	initial := health.Snapshot()
	if !initial.Healthy || initial.Ready || initial.Status != StatusStarting {
		t.Fatalf("initial snapshot = %+v", initial)
	}

	health.ObserveNetworkState(
		3,
		appliance.StageInfrastructure,
		appliance.ModeInfrastructure,
		appliance.ReasonInfrastructureReady,
	)
	ready := health.Snapshot()
	if !ready.Healthy || !ready.Ready || ready.Status != StatusReady {
		t.Fatalf("ready snapshot = %+v", ready)
	}
	if ready.Stage != appliance.StageInfrastructure || ready.Sequence != 3 {
		t.Fatalf("ready state = %+v", ready)
	}

	health.ComponentRetry(ComponentHTTP, 1, 3)
	recovering := health.Snapshot()
	if !recovering.Healthy || recovering.Ready || recovering.Status != StatusRecovering {
		t.Fatalf("recovering snapshot = %+v", recovering)
	}
	health.ComponentRecovered(ComponentHTTP)
	if snapshot := health.Snapshot(); !snapshot.Ready || snapshot.Status != StatusReady {
		t.Fatalf("recovered snapshot = %+v", snapshot)
	}

	health.Stopping()
	if snapshot := health.Snapshot(); snapshot.Healthy || snapshot.Ready || snapshot.Status != StatusStopping {
		t.Fatalf("stopping snapshot = %+v", snapshot)
	}
	health.Stopped()
	if snapshot := health.Snapshot(); snapshot.Healthy || snapshot.Status != StatusStopped {
		t.Fatalf("stopped snapshot = %+v", snapshot)
	}
}

func TestHealthChangesAreCoalescedAndWatchdogReady(t *testing.T) {
	health := NewHealth()
	health.ObserveNetworkState(
		1,
		appliance.StageReconciling,
		appliance.ModeNone,
		appliance.ReasonInspectingNetwork,
	)
	health.ObserveNetworkState(
		2,
		appliance.StageStandalone,
		appliance.ModeStandalone,
		appliance.ReasonStandaloneActive,
	)

	select {
	case snapshot := <-health.Changes():
		if snapshot.Status != StatusReady || !snapshot.Healthy {
			t.Fatalf("coalesced snapshot = %+v", snapshot)
		}
	default:
		t.Fatal("health change signal was empty")
	}
	select {
	case duplicate := <-health.Changes():
		t.Fatalf("unexpected stale health signal = %+v", duplicate)
	default:
	}
}

func TestHealthHandlerReportsLivenessWithoutSensitiveDetails(t *testing.T) {
	health := NewHealth()
	health.ObserveNetworkState(
		1,
		appliance.StageFailed,
		appliance.ModeNone,
		appliance.ReasonObservationFailed,
	)

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	health.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	var snapshot Snapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if !snapshot.Healthy || snapshot.Ready || snapshot.Status != StatusRecovering {
		t.Fatalf("health response = %+v", snapshot)
	}
	if response.Body.String() == "" || containsSensitiveHealthText(response.Body.String()) {
		t.Fatalf("health response exposed sensitive detail: %s", response.Body.String())
	}

	health.Failed(ComponentReconciler, FailureExhausted)
	response = httptest.NewRecorder()
	health.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed status = %d, body = %s", response.Code, response.Body.String())
	}
}

func containsSensitiveHealthText(value string) bool {
	for _, forbidden := range []string{"hunter2", "freedesktop", "password", "detail"} {
		if strings.Contains(value, forbidden) {
			return true
		}
	}
	return false
}
