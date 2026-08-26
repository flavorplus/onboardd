package config

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flavorplus/onboardd/internal/connectivity"
)

func TestDecodeOverlaysDefaults(t *testing.T) {
	loaded, err := decodeForTest(strings.NewReader(`
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
	_, err := decodeForTest(strings.NewReader(`[product]
name = "Display"
`))
	if err == nil || !strings.Contains(err.Error(), "schema_version is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsUnknownKeys(t *testing.T) {
	_, err := decodeForTest(strings.NewReader(`
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

func TestDecodeRejectsRemovedHandoffHostname(t *testing.T) {
	_, err := decodeForTest(strings.NewReader(`
schema_version = 1

[handoff]
hostname = "display-player"
`))
	if err == nil || !strings.Contains(err.Error(), "handoff.hostname") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsDisabledProductionModes(t *testing.T) {
	_, err := decodeForTest(strings.NewReader(`
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
	_, err := decodeForTest(strings.NewReader(`
schema_version = 1

[network.standalone]
address = "10.50.0.0/24"
`))
	if err == nil || !strings.Contains(err.Error(), "usable host") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsBroadcastAddressForStandaloneGateway(t *testing.T) {
	_, err := decodeForTest(strings.NewReader(`
schema_version = 1

[network.standalone]
address = "10.50.0.255/24"
`))
	if err == nil || !strings.Contains(err.Error(), "usable host") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsCaptivePublicPortAsListener(t *testing.T) {
	_, err := decodeForTest(strings.NewReader(`
schema_version = 1

[portal]
listener_port = 80
`))
	if err == nil || !strings.Contains(err.Error(), "fixed captive public port") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsEmptyAdminPasswordFile(t *testing.T) {
	_, err := decodeForTest(strings.NewReader(`
schema_version = 1

[portal]
password_file = ""
`))
	if err == nil || !strings.Contains(err.Error(), "portal.password_file") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsIncompleteApplicationHandoff(t *testing.T) {
	_, err := decodeForTest(strings.NewReader(`
schema_version = 1

[handoff]
application_label = "Open player"
`))
	if err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsWhitespaceOnlyApplicationHandoff(t *testing.T) {
	_, err := decodeForTest(strings.NewReader(`
schema_version = 1

[handoff]
application_label = "   "
application_url = "http://device.local/"
`))
	if err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsHealthCheckWithoutApplication(t *testing.T) {
	_, err := decodeForTest(strings.NewReader(`
schema_version = 1

[handoff]
health_check_url = "http://127.0.0.1/health"
`))
	if err == nil || !strings.Contains(err.Error(), "requires handoff.application_url") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsInvalidStaticHandoffURL(t *testing.T) {
	_, err := decodeForTest(strings.NewReader(`
schema_version = 1

[handoff]
application_label = "Open player"
application_url = "file:///etc/passwd"
`))
	if err == nil || !strings.Contains(err.Error(), "absolute HTTP or HTTPS") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadFileIncludesPathInError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("schema_version = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve(ResolveOptions{ConfigPath: path})
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "unsupported schema_version") {
		t.Fatalf("error = %v", err)
	}
}

func TestReferenceConfigurationLoads(t *testing.T) {
	loaded, err := Resolve(ResolveOptions{ConfigPath: filepath.Join("..", "..", "config", "example.toml")})
	if err != nil {
		t.Fatalf("Resolve(example.toml) error = %v", err)
	}
	if loaded.Product.Name != "Display Player" {
		t.Fatalf("Product.Name = %q", loaded.Product.Name)
	}
}

func TestResolveAppliesOverridesAfterFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
schema_version = 1

[network]
interface = "from-toml"
requirement = "local"
standalone_enabled = false

[portal]
listener_port = 18001
`), 0o600); err != nil {
		t.Fatal(err)
	}
	interfaceOverride := "from-cli"
	requirementOverride := connectivity.RequirementLocal
	standaloneOverride := true
	portOverride := uint16(18003)

	resolved, err := Resolve(ResolveOptions{
		ConfigPath: path,
		Overrides: Overrides{
			NetworkInterface:   &interfaceOverride,
			NetworkRequirement: &requirementOverride,
			StandaloneEnabled:  &standaloneOverride,
			ListenerPort:       &portOverride,
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Network.Interface != "from-cli" {
		t.Errorf("Interface = %q", resolved.Network.Interface)
	}
	if resolved.Network.Requirement != connectivity.RequirementLocal {
		t.Errorf("Requirement = %q", resolved.Network.Requirement)
	}
	if !resolved.Network.StandaloneEnabled {
		t.Error("StandaloneEnabled = false")
	}
	if resolved.Portal.ListenerPort != 18003 {
		t.Errorf("ListenerPort = %d", resolved.Portal.ListenerPort)
	}
}

func TestResolveAllowsOverrideToRepairFileCombination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
schema_version = 1

[network]
infrastructure_enabled = false
standalone_enabled = false
`), 0o600); err != nil {
		t.Fatal(err)
	}

	standaloneEnabled := true
	resolved, err := Resolve(ResolveOptions{
		ConfigPath: path,
		Overrides:  Overrides{StandaloneEnabled: &standaloneEnabled},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !resolved.Network.StandaloneEnabled {
		t.Fatal("override did not repair the final mode combination")
	}
}

func TestResolveUsesDefaultsWhenOptionalFileIsMissing(t *testing.T) {
	resolved, err := Resolve(ResolveOptions{
		ConfigPath:     filepath.Join(t.TempDir(), "missing.toml"),
		ConfigOptional: true,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Network.Interface != "wlan0" {
		t.Fatalf("Interface = %q", resolved.Network.Interface)
	}
}

func decodeForTest(reader io.Reader) (Config, error) {
	resolved := Defaults()
	if err := decodeInto(reader, &resolved); err != nil {
		return Config{}, err
	}
	if err := resolved.Validate(); err != nil {
		return Config{}, err
	}
	return resolved, nil
}
