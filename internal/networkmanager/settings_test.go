package networkmanager

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

const testUUID = "12345678-1234-4234-8234-123456789abc"

func TestBuildInfrastructureSettingsSecured(t *testing.T) {
	settings, uuid, err := BuildInfrastructureSettings(InfrastructureOptions{
		UUID:        testUUID,
		Interface:   "wlan0",
		SSID:        "Office",
		Password:    "correct-horse",
		Autoconnect: true,
		Priority:    20,
	})
	if err != nil {
		t.Fatalf("BuildInfrastructureSettings() error = %v", err)
	}
	if uuid != testUUID {
		t.Fatalf("uuid = %q, want %q", uuid, testUUID)
	}
	if got := dbus.SignatureOf(settings).String(); got != "a{sa{sv}}" {
		t.Fatalf("D-Bus signature = %q, want a{sa{sv}}", got)
	}

	assertVariant(t, settings, "connection", "type", "802-11-wireless")
	assertVariant(t, settings, "connection", "interface-name", "wlan0")
	assertVariant(t, settings, "connection", "autoconnect", true)
	assertVariant(t, settings, "connection", "autoconnect-priority", int32(20))
	assertVariant(t, settings, "802-11-wireless", "mode", "infrastructure")
	assertVariant(t, settings, "802-11-wireless-security", "key-mgmt", "wpa-psk")
	assertVariant(t, settings, "802-11-wireless-security", "psk", "correct-horse")
	assertVariant(t, settings, "ipv4", "method", "auto")
	assertVariant(t, settings, "ipv6", "method", "auto")

	metadata := variantMapStringString(settings["user"]["data"])
	if metadata[ownerKey] != ownerName || metadata[roleKey] != string(RoleInfrastructure) {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestBuildInfrastructureSettingsMarksUncommittedCandidate(t *testing.T) {
	settings, _, err := BuildInfrastructureSettings(InfrastructureOptions{
		UUID:      testUUID,
		Interface: "wlan0",
		SSID:      "Office",
		Password:  "correct-horse",
		Pending:   true,
	})
	if err != nil {
		t.Fatalf("BuildInfrastructureSettings() error = %v", err)
	}
	metadata := variantMapStringString(settings["user"]["data"])
	if metadata[pendingKey] != "true" {
		t.Fatalf("pending metadata = %q, want true", metadata[pendingKey])
	}

	profile := Profile{
		ID: "Office profile", UUID: testUUID, Interface: "wlan0", SSID: "Office",
		Priority: 20, Owned: true, Role: RoleInfrastructure, Pending: true,
	}
	rebuilt, err := rebuildOwnedSettings(profile, settings, settings, true)
	if err != nil {
		t.Fatalf("rebuildOwnedSettings() error = %v", err)
	}
	committed := variantMapStringString(rebuilt["user"]["data"])
	if _, exists := committed[pendingKey]; exists {
		t.Fatalf("committed metadata retained pending marker: %#v", committed)
	}
}

func TestBuildInfrastructureSettingsOpen(t *testing.T) {
	settings, _, err := BuildInfrastructureSettings(InfrastructureOptions{
		UUID:      testUUID,
		Interface: "wlan0",
		SSID:      "Guest",
		Open:      true,
	})
	if err != nil {
		t.Fatalf("BuildInfrastructureSettings() error = %v", err)
	}
	if _, exists := settings["802-11-wireless-security"]; exists {
		t.Fatal("open profile unexpectedly contains wireless security settings")
	}
}

func TestBuildAccessPointSettings(t *testing.T) {
	settings, uuid, err := BuildAccessPointSettings(AccessPointOptions{
		UUID:        testUUID,
		Interface:   "wlan0",
		SSID:        "Display-A42F",
		Password:    "display-pass",
		Address:     "10.42.0.1/24",
		Role:        RoleStandalone,
		Autoconnect: true,
		Priority:    999,
		Band:        "bg",
	})
	if err != nil {
		t.Fatalf("BuildAccessPointSettings() error = %v", err)
	}
	if uuid != testUUID {
		t.Fatalf("uuid = %q, want %q", uuid, testUUID)
	}
	if got := dbus.SignatureOf(settings).String(); got != "a{sa{sv}}" {
		t.Fatalf("D-Bus signature = %q, want a{sa{sv}}", got)
	}

	assertVariant(t, settings, "802-11-wireless", "mode", "ap")
	assertVariant(t, settings, "802-11-wireless", "band", "bg")
	assertVariant(t, settings, "connection", "autoconnect", true)
	assertVariant(t, settings, "connection", "autoconnect-priority", int32(999))
	assertVariant(t, settings, "ipv4", "method", "shared")
	assertVariant(t, settings, "ipv6", "method", "disabled")
	addresses, ok := settings["ipv4"]["address-data"].Value().([]map[string]dbus.Variant)
	if !ok || len(addresses) != 1 {
		t.Fatalf("address-data = %#v", settings["ipv4"]["address-data"].Value())
	}
	if got := variantString(addresses[0]["address"]); got != "10.42.0.1" {
		t.Fatalf("address = %q, want 10.42.0.1", got)
	}
	if got, ok := addresses[0]["prefix"].Value().(uint32); !ok || got != 24 {
		t.Fatalf("prefix = %#v, want uint32(24)", addresses[0]["prefix"].Value())
	}

	metadata := variantMapStringString(settings["user"]["data"])
	if metadata[roleKey] != string(RoleStandalone) {
		t.Fatalf("role metadata = %q, want standalone", metadata[roleKey])
	}
}

func TestRebuildOwnedInfrastructureSettings(t *testing.T) {
	current, _, err := BuildInfrastructureSettings(InfrastructureOptions{
		UUID:        testUUID,
		Interface:   "wlan0",
		SSID:        "Office",
		Password:    "original-password",
		Hidden:      true,
		Autoconnect: false,
		Priority:    20,
	})
	if err != nil {
		t.Fatal(err)
	}
	delete(current["802-11-wireless-security"], "psk")
	current["ipv6"]["addresses"] = dbus.MakeVariantWithSignature(
		[]any{},
		dbus.ParseSignatureMust("a(ayuay)"),
	)
	secrets := Settings{
		"802-11-wireless-security": {"psk": dbus.MakeVariant("secret-password")},
	}
	profile := Profile{
		ID: "Office profile", UUID: testUUID, Interface: "wlan0", SSID: "Office",
		Priority: 20, Owned: true, Role: RoleInfrastructure,
	}

	rebuilt, err := rebuildOwnedSettings(profile, current, secrets, true)
	if err != nil {
		t.Fatalf("rebuildOwnedSettings() error = %v", err)
	}
	assertVariant(t, rebuilt, "connection", "autoconnect", true)
	assertVariant(t, rebuilt, "802-11-wireless", "hidden", true)
	assertVariant(t, rebuilt, "802-11-wireless-security", "psk", "secret-password")
	if _, exists := rebuilt["ipv6"]["addresses"]; exists {
		t.Fatal("rebuilt settings retained NetworkManager legacy property")
	}
}

func TestRebuildOwnedStandaloneSettings(t *testing.T) {
	current, _, err := BuildAccessPointSettings(AccessPointOptions{
		UUID: testUUID, Interface: "wlan0", SSID: "Display-A42F",
		Password: "original-password", Address: "10.42.0.1/24",
		Role: RoleStandalone, Autoconnect: true, Priority: 999, Band: "bg",
	})
	if err != nil {
		t.Fatal(err)
	}
	delete(current["802-11-wireless-security"], "psk")
	secrets := Settings{
		"802-11-wireless-security": {"psk": dbus.MakeVariant("secret-password")},
	}
	profile := Profile{
		ID: "onboardd standalone", UUID: testUUID, Interface: "wlan0",
		SSID: "Display-A42F", Priority: 999, Owned: true, Role: RoleStandalone,
	}

	rebuilt, err := rebuildOwnedSettings(profile, current, secrets, false)
	if err != nil {
		t.Fatalf("rebuildOwnedSettings() error = %v", err)
	}
	assertVariant(t, rebuilt, "connection", "autoconnect", false)
	assertVariant(t, rebuilt, "802-11-wireless", "band", "bg")
	assertVariant(t, rebuilt, "802-11-wireless-security", "psk", "secret-password")
	addresses := rebuilt["ipv4"]["address-data"].Value().([]map[string]dbus.Variant)
	if got := variantString(addresses[0]["address"]); got != "10.42.0.1" {
		t.Fatalf("rebuilt address = %q", got)
	}
}

func TestSettingsValidation(t *testing.T) {
	tests := []struct {
		name  string
		build func() error
	}{
		{
			name: "missing interface",
			build: func() error {
				_, _, err := BuildInfrastructureSettings(InfrastructureOptions{SSID: "Office", Open: true})
				return err
			},
		},
		{
			name: "oversized SSID",
			build: func() error {
				_, _, err := BuildInfrastructureSettings(InfrastructureOptions{
					Interface: "wlan0",
					SSID:      "123456789012345678901234567890123",
					Open:      true,
				})
				return err
			},
		},
		{
			name: "short password",
			build: func() error {
				_, _, err := BuildInfrastructureSettings(InfrastructureOptions{
					Interface: "wlan0",
					SSID:      "Office",
					Password:  "short",
				})
				return err
			},
		},
		{
			name: "bad AP role",
			build: func() error {
				_, _, err := BuildAccessPointSettings(AccessPointOptions{
					Interface: "wlan0",
					SSID:      "Display",
					Password:  "display-pass",
					Role:      RoleInfrastructure,
				})
				return err
			},
		},
		{
			name: "bad CIDR",
			build: func() error {
				_, _, err := BuildAccessPointSettings(AccessPointOptions{
					Interface: "wlan0",
					SSID:      "Display",
					Password:  "display-pass",
					Address:   "not-an-address",
					Role:      RoleProvisioning,
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.build(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestAccessPointSecurity(t *testing.T) {
	tests := []struct {
		name            string
		flags, wpa, rsn uint32
		want            Security
	}{
		{name: "open", want: SecurityOpen},
		{name: "wep", flags: 1, want: SecurityWEP},
		{name: "wpa", flags: 1, wpa: 1, want: SecurityWPA},
		{name: "rsn", flags: 1, rsn: 1, want: SecurityWPA2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := accessPointSecurity(test.flags, test.wpa, test.rsn); got != test.want {
				t.Fatalf("security = %q, want %q", got, test.want)
			}
		})
	}
}

func TestValidateCheckpointPath(t *testing.T) {
	valid := "/org/freedesktop/NetworkManager/Checkpoint/7"
	if got, err := validateCheckpointPath(valid); err != nil || string(got) != valid {
		t.Fatalf("validateCheckpointPath(%q) = %q, %v", valid, got, err)
	}
	for _, invalid := range []string{"", "/", "/org/freedesktop/NetworkManager/Settings/7"} {
		if _, err := validateCheckpointPath(invalid); err == nil {
			t.Errorf("validateCheckpointPath(%q) unexpectedly succeeded", invalid)
		}
	}
}

func assertVariant(t *testing.T, settings Settings, section, key string, want any) {
	t.Helper()
	sectionValues, ok := settings[section]
	if !ok {
		t.Fatalf("missing section %q", section)
	}
	value, ok := sectionValues[key]
	if !ok {
		t.Fatalf("missing %s.%s", section, key)
	}
	if got := value.Value(); got != want {
		t.Fatalf("%s.%s = %#v, want %#v", section, key, got, want)
	}
}
