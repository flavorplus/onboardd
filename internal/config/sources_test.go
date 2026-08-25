package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flavorplus/onboardd/internal/connectivity"
)

func TestResolveAppliesSourcesInPrecedenceOrder(t *testing.T) {
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
		Environment: []string{
			"ONBOARDD_NETWORK_INTERFACE=from-environment",
			"ONBOARDD_NETWORK_REQUIREMENT=internet",
			"ONBOARDD_PORTAL_LISTENER_PORT=18002",
		},
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

func TestResolveAllowsLaterSourceToRepairFileCombination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
schema_version = 1

[network]
infrastructure_enabled = false
standalone_enabled = false
`), 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, err := Resolve(ResolveOptions{
		ConfigPath:  path,
		Environment: []string{"ONBOARDD_NETWORK_STANDALONE_ENABLED=true"},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !resolved.Network.StandaloneEnabled {
		t.Fatal("environment did not repair the final mode combination")
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

func TestResolveRejectsUnknownOnboarddEnvironmentVariable(t *testing.T) {
	_, err := Resolve(ResolveOptions{Environment: []string{"ONBOARDD_NETWORK_INTERFASE=wlan1"}})
	if err == nil || !strings.Contains(err.Error(), "ONBOARDD_NETWORK_INTERFASE") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveRejectsRemovedHandoffHostnameEnvironment(t *testing.T) {
	_, err := Resolve(ResolveOptions{Environment: []string{"ONBOARDD_HANDOFF_HOSTNAME=display-player"}})
	if err == nil || !strings.Contains(err.Error(), "ONBOARDD_HANDOFF_HOSTNAME") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveRejectsInvalidEnvironmentValue(t *testing.T) {
	_, err := Resolve(ResolveOptions{Environment: []string{"ONBOARDD_NETWORK_STANDALONE_ENABLED=maybe"}})
	if err == nil || !strings.Contains(err.Error(), "must be true or false") {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveAppliesHandoffEnvironment(t *testing.T) {
	resolved, err := Resolve(ResolveOptions{Environment: []string{
		"ONBOARDD_HANDOFF_APPLICATION_LABEL=Open player",
		"ONBOARDD_HANDOFF_APPLICATION_URL=http://display-player.local/",
		"ONBOARDD_HANDOFF_HEALTH_CHECK_URL=http://127.0.0.1/health",
		"ONBOARDD_HANDOFF_SHOW_STANDALONE_CREDENTIALS=true",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Handoff.ApplicationURL != "http://display-player.local/" ||
		!resolved.Handoff.ShowStandaloneCredentials {
		t.Fatalf("handoff = %+v", resolved.Handoff)
	}
}

func TestResolveAppliesGPIORecoveryEnvironment(t *testing.T) {
	resolved, err := Resolve(ResolveOptions{Environment: []string{
		"ONBOARDD_RECOVERY_GPIO_ENABLED=true",
		"ONBOARDD_RECOVERY_GPIO_CHIP=/dev/gpiochip2",
		"ONBOARDD_RECOVERY_GPIO_LINE=23",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Recovery.GPIO.Enabled || resolved.Recovery.GPIO.Chip != "/dev/gpiochip2" ||
		resolved.Recovery.GPIO.Line != 23 {
		t.Fatalf("Recovery.GPIO = %+v", resolved.Recovery.GPIO)
	}
}
