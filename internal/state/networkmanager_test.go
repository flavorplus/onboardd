package state

import (
	"context"
	"testing"

	"github.com/flavorplus/onboardd/internal/connectivity"
	"github.com/flavorplus/onboardd/internal/networkmanager"
)

func TestNetworkManagerObserverNormalizesSnapshot(t *testing.T) {
	client := &fakeNetworkManagerClient{
		status: networkmanager.Status{
			Connectivity: networkmanager.ConnectivityLimited,
			Device: networkmanager.Device{
				Interface:     "wlan0",
				Managed:       true,
				State:         networkmanager.DeviceStateActivated,
				ActiveUUID:    "office",
				IPv4Addresses: []string{"192.0.2.10"},
			},
		},
		profiles: []networkmanager.Profile{
			{
				UUID:        "office",
				Interface:   "wlan0",
				Mode:        "infrastructure",
				Autoconnect: true,
			},
			{
				UUID:        "standalone",
				Interface:   "wlan0",
				Role:        networkmanager.RoleStandalone,
				Mode:        "ap",
				Autoconnect: false,
			},
			{
				UUID:        "other-interface",
				Interface:   "wlan1",
				Mode:        "infrastructure",
				Autoconnect: true,
			},
		},
	}

	observer := NewNetworkManagerObserver(client, "wlan0")
	snapshot, err := observer.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.ActiveMode != ModeInfrastructure || snapshot.ActiveUUID != "office" {
		t.Fatalf("active snapshot = %#v", snapshot)
	}
	if snapshot.DeviceState != DeviceActivated || !snapshot.Connectivity.HasLocalAddress {
		t.Fatalf("device snapshot = %#v", snapshot)
	}
	if snapshot.Connectivity.Internet != connectivity.InternetLimited {
		t.Fatalf("internet = %q", snapshot.Connectivity.Internet)
	}
	if len(snapshot.Profiles) != 2 {
		t.Fatalf("profiles = %#v, want two wlan0 profiles", snapshot.Profiles)
	}
}

type fakeNetworkManagerClient struct {
	status   networkmanager.Status
	profiles []networkmanager.Profile
}

func (client *fakeNetworkManagerClient) Status(context.Context, string) (networkmanager.Status, error) {
	return client.status, nil
}

func (client *fakeNetworkManagerClient) Profiles(context.Context) ([]networkmanager.Profile, error) {
	return client.profiles, nil
}

func (client *fakeNetworkManagerClient) WatchProperties(
	context.Context,
) (<-chan networkmanager.Event, <-chan error, error) {
	return make(chan networkmanager.Event), make(chan error), nil
}
