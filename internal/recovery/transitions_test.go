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
	if !network.candidate.Pending {
		t.Fatal("candidate was not marked pending before activation")
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

func TestInfrastructureAttemptSavedCommitsAcceptedProfile(t *testing.T) {
	network := newFakeNetwork()
	transition, err := NewInfrastructure(network)
	if err != nil {
		t.Fatal(err)
	}

	activation, err := transition.AttemptSaved(context.Background(), validSavedOptions())
	if err != nil {
		t.Fatalf("AttemptSaved() error = %v", err)
	}
	if activation.UUID != "saved-uuid" {
		t.Fatalf("activation UUID = %q", activation.UUID)
	}
	want := []string{
		"checkpoint-create",
		"activate:saved-uuid",
		"wait",
		"status",
		"finalize",
		"checkpoint-commit",
	}
	if !reflect.DeepEqual(network.calls, want) {
		t.Fatalf("calls = %#v, want %#v", network.calls, want)
	}
}

func TestInfrastructureAttemptSavedRollsBackWithoutDeletingProfile(t *testing.T) {
	network := newFakeNetwork()
	network.fail = "wait"
	transition, err := NewInfrastructure(network)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := transition.AttemptSaved(context.Background(), validSavedOptions()); err == nil {
		t.Fatal("AttemptSaved() error = nil")
	}
	if !contains(network.calls, "checkpoint-rollback") {
		t.Fatalf("rollback missing from calls: %#v", network.calls)
	}
	if contains(network.calls, "delete:saved-uuid") {
		t.Fatalf("existing profile was deleted during rollback: %#v", network.calls)
	}
}

func TestInfrastructureAttemptSavedValidatesBeforeCheckpoint(t *testing.T) {
	network := newFakeNetwork()
	transition, err := NewInfrastructure(network)
	if err != nil {
		t.Fatal(err)
	}
	options := validSavedOptions()
	options.UUID = ""

	if _, err := transition.AttemptSaved(context.Background(), options); err == nil {
		t.Fatal("AttemptSaved() error = nil")
	}
	if len(network.calls) != 0 {
		t.Fatalf("invalid saved profile caused side effects: %#v", network.calls)
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

func TestInfrastructureAttemptRemovesPartiallyCreatedCandidate(t *testing.T) {
	network := newFakeNetwork()
	network.fail = "partial-connect"
	transition, err := NewInfrastructure(network)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transition.Attempt(context.Background(), validOptions()); err == nil {
		t.Fatal("Attempt() error = nil")
	}
	if !contains(network.calls, "delete:candidate-uuid") {
		t.Fatalf("partial candidate was not removed: %#v", network.calls)
	}
}

func TestInfrastructureRollbackReactivatesExactPreviousProfile(t *testing.T) {
	network := newFakeNetwork()
	network.fail = "wait"
	network.competingProfile = true
	transition, err := NewInfrastructure(network)
	if err != nil {
		t.Fatal(err)
	}

	options := validOptions()
	options.PreviousUUID = "standalone-uuid"
	_, err = transition.Attempt(context.Background(), options)
	if err == nil {
		t.Fatal("Attempt() error = nil, want rejected candidate")
	}
	wantOrder := []string{
		"checkpoint-rollback",
		"delete:candidate-uuid",
		"status",
		"activate:standalone-uuid",
		"restore-wait",
		"status",
	}
	if !containsInOrder(network.calls, wantOrder) {
		t.Fatalf("exact restoration order missing: calls = %#v, want subsequence %#v", network.calls, wantOrder)
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
		Requirement:     connectivity.RequirementLocal,
		ActivationWait:  30 * time.Second,
		RollbackAfter:   90 * time.Second,
		RestorationWait: time.Second,
		PreviousUUID:    "provisioning-uuid",
		PreviousAddress: netip.MustParseAddr("10.42.0.1"),
	}
}

func validSavedOptions() SavedInfrastructureOptions {
	return SavedInfrastructureOptions{
		Interface:       "wlan0",
		UUID:            "saved-uuid",
		SSID:            "Office",
		Requirement:     connectivity.RequirementLocal,
		ActivationWait:  30 * time.Second,
		RollbackAfter:   90 * time.Second,
		RestorationWait: time.Second,
		PreviousUUID:    "provisioning-uuid",
		PreviousAddress: netip.MustParseAddr("10.42.0.1"),
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

func containsInOrder(calls, want []string) bool {
	next := 0
	for _, call := range calls {
		if next < len(want) && call == want[next] {
			next++
		}
	}
	return next == len(want)
}

type fakeNetwork struct {
	fakeCheckpointBase
	candidate        networkmanager.InfrastructureOptions
	connectivity     networkmanager.Connectivity
	competingProfile bool
	restoredUUID     string
	activatedUUID    string
}

func newFakeNetwork() *fakeNetwork {
	return &fakeNetwork{connectivity: networkmanager.ConnectivityFull}
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
	if network.fail == "partial-connect" {
		return networkmanager.Activation{UUID: "candidate-uuid"}, errors.New("cleanup failed")
	}
	return networkmanager.Activation{
		UUID:       "candidate-uuid",
		ActivePath: "/org/freedesktop/NetworkManager/ActiveConnection/2",
	}, nil
}

func (network *fakeNetwork) WaitForActivation(context.Context, string, string, time.Duration) error {
	if contains(network.calls, "checkpoint-rollback") && network.restoredUUID != "" {
		network.calls = append(network.calls, "restore-wait")
		return nil
	}
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
		if network.competingProfile && network.restoredUUID == "" {
			return networkmanager.Status{Device: networkmanager.Device{
				State:         networkmanager.DeviceStateActivated,
				ActiveUUID:    "foreign-uuid",
				IPv4Addresses: []string{"192.168.1.30"},
			}}, nil
		}
		if network.restoredUUID != "" {
			return networkmanager.Status{Device: networkmanager.Device{
				State:         networkmanager.DeviceStateActivated,
				ActiveUUID:    network.restoredUUID,
				IPv4Addresses: []string{"10.42.0.1"},
			}}, nil
		}
		return provisioningStatus(), nil
	}
	return networkmanager.Status{
		Connectivity: network.connectivity,
		Device: networkmanager.Device{
			State:         networkmanager.DeviceStateActivated,
			ActiveUUID:    network.activeCandidateUUID(),
			IPv4Addresses: []string{"192.168.1.20"},
		},
	}, nil
}

func (network *fakeNetwork) ActivateProfile(
	_ context.Context,
	_ string,
	uuid string,
) (networkmanager.Activation, error) {
	network.calls = append(network.calls, "activate:"+uuid)
	if contains(network.calls, "checkpoint-rollback") {
		network.restoredUUID = uuid
	} else {
		network.activatedUUID = uuid
	}
	return networkmanager.Activation{
		UUID:       uuid,
		ActivePath: "/org/freedesktop/NetworkManager/ActiveConnection/restored",
	}, nil
}

func (network *fakeNetwork) activeCandidateUUID() string {
	if network.activatedUUID != "" {
		return network.activatedUUID
	}
	return "candidate-uuid"
}

func (network *fakeNetwork) CheckConnectivity(context.Context) (networkmanager.Connectivity, error) {
	network.calls = append(network.calls, "connectivity-check")
	return network.connectivity, nil
}

func provisioningStatus() networkmanager.Status {
	return networkmanager.Status{Device: networkmanager.Device{
		State:         networkmanager.DeviceStateActivated,
		ActiveUUID:    "provisioning-uuid",
		IPv4Addresses: []string{"10.42.0.1"},
	}}
}

// fakeCheckpointBase is the half of the transition client both fakes implement
// the same way. The checkpoint path it returns and the text of its rollback
// failure are arbitrary -- no test asserts either -- so one value serves both.
type fakeCheckpointBase struct {
	calls       []string
	fail        string
	statusCalls int
}

func (network *fakeCheckpointBase) CreateCheckpoint(
	context.Context,
	string,
	time.Duration,
) (string, error) {
	network.calls = append(network.calls, "checkpoint-create")
	return "/org/freedesktop/NetworkManager/Checkpoint/1", nil
}

func (network *fakeCheckpointBase) FinalizeTransition(
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

func (network *fakeCheckpointBase) CommitCheckpoint(context.Context, string) error {
	network.calls = append(network.calls, "checkpoint-commit")
	if network.fail == "checkpoint-commit" {
		return errors.New("test failure")
	}
	return nil
}

func (network *fakeCheckpointBase) RollbackCheckpoint(
	context.Context,
	string,
) error {
	network.calls = append(network.calls, "checkpoint-rollback")
	if network.fail == "rollback-after-wait" {
		return errors.New("test rollback failure")
	}
	return nil
}

func (network *fakeCheckpointBase) DeleteOwnedProfile(_ context.Context, uuid string) error {
	network.calls = append(network.calls, "delete:"+uuid)
	return nil
}

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
	fakeCheckpointBase
	candidate networkmanager.AccessPointOptions
}

func newFakeStandaloneNetwork() *fakeStandaloneNetwork { return &fakeStandaloneNetwork{} }

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
