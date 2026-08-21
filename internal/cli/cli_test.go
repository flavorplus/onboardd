package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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
		"captive-start",
		"setup-start",
		"connect-protected",
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

func TestSetupStartRequiresAnEnabledModeBeforeDBus(t *testing.T) {
	err := Run(
		context.Background(),
		[]string{
			"debug",
			"setup-start",
			"--network-enabled=false",
			"--standalone-enabled=false",
			"--yes",
		},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("error = %v, want enabled-mode validation", err)
	}
}

func TestSetupStartRejectsFrontendSourceBeforeDBus(t *testing.T) {
	frontendDirectory := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(frontendDirectory, "index.html"),
		[]byte(`<script type="module" src="/src/main.ts"></script>`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	passwordPath := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(passwordPath, []byte("test-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := Run(
		context.Background(),
		[]string{
			"debug",
			"setup-start",
			"--ssid",
			"Onboardd-Setup-Test",
			"--password-file",
			passwordPath,
			"--frontend-dir",
			frontendDirectory,
			"--standalone-enabled=false",
			"--yes",
		},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "compiled frontend/dist") {
		t.Fatalf("error = %v, want source frontend validation", err)
	}
}

func TestCaptiveStartValidatesHTTPPortBeforeDBus(t *testing.T) {
	err := Run(
		context.Background(),
		[]string{"debug", "captive-start", "--http-port", "0", "--yes"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "--http-port") {
		t.Fatalf("error = %v, want HTTP port validation", err)
	}
}

func TestCaptiveStartRequiresSeparateListenerPort(t *testing.T) {
	err := Run(
		context.Background(),
		[]string{
			"debug",
			"captive-start",
			"--http-port",
			"8080",
			"--listener-http-port",
			"8080",
			"--yes",
		},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("error = %v, want separate listener port validation", err)
	}
}

func TestProtectedConnectValidatesRequirementBeforeDBus(t *testing.T) {
	err := Run(
		context.Background(),
		[]string{"debug", "connect-protected", "--requirement", "sometimes", "--yes"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "unknown connectivity requirement") {
		t.Fatalf("error = %v, want requirement validation", err)
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
