package recovery

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/flavorplus/onboardd/internal/networkmanager"
)

type standaloneNetworkManager interface {
	CreateCheckpoint(context.Context, string, time.Duration) (networkmanager.Checkpoint, error)
	StartAccessPoint(context.Context, networkmanager.AccessPointOptions) (networkmanager.Activation, error)
	ActivateProfile(context.Context, string, string) (networkmanager.Activation, error)
	WaitForActivation(context.Context, string, string, time.Duration) error
	Status(context.Context, string) (networkmanager.Status, error)
	FinalizeTransition(context.Context, string, networkmanager.Role, string, string) error
	CommitCheckpoint(context.Context, string) error
	RollbackCheckpoint(context.Context, string) (networkmanager.RollbackResult, error)
	DeleteOwnedProfile(context.Context, string) error
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
	if err := validateStandaloneOptions(options); err != nil {
		return networkmanager.Activation{}, err
	}
	candidate := options.Candidate
	candidate.Interface = options.Interface
	candidate.Role = networkmanager.RoleStandalone
	candidate.Autoconnect = false
	if _, _, err := networkmanager.BuildAccessPointSettings(candidate); err != nil {
		return networkmanager.Activation{}, fmt.Errorf("validate standalone profile: %w", err)
	}

	checkpoint, err := transition.network.CreateCheckpoint(ctx, options.Interface, options.RollbackAfter)
	if err != nil {
		return networkmanager.Activation{}, fmt.Errorf("protect setup AP with checkpoint: %w", err)
	}
	activation, err := transition.network.StartAccessPoint(ctx, candidate)
	if err != nil {
		return networkmanager.Activation{}, transition.rollback(
			options,
			checkpoint.Path,
			activation.UUID,
			fmt.Errorf("activate standalone profile: %w", err),
		)
	}
	if err := transition.network.WaitForActivation(
		ctx,
		activation.ActivePath,
		options.Interface,
		options.ActivationWait,
	); err != nil {
		return networkmanager.Activation{}, transition.rollback(
			options,
			checkpoint.Path,
			activation.UUID,
			fmt.Errorf("wait for standalone profile: %w", err),
		)
	}
	status, err := transition.network.Status(ctx, options.Interface)
	if err != nil {
		return networkmanager.Activation{}, transition.rollback(
			options,
			checkpoint.Path,
			activation.UUID,
			fmt.Errorf("inspect standalone profile: %w", err),
		)
	}
	standaloneAddress := netip.MustParsePrefix(candidate.Address).Addr()
	if !connectionActive(status, activation.UUID, standaloneAddress) {
		return networkmanager.Activation{}, transition.rollback(
			options,
			checkpoint.Path,
			activation.UUID,
			fmt.Errorf("standalone profile %s is not active at %s", activation.UUID, standaloneAddress),
		)
	}
	if err := transition.network.FinalizeTransition(
		ctx,
		options.Interface,
		networkmanager.RoleStandalone,
		candidate.SSID,
		activation.UUID,
	); err != nil {
		return networkmanager.Activation{}, transition.rollback(
			options,
			checkpoint.Path,
			activation.UUID,
			fmt.Errorf("select standalone mode: %w", err),
		)
	}
	if err := transition.network.CommitCheckpoint(ctx, checkpoint.Path); err != nil {
		return networkmanager.Activation{}, transition.rollback(
			options,
			checkpoint.Path,
			activation.UUID,
			fmt.Errorf("commit standalone transition: %w", err),
		)
	}
	return activation, nil
}

func (transition *Standalone) rollback(
	options StandaloneOptions,
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
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove rejected standalone profile: %w", err))
		}
	}
	if err := restorePreviousConnection(
		cleanupContext,
		transition.network,
		options.Interface,
		options.PreviousUUID,
		options.PreviousAddress,
		options.RestorationWait,
	); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("confirm previous connection restoration: %w", err))
	}
	return errors.Join(cause, errors.Join(cleanupErrors...))
}

func validateStandaloneOptions(options StandaloneOptions) error {
	if options.Interface == "" {
		return errors.New("interface is required")
	}
	if options.ActivationWait <= 0 {
		return errors.New("activation wait must be positive")
	}
	if options.RollbackAfter <= 0 || options.RollbackAfter%time.Second != 0 {
		return errors.New("checkpoint rollback duration must be a positive whole number of seconds")
	}
	if options.RestorationWait <= 0 {
		return errors.New("setup restoration wait must be positive")
	}
	if options.PreviousUUID == "" {
		return errors.New("previous profile UUID is required")
	}
	if !usableIPv4(options.PreviousAddress) {
		return errors.New("previous IPv4 address must be usable")
	}
	return nil
}
