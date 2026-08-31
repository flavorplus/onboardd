package setupflow

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/flavorplus/onboardd/internal/connectivity"
	"github.com/flavorplus/onboardd/internal/networkmanager"
	"github.com/flavorplus/onboardd/internal/recovery"
)

type networkClient interface {
	Status(context.Context, string) (networkmanager.Status, error)
	Profiles(context.Context) ([]networkmanager.Profile, error)
	Scan(context.Context, string, time.Duration) ([]networkmanager.AccessPoint, error)
	DeleteOwnedInfrastructureProfile(context.Context, string, string) error
}

type infrastructureTransition interface {
	Attempt(context.Context, recovery.InfrastructureOptions) (networkmanager.Activation, error)
	AttemptSaved(context.Context, recovery.SavedInfrastructureOptions) (networkmanager.Activation, error)
}

type standaloneTransition interface {
	Attempt(context.Context, recovery.StandaloneOptions) (networkmanager.Activation, error)
}

// CaptiveExiter removes temporary captive resources after a production mode succeeds.
// LeaveProvisioning is idempotent so later mode changes can reuse the same backend.
type CaptiveExiter interface {
	LeaveProvisioning(context.Context) error
}

// NetworkOptions contains runtime policy already resolved from product configuration.
type NetworkOptions struct {
	Interface         string
	Requirement       connectivity.Requirement
	ScanWait          time.Duration
	ActivationWait    time.Duration
	RollbackAfter     time.Duration
	RestorationWait   time.Duration
	StandaloneEnabled bool
	Standalone        networkmanager.AccessPointOptions
}

// NetworkBackend connects the setup workflow to NetworkManager and recovery adapters.
type NetworkBackend struct {
	network        networkClient
	infrastructure infrastructureTransition
	standalone     standaloneTransition
	captive        CaptiveExiter
	options        NetworkOptions
}

// NewNetworkBackend validates resolved network policy and creates the real setup backend.
func NewNetworkBackend(
	network networkClient,
	infrastructure infrastructureTransition,
	standalone standaloneTransition,
	captive CaptiveExiter,
	options NetworkOptions,
) (*NetworkBackend, error) {
	if network == nil || infrastructure == nil || standalone == nil {
		return nil, errors.New("network and recovery adapters are required")
	}
	if options.Interface == "" {
		return nil, errors.New("interface is required")
	}
	if err := options.Requirement.Validate(); err != nil {
		return nil, err
	}
	if options.ScanWait < 0 {
		return nil, errors.New("scan wait cannot be negative")
	}
	if options.ActivationWait <= 0 || options.RestorationWait <= 0 {
		return nil, errors.New("activation and restoration waits must be positive")
	}
	if options.RollbackAfter <= 0 || options.RollbackAfter%time.Second != 0 {
		return nil, errors.New("checkpoint rollback duration must be a positive whole number of seconds")
	}
	if options.StandaloneEnabled {
		standaloneCandidate := options.Standalone
		standaloneCandidate.Interface = options.Interface
		standaloneCandidate.Role = networkmanager.RoleStandalone
		standaloneCandidate.Autoconnect = false
		if _, _, err := networkmanager.BuildAccessPointSettings(standaloneCandidate); err != nil {
			return nil, err
		}
		options.Standalone = standaloneCandidate
	}
	return &NetworkBackend{
		network:        network,
		infrastructure: infrastructure,
		standalone:     standalone,
		captive:        captive,
		options:        options,
	}, nil
}

// CurrentMode derives product-facing mode from the active profile and durable
// autoconnect intent without relying on connection names.
func (backend *NetworkBackend) CurrentMode(ctx context.Context) (Mode, error) {
	status, err := backend.network.Status(ctx, backend.options.Interface)
	if err != nil {
		return ModeUnknown, err
	}
	profiles, err := backend.network.Profiles(ctx)
	if err != nil {
		return ModeUnknown, err
	}
	if status.Device.ActiveUUID != "" {
		for _, profile := range profiles {
			if profile.UUID == status.Device.ActiveUUID {
				if profile.Owned {
					return productMode(profile.Role), nil
				}
				return ModeNetwork, nil
			}
		}
		return ModeNetwork, nil
	}
	for _, profile := range profiles {
		if profile.Owned && profile.Role == networkmanager.RoleStandalone && profile.Autoconnect {
			return ModeStandalone, nil
		}
	}
	for _, profile := range profiles {
		if profile.Owned && profile.Role == networkmanager.RoleInfrastructure && profile.Autoconnect {
			return ModeNetwork, nil
		}
	}
	return ModeUnknown, nil
}

// Networks translates NetworkManager scan details into the small browser contract.
func (backend *NetworkBackend) Networks(ctx context.Context) ([]Network, error) {
	accessPoints, err := backend.network.Scan(ctx, backend.options.Interface, backend.options.ScanWait)
	if err != nil {
		return nil, err
	}
	networks := make([]Network, 0, len(accessPoints))
	for _, accessPoint := range accessPoints {
		security := "protected"
		switch accessPoint.Security {
		case networkmanager.SecurityOpen:
			security = "open"
		case networkmanager.SecurityWPA, networkmanager.SecurityWPA2:
		default:
			continue
		}
		networks = append(networks, Network{
			SSID:     accessPoint.SSID,
			Security: security,
			Strength: accessPoint.Strength,
		})
	}
	return networks, nil
}

// KnownNetworks returns saved Wi-Fi client profiles that may apply to the configured
// interface. Only onboardd-owned profiles on the exact interface are forgettable.
func (backend *NetworkBackend) KnownNetworks(ctx context.Context) ([]KnownNetwork, error) {
	status, err := backend.network.Status(ctx, backend.options.Interface)
	if err != nil {
		return nil, err
	}
	profiles, err := backend.network.Profiles(ctx)
	if err != nil {
		return nil, err
	}
	known := make([]KnownNetwork, 0, len(profiles))
	for _, profile := range profiles {
		if profile.SSID == "" || !profile.IsInfrastructureWiFi() ||
			!profile.AppliesTo(backend.options.Interface) {
			continue
		}
		managed := profile.Owned && profile.Role == networkmanager.RoleInfrastructure &&
			profile.Interface == backend.options.Interface
		active := profile.UUID == status.Device.ActiveUUID
		known = append(known, KnownNetwork{
			UUID:       profile.UUID,
			SSID:       profile.SSID,
			Managed:    managed,
			Active:     active,
			Automatic:  profile.Autoconnect,
			CanConnect: managed && !active,
			CanForget:  managed && !active,
		})
	}
	return known, nil
}

// ForgetKnownNetwork removes one inactive profile after rechecking ownership, role,
// interface, and active connection state on the server.
func (backend *NetworkBackend) ForgetKnownNetwork(ctx context.Context, uuid string) error {
	profile, status, err := backend.knownProfile(ctx, uuid)
	if err != nil {
		return err
	}
	if profile.UUID == status.Device.ActiveUUID {
		return NewPublicError(
			"active_network",
			"The network currently in use cannot be forgotten. Connect to another network first.",
		)
	}
	if !backend.managesInfrastructureProfile(profile) {
		return NewPublicError(
			"network_read_only",
			"This network profile is managed outside onboardd and cannot be forgotten here.",
		)
	}
	return backend.network.DeleteOwnedInfrastructureProfile(
		ctx,
		backend.options.Interface,
		uuid,
	)
}

// Connect applies a protected infrastructure transition and exits temporary captive
// mode only after durable mode selection succeeds.
func (backend *NetworkBackend) Connect(ctx context.Context, request ConnectionRequest) error {
	previousUUID, previousAddress, err := backend.previousConnection(ctx)
	if err != nil {
		return err
	}
	_, err = backend.infrastructure.Attempt(ctx, recovery.InfrastructureOptions{
		Interface: backend.options.Interface,
		Candidate: networkmanager.InfrastructureOptions{
			SSID:     request.SSID,
			Password: request.Password,
			Open:     request.Open,
			Hidden:   request.Hidden,
		},
		Requirement:     backend.options.Requirement,
		ActivationWait:  backend.options.ActivationWait,
		RollbackAfter:   backend.options.RollbackAfter,
		RestorationWait: backend.options.RestorationWait,
		PreviousUUID:    previousUUID,
		PreviousAddress: previousAddress,
	})
	if err != nil {
		return publicTransitionFailure(err)
	}
	return backend.exitCaptive(ctx)
}

// ConnectKnownNetwork activates an existing onboardd-owned infrastructure profile
// through the same checkpoint and connectivity policy as a newly entered network.
func (backend *NetworkBackend) ConnectKnownNetwork(ctx context.Context, uuid string) error {
	profile, status, err := backend.knownProfile(ctx, uuid)
	if err != nil {
		return err
	}
	if profile.UUID == status.Device.ActiveUUID {
		return errActiveNetwork
	}
	if !backend.managesInfrastructureProfile(profile) {
		return errNetworkReadOnly
	}
	previousUUID, previousAddress, err := previousConnection(status)
	if err != nil {
		return err
	}
	_, err = backend.infrastructure.AttemptSaved(ctx, recovery.SavedInfrastructureOptions{
		Interface:       backend.options.Interface,
		UUID:            profile.UUID,
		SSID:            profile.SSID,
		Requirement:     backend.options.Requirement,
		ActivationWait:  backend.options.ActivationWait,
		RollbackAfter:   backend.options.RollbackAfter,
		RestorationWait: backend.options.RestorationWait,
		PreviousUUID:    previousUUID,
		PreviousAddress: previousAddress,
	})
	if err != nil {
		return publicTransitionFailure(err)
	}
	return backend.exitCaptive(ctx)
}

// Standalone applies a checkpoint-protected AP transition using server-owned policy.
func (backend *NetworkBackend) Standalone(ctx context.Context) error {
	if !backend.options.StandaloneEnabled {
		return errModeUnavailableStandalone
	}
	previousUUID, previousAddress, err := backend.previousConnection(ctx)
	if err != nil {
		return err
	}
	_, err = backend.standalone.Attempt(ctx, recovery.StandaloneOptions{
		Interface:       backend.options.Interface,
		Candidate:       backend.options.Standalone,
		ActivationWait:  backend.options.ActivationWait,
		RollbackAfter:   backend.options.RollbackAfter,
		RestorationWait: backend.options.RestorationWait,
		PreviousUUID:    previousUUID,
		PreviousAddress: previousAddress,
	})
	if err != nil {
		return publicTransitionFailure(err)
	}
	return backend.exitCaptive(ctx)
}

func (backend *NetworkBackend) previousConnection(ctx context.Context) (string, netip.Addr, error) {
	status, err := backend.network.Status(ctx, backend.options.Interface)
	if err != nil {
		return "", netip.Addr{}, errCurrentConnectionUnavailable
	}
	return previousConnection(status)
}

func previousConnection(status networkmanager.Status) (string, netip.Addr, error) {
	if status.Device.State != networkmanager.DeviceStateActivated || status.Device.ActiveUUID == "" {
		return "", netip.Addr{}, errCurrentConnectionUnavailable
	}
	for _, value := range status.Device.IPv4Addresses {
		address, parseErr := netip.ParseAddr(value)
		if parseErr == nil && address.Is4() && !address.IsUnspecified() {
			return status.Device.ActiveUUID, address, nil
		}
	}
	return "", netip.Addr{}, errCurrentConnectionUnavailable
}

func (backend *NetworkBackend) knownProfile(
	ctx context.Context,
	uuid string,
) (networkmanager.Profile, networkmanager.Status, error) {
	status, err := backend.network.Status(ctx, backend.options.Interface)
	if err != nil {
		return networkmanager.Profile{}, networkmanager.Status{}, err
	}
	profiles, err := backend.network.Profiles(ctx)
	if err != nil {
		return networkmanager.Profile{}, networkmanager.Status{}, err
	}
	for _, profile := range profiles {
		if profile.UUID == uuid {
			return profile, status, nil
		}
	}
	return networkmanager.Profile{}, networkmanager.Status{}, errKnownNetworkNotFound
}

func (backend *NetworkBackend) managesInfrastructureProfile(profile networkmanager.Profile) bool {
	return profile.Owned && profile.Role == networkmanager.RoleInfrastructure &&
		profile.Interface == backend.options.Interface && profile.IsInfrastructureWiFi()
}

func (backend *NetworkBackend) exitCaptive(ctx context.Context) error {
	if backend.captive == nil {
		return nil
	}
	if err := backend.captive.LeaveProvisioning(ctx); err != nil {
		return NewPublicError(
			"cleanup_failed",
			"The network changed, but setup could not finish cleaning up. Restart the device before trying again.",
		)
	}
	return nil
}

func productMode(role networkmanager.Role) Mode {
	switch role {
	case networkmanager.RoleProvisioning:
		return ModeSetup
	case networkmanager.RoleInfrastructure:
		return ModeNetwork
	case networkmanager.RoleStandalone:
		return ModeStandalone
	default:
		return ModeUnknown
	}
}

func publicTransitionFailure(err error) error {
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "rollback networkmanager checkpoint") ||
		strings.Contains(message, "confirm previous connection restoration") {
		return NewPublicError(
			"restoration_failed",
			"The previous connection could not be restored automatically. Restart the device to reopen setup.",
		)
	}
	if strings.Contains(message, "internet-not-confirmed") ||
		strings.Contains(message, "connectivity") {
		return NewPublicError(
			"connectivity_unavailable",
			"The network connected, but it does not provide the access this device requires.",
		)
	}
	return NewPublicError(
		"connection_failed",
		"The device could not join that network. Check the network name and password, then try again.",
	)
}
