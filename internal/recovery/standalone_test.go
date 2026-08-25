package recovery

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/flavorplus/onboardd/internal/networkmanager"
)

func TestStandaloneAttemptCommitsCandidate(t *testing.T) {
	network := newFakeStandaloneNetwork()
	transition, err := NewStandalone(network)
	if err != nil {
		t.Fatal(err)
	}
	activation, err := transition.Attempt(context.Background(), validStandaloneOptions())
	if err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	if activation.UUID != "standalone-uuid" {
		t.Fatalf("activation = %#v", activation)
	}
	want := []string{"checkpoint-create", "start", "wait", "status", "finalize", "checkpoint-commit"}
	if !reflect.DeepEqual(network.calls, want) {
		t.Fatalf("calls = %#v, want %#v", network.calls, want)
	}
	if network.candidate.Autoconnect || network.candidate.Role != networkmanager.RoleStandalone {
		t.Fatalf("candidate = %#v", network.candidate)
	}
}

func TestStandaloneAttemptRollsBackAndConfirmsSetup(t *testing.T) {
	for _, stage := range []string{"start", "wait", "candidate-status", "finalize", "checkpoint-commit"} {
		t.Run(stage, func(t *testing.T) {
			network := newFakeStandaloneNetwork()
			network.fail = stage
			transition, err := NewStandalone(network)
			if err != nil {
				t.Fatal(err)
			}
			_, err = transition.Attempt(context.Background(), validStandaloneOptions())
			if err == nil {
				t.Fatal("Attempt() error = nil")
			}
			if !contains(network.calls, "checkpoint-rollback") {
				t.Fatalf("rollback missing: %#v", network.calls)
			}
			wantDelete := stage != "start"
			if got := contains(network.calls, "delete:standalone-uuid"); got != wantDelete {
				t.Fatalf("candidate delete = %t, want %t; calls = %#v", got, wantDelete, network.calls)
			}
			if network.statusCalls < 1 {
				t.Fatal("setup restoration was not checked")
			}
		})
	}
}

func TestStandaloneAttemptRemovesPartiallyCreatedCandidate(t *testing.T) {
	network := newFakeStandaloneNetwork()
	network.fail = "partial-start"
	transition, err := NewStandalone(network)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transition.Attempt(context.Background(), validStandaloneOptions()); err == nil {
		t.Fatal("Attempt() error = nil")
	}
	if !contains(network.calls, "delete:standalone-uuid") {
		t.Fatalf("partial candidate was not removed: %#v", network.calls)
	}
}

func TestStandaloneAttemptReportsRollbackFailure(t *testing.T) {
	network := newFakeStandaloneNetwork()
	network.fail = "rollback-after-wait"
	transition, err := NewStandalone(network)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transition.Attempt(context.Background(), validStandaloneOptions())
	if err == nil || !strings.Contains(err.Error(), "rollback NetworkManager checkpoint") {
		t.Fatalf("Attempt() error = %v", err)
	}
}

func validStandaloneOptions() StandaloneOptions {
	return StandaloneOptions{
		Interface: "wlan0",
		Candidate: networkmanager.AccessPointOptions{
			SSID:     "Device",
			Password: "standalone-password",
			Address:  "10.42.0.1/24",
			Band:     "bg",
		},
		ActivationWait:  30 * time.Second,
		RollbackAfter:   90 * time.Second,
		RestorationWait: time.Second,
		PreviousUUID:    "setup-uuid",
		PreviousAddress: netip.MustParseAddr("10.42.0.1"),
	}
}

type fakeStandaloneNetwork struct {
	calls       []string
	fail        string
	statusCalls int
	candidate   networkmanager.AccessPointOptions
}

func newFakeStandaloneNetwork() *fakeStandaloneNetwork { return &fakeStandaloneNetwork{} }

func (network *fakeStandaloneNetwork) CreateCheckpoint(
	context.Context,
	string,
	time.Duration,
) (networkmanager.Checkpoint, error) {
	network.calls = append(network.calls, "checkpoint-create")
	return networkmanager.Checkpoint{Path: "/org/freedesktop/NetworkManager/Checkpoint/2"}, nil
}

func (network *fakeStandaloneNetwork) StartAccessPoint(
	_ context.Context,
	options networkmanager.AccessPointOptions,
) (networkmanager.Activation, error) {
	network.calls = append(network.calls, "start")
	network.candidate = options
	if network.fail == "start" {
		return networkmanager.Activation{}, errors.New("test failure")
	}
	if network.fail == "partial-start" {
		return networkmanager.Activation{UUID: "standalone-uuid"}, errors.New("cleanup failed")
	}
	return networkmanager.Activation{
		UUID:       "standalone-uuid",
		ActivePath: "/org/freedesktop/NetworkManager/ActiveConnection/3",
	}, nil
}

func (network *fakeStandaloneNetwork) WaitForActivation(
	context.Context,
	string,
	string,
	time.Duration,
) error {
	network.calls = append(network.calls, "wait")
	if network.fail == "wait" || network.fail == "rollback-after-wait" {
		return errors.New("test failure")
	}
	return nil
}

func (network *fakeStandaloneNetwork) ActivateProfile(
	_ context.Context,
	_ string,
	uuid string,
) (networkmanager.Activation, error) {
	network.calls = append(network.calls, "activate:"+uuid)
	return networkmanager.Activation{
		UUID:       uuid,
		ActivePath: "/org/freedesktop/NetworkManager/ActiveConnection/restored",
	}, nil
}

func (network *fakeStandaloneNetwork) Status(context.Context, string) (networkmanager.Status, error) {
	network.calls = append(network.calls, "status")
	network.statusCalls++
	if network.fail == "candidate-status" && network.statusCalls == 1 {
		return networkmanager.Status{}, errors.New("test failure")
	}
	if contains(network.calls, "checkpoint-rollback") {
		return networkmanager.Status{Device: networkmanager.Device{
			State:         networkmanager.DeviceStateActivated,
			ActiveUUID:    "setup-uuid",
			IPv4Addresses: []string{"10.42.0.1"},
		}}, nil
	}
	return networkmanager.Status{Device: networkmanager.Device{
		State:         networkmanager.DeviceStateActivated,
		ActiveUUID:    "standalone-uuid",
		IPv4Addresses: []string{"10.42.0.1"},
	}}, nil
}

func (network *fakeStandaloneNetwork) FinalizeTransition(
	context.Context,
	string,
	networkmanager.Role,
	string,
	string,
) error {
	network.calls = append(network.calls, "finalize")
	if network.fail == "finalize" {
		return errors.New("test failure")
	}
	return nil
}

func (network *fakeStandaloneNetwork) CommitCheckpoint(context.Context, string) error {
	network.calls = append(network.calls, "checkpoint-commit")
	if network.fail == "checkpoint-commit" {
		return errors.New("test failure")
	}
	return nil
}

func (network *fakeStandaloneNetwork) RollbackCheckpoint(
	context.Context,
	string,
) (networkmanager.RollbackResult, error) {
	network.calls = append(network.calls, "checkpoint-rollback")
	if network.fail == "rollback-after-wait" {
		return networkmanager.RollbackResult{}, errors.New("rollback failure")
	}
	return networkmanager.RollbackResult{}, nil
}

func (network *fakeStandaloneNetwork) DeleteOwnedProfile(_ context.Context, uuid string) error {
	network.calls = append(network.calls, "delete:"+uuid)
	return nil
}
