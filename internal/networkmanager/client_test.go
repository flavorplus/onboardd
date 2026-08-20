package networkmanager

import "testing"

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
