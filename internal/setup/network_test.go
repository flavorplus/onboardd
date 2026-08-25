package setup

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/flavorplus/onboardd/internal/connectivity"
	"github.com/flavorplus/onboardd/internal/networkmanager"
	"github.com/flavorplus/onboardd/internal/recovery"
)

func TestNetworkBackendCurrentMode(t *testing.T) {
	for _, test := range []struct {
		name     string
		active   string
		profiles []networkmanager.Profile
		want     Mode
	}{
		{
			name: "setup active", active: "setup",
			profiles: []networkmanager.Profile{{UUID: "setup", Owned: true, Role: networkmanager.RoleProvisioning}},
			want:     ModeSetup,
		},
		{
			name:     "standalone intent",
			profiles: []networkmanager.Profile{{UUID: "standalone", Owned: true, Role: networkmanager.RoleStandalone, Autoconnect: true}},
			want:     ModeStandalone,
		},
		{
			name: "foreign active", active: "foreign",
			profiles: []networkmanager.Profile{{UUID: "foreign", Owned: false}},
			want:     ModeNetwork,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			network := &fakeSetupNetwork{activeUUID: test.active, profiles: test.profiles}
			backend := newTestNetworkBackend(t, network)
			mode, err := backend.CurrentMode(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if mode != test.want {
				t.Fatalf("CurrentMode() = %q, want %q", mode, test.want)
			}
		})
	}
}

func TestNetworkBackendFiltersAndTranslatesScans(t *testing.T) {
	network := &fakeSetupNetwork{accessPoints: []networkmanager.AccessPoint{
		{SSID: "Open", Security: networkmanager.SecurityOpen, Strength: 80},
		{SSID: "Secure", Security: networkmanager.SecurityWPA2, Strength: 70},
		{SSID: "Legacy", Security: networkmanager.SecurityWEP, Strength: 100},
	}}
	backend := newTestNetworkBackend(t, network)
	networks, err := backend.Networks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(networks) != 2 || networks[0].Security != "open" || networks[1].Security != "protected" {
		t.Fatalf("Networks() = %#v", networks)
	}
}

func TestNetworkBackendKnownNetworksAreScopedAndOwnedOnlyDeletion(t *testing.T) {
	network := &fakeSetupNetwork{
		activeUUID: "active",
		profiles: []networkmanager.Profile{
			{
				UUID: "active", SSID: "Office", Type: "802-11-wireless", Mode: "infrastructure",
				Interface: "wlan0", Owned: true, Role: networkmanager.RoleInfrastructure,
				Autoconnect: true,
			},
			{
				UUID: "old", SSID: "Workshop", Type: "802-11-wireless", Mode: "infrastructure",
				Interface: "wlan0", Owned: true, Role: networkmanager.RoleInfrastructure,
			},
			{
				UUID: "system", SSID: "System Wi-Fi", Type: "802-11-wireless", Mode: "infrastructure",
				Autoconnect: true,
			},
			{
				UUID: "standalone", SSID: "Device", Type: "802-11-wireless", Mode: "ap",
				Interface: "wlan0", Owned: true, Role: networkmanager.RoleStandalone,
			},
			{
				UUID: "other", SSID: "Other radio", Type: "802-11-wireless", Mode: "infrastructure",
				Interface: "wlan1", Owned: true, Role: networkmanager.RoleInfrastructure,
			},
		},
	}
	backend := newTestNetworkBackend(t, network)

	known, err := backend.KnownNetworks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(known) != 3 {
		t.Fatalf("KnownNetworks() = %#v, want three applicable client profiles", known)
	}
	if !known[0].Managed || !known[0].Active || known[0].CanConnect || known[0].CanForget {
		t.Fatalf("active known network = %#v", known[0])
	}
	if !known[1].Managed || known[1].Active || !known[1].CanConnect || !known[1].CanForget {
		t.Fatalf("inactive managed network = %#v", known[1])
	}
	if known[2].Managed || known[2].CanConnect || known[2].CanForget {
		t.Fatalf("system network = %#v", known[2])
	}

	if err := backend.ForgetKnownNetwork(context.Background(), "old"); err != nil {
		t.Fatal(err)
	}
	if network.deletedUUID != "old" || network.deletedInterface != "wlan0" {
		t.Fatalf("deleted profile = %q on %q", network.deletedUUID, network.deletedInterface)
	}
}

func TestNetworkBackendConnectsKnownNetworkWithProtectedTransition(t *testing.T) {
	const uuid = "0a3aeac5-3e46-4f46-b9b0-99b2f83d4cb1"
	network := &fakeSetupNetwork{
		activeUUID: "provisioning",
		address:    "10.42.0.1",
		profiles: []networkmanager.Profile{{
			UUID: uuid, SSID: "Workshop", Type: "802-11-wireless", Mode: "infrastructure",
			Interface: "wlan0", Owned: true, Role: networkmanager.RoleInfrastructure,
		}},
	}
	infrastructure := &fakeInfrastructureTransition{}
	captive := &fakeCaptiveExiter{}
	backend := newTestNetworkBackendWithTransitions(
		t,
		network,
		infrastructure,
		&fakeStandaloneTransition{},
		captive,
	)

	if err := backend.ConnectKnownNetwork(context.Background(), uuid); err != nil {
		t.Fatalf("ConnectKnownNetwork() error = %v", err)
	}
	options := infrastructure.savedOptions
	if options.UUID != uuid || options.SSID != "Workshop" ||
		options.PreviousUUID != "provisioning" ||
		options.PreviousIPv4Address != netip.MustParseAddr("10.42.0.1") {
		t.Fatalf("saved infrastructure options = %#v", options)
	}
	if captive.exits != 1 {
		t.Fatalf("captive exits = %d, want 1", captive.exits)
	}
}

func TestNetworkBackendRefusesUnsafeKnownNetworkActivation(t *testing.T) {
	profiles := []networkmanager.Profile{
		{
			UUID: "active", SSID: "Office", Type: "802-11-wireless", Mode: "infrastructure",
			Interface: "wlan0", Owned: true, Role: networkmanager.RoleInfrastructure,
		},
		{
			UUID: "system", SSID: "System", Type: "802-11-wireless", Mode: "infrastructure",
			Interface: "wlan0",
		},
		{
			UUID: "standalone", SSID: "Device", Type: "802-11-wireless", Mode: "ap",
			Interface: "wlan0", Owned: true, Role: networkmanager.RoleStandalone,
		},
	}
	for _, test := range []struct {
		name string
		uuid string
		code string
	}{
		{name: "active profile", uuid: "active", code: "active_network"},
		{name: "system profile", uuid: "system", code: "network_read_only"},
		{name: "standalone profile", uuid: "standalone", code: "network_read_only"},
		{name: "missing profile", uuid: "missing", code: "known_network_not_found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			infrastructure := &fakeInfrastructureTransition{}
			network := &fakeSetupNetwork{
				activeUUID: "active",
				address:    "192.168.1.20",
				profiles:   profiles,
			}
			backend := newTestNetworkBackendWithTransitions(
				t,
				network,
				infrastructure,
				&fakeStandaloneTransition{},
				&fakeCaptiveExiter{},
			)
			err := backend.ConnectKnownNetwork(context.Background(), test.uuid)
			var public *PublicError
			if !errors.As(err, &public) || public.Failure.Code != test.code {
				t.Fatalf("ConnectKnownNetwork() error = %v, want %s", err, test.code)
			}
			if infrastructure.savedOptions.UUID != "" {
				t.Fatalf("unsafe profile reached transition: %#v", infrastructure.savedOptions)
			}
		})
	}
}

func TestNetworkBackendRefusesUnsafeKnownNetworkDeletion(t *testing.T) {
	profiles := []networkmanager.Profile{
		{
			UUID: "active", SSID: "Office", Type: "802-11-wireless", Mode: "infrastructure",
			Interface: "wlan0", Owned: true, Role: networkmanager.RoleInfrastructure,
		},
		{
			UUID: "system", SSID: "System", Type: "802-11-wireless", Mode: "infrastructure",
			Interface: "wlan0",
		},
		{
			UUID: "standalone", SSID: "Device", Type: "802-11-wireless", Mode: "ap",
			Interface: "wlan0", Owned: true, Role: networkmanager.RoleStandalone,
		},
	}
	for _, test := range []struct {
		name string
		uuid string
		code string
	}{
		{name: "active profile", uuid: "active", code: "active_network"},
		{name: "system profile", uuid: "system", code: "network_read_only"},
		{name: "standalone profile", uuid: "standalone", code: "network_read_only"},
		{name: "missing profile", uuid: "missing", code: "known_network_not_found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			network := &fakeSetupNetwork{activeUUID: "active", profiles: profiles}
			backend := newTestNetworkBackend(t, network)
			err := backend.ForgetKnownNetwork(context.Background(), test.uuid)
			var public *PublicError
			if !errors.As(err, &public) || public.Failure.Code != test.code {
				t.Fatalf("ForgetKnownNetwork() error = %v, want %s", err, test.code)
			}
			if network.deletedUUID != "" {
				t.Fatalf("unsafe profile %q was deleted", network.deletedUUID)
			}
		})
	}
}

func TestNetworkBackendProtectedTransitionsUseCurrentConnection(t *testing.T) {
	for _, kind := range []string{"connect", "standalone"} {
		t.Run(kind, func(t *testing.T) {
			network := &fakeSetupNetwork{activeUUID: "current", address: "192.168.1.24"}
			infrastructure := &fakeInfrastructureTransition{}
			standalone := &fakeStandaloneTransition{}
			captive := &fakeCaptiveExiter{}
			backend := newTestNetworkBackendWithTransitions(t, network, infrastructure, standalone, captive)
			var err error
			if kind == "connect" {
				err = backend.Connect(context.Background(), ConnectionRequest{
					SSID: "Office", Password: "test-password",
				})
				if infrastructure.options.PreviousUUID != "current" ||
					infrastructure.options.PreviousIPv4Address != netip.MustParseAddr("192.168.1.24") {
					t.Fatalf("infrastructure options = %#v", infrastructure.options)
				}
			} else {
				err = backend.Standalone(context.Background())
				if standalone.options.PreviousUUID != "current" ||
					standalone.options.PreviousAddress != netip.MustParseAddr("192.168.1.24") {
					t.Fatalf("standalone options = %#v", standalone.options)
				}
			}
			if err != nil {
				t.Fatalf("transition error = %v", err)
			}
			if captive.exits != 1 {
				t.Fatalf("captive exits = %d, want 1", captive.exits)
			}
		})
	}
}

func TestNetworkBackendMapsTransitionFailureAndKeepsCaptive(t *testing.T) {
	network := &fakeSetupNetwork{activeUUID: "setup", address: "10.42.0.1"}
	infrastructure := &fakeInfrastructureTransition{err: errors.New("supplicant rejected candidate")}
	captive := &fakeCaptiveExiter{}
	backend := newTestNetworkBackendWithTransitions(
		t,
		network,
		infrastructure,
		&fakeStandaloneTransition{},
		captive,
	)
	err := backend.Connect(context.Background(), ConnectionRequest{SSID: "Office", Password: "wrong-pass"})
	var public *PublicError
	if !errors.As(err, &public) || public.Failure.Code != "connection_failed" {
		t.Fatalf("Connect() error = %v", err)
	}
	if captive.exits != 0 {
		t.Fatalf("captive exits = %d", captive.exits)
	}
}

func newTestNetworkBackend(t *testing.T, network *fakeSetupNetwork) *NetworkBackend {
	t.Helper()
	return newTestNetworkBackendWithTransitions(
		t,
		network,
		&fakeInfrastructureTransition{},
		&fakeStandaloneTransition{},
		&fakeCaptiveExiter{},
	)
}

func newTestNetworkBackendWithTransitions(
	t *testing.T,
	network *fakeSetupNetwork,
	infrastructure *fakeInfrastructureTransition,
	standalone *fakeStandaloneTransition,
	captive *fakeCaptiveExiter,
) *NetworkBackend {
	t.Helper()
	backend, err := NewNetworkBackend(network, infrastructure, standalone, captive, NetworkOptions{
		Interface:         "wlan0",
		Requirement:       connectivity.RequirementLocal,
		ScanWait:          time.Second,
		ActivationWait:    30 * time.Second,
		RollbackAfter:     90 * time.Second,
		RestorationWait:   30 * time.Second,
		StandaloneEnabled: true,
		Standalone: networkmanager.AccessPointOptions{
			SSID: "Device", Password: "standalone-password", Address: "10.42.0.1/24", Band: "bg",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return backend
}

type fakeSetupNetwork struct {
	activeUUID       string
	address          string
	profiles         []networkmanager.Profile
	accessPoints     []networkmanager.AccessPoint
	deletedUUID      string
	deletedInterface string
}

func (network *fakeSetupNetwork) Status(context.Context, string) (networkmanager.Status, error) {
	state := networkmanager.DeviceStateDisconnected
	addresses := []string(nil)
	if network.activeUUID != "" {
		state = networkmanager.DeviceStateActivated
		address := network.address
		if address == "" {
			address = "10.42.0.1"
		}
		addresses = []string{address}
	}
	return networkmanager.Status{Device: networkmanager.Device{
		State: state, ActiveUUID: network.activeUUID, IPv4Addresses: addresses,
	}}, nil
}

func (network *fakeSetupNetwork) Profiles(context.Context) ([]networkmanager.Profile, error) {
	return network.profiles, nil
}

func (network *fakeSetupNetwork) Scan(
	context.Context,
	string,
	time.Duration,
) ([]networkmanager.AccessPoint, error) {
	return network.accessPoints, nil
}

func (network *fakeSetupNetwork) DeleteOwnedInfrastructureProfile(
	_ context.Context,
	interfaceName string,
	uuid string,
) error {
	network.deletedInterface = interfaceName
	network.deletedUUID = uuid
	return nil
}

type fakeInfrastructureTransition struct {
	options      recovery.InfrastructureOptions
	savedOptions recovery.SavedInfrastructureOptions
	err          error
}

func (transition *fakeInfrastructureTransition) AttemptSaved(
	_ context.Context,
	options recovery.SavedInfrastructureOptions,
) (networkmanager.Activation, error) {
	transition.savedOptions = options
	return networkmanager.Activation{UUID: options.UUID}, transition.err
}

func (transition *fakeInfrastructureTransition) Attempt(
	_ context.Context,
	options recovery.InfrastructureOptions,
) (networkmanager.Activation, error) {
	transition.options = options
	return networkmanager.Activation{UUID: "infrastructure"}, transition.err
}

type fakeStandaloneTransition struct {
	options recovery.StandaloneOptions
	err     error
}

func (transition *fakeStandaloneTransition) Attempt(
	_ context.Context,
	options recovery.StandaloneOptions,
) (networkmanager.Activation, error) {
	transition.options = options
	return networkmanager.Activation{UUID: "standalone"}, transition.err
}

type fakeCaptiveExiter struct {
	exits int
	err   error
}

func (captive *fakeCaptiveExiter) ExitCaptive(context.Context) error {
	captive.exits++
	return captive.err
}
