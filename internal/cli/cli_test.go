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
		"checkpoint-create",
		"checkpoint-commit",
		"checkpoint-rollback",
	} {
		if !strings.Contains(stdout.String(), command) {
			t.Errorf("help is missing command %q", command)
		}
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
