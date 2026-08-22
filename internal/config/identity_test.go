package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveDeviceIDIsStableAndApplicationSpecific(t *testing.T) {
	const machineID = "0123456789abcdef0123456789abcdef"
	first, err := deriveDeviceID(machineID + "\n")
	if err != nil {
		t.Fatalf("deriveDeviceID() error = %v", err)
	}
	second, err := deriveDeviceID(machineID)
	if err != nil {
		t.Fatalf("deriveDeviceID() error = %v", err)
	}
	if first != second {
		t.Fatalf("device IDs differ: %q and %q", first, second)
	}
	if first != "SSWSBB6V" {
		t.Fatalf("device ID = %q; changing it would rename existing appliance networks", first)
	}
	if len(first) != 8 || strings.Contains(strings.ToLower(machineID), strings.ToLower(first)) {
		t.Fatalf("derived device ID %q is not a short application-specific value", first)
	}
}

func TestDeriveDeviceIDChangesWithMachine(t *testing.T) {
	first, err := deriveDeviceID("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	second, err := deriveDeviceID("1123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("different machine IDs produced %q", first)
	}
}

func TestLoadIdentityUsesFallbackMachineIDPath(t *testing.T) {
	directory := t.TempDir()
	validPath := filepath.Join(directory, "machine-id")
	if err := os.WriteFile(validPath, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := loadIdentity(
		[]string{filepath.Join(directory, "missing"), validPath},
		func() (string, error) { return "display-player", nil },
	)
	if err != nil {
		t.Fatalf("loadIdentity() error = %v", err)
	}
	if identity.DeviceID == "" || identity.Hostname != "display-player" {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestLoadIdentityRejectsMalformedMachineID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "machine-id")
	if err := os.WriteFile(path, []byte("not-a-machine-id\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadIdentity([]string{path}, func() (string, error) { return "display", nil })
	if err == nil || !strings.Contains(err.Error(), "32 hexadecimal") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadIdentityReportsHostnameFailure(t *testing.T) {
	_, err := loadIdentity(nil, func() (string, error) { return "", errors.New("unavailable") })
	if err == nil || !strings.Contains(err.Error(), "read hostname") {
		t.Fatalf("error = %v", err)
	}
}
