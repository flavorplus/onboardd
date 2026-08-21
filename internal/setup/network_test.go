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
	activeUUID   string
	address      string
	profiles     []networkmanager.Profile
	accessPoints []networkmanager.AccessPoint
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

type fakeInfrastructureTransition struct {
	options recovery.InfrastructureOptions
	err     error
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
