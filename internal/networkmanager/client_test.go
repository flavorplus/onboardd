package networkmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestProfilePersistence(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{name: "no backing file", want: "memory"},
		{name: "runtime file", filename: "/run/NetworkManager/system-connections/test.nmconnection", want: "memory"},
		{name: "legacy runtime file", filename: "/var/run/NetworkManager/system-connections/test.nmconnection", want: "memory"},
		{name: "system file", filename: "/etc/NetworkManager/system-connections/test.nmconnection", want: "disk"},
		{name: "vendor file", filename: "/usr/lib/NetworkManager/system-connections/test.nmconnection", want: "disk"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := profilePersistence(test.filename); got != test.want {
				t.Fatalf("profilePersistence(%q) = %q, want %q", test.filename, got, test.want)
			}
		})
	}
}

func TestSupersededProfilesOnlyReturnsOwnedMatchingRoleAndInterface(t *testing.T) {
	profiles := []Profile{
		{UUID: "keep", Interface: "wlan0", Owned: true, Role: RoleProvisioning},
		{UUID: "old-1", Interface: "wlan0", Owned: true, Role: RoleProvisioning},
		{UUID: "old-2", Interface: "wlan0", Owned: true, Role: RoleProvisioning},
		{UUID: "foreign", Interface: "wlan0", Owned: false, Role: RoleProvisioning},
		{UUID: "standalone", Interface: "wlan0", Owned: true, Role: RoleStandalone},
		{UUID: "other-interface", Interface: "wlan1", Owned: true, Role: RoleProvisioning},
	}

	got := supersededProfiles(profiles, profileScope{
		interfaceName: "wlan0",
		role:          RoleProvisioning,
	}, "keep")
	if len(got) != 2 || got[0].UUID != "old-1" || got[1].UUID != "old-2" {
		t.Fatalf("supersededProfiles() = %#v, want old-1 and old-2", got)
	}
}

func TestSupersededProfilesMatchInfrastructureSSID(t *testing.T) {
	profiles := []Profile{
		{UUID: "keep", Interface: "wlan0", SSID: "Office", Owned: true, Role: RoleInfrastructure},
		{UUID: "old", Interface: "wlan0", SSID: "Office", Owned: true, Role: RoleInfrastructure},
		{UUID: "other-network", Interface: "wlan0", SSID: "Guest", Owned: true, Role: RoleInfrastructure},
		{UUID: "foreign", Interface: "wlan0", SSID: "Office", Owned: false, Role: RoleInfrastructure},
		{UUID: "other-interface", Interface: "wlan1", SSID: "Office", Owned: true, Role: RoleInfrastructure},
	}

	got := supersededProfiles(profiles, profileScope{
		interfaceName: "wlan0",
		role:          RoleInfrastructure,
		ssid:          "Office",
	}, "keep")
	if len(got) != 1 || got[0].UUID != "old" {
		t.Fatalf("supersededProfiles() = %#v, want old", got)
	}
}

func TestAutoconnectUpdatesSelectOneProductionMode(t *testing.T) {
	profiles := []Profile{
		{UUID: "infra-enabled", Interface: "wlan0", Owned: true, Role: RoleInfrastructure, Autoconnect: true},
		{UUID: "infra-disabled", Interface: "wlan0", Owned: true, Role: RoleInfrastructure, Autoconnect: false},
		{UUID: "standalone-enabled", Interface: "wlan0", Owned: true, Role: RoleStandalone, Autoconnect: true},
		{UUID: "provisioning", Interface: "wlan0", Owned: true, Role: RoleProvisioning, Autoconnect: false},
		{UUID: "foreign", Interface: "wlan0", Owned: false, Role: RoleInfrastructure, Autoconnect: true},
		{UUID: "other-interface", Interface: "wlan1", Owned: true, Role: RoleStandalone, Autoconnect: true},
	}

	updates := autoconnectUpdates(profiles, "wlan0", RoleInfrastructure)
	if len(updates) != 2 {
		t.Fatalf("autoconnectUpdates() = %#v, want two updates", updates)
	}
	if updates[0].Profile.UUID != "infra-disabled" || !updates[0].Enabled {
		t.Fatalf("first update = %#v, want infrastructure enable", updates[0])
	}
	if updates[1].Profile.UUID != "standalone-enabled" || updates[1].Enabled {
		t.Fatalf("second update = %#v, want standalone disable", updates[1])
	}
}

func TestFinalizeTransitionRejectsUnknownRole(t *testing.T) {
	client := &Client{}
	if err := client.FinalizeTransition(context.Background(), "wlan0", Role("mystery"), "", "keep"); err == nil {
		t.Fatal("unknown role unexpectedly finalized")
	}
}

func TestSettingsFromCallBodyPreservesVariantSignature(t *testing.T) {
	legacyAddresses := dbus.MakeVariantWithSignature(
		[]any{},
		dbus.ParseSignatureMust("a(ayuay)"),
	)
	body := []any{map[string]map[string]dbus.Variant{
		"ipv6": {"addresses": legacyAddresses},
	}}

	settings, err := settingsFromCallBody(body)
	if err != nil {
		t.Fatalf("settingsFromCallBody() error = %v", err)
	}
	if got := settings["ipv6"]["addresses"].Signature().String(); got != "a(ayuay)" {
		t.Fatalf("legacy signature = %q, want a(ayuay)", got)
	}
}

func TestObjectGoneDBusErrors(t *testing.T) {
	for _, name := range []string{
		"org.freedesktop.DBus.Error.UnknownObject",
		"org.freedesktop.DBus.Error.UnknownInterface",
		"org.freedesktop.DBus.Error.UnknownMethod",
		"org.freedesktop.NetworkManager.UnknownConnection",
	} {
		t.Run(name, func(t *testing.T) {
			dbusErr := dbus.Error{Name: name}
			if !isObjectGone(dbusErr) || !isObjectGone(&dbusErr) {
				t.Fatalf("isObjectGone(%q) = false", name)
			}
		})
	}
	if isObjectGone(errors.New("permission denied")) {
		t.Fatal("unrelated error classified as a removed object")
	}
}

func TestParseDeviceStateReason(t *testing.T) {
	state, reason, err := parseDeviceStateReason([]any{
		uint32(DeviceStateFailed),
		uint32(DeviceStateReasonSSIDNotFound),
	})
	if err != nil {
		t.Fatalf("parseDeviceStateReason() error = %v", err)
	}
	if state != DeviceStateFailed || reason != DeviceStateReasonSSIDNotFound {
		t.Fatalf("parseDeviceStateReason() = %s, %s", state, reason)
	}
	if got := reason.String(); got != "ssid-not-found" {
		t.Fatalf("reason.String() = %q, want ssid-not-found", got)
	}
}

func TestParseDeviceStateReasonRejectsMalformedTuple(t *testing.T) {
	for _, value := range []any{
		"failed",
		[]any{uint32(DeviceStateFailed)},
		[]any{uint32(DeviceStateFailed), "ssid-not-found"},
	} {
		if _, _, err := parseDeviceStateReason(value); err == nil {
			t.Fatalf("parseDeviceStateReason(%#v) error = nil", value)
		}
	}
}
