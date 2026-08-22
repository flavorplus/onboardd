package cli

import (
	"bytes"
	"context"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	appconfig "github.com/flavorplus/onboardd/internal/config"
	"github.com/flavorplus/onboardd/internal/connectivity"
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
		"config",
		"status",
		"profiles",
		"profile-delete",
		"scan",
		"watch",
		"reconcile",
	} {
		if !strings.Contains(stdout.String(), command) {
			t.Errorf("help is missing command %q", command)
		}
	}
	for _, retired := range []string{"connect", "captive-start", "setup-start", "checkpoint-create"} {
		if strings.Contains(stdout.String(), "debug "+retired) {
			t.Errorf("help still advertises retired command %q", retired)
		}
	}
}

func TestRetiredDebugMutationCommandIsRejected(t *testing.T) {
	err := Run(
		context.Background(),
		[]string{"debug", "setup-start"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "unknown debug command") {
		t.Fatalf("error = %v", err)
	}
}

func TestRootHelpIncludesConfiguredSetup(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"help"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "onboardd setup") || !strings.Contains(stdout.String(), "embedded setup portal") {
		t.Fatalf("root help does not describe configured setup:\n%s", stdout.String())
	}
}

func TestSetupHelpIsSuccessful(t *testing.T) {
	var stderr bytes.Buffer
	if err := Run(context.Background(), []string{"setup", "-h"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(stderr.String(), "-config") || strings.Contains(stderr.String(), "password") {
		t.Fatalf("setup help is missing operational flags or exposes secrets:\n%s", stderr.String())
	}
}

func TestSetupRequiresExplicitFileToExistBeforeRuntime(t *testing.T) {
	err := Run(
		context.Background(),
		[]string{"setup", "--config", filepath.Join(t.TempDir(), "missing.toml")},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "open configuration") {
		t.Fatalf("error = %v", err)
	}
}

func TestSetupRejectsListenerCollisionBeforeRuntime(t *testing.T) {
	err := Run(
		context.Background(),
		[]string{"setup", "--listener-port", "80"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "fixed captive public port") {
		t.Fatalf("error = %v", err)
	}
}

func TestInteractiveSetupValidatesProfilesBeforeDBus(t *testing.T) {
	err := runInteractiveSetup(context.Background(), interactiveSetupOptions{
		Interface:           "wlan0",
		ProvisioningSSID:    "Setup",
		ProvisioningPSK:     "short",
		ProvisioningAddress: mustPrefix(t, "10.42.0.1/24"),
		Band:                "bg",
		PublicHTTPPort:      80,
		ListenerHTTPPort:    18080,
		PortalURL:           "http://10.42.0.1/",
		PortalOrigin:        "http://10.42.0.1",
		DNSConfigPath:       "/unused",
		Assets:              fstest.MapFS{"index.html": {Data: []byte("compiled")}},
		NetworkEnabled:      true,
		Requirement:         connectivity.RequirementLocal,
		ActivationWait:      30,
		RollbackAfter:       90,
		RestorationWait:     30,
	}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "invalid provisioning network") {
		t.Fatalf("error = %v", err)
	}
}

func TestReadSecurePasswordFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte("test-password\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecurePasswordFile(path); err == nil || !strings.Contains(err.Error(), "mode 0600") {
		t.Fatalf("error = %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	password, err := readSecurePasswordFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if password != "test-password" {
		t.Fatalf("password = %q", password)
	}
}

func TestConfiguredSetupOptionsUseRenderedConfigAndEmbeddedAssets(t *testing.T) {
	directory := t.TempDir()
	provisioningPassword := filepath.Join(directory, "provisioning-password")
	standalonePassword := filepath.Join(directory, "standalone-password")
	for _, path := range []string{provisioningPassword, standalonePassword} {
		if err := os.WriteFile(path, []byte("test-password\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	configured := appconfig.Defaults()
	configured.Product.Name = "InkyPi"
	configured.Network.Interface = "wlan-test"
	configured.Network.Provisioning.PasswordFile = provisioningPassword
	configured.Network.Standalone.PasswordFile = standalonePassword
	configured.Portal.ListenerPort = 19000
	rendered, err := appconfig.RenderTemplates(
		configured,
		appconfig.Identity{DeviceID: "AB12CD34", Hostname: "inkypi"},
	)
	if err != nil {
		t.Fatal(err)
	}
	options, err := configuredSetupOptions(rendered)
	if err != nil {
		t.Fatal(err)
	}
	if options.Interface != "wlan-test" || options.ListenerHTTPPort != 19000 {
		t.Fatalf("runtime options = %+v", options)
	}
	if options.ProvisioningSSID != "InkyPi-Setup-AB12CD34" || options.StandaloneSSID != "InkyPi-AB12CD34" {
		t.Fatalf("rendered SSIDs = %q, %q", options.ProvisioningSSID, options.StandaloneSSID)
	}
	if options.Branding.Branding.ProductName != "InkyPi" || options.ProvisioningPSK != "test-password" {
		t.Fatalf("branding or secret mapping = %+v", options)
	}
	if _, err := fs.Stat(options.Assets, "index.html"); err != nil {
		t.Fatalf("embedded assets: %v", err)
	}
}

func mustPrefix(t *testing.T, value string) netip.Prefix {
	t.Helper()
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		t.Fatal(err)
	}
	return prefix
}

func TestDebugConfigAppliesEnvironmentThenCLI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
schema_version = 1

[product]
name = "Configured product"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ONBOARDD_NETWORK_INTERFACE", "from-environment")
	t.Setenv("ONBOARDD_NETWORK_REQUIREMENT", "internet")

	var stdout bytes.Buffer
	err := Run(
		context.Background(),
		[]string{
			"debug",
			"config",
			"--config",
			path,
			"--network-interface",
			"from-cli",
		},
		&stdout,
		&bytes.Buffer{},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, expected := range []string{
		`name = "Configured product"`,
		`interface = "from-cli"`,
		`requirement = "internet"`,
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("resolved output does not contain %q:\n%s", expected, stdout.String())
		}
	}
}

func TestDebugConfigRequiresExplicitFileToExist(t *testing.T) {
	err := Run(
		context.Background(),
		[]string{"debug", "config", "--config", filepath.Join(t.TempDir(), "missing.toml")},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "open configuration") {
		t.Fatalf("error = %v", err)
	}
}

func TestDebugConfigRendersWithExplicitDevelopmentIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(`
schema_version = 1

[product]
name = "InkyPi"
device_name = "Kitchen"

[branding.text]
title = "Set up {{ .DeviceName }}"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err := Run(
		context.Background(),
		[]string{
			"debug", "config",
			"--config", path,
			"--render",
			"--device-id", "AB12CD34",
			"--hostname", "inkypi",
		},
		&stdout,
		&bytes.Buffer{},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, expected := range []string{
		`title = "Set up Kitchen"`,
		`ssid = "InkyPi-Setup-AB12CD34"`,
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("rendered output does not contain %q:\n%s", expected, stdout.String())
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
