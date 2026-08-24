package handoff

import (
	"testing"

	appconfig "github.com/flavorplus/onboardd/internal/config"
)

func TestFromConfigDerivesStableSetupURL(t *testing.T) {
	configured := appconfig.Defaults()
	configured.Handoff.ApplicationLabel = "Open player"
	configured.Handoff.ApplicationURL = "http://lobby-display.local/"
	configured.Handoff.HealthCheckURL = "http://127.0.0.1/health"
	configured.Handoff.ShowStandaloneCredentials = true
	configured.Portal.ListenerPort = 19000

	info, err := FromConfig(configured, "Lobby-Display")
	if err != nil {
		t.Fatal(err)
	}
	if info.SetupURL != "http://lobby-display.local:19000/" {
		t.Fatalf("info = %+v", info)
	}
	if info.Application == nil || info.Application.Label != "Open player" ||
		info.HealthCheckURL != "http://127.0.0.1/health" || !info.ShowStandaloneCredentials {
		t.Fatalf("info = %+v", info)
	}
	if info.Standalone == nil || info.Standalone.SSID != configured.Network.Standalone.SSID ||
		info.Standalone.Password != "" {
		t.Fatalf("standalone = %+v", info.Standalone)
	}
}

func TestFromConfigRejectsInvalidAvahiHostname(t *testing.T) {
	configured := appconfig.Defaults()
	if _, err := FromConfig(configured, "display.local"); err == nil {
		t.Fatal("FromConfig() accepted a multi-label Avahi hostname")
	}
}
