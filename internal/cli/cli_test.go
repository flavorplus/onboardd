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

	"github.com/flavorplus/onboardd/internal/appconfig"
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

func TestRootHelpIncludesConfiguredCommands(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"help"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, expected := range []string{"onboardd run", "onboardd recover", "manual recovery"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("root help is missing %q:\n%s", expected, stdout.String())
		}
	}
}

func TestRecoverRejectsArgumentsBeforeContactingRuntime(t *testing.T) {
	err := Run(
		context.Background(),
		[]string{"recover", "now"},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunHelpIsSuccessful(t *testing.T) {
	var stderr bytes.Buffer
	if err := Run(context.Background(), []string{"run", "-h"}, &bytes.Buffer{}, &stderr); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(stderr.String(), "-config") || strings.Contains(stderr.String(), "password") {
		t.Fatalf("run help is missing operational flags or exposes secrets:\n%s", stderr.String())
	}
}

func TestRunRequiresExplicitFileToExistBeforeRuntime(t *testing.T) {
	err := Run(
		context.Background(),
		[]string{"run", "--config", filepath.Join(t.TempDir(), "missing.toml")},
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil || !strings.Contains(err.Error(), "open configuration") {
		t.Fatalf("error = %v", err)
	}
}

func TestManagedApplianceValidatesProfilesBeforeDBus(t *testing.T) {
	err := runManagedAppliance(context.Background(), setupOptions{
		Interface:           "wlan0",
		ProvisioningSSID:    "Setup",
		ProvisioningPSK:     "short",
		ProvisioningAddress: mustPrefix(t, "10.42.0.1/24"),
		Band:                "bg",
		PublicHTTPPort:      80,
		ListenerHTTPPort:    18080,
		AdminPassword:       "test-admin-password",
		Assets:              fstest.MapFS{"index.html": {Data: []byte("compiled")}},
		NetworkEnabled:      true,
	}, &bytes.Buffer{}, nil, nil)
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

func TestSetupOptionsUseRenderedConfigAndEmbeddedAssets(t *testing.T) {
	directory := t.TempDir()
	adminPassword := filepath.Join(directory, "admin-password")
	provisioningPassword := filepath.Join(directory, "provisioning-password")
	standalonePassword := filepath.Join(directory, "standalone-password")
	for _, path := range []string{adminPassword, provisioningPassword, standalonePassword} {
		if err := os.WriteFile(path, []byte("test-password\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	configured := appconfig.Defaults()
	configured.Product.Name = "InkyPi"
	configured.Network.Interface = "wlan-test"
	configured.Portal.PasswordFile = adminPassword
	configured.Network.Provisioning.PasswordFile = provisioningPassword
	configured.Network.Standalone.PasswordFile = standalonePassword
	configured.Handoff.ShowStandaloneCredentials = true
	configured.Portal.ListenerPort = 19000
	rendered, err := appconfig.RenderTemplates(
		configured,
		appconfig.Identity{DeviceID: "AB12CD34", Hostname: "inkypi"},
	)
	if err != nil {
		t.Fatal(err)
	}
	options, err := setupOptionsFromConfig(rendered, "inkypi")
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
	if options.AdminPassword != "test-password" {
		t.Fatalf("admin password was not loaded from its secure file")
	}
	if options.Branding.Handoff == nil || options.Branding.Handoff.SetupURL != "http://inkypi.local:19000/" {
		t.Fatalf("handoff mapping = %+v", options.Branding.Handoff)
	}
	if options.Branding.Handoff.Standalone == nil ||
		options.Branding.Handoff.Standalone.SSID != "InkyPi-AB12CD34" ||
		options.Branding.Handoff.Standalone.Password != "test-password" {
		t.Fatalf("standalone handoff = %+v", options.Branding.Handoff.Standalone)
	}
	if _, err := fs.Stat(options.Assets, "index.html"); err != nil {
		t.Fatalf("embedded assets: %v", err)
	}
	if _, err := fs.Stat(options.Assets, "landing.html"); err != nil {
		t.Fatalf("embedded landing page: %v", err)
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
