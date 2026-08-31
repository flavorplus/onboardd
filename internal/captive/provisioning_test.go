package captive

import (
	"context"
	"net/netip"
	"reflect"
	"testing"
	"time"
)

func TestProvisionerCanStartAgainAfterStop(t *testing.T) {
	calls := []string{}
	provisioner, err := NewProvisioner(
		&fakeNetworkManager{calls: &calls},
		&fakeDNSConfigurer{calls: &calls},
	)
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		session, startErr := provisioner.Start(context.Background(), validProvisioningOptions())
		if startErr != nil {
			t.Fatalf("Start() error = %v", startErr)
		}
		if got := session.activation.UUID; got != "provisioning-uuid" {
			t.Fatalf("session activation UUID = %q", got)
		}
		stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
		stopErr := session.Stop(stopContext)
		cancel()
		if stopErr != nil {
			t.Fatalf("Stop() error = %v", stopErr)
		}
	}

	wantCycle := []string{
		"dns-install:10.42.0.1",
		"start-ap",
		"wait",
		"status",
		"finalize",
		"delete:provisioning-uuid",
		"dns-remove",
	}
	want := append(append([]string{}, wantCycle...), wantCycle...)
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestProvisioningSessionRetriesOnlyFailedResources(t *testing.T) {
	calls := []string{}
	network := &fakeNetworkManager{calls: &calls, deleteFailures: 1}
	dns := &fakeDNSConfigurer{calls: &calls}
	provisioner, err := NewProvisioner(network, dns)
	if err != nil {
		t.Fatal(err)
	}
	session, err := provisioner.Start(context.Background(), validProvisioningOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Stop(context.Background()); err == nil {
		t.Fatal("first Stop() error = nil")
	}
	if err := session.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	if got := countCalls(calls, "delete:provisioning-uuid"); got != 2 {
		t.Fatalf("profile delete calls = %d, want 2; calls = %#v", got, calls)
	}
	if got := countCalls(calls, "dns-remove"); got != 1 {
		t.Fatalf("DNS remove calls = %d, want 1; calls = %#v", got, calls)
	}
}

func TestProvisionerReturnsIncompleteFailedStartForCleanupRetry(t *testing.T) {
	calls := []string{}
	network := &fakeNetworkManager{
		calls:          &calls,
		fail:           "wait",
		deleteFailures: 1,
	}
	dns := &fakeDNSConfigurer{calls: &calls}
	provisioner, err := NewProvisioner(network, dns)
	if err != nil {
		t.Fatal(err)
	}
	session, err := provisioner.Start(context.Background(), validProvisioningOptions())
	if err == nil {
		t.Fatal("Start() error = nil")
	}
	if session == nil {
		t.Fatal("Start() discarded the incompletely cleaned session")
	}
	if err := session.Stop(context.Background()); err != nil {
		t.Fatalf("retry Stop() error = %v", err)
	}
	if got := countCalls(calls, "delete:provisioning-uuid"); got != 2 {
		t.Fatalf("profile delete calls = %d, want 2; calls = %#v", got, calls)
	}
	if got := countCalls(calls, "dns-remove"); got != 1 {
		t.Fatalf("DNS remove calls = %d, want 1; calls = %#v", got, calls)
	}
}

func TestProvisionerValidatesDependencies(t *testing.T) {
	calls := []string{}
	if _, err := NewProvisioner(nil, &fakeDNSConfigurer{calls: &calls}); err == nil {
		t.Fatal("nil NetworkManager client unexpectedly accepted")
	}
	if _, err := NewProvisioner(&fakeNetworkManager{calls: &calls}, nil); err == nil {
		t.Fatal("nil DNS configurer unexpectedly accepted")
	}
}

func validProvisioningOptions() ProvisioningOptions {
	return ProvisioningOptions{
		Interface: "wlan0",
		SSID:      "Onboardd Setup",
		Password:  "test-password",
		Address:   netip.MustParsePrefix("10.42.0.1/24"),
		Band:      "bg",
		Wait:      30 * time.Second,
	}
}
