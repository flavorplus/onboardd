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
	CreateCheckpoint(context.Context, string, time.Duration) (networkmanager.Checkpoint, error)
	ConnectInfrastructure(context.Context, networkmanager.InfrastructureOptions) (networkmanager.Activation, error)
	WaitForActivation(context.Context, string, string, time.Duration) error
	Status(context.Context, string) (networkmanager.Status, error)
	CheckConnectivity(context.Context) (networkmanager.Connectivity, error)
	FinalizeTransition(context.Context, string, networkmanager.Role, string, string) error
	CommitCheckpoint(context.Context, string) error
	RollbackCheckpoint(context.Context, string) (networkmanager.RollbackResult, error)
	DeleteOwnedProfile(context.Context, string) error
}

// InfrastructureOptions describes a candidate connection and the provisioning AP that
// must be restored if the candidate is rejected.
type InfrastructureOptions struct {
	Interface               string
	Candidate               networkmanager.InfrastructureOptions
	Requirement             connectivity.Requirement
	ActivationWait          time.Duration
	RollbackAfter           time.Duration
	RestorationWait         time.Duration
	ProvisioningUUID        string
	ProvisioningIPv4Address netip.Addr
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
	if _, _, err := networkmanager.BuildInfrastructureSettings(candidate); err != nil {
		return networkmanager.Activation{}, fmt.Errorf("validate candidate infrastructure profile: %w", err)
	}

	checkpoint, err := transition.network.CreateCheckpoint(
		ctx,
		options.Interface,
		options.RollbackAfter,
	)
	if err != nil {
		return networkmanager.Activation{}, fmt.Errorf("protect provisioning AP with checkpoint: %w", err)
	}

	activation, err := transition.network.ConnectInfrastructure(ctx, candidate)
	if err != nil {
		return networkmanager.Activation{}, transition.rollback(options, checkpoint.Path, "", fmt.Errorf("activate candidate infrastructure profile: %w", err))
	}
	if err := transition.network.WaitForActivation(ctx, activation.ActivePath, options.Interface, options.ActivationWait); err != nil {
		return networkmanager.Activation{}, transition.rollback(options, checkpoint.Path, activation.UUID, fmt.Errorf("wait for candidate infrastructure profile: %w", err))
	}

	status, err := transition.network.Status(ctx, options.Interface)
	if err != nil {
		return networkmanager.Activation{}, transition.rollback(options, checkpoint.Path, activation.UUID, fmt.Errorf("inspect candidate connectivity: %w", err))
	}
	if status.Device.ActiveUUID != activation.UUID {
		err = fmt.Errorf("candidate profile %s is not active", activation.UUID)
		return networkmanager.Activation{}, transition.rollback(options, checkpoint.Path, activation.UUID, err)
	}
	if options.Requirement == connectivity.RequirementInternet {
		status.Connectivity, err = transition.network.CheckConnectivity(ctx)
		if err != nil {
			return networkmanager.Activation{}, transition.rollback(options, checkpoint.Path, activation.UUID, err)
		}
	}
	result := connectivity.Evaluate(options.Requirement, connectivity.Observation{
		Activated:       status.Device.State == networkmanager.DeviceStateActivated,
		HasLocalAddress: len(status.Device.IPv4Addresses) > 0,
		Internet:        normalizeConnectivity(status.Connectivity),
	})
	if !result.Accepted {
		err = fmt.Errorf("candidate does not satisfy %s requirement: %s", options.Requirement, result.Reason)
		return networkmanager.Activation{}, transition.rollback(options, checkpoint.Path, activation.UUID, err)
	}

	if err := transition.network.FinalizeTransition(
		ctx,
		options.Interface,
		networkmanager.RoleInfrastructure,
		candidate.SSID,
		activation.UUID,
	); err != nil {
		return networkmanager.Activation{}, transition.rollback(options, checkpoint.Path, activation.UUID, fmt.Errorf("select infrastructure mode: %w", err))
	}
	if err := transition.network.CommitCheckpoint(ctx, checkpoint.Path); err != nil {
		return networkmanager.Activation{}, transition.rollback(options, checkpoint.Path, activation.UUID, fmt.Errorf("commit infrastructure transition: %w", err))
	}
	return activation, nil
}

func (transition *Infrastructure) rollback(
	options InfrastructureOptions,
	checkpointPath string,
	candidateUUID string,
	cause error,
) error {
	cleanupContext, cancel := context.WithTimeout(context.Background(), options.RestorationWait)
	defer cancel()
	if _, err := transition.network.RollbackCheckpoint(cleanupContext, checkpointPath); err != nil {
		return errors.Join(cause, fmt.Errorf("rollback NetworkManager checkpoint: %w", err))
	}

	var cleanupErrors []error
	if candidateUUID != "" {
		if err := transition.network.DeleteOwnedProfile(cleanupContext, candidateUUID); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove rejected infrastructure profile: %w", err))
		}
	}
	if err := transition.waitForProvisioning(cleanupContext, options); err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}
	return errors.Join(cause, errors.Join(cleanupErrors...))
}

func (transition *Infrastructure) waitForProvisioning(
	ctx context.Context,
	options InfrastructureOptions,
) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := transition.network.Status(ctx, options.Interface)
		if err == nil && provisioningRestored(
			status,
			options.ProvisioningUUID,
			options.ProvisioningIPv4Address,
		) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("confirm provisioning restoration: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func validateInfrastructureOptions(options InfrastructureOptions) error {
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
	if options.ProvisioningUUID == "" {
		return errors.New("provisioning profile UUID is required")
	}
	address := options.ProvisioningIPv4Address
	if !address.IsValid() || !address.Is4() || address.IsUnspecified() || address.IsMulticast() {
		return errors.New("provisioning IPv4 address must be usable")
	}
	return nil
}

func provisioningRestored(
	status networkmanager.Status,
	uuid string,
	address netip.Addr,
) bool {
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
