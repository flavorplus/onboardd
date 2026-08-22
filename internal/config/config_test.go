package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeOverlaysDefaults(t *testing.T) {
	loaded, err := Decode(strings.NewReader(`
schema_version = 1

[product]
name = "InkyPi"

[network]
requirement = "internet"
standalone_enabled = false

[portal]
listener_port = 19000
`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if loaded.Product.Name != "InkyPi" {
		t.Fatalf("Product.Name = %q", loaded.Product.Name)
	}
	if loaded.Product.DeviceName != "Device" {
		t.Fatalf("Product.DeviceName = %q, want default", loaded.Product.DeviceName)
	}
	if loaded.Network.StandaloneEnabled {
		t.Fatal("standalone mode remained enabled")
	}
	if loaded.Portal.ListenerPort != 19000 {
		t.Fatalf("ListenerPort = %d", loaded.Portal.ListenerPort)
	}
}

func TestDecodeRequiresSchemaVersion(t *testing.T) {
	_, err := Decode(strings.NewReader(`[product]
name = "Display"
`))
	if err == nil || !strings.Contains(err.Error(), "schema_version is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsUnknownKeys(t *testing.T) {
	_, err := Decode(strings.NewReader(`
schema_version = 1

[network]
interfase = "wlan1"

[branding]
accent_color = "#123456"
`))
	if err == nil {
		t.Fatal("Decode() accepted unknown keys")
	}
	for _, key := range []string{"branding.accent_color", "network.interfase"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not identify %q", err, key)
		}
	}
}

func TestDecodeRejectsDisabledProductionModes(t *testing.T) {
	_, err := Decode(strings.NewReader(`
schema_version = 1

[network]
infrastructure_enabled = false
standalone_enabled = false
`))
	if err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsNetworkAddressForStandaloneGateway(t *testing.T) {
	_, err := Decode(strings.NewReader(`
schema_version = 1

[network.standalone]
address = "10.50.0.0/24"
`))
	if err == nil || !strings.Contains(err.Error(), "usable host") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsBroadcastAddressForStandaloneGateway(t *testing.T) {
	_, err := Decode(strings.NewReader(`
schema_version = 1

[network.standalone]
address = "10.50.0.255/24"
`))
	if err == nil || !strings.Contains(err.Error(), "usable host") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsCaptivePublicPortAsListener(t *testing.T) {
	_, err := Decode(strings.NewReader(`
schema_version = 1

[portal]
listener_port = 80
`))
	if err == nil || !strings.Contains(err.Error(), "fixed captive public port") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadFileIncludesPathInError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("schema_version = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "unsupported schema_version") {
		t.Fatalf("error = %v", err)
	}
}

func TestReferenceConfigurationLoads(t *testing.T) {
	loaded, err := LoadFile(filepath.Join("..", "..", "config", "example.toml"))
	if err != nil {
		t.Fatalf("LoadFile(example.toml) error = %v", err)
	}
	if loaded.Product.Name != "Display Player" {
		t.Fatalf("Product.Name = %q", loaded.Product.Name)
	}
}
