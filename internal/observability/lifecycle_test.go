package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/flavorplus/onboardd/internal/appliance"
)

func TestLifecycleEmitsStructuredRedactedEvents(t *testing.T) {
	var output bytes.Buffer
	lifecycle := NewLifecycle(&output)
	lifecycle.Starting(context.Background())
	lifecycle.ObserveNetworkState(
		context.Background(),
		7,
		appliance.StageReconciling,
		appliance.ModeNone,
		appliance.ReasonInspectingNetwork,
		appliance.EventBoot,
	)
	lifecycle.ObserveNetworkState(
		context.Background(),
		8,
		appliance.StageFailed,
		appliance.ModeNone,
		appliance.ReasonObservationFailed,
		appliance.EventNetworkChanged,
	)
	lifecycle.RecoveryRequested(context.Background())
	lifecycle.ProvisioningAction(context.Background(), true, true)
	lifecycle.ProvisioningAction(context.Background(), true, false)
	lifecycle.ComponentRetry(context.Background(), ComponentReconciler, 2, 3)
	lifecycle.ComponentRecovered(context.Background(), ComponentReconciler)
	lifecycle.Failed(context.Background(), ComponentReconciler, FailureExhausted)

	logs := output.String()
	for _, forbidden := range []string{"hunter2", "freedesktop", "ActiveConnection", "psk="} {
		if strings.Contains(logs, forbidden) {
			t.Fatalf("structured logs exposed %q:\n%s", forbidden, logs)
		}
	}

	wantEvents := map[string]bool{
		"runtime_starting":      false,
		"network_state_changed": false,
		"recovery_requested":    false,
		"provisioning_action":   false,
		"component_retry":       false,
		"component_recovered":   false,
		"runtime_failed":        false,
	}
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid JSON log %q: %v", line, err)
		}
		event, _ := record["event"].(string)
		if _, ok := wantEvents[event]; ok {
			wantEvents[event] = true
		}
		if record["time"] == nil || record["level"] == nil || record["msg"] == nil {
			t.Fatalf("incomplete structured record: %#v", record)
		}
	}
	for event, found := range wantEvents {
		if !found {
			t.Errorf("event %q was not logged", event)
		}
	}
	if strings.Count(logs, `"event":"network_state_changed"`) != 1 {
		t.Fatalf("transient network state was logged:\n%s", logs)
	}
	if strings.Count(logs, `"event":"provisioning_action"`) != 1 {
		t.Fatalf("successful provisioning action was logged:\n%s", logs)
	}
}

func TestRuntimeErrorKeepsCauseButRedactsMessage(t *testing.T) {
	cause := context.Canceled
	err := RedactRuntimeError(cause)
	if err == nil || err.Error() != "appliance runtime failed" {
		t.Fatalf("redacted error = %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "failed") {
		t.Fatalf("redacted error is not actionable: %v", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("redacted error does not unwrap to cause: %v", err)
	}
}
