package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := stdout.String(); got != "onboardd development\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestDebugHelp(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"debug", "help"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, command := range []string{
		"status",
		"profiles",
		"profile-delete",
		"scan",
		"connect",
		"provisioning-start",
		"standalone-start",
		"watch",
		"reconcile",
		"checkpoint-create",
		"checkpoint-commit",
		"checkpoint-rollback",
	} {
		if !strings.Contains(stdout.String(), command) {
			t.Errorf("help is missing command %q", command)
		}
	}
}

func TestReconcileRejectsInvalidPolicyBeforeDBus(t *testing.T) {
	err := Run(
		context.Background(),
		[]string{"debug", "reconcile", "--requirement", "sometimes"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "unknown connectivity requirement") {
		t.Fatalf("error = %v, want connectivity requirement validation", err)
	}
}

func TestModeChangeRequiresActivationWaitBeforeDBus(t *testing.T) {
	err := Run(
		context.Background(),
		[]string{"debug", "connect", "--ssid", "Office", "--open", "--wait", "0", "--yes"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "--wait must be positive") {
		t.Fatalf("error = %v, want positive activation wait validation", err)
	}
}

func TestDisruptiveCommandRequiresConfirmationBeforeDBus(t *testing.T) {
	err := Run(
		context.Background(),
		[]string{"debug", "connect", "--ssid", "Office", "--open"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %v, want explicit confirmation error", err)
	}
}

func TestCheckpointCommitRequiresConfirmationBeforeDBus(t *testing.T) {
	err := Run(
		context.Background(),
		[]string{
			"debug",
			"checkpoint-commit",
			"--path",
			"/org/freedesktop/NetworkManager/Checkpoint/1",
		},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %v, want explicit confirmation error", err)
	}
}
