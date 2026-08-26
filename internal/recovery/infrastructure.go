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

type networkManager interface {
	CreateCheckpoint(context.Context, string, time.Duration) (string, error)
	ConnectInfrastructure(context.Context, networkmanager.InfrastructureOptions) (networkmanager.Activation, error)
	ActivateProfile(context.Context, string, string) (networkmanager.Activation, error)
	WaitForActivation(context.Context, string, string, time.Duration) error
	Status(context.Context, string) (networkmanager.Status, error)
	CheckConnectivity(context.Context) (networkmanager.Connectivity, error)
	FinalizeTransition(context.Context, string, networkmanager.Role, string, string) error
	CommitCheckpoint(context.Context, string) error
	RollbackCheckpoint(context.Context, string) error
	DeleteOwnedProfile(context.Context, string) error
}

// InfrastructureOptions describes a candidate connection and the active connection
// that must be restored if the candidate is rejected.
type InfrastructureOptions struct {
	Interface           string
	Candidate           networkmanager.InfrastructureOptions
	Requirement         connectivity.Requirement
	ActivationWait      time.Duration
	RollbackAfter       time.Duration
	RestorationWait     time.Duration
	PreviousUUID        string
	PreviousIPv4Address netip.Addr
}

// SavedInfrastructureOptions describes an existing onboardd-owned infrastructure
// profile and the active connection that must be restored if validation fails.
type SavedInfrastructureOptions struct {
	Interface           string
	UUID                string
	SSID                string
	Requirement         connectivity.Requirement
	ActivationWait      time.Duration
	RollbackAfter       time.Duration
	RestorationWait     time.Duration
	PreviousUUID        string
	PreviousIPv4Address netip.Addr
}

type protectedInfrastructureOptions struct {
	Interface           string
	SSID                string
	Requirement         connectivity.Requirement
	ActivationWait      time.Duration
	RollbackAfter       time.Duration
	RestorationWait     time.Duration
	PreviousUUID        string
	PreviousIPv4Address netip.Addr
	RemoveRejected      bool
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
	if err := validateInfrastructureOptions(options); err != nil {
		return networkmanager.Activation{}, err
	}
	candidate := options.Candidate
	candidate.Interface = options.Interface
	candidate.Autoconnect = false
	candidate.Pending = true
	if _, _, err := networkmanager.BuildInfrastructureSettings(candidate); err != nil {
		return networkmanager.Activation{}, fmt.Errorf("validate candidate infrastructure profile: %w", err)
	}
	return transition.attempt(
		ctx,
		protectedOptions(options, candidate.SSID, true),
		func(ctx context.Context) (networkmanager.Activation, error) {
			return transition.network.ConnectInfrastructure(ctx, candidate)
		},
	)
}

// AttemptSaved activates and validates an existing profile without changing or
// deleting it before the protected transition succeeds.
func (transition *Infrastructure) AttemptSaved(
	ctx context.Context,
	options SavedInfrastructureOptions,
) (networkmanager.Activation, error) {
	if err := validateSavedInfrastructureOptions(options); err != nil {
		return networkmanager.Activation{}, err
	}
	return transition.attempt(
		ctx,
		protectedInfrastructureOptions{
			Interface:           options.Interface,
			SSID:                options.SSID,
			Requirement:         options.Requirement,
			ActivationWait:      options.ActivationWait,
			RollbackAfter:       options.RollbackAfter,
			RestorationWait:     options.RestorationWait,
			PreviousUUID:        options.PreviousUUID,
			PreviousIPv4Address: options.PreviousIPv4Address,
		},
		func(ctx context.Context) (networkmanager.Activation, error) {
			return transition.network.ActivateProfile(ctx, options.Interface, options.UUID)
		},
	)
}

func (transition *Infrastructure) attempt(
	ctx context.Context,
	options protectedInfrastructureOptions,
	activate func(context.Context) (networkmanager.Activation, error),
) (networkmanager.Activation, error) {
	checkpoint, err := transition.network.CreateCheckpoint(
		ctx,
		options.Interface,
		options.RollbackAfter,
	)
	if err != nil {
		return networkmanager.Activation{}, fmt.Errorf("protect current connection with checkpoint: %w", err)
	}

	activation, err := activate(ctx)
	if err != nil {
		return networkmanager.Activation{}, transition.rollback(options, checkpoint, activation.UUID, fmt.Errorf("activate infrastructure profile: %w", err))
	}
	if err := transition.network.WaitForActivation(ctx, activation.ActivePath, options.Interface, options.ActivationWait); err != nil {
		return networkmanager.Activation{}, transition.rollback(options, checkpoint, activation.UUID, fmt.Errorf("wait for candidate infrastructure profile: %w", err))
	}

	status, err := transition.network.Status(ctx, options.Interface)
	if err != nil {
		return networkmanager.Activation{}, transition.rollback(options, checkpoint, activation.UUID, fmt.Errorf("inspect candidate connectivity: %w", err))
	}
	if status.Device.ActiveUUID != activation.UUID {
		err = fmt.Errorf("candidate profile %s is not active", activation.UUID)
		return networkmanager.Activation{}, transition.rollback(options, checkpoint, activation.UUID, err)
	}
	if options.Requirement == connectivity.RequirementInternet {
		status.Connectivity, err = transition.network.CheckConnectivity(ctx)
		if err != nil {
			return networkmanager.Activation{}, transition.rollback(options, checkpoint, activation.UUID, err)
		}
	}
	result := connectivity.Evaluate(options.Requirement, connectivity.Observation{
		Activated:       status.Device.State == networkmanager.DeviceStateActivated,
		HasLocalAddress: len(status.Device.IPv4Addresses) > 0,
		Internet:        normalizeConnectivity(status.Connectivity),
	})
	if !result.Accepted {
		err = fmt.Errorf("candidate does not satisfy %s requirement: %s", options.Requirement, result.Reason)
		return networkmanager.Activation{}, transition.rollback(options, checkpoint, activation.UUID, err)
	}

	if err := transition.network.FinalizeTransition(
		ctx,
		options.Interface,
		networkmanager.RoleInfrastructure,
		options.SSID,
		activation.UUID,
	); err != nil {
		return networkmanager.Activation{}, transition.rollback(options, checkpoint, activation.UUID, fmt.Errorf("select infrastructure mode: %w", err))
	}
	if err := transition.network.CommitCheckpoint(ctx, checkpoint); err != nil {
		return networkmanager.Activation{}, transition.rollback(options, checkpoint, activation.UUID, fmt.Errorf("commit infrastructure transition: %w", err))
	}
	return activation, nil
}

func (transition *Infrastructure) rollback(
	options protectedInfrastructureOptions,
	checkpointPath string,
	candidateUUID string,
	cause error,
) error {
	cleanupContext, cancel := context.WithTimeout(context.Background(), options.RestorationWait)
	defer cancel()
	if err := transition.network.RollbackCheckpoint(cleanupContext, checkpointPath); err != nil {
		return errors.Join(cause, fmt.Errorf("rollback NetworkManager checkpoint: %w", err))
	}

	var cleanupErrors []error
	if options.RemoveRejected && candidateUUID != "" {
		if err := transition.network.DeleteOwnedProfile(cleanupContext, candidateUUID); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove rejected infrastructure profile: %w", err))
		}
	}
	if err := transition.waitForPreviousConnection(cleanupContext, options); err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}
	return errors.Join(cause, errors.Join(cleanupErrors...))
}

func (transition *Infrastructure) waitForPreviousConnection(
	ctx context.Context,
	options protectedInfrastructureOptions,
) error {
	if err := restorePreviousConnection(
		ctx,
		transition.network,
		options.Interface,
		options.PreviousUUID,
		options.PreviousIPv4Address,
		options.RestorationWait,
	); err != nil {
		return fmt.Errorf("confirm previous connection restoration: %w", err)
	}
	return nil
}

func validateInfrastructureOptions(options InfrastructureOptions) error {
	return validateProtectedInfrastructureOptions(protectedOptions(options, options.Candidate.SSID, true))
}

func validateSavedInfrastructureOptions(options SavedInfrastructureOptions) error {
	if options.UUID == "" {
		return errors.New("saved profile uuid is required")
	}
	if options.SSID == "" {
		return errors.New("saved profile ssid is required")
	}
	return validateProtectedInfrastructureOptions(protectedInfrastructureOptions{
		Interface:           options.Interface,
		SSID:                options.SSID,
		Requirement:         options.Requirement,
		ActivationWait:      options.ActivationWait,
		RollbackAfter:       options.RollbackAfter,
		RestorationWait:     options.RestorationWait,
		PreviousUUID:        options.PreviousUUID,
		PreviousIPv4Address: options.PreviousIPv4Address,
	})
}

func validateProtectedInfrastructureOptions(options protectedInfrastructureOptions) error {
	if options.Interface == "" {
		return errors.New("interface is required")
	}
	if err := options.Requirement.Validate(); err != nil {
		return err
	}
	if options.ActivationWait <= 0 {
		return errors.New("activation wait must be positive")
	}
	if options.RollbackAfter <= 0 || options.RollbackAfter%time.Second != 0 {
		return errors.New("checkpoint rollback duration must be a positive whole number of seconds")
	}
	if options.RestorationWait <= 0 {
		return errors.New("provisioning restoration wait must be positive")
	}
	if options.PreviousUUID == "" {
		return errors.New("previous profile UUID is required")
	}
	address := options.PreviousIPv4Address
	if !address.IsValid() || !address.Is4() || address.IsUnspecified() || address.IsMulticast() {
		return errors.New("previous IPv4 address must be usable")
	}
	return nil
}

func protectedOptions(
	options InfrastructureOptions,
	ssid string,
	removeRejected bool,
) protectedInfrastructureOptions {
	return protectedInfrastructureOptions{
		Interface:           options.Interface,
		SSID:                ssid,
		Requirement:         options.Requirement,
		ActivationWait:      options.ActivationWait,
		RollbackAfter:       options.RollbackAfter,
		RestorationWait:     options.RestorationWait,
		PreviousUUID:        options.PreviousUUID,
		PreviousIPv4Address: options.PreviousIPv4Address,
		RemoveRejected:      removeRejected,
	}
}

func normalizeConnectivity(value networkmanager.Connectivity) connectivity.InternetState {
	switch value {
	case networkmanager.ConnectivityNone:
		return connectivity.InternetNone
	case networkmanager.ConnectivityPortal:
		return connectivity.InternetPortal
	case networkmanager.ConnectivityLimited:
		return connectivity.InternetLimited
	case networkmanager.ConnectivityFull:
		return connectivity.InternetFull
	default:
		return connectivity.InternetUnknown
	}
}
