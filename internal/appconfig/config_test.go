package appconfig

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

// Every rule Decode enforces, with the fixture that violates it and the text
// the error must identify it by. Each entry was previously its own test.
func TestDecodeRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name string
		toml string
		want []string
	}{
		{
			name: "missing schema version",
			toml: `[product]
name = "Display"
`,
			want: []string{"schema_version is required"},
		},
		{
			name: "unknown keys",
			toml: `
schema_version = 1

[network]
interfase = "wlan1"

[branding]
accent_color = "#123456"
`,
			// Both offending keys must be named, not just the first.
			want: []string{"branding.accent_color", "network.interfase"},
		},
		{
			name: "removed handoff hostname",
			toml: `
schema_version = 1

[handoff]
hostname = "display-player"
`,
			want: []string{"handoff.hostname"},
		},
		{
			name: "both production modes disabled",
			toml: `
schema_version = 1

[network]
infrastructure_enabled = false
standalone_enabled = false
`,
			want: []string{"at least one"},
		},
		{
			name: "network address as standalone gateway",
			toml: `
schema_version = 1

[network.standalone]
address = "10.50.0.0/24"
`,
			want: []string{"usable host"},
		},
		{
			name: "broadcast address as standalone gateway",
			toml: `
schema_version = 1

[network.standalone]
address = "10.50.0.255/24"
`,
			want: []string{"usable host"},
		},
		{
			name: "captive public port as listener",
			toml: `
schema_version = 1

[portal]
listener_port = 80
`,
			want: []string{"fixed captive public port"},
		},
		{
			name: "empty admin password file",
			toml: `
schema_version = 1

[portal]
password_file = ""
`,
			want: []string{"portal.password_file"},
		},
		{
			name: "incomplete application handoff",
			toml: `
schema_version = 1

[handoff]
application_label = "Open player"
`,
			want: []string{"configured together"},
		},
		{
			name: "whitespace only application handoff",
			toml: `
schema_version = 1

[handoff]
application_label = "   "
application_url = "http://device.local/"
`,
			want: []string{"configured together"},
		},
		{
			name: "health check without application",
			toml: `
schema_version = 1

[handoff]
health_check_url = "http://127.0.0.1/health"
`,
			want: []string{"requires handoff.application_url"},
		},
		{
			name: "non http static handoff url",
			toml: `
schema_version = 1

[handoff]
application_label = "Open player"
application_url = "file:///etc/passwd"
`,
			want: []string{"absolute HTTP or HTTPS"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeForTest(strings.NewReader(test.toml))
			if err == nil {
				t.Fatalf("Decode() accepted %s", test.name)
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not identify %q", err, want)
				}
			}
		})
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
