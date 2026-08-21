package recovery

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/flavorplus/onboardd/internal/connectivity"
	"github.com/flavorplus/onboardd/internal/networkmanager"
)

func TestInfrastructureAttemptCommitsAcceptedCandidate(t *testing.T) {
	network := newFakeNetwork()
	transition, err := NewInfrastructure(network)
	if err != nil {
		t.Fatal(err)
	}

	activation, err := transition.Attempt(context.Background(), validOptions())
	if err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	if activation.UUID != "candidate-uuid" {
		t.Fatalf("activation UUID = %q", activation.UUID)
	}
	want := []string{"checkpoint-create", "connect", "wait", "status", "finalize", "checkpoint-commit"}
	if !reflect.DeepEqual(network.calls, want) {
		t.Fatalf("calls = %#v, want %#v", network.calls, want)
	}
	if network.candidate.Autoconnect {
		t.Fatal("candidate was eligible for autoconnect before validation")
	}
	if network.candidate.Interface != "wlan0" {
		t.Fatalf("candidate interface = %q", network.candidate.Interface)
	}
}

func TestInfrastructureAttemptRunsFreshInternetCheck(t *testing.T) {
	network := newFakeNetwork()
	transition, err := NewInfrastructure(network)
	if err != nil {
		t.Fatal(err)
	}
	options := validOptions()
	options.Requirement = connectivity.RequirementInternet

	if _, err := transition.Attempt(context.Background(), options); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}
	want := []string{
		"checkpoint-create",
		"connect",
		"wait",
		"status",
		"connectivity-check",
		"finalize",
		"checkpoint-commit",
	}
	if !reflect.DeepEqual(network.calls, want) {
		t.Fatalf("calls = %#v, want %#v", network.calls, want)
	}
}

func TestInfrastructureAttemptRollsBackAndConfirmsProvisioning(t *testing.T) {
	for _, stage := range []string{"connect", "wait", "candidate-status", "finalize", "checkpoint-commit"} {
		t.Run(stage, func(t *testing.T) {
			network := newFakeNetwork()
			network.fail = stage
			transition, err := NewInfrastructure(network)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := transition.Attempt(context.Background(), validOptions()); err == nil {
				t.Fatal("Attempt() error = nil, want rejected transition")
			}
			if !contains(network.calls, "checkpoint-rollback") {
				t.Fatalf("rollback missing from calls: %#v", network.calls)
			}
			wantDelete := stage != "connect"
			if got := contains(network.calls, "delete:candidate-uuid"); got != wantDelete {
				t.Fatalf("candidate delete = %t, want %t; calls = %#v", got, wantDelete, network.calls)
			}
			if network.statusCalls < 1 {
				t.Fatal("restored provisioning state was not inspected")
			}
		})
	}
}

func TestInfrastructureAttemptRejectsUnacceptableConnectivity(t *testing.T) {
	network := newFakeNetwork()
	network.connectivity = networkmanager.ConnectivityLimited
	transition, err := NewInfrastructure(network)
	if err != nil {
		t.Fatal(err)
	}
	options := validOptions()
	options.Requirement = connectivity.RequirementInternet

	_, err = transition.Attempt(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "internet-not-confirmed") {
		t.Fatalf("Attempt() error = %v", err)
	}
	if !contains(network.calls, "checkpoint-rollback") || !contains(network.calls, "delete:candidate-uuid") {
		t.Fatalf("rejected connectivity did not clean up: %#v", network.calls)
	}
}

func TestInfrastructureAttemptDoesNotDeleteCandidateWhenRollbackFails(t *testing.T) {
	network := newFakeNetwork()
	network.fail = "rollback-after-wait"
	transition, err := NewInfrastructure(network)
	if err != nil {
		t.Fatal(err)
	}

	_, err = transition.Attempt(context.Background(), validOptions())
	if err == nil || !strings.Contains(err.Error(), "rollback NetworkManager checkpoint") {
		t.Fatalf("Attempt() error = %v", err)
	}
	if contains(network.calls, "delete:candidate-uuid") {
		t.Fatalf("candidate deleted despite failed rollback: %#v", network.calls)
	}
}

func TestInfrastructureAttemptValidatesCandidateBeforeCheckpoint(t *testing.T) {
	network := newFakeNetwork()
	transition, err := NewInfrastructure(network)
	if err != nil {
		t.Fatal(err)
	}
	options := validOptions()
	options.Candidate.Password = "short"

	_, err = transition.Attempt(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "validate candidate") {
		t.Fatalf("Attempt() error = %v", err)
	}
	if len(network.calls) != 0 {
		t.Fatalf("invalid candidate caused side effects: %#v", network.calls)
	}
}

func validOptions() InfrastructureOptions {
	return InfrastructureOptions{
		Interface: "wlan0",
		Candidate: networkmanager.InfrastructureOptions{
			SSID:     "Office",
			Password: "test-password",
		},
		Requirement:             connectivity.RequirementLocal,
		ActivationWait:          30 * time.Second,
		RollbackAfter:           90 * time.Second,
		RestorationWait:         time.Second,
		ProvisioningUUID:        "provisioning-uuid",
		ProvisioningIPv4Address: netip.MustParseAddr("10.42.0.1"),
	}
}

func contains(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

type fakeNetwork struct {
	calls        []string
	fail         string
	statusCalls  int
	candidate    networkmanager.InfrastructureOptions
	connectivity networkmanager.Connectivity
}

func newFakeNetwork() *fakeNetwork {
	return &fakeNetwork{connectivity: networkmanager.ConnectivityFull}
}

func (network *fakeNetwork) CreateCheckpoint(
	context.Context,
	string,
	time.Duration,
) (networkmanager.Checkpoint, error) {
	network.calls = append(network.calls, "checkpoint-create")
	return networkmanager.Checkpoint{Path: "/org/freedesktop/NetworkManager/Checkpoint/1"}, nil
}

func (network *fakeNetwork) ConnectInfrastructure(
	_ context.Context,
	options networkmanager.InfrastructureOptions,
) (networkmanager.Activation, error) {
	network.calls = append(network.calls, "connect")
	network.candidate = options
	if network.fail == "connect" {
		return networkmanager.Activation{}, errors.New("test failure")
	}
	return networkmanager.Activation{
		UUID:       "candidate-uuid",
		ActivePath: "/org/freedesktop/NetworkManager/ActiveConnection/2",
	}, nil
}

func (network *fakeNetwork) WaitForActivation(context.Context, string, string, time.Duration) error {
	network.calls = append(network.calls, "wait")
	if network.fail == "wait" || network.fail == "rollback-after-wait" {
		return errors.New("test failure")
	}
	return nil
}

func (network *fakeNetwork) Status(context.Context, string) (networkmanager.Status, error) {
	network.calls = append(network.calls, "status")
	network.statusCalls++
	if network.fail == "candidate-status" && network.statusCalls == 1 {
		return networkmanager.Status{}, errors.New("test failure")
	}
	if contains(network.calls, "checkpoint-rollback") {
		return provisioningStatus(), nil
	}
	return networkmanager.Status{
		Connectivity: network.connectivity,
		Device: networkmanager.Device{
			State:         networkmanager.DeviceStateActivated,
			StateName:     "activated",
			ActiveUUID:    "candidate-uuid",
			IPv4Addresses: []string{"192.168.1.20"},
		},
	}, nil
}

func (network *fakeNetwork) CheckConnectivity(context.Context) (networkmanager.Connectivity, error) {
	network.calls = append(network.calls, "connectivity-check")
	return network.connectivity, nil
}

func (network *fakeNetwork) FinalizeTransition(
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

func (network *fakeNetwork) CommitCheckpoint(context.Context, string) error {
	network.calls = append(network.calls, "checkpoint-commit")
	if network.fail == "checkpoint-commit" {
		return errors.New("test failure")
	}
	return nil
}

func (network *fakeNetwork) RollbackCheckpoint(
	context.Context,
	string,
) (networkmanager.RollbackResult, error) {
	network.calls = append(network.calls, "checkpoint-rollback")
	if network.fail == "rollback-after-wait" {
		return networkmanager.RollbackResult{}, errors.New("test rollback failure")
	}
	return networkmanager.RollbackResult{}, nil
}

func (network *fakeNetwork) DeleteOwnedProfile(_ context.Context, uuid string) error {
	network.calls = append(network.calls, "delete:"+uuid)
	return nil
}

func provisioningStatus() networkmanager.Status {
	return networkmanager.Status{Device: networkmanager.Device{
		State:         networkmanager.DeviceStateActivated,
		StateName:     "activated",
		ActiveUUID:    "provisioning-uuid",
		IPv4Addresses: []string{"10.42.0.1"},
	}}
}
