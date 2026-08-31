// Package recovery provides checkpoint-backed network transitions.
package recovery

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/flavorplus/onboardd/internal/connectivity"
	"github.com/flavorplus/onboardd/internal/networkmanager"
)

// transitionClient is the part of the NetworkManager client every protected
// transition needs. Each transition adds its own activation method on top.
type transitionClient interface {
	CreateCheckpoint(context.Context, string, time.Duration) (string, error)
	ActivateProfile(context.Context, string, string) (networkmanager.Activation, error)
	WaitForActivation(context.Context, string, string, time.Duration) error
	Status(context.Context, string) (networkmanager.Status, error)
	FinalizeTransition(context.Context, string, networkmanager.Role, string, string) error
	CommitCheckpoint(context.Context, string) error
	RollbackCheckpoint(context.Context, string) error
	DeleteOwnedProfile(context.Context, string) error
}

type networkManager interface {
	transitionClient
	ConnectInfrastructure(context.Context, networkmanager.InfrastructureOptions) (networkmanager.Activation, error)
	CheckConnectivity(context.Context) (networkmanager.Connectivity, error)
}

type standaloneNetworkManager interface {
	transitionClient
	StartAccessPoint(context.Context, networkmanager.AccessPointOptions) (networkmanager.Activation, error)
}

// transitionMessages is the wording of each failure in a protected transition.
//
// These strings are a contract, not diagnostics. setup.publicTransitionFailure
// picks the browser-facing error code by substring-matching them, so rewording
// one changes what the user sees. In particular "inspect candidate
// connectivity" classifies as connectivity_unavailable while the standalone
// equivalent, "inspect standalone profile", classifies as connection_failed.
// setup.TestPublicTransitionFailure pins that mapping.
type transitionMessages struct {
	checkpoint string
	activate   string
	wait       string
	inspect    string
	finalize   string
	commit     string
	rejected   string
}

// transitionPlan is one protected transition: the durable intent it selects, the
// connection it restores on failure, and the two steps that differ between
// transitions -- how the candidate is activated, and what counts as accepting it.
type transitionPlan struct {
	interfaceName   string
	role            networkmanager.Role
	ssid            string
	activationWait  time.Duration
	rollbackAfter   time.Duration
	restorationWait time.Duration
	previousUUID    string
	previousAddress netip.Addr
	removeRejected  bool
	messages        transitionMessages

	activate func(context.Context) (networkmanager.Activation, error)
	accept   func(context.Context, *networkmanager.Status, networkmanager.Activation) error
}

// runTransition creates a checkpoint, activates the candidate, and only records
// durable intent once the candidate has been accepted. Every failure after the
// checkpoint exists rolls back and confirms the previous connection is live
// again before returning.
func runTransition(
	ctx context.Context,
	network transitionClient,
	plan transitionPlan,
) (networkmanager.Activation, error) {
	checkpoint, err := network.CreateCheckpoint(ctx, plan.interfaceName, plan.rollbackAfter)
	if err != nil {
		return networkmanager.Activation{}, fmt.Errorf("%s: %w", plan.messages.checkpoint, err)
	}

	activation, err := plan.activate(ctx)
	if err != nil {
		return networkmanager.Activation{}, rollbackTransition(
			network, plan, checkpoint, activation.UUID,
			fmt.Errorf("%s: %w", plan.messages.activate, err),
		)
	}
	if err := network.WaitForActivation(
		ctx,
		activation.ActivePath,
		plan.interfaceName,
		plan.activationWait,
	); err != nil {
		return networkmanager.Activation{}, rollbackTransition(
			network, plan, checkpoint, activation.UUID,
			fmt.Errorf("%s: %w", plan.messages.wait, err),
		)
	}

	status, err := network.Status(ctx, plan.interfaceName)
	if err != nil {
		return networkmanager.Activation{}, rollbackTransition(
			network, plan, checkpoint, activation.UUID,
			fmt.Errorf("%s: %w", plan.messages.inspect, err),
		)
	}
	if err := plan.accept(ctx, &status, activation); err != nil {
		return networkmanager.Activation{}, rollbackTransition(
			network, plan, checkpoint, activation.UUID, err,
		)
	}

	if err := network.FinalizeTransition(
		ctx,
		plan.interfaceName,
		plan.role,
		plan.ssid,
		activation.UUID,
	); err != nil {
		return networkmanager.Activation{}, rollbackTransition(
			network, plan, checkpoint, activation.UUID,
			fmt.Errorf("%s: %w", plan.messages.finalize, err),
		)
	}
	if err := network.CommitCheckpoint(ctx, checkpoint); err != nil {
		return networkmanager.Activation{}, rollbackTransition(
			network, plan, checkpoint, activation.UUID,
			fmt.Errorf("%s: %w", plan.messages.commit, err),
		)
	}
	return activation, nil
}

// rollbackTransition restores the checkpoint, removes the rejected candidate,
// and confirms the previous connection is active again. A failed rollback is
// returned immediately: the later cleanup steps assume it succeeded.
func rollbackTransition(
	network transitionClient,
	plan transitionPlan,
	checkpointPath string,
	candidateUUID string,
	cause error,
) error {
	cleanupContext, cancel := context.WithTimeout(context.Background(), plan.restorationWait)
	defer cancel()
	if err := network.RollbackCheckpoint(cleanupContext, checkpointPath); err != nil {
		return errors.Join(cause, fmt.Errorf("rollback NetworkManager checkpoint: %w", err))
	}

	var cleanupErrors []error
	if plan.removeRejected && candidateUUID != "" {
		if err := network.DeleteOwnedProfile(cleanupContext, candidateUUID); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("%s: %w", plan.messages.rejected, err))
		}
	}
	if err := restorePreviousConnection(
		cleanupContext,
		network,
		plan.interfaceName,
		plan.previousUUID,
		plan.previousAddress,
		plan.restorationWait,
	); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("confirm previous connection restoration: %w", err))
	}
	return errors.Join(cause, errors.Join(cleanupErrors...))
}

var infrastructureMessages = transitionMessages{
	checkpoint: "protect current connection with checkpoint",
	activate:   "activate infrastructure profile",
	wait:       "wait for candidate infrastructure profile",
	inspect:    "inspect candidate connectivity",
	finalize:   "select infrastructure mode",
	commit:     "commit infrastructure transition",
	rejected:   "remove rejected infrastructure profile",
}

var standaloneMessages = transitionMessages{
	checkpoint: "protect setup AP with checkpoint",
	activate:   "activate standalone profile",
	wait:       "wait for standalone profile",
	inspect:    "inspect standalone profile",
	finalize:   "select standalone mode",
	commit:     "commit standalone transition",
	rejected:   "remove rejected standalone profile",
}

// InfrastructureOptions describes a candidate connection and the active connection
// that must be restored if the candidate is rejected.
type InfrastructureOptions struct {
	Interface       string
	Candidate       networkmanager.InfrastructureOptions
	Requirement     connectivity.Requirement
	ActivationWait  time.Duration
	RollbackAfter   time.Duration
	RestorationWait time.Duration
	PreviousUUID    string
	PreviousAddress netip.Addr
}

// SavedInfrastructureOptions describes an existing onboardd-owned infrastructure
// profile and the active connection that must be restored if validation fails.
type SavedInfrastructureOptions struct {
	Interface       string
	UUID            string
	SSID            string
	Requirement     connectivity.Requirement
	ActivationWait  time.Duration
	RollbackAfter   time.Duration
	RestorationWait time.Duration
	PreviousUUID    string
	PreviousAddress netip.Addr
}

// Infrastructure manages checkpoint-backed transitions away from provisioning.
type Infrastructure struct {
	network networkManager
}

// NewInfrastructure creates a protected transition coordinator.
func NewInfrastructure(network networkManager) (*Infrastructure, error) {
	if network == nil {
		return nil, errors.New("NetworkManager client is required")
	}
	return &Infrastructure{network: network}, nil
}

// Attempt connects and validates a candidate. Any failure after checkpoint creation
// triggers rollback, candidate cleanup, and positive confirmation that provisioning is
// active again.
func (transition *Infrastructure) Attempt(
	ctx context.Context,
	options InfrastructureOptions,
) (networkmanager.Activation, error) {
	if err := validateTransitionOptions(infrastructureCheck(
		options.Interface,
		options.Requirement,
		options.ActivationWait,
		options.RollbackAfter,
		options.RestorationWait,
		options.PreviousUUID,
		options.PreviousAddress,
	)); err != nil {
		return networkmanager.Activation{}, err
	}
	candidate := options.Candidate
	candidate.Interface = options.Interface
	candidate.Autoconnect = false
	candidate.Pending = true
	if _, _, err := networkmanager.BuildInfrastructureSettings(candidate); err != nil {
		return networkmanager.Activation{}, fmt.Errorf("validate candidate infrastructure profile: %w", err)
	}

	return runTransition(ctx, transition.network, transitionPlan{
		interfaceName:   options.Interface,
		role:            networkmanager.RoleInfrastructure,
		ssid:            candidate.SSID,
		activationWait:  options.ActivationWait,
		rollbackAfter:   options.RollbackAfter,
		restorationWait: options.RestorationWait,
		previousUUID:    options.PreviousUUID,
		previousAddress: options.PreviousAddress,
		removeRejected:  true,
		messages:        infrastructureMessages,
		activate: func(ctx context.Context) (networkmanager.Activation, error) {
			return transition.network.ConnectInfrastructure(ctx, candidate)
		},
		accept: transition.accept(options.Requirement),
	})
}

// AttemptSaved activates and validates an existing profile without changing or
// deleting it before the protected transition succeeds.
func (transition *Infrastructure) AttemptSaved(
	ctx context.Context,
	options SavedInfrastructureOptions,
) (networkmanager.Activation, error) {
	if options.UUID == "" {
		return networkmanager.Activation{}, errors.New("saved profile uuid is required")
	}
	if options.SSID == "" {
		return networkmanager.Activation{}, errors.New("saved profile ssid is required")
	}
	if err := validateTransitionOptions(infrastructureCheck(
		options.Interface,
		options.Requirement,
		options.ActivationWait,
		options.RollbackAfter,
		options.RestorationWait,
		options.PreviousUUID,
		options.PreviousAddress,
	)); err != nil {
		return networkmanager.Activation{}, err
	}

	return runTransition(ctx, transition.network, transitionPlan{
		interfaceName:   options.Interface,
		role:            networkmanager.RoleInfrastructure,
		ssid:            options.SSID,
		activationWait:  options.ActivationWait,
		rollbackAfter:   options.RollbackAfter,
		restorationWait: options.RestorationWait,
		previousUUID:    options.PreviousUUID,
		previousAddress: options.PreviousAddress,
		// A saved profile is not onboardd's to delete when validation fails.
		removeRejected: false,
		messages:       infrastructureMessages,
		activate: func(ctx context.Context) (networkmanager.Activation, error) {
			return transition.network.ActivateProfile(ctx, options.Interface, options.UUID)
		},
		accept: transition.accept(options.Requirement),
	})
}

// accept holds the candidate to the requirement: it must be the active profile,
// and it must satisfy connectivity policy. An internet requirement forces a fresh
// connectivity check rather than trusting NetworkManager's cached result.
func (transition *Infrastructure) accept(
	requirement connectivity.Requirement,
) func(context.Context, *networkmanager.Status, networkmanager.Activation) error {
	return func(
		ctx context.Context,
		status *networkmanager.Status,
		activation networkmanager.Activation,
	) error {
		if status.Device.ActiveUUID != activation.UUID {
			return fmt.Errorf("candidate profile %s is not active", activation.UUID)
		}
		if requirement == connectivity.RequirementInternet {
			checked, err := transition.network.CheckConnectivity(ctx)
			if err != nil {
				return err
			}
			status.Connectivity = checked
		}
		result := connectivity.Evaluate(requirement, status.Observation())
		if !result.Accepted {
			return fmt.Errorf(
				"candidate does not satisfy %s requirement: %s",
				requirement,
				result.Reason,
			)
		}
		return nil
	}
}

// StandaloneOptions describes a standalone candidate and the active connection restored
// if it cannot be activated.
type StandaloneOptions struct {
	Interface       string
	Candidate       networkmanager.AccessPointOptions
	ActivationWait  time.Duration
	RollbackAfter   time.Duration
	RestorationWait time.Duration
	PreviousUUID    string
	PreviousAddress netip.Addr
}

// Standalone manages a checkpoint-backed transition from setup to standalone mode.
type Standalone struct {
	network standaloneNetworkManager
}

// NewStandalone creates a protected standalone transition coordinator.
func NewStandalone(network standaloneNetworkManager) (*Standalone, error) {
	if network == nil {
		return nil, errors.New("NetworkManager client is required")
	}
	return &Standalone{network: network}, nil
}

// Attempt activates and selects standalone mode. Any failure restores and confirms the
// setup AP before returning.
func (transition *Standalone) Attempt(
	ctx context.Context,
	options StandaloneOptions,
) (networkmanager.Activation, error) {
	if err := validateTransitionOptions(transitionCheck{
		interfaceName:      options.Interface,
		activationWait:     options.ActivationWait,
		rollbackAfter:      options.RollbackAfter,
		restorationWait:    options.RestorationWait,
		restorationMessage: "setup restoration wait must be positive",
		previousUUID:       options.PreviousUUID,
		previousAddress:    options.PreviousAddress,
	}); err != nil {
		return networkmanager.Activation{}, err
	}
	candidate := options.Candidate
	candidate.Interface = options.Interface
	candidate.Role = networkmanager.RoleStandalone
	candidate.Autoconnect = false
	if _, _, err := networkmanager.BuildAccessPointSettings(candidate); err != nil {
		return networkmanager.Activation{}, fmt.Errorf("validate standalone profile: %w", err)
	}
	standaloneAddress := netip.MustParsePrefix(candidate.Address).Addr()

	return runTransition(ctx, transition.network, transitionPlan{
		interfaceName:   options.Interface,
		role:            networkmanager.RoleStandalone,
		ssid:            candidate.SSID,
		activationWait:  options.ActivationWait,
		rollbackAfter:   options.RollbackAfter,
		restorationWait: options.RestorationWait,
		previousUUID:    options.PreviousUUID,
		previousAddress: options.PreviousAddress,
		removeRejected:  true,
		messages:        standaloneMessages,
		activate: func(ctx context.Context) (networkmanager.Activation, error) {
			return transition.network.StartAccessPoint(ctx, candidate)
		},
		accept: func(
			_ context.Context,
			status *networkmanager.Status,
			activation networkmanager.Activation,
		) error {
			if !connectionActive(*status, activation.UUID, standaloneAddress) {
				return fmt.Errorf(
					"standalone profile %s is not active at %s",
					activation.UUID,
					standaloneAddress,
				)
			}
			return nil
		},
	})
}

// transitionCheck carries the option values every protected transition validates.
// requirement is nil for transitions that do not evaluate connectivity policy.
type transitionCheck struct {
	interfaceName      string
	requirement        *connectivity.Requirement
	activationWait     time.Duration
	rollbackAfter      time.Duration
	restorationWait    time.Duration
	restorationMessage string
	previousUUID       string
	previousAddress    netip.Addr
}

// infrastructureCheck fills in the parts of a transitionCheck that are the same
// for both infrastructure entry points.
func infrastructureCheck(
	interfaceName string,
	requirement connectivity.Requirement,
	activationWait, rollbackAfter, restorationWait time.Duration,
	previousUUID string,
	previousAddress netip.Addr,
) transitionCheck {
	return transitionCheck{
		interfaceName:      interfaceName,
		requirement:        &requirement,
		activationWait:     activationWait,
		rollbackAfter:      rollbackAfter,
		restorationWait:    restorationWait,
		restorationMessage: "provisioning restoration wait must be positive",
		previousUUID:       previousUUID,
		previousAddress:    previousAddress,
	}
}

func validateTransitionOptions(check transitionCheck) error {
	if check.interfaceName == "" {
		return errors.New("interface is required")
	}
	if check.requirement != nil {
		if err := check.requirement.Validate(); err != nil {
			return err
		}
	}
	if check.activationWait <= 0 {
		return errors.New("activation wait must be positive")
	}
	if check.rollbackAfter <= 0 || check.rollbackAfter%time.Second != 0 {
		return errors.New("checkpoint rollback duration must be a positive whole number of seconds")
	}
	if check.restorationWait <= 0 {
		return errors.New(check.restorationMessage)
	}
	if check.previousUUID == "" {
		return errors.New("previous profile UUID is required")
	}
	if !usableIPv4(check.previousAddress) {
		return errors.New("previous IPv4 address must be usable")
	}
	return nil
}

func restorePreviousConnection(
	ctx context.Context,
	network transitionClient,
	interfaceName string,
	uuid string,
	address netip.Addr,
	wait time.Duration,
) error {
	status, err := network.Status(ctx, interfaceName)
	if err == nil && connectionActive(status, uuid, address) {
		return nil
	}

	activePath := ""
	if err == nil && status.Device.ActiveUUID == uuid {
		activePath = status.Device.ActiveConnection
	}
	if activePath == "" {
		activation, activationErr := network.ActivateProfile(ctx, interfaceName, uuid)
		if activationErr != nil {
			return fmt.Errorf("reactivate previous profile %s: %w", uuid, activationErr)
		}
		activePath = activation.ActivePath
	}
	if err := network.WaitForActivation(ctx, activePath, interfaceName, wait); err != nil {
		return fmt.Errorf("wait for previous profile %s activation: %w", uuid, err)
	}
	return waitForConnection(ctx, network, interfaceName, uuid, address)
}

func waitForConnection(
	ctx context.Context,
	network transitionClient,
	interfaceName string,
	uuid string,
	address netip.Addr,
) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := network.Status(ctx, interfaceName)
		if err == nil && connectionActive(status, uuid, address) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func connectionActive(status networkmanager.Status, uuid string, address netip.Addr) bool {
	if status.Device.State != networkmanager.DeviceStateActivated || status.Device.ActiveUUID != uuid {
		return false
	}
	for _, candidate := range status.Device.IPv4Addresses {
		parsed, err := netip.ParseAddr(candidate)
		if err == nil && parsed == address {
			return true
		}
	}
	return false
}

func usableIPv4(address netip.Addr) bool {
	return address.IsValid() && address.Is4() && !address.IsUnspecified() && !address.IsMulticast()
}
