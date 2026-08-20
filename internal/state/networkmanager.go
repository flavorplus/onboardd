package state

import (
	"context"

	"github.com/flavorplus/onboardd/internal/connectivity"
	"github.com/flavorplus/onboardd/internal/networkmanager"
)

type networkManagerClient interface {
	Status(context.Context, string) (networkmanager.Status, error)
	Profiles(context.Context) ([]networkmanager.Profile, error)
	WatchProperties(context.Context) (<-chan networkmanager.Event, <-chan error, error)
}

// NetworkManagerObserver translates the D-Bus adapter into state-engine observations.
type NetworkManagerObserver struct {
	client        networkManagerClient
	interfaceName string
}

// NewNetworkManagerObserver creates a normalized observer for one Wi-Fi interface.
func NewNetworkManagerObserver(client networkManagerClient, interfaceName string) *NetworkManagerObserver {
	return &NetworkManagerObserver{client: client, interfaceName: interfaceName}
}

func (observer *NetworkManagerObserver) Snapshot(ctx context.Context) (Snapshot, error) {
	status, err := observer.client.Status(ctx, observer.interfaceName)
	if err != nil {
		return Snapshot{}, err
	}
	profiles, err := observer.client.Profiles(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	normalizedProfiles := make([]Profile, 0, len(profiles))
	activeMode := ModeNone
	for _, profile := range profiles {
		mode := profileMode(profile)
		if profile.Interface == observer.interfaceName && mode != ModeNone {
			normalizedProfiles = append(normalizedProfiles, Profile{
				UUID:        profile.UUID,
				Mode:        mode,
				Autoconnect: profile.Autoconnect,
			})
		}
		if profile.UUID == status.Device.ActiveUUID {
			activeMode = mode
		}
	}

	return Snapshot{
		DeviceManaged: status.Device.Managed,
		DeviceState:   normalizedDeviceState(status.Device.State),
		ActiveUUID:    status.Device.ActiveUUID,
		ActiveMode:    activeMode,
		Connectivity: connectivity.Observation{
			Activated:       status.Device.State == networkmanager.DeviceStateActivated,
			HasLocalAddress: len(status.Device.IPv4Addresses) > 0,
			Internet:        normalizedInternetState(status.Connectivity),
		},
		Profiles: normalizedProfiles,
	}, nil
}

func (observer *NetworkManagerObserver) Watch(
	ctx context.Context,
) (<-chan NetworkChange, <-chan error, error) {
	events, sourceErrors, err := observer.client.WatchProperties(ctx)
	if err != nil {
		return nil, nil, err
	}
	changes := make(chan NetworkChange, 1)
	errorsOut := make(chan error, 1)
	go func() {
		defer close(changes)
		defer close(errorsOut)
		for events != nil || sourceErrors != nil {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				change := NetworkChange{Path: event.Path, Interface: event.Interface}
				select {
				case changes <- change:
				default:
				}
			case sourceErr, ok := <-sourceErrors:
				if !ok {
					sourceErrors = nil
					continue
				}
				select {
				case errorsOut <- sourceErr:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return changes, errorsOut, nil
}

func profileMode(profile networkmanager.Profile) Mode {
	switch profile.Role {
	case networkmanager.RoleInfrastructure:
		return ModeInfrastructure
	case networkmanager.RoleStandalone:
		return ModeStandalone
	case networkmanager.RoleProvisioning:
		return ModeProvisioning
	}
	if profile.Mode == "infrastructure" {
		return ModeInfrastructure
	}
	return ModeNone
}

func normalizedDeviceState(value networkmanager.DeviceState) DeviceState {
	switch value {
	case networkmanager.DeviceStateDisconnected:
		return DeviceDisconnected
	case networkmanager.DeviceStateActivated:
		return DeviceActivated
	case networkmanager.DeviceStateFailed:
		return DeviceFailed
	case networkmanager.DeviceStatePrepare,
		networkmanager.DeviceStateConfig,
		networkmanager.DeviceStateNeedAuth,
		networkmanager.DeviceStateIPConfig,
		networkmanager.DeviceStateIPCheck,
		networkmanager.DeviceStateSecondaries:
		return DeviceConnecting
	default:
		return DeviceUnknown
	}
}

func normalizedInternetState(value networkmanager.Connectivity) connectivity.InternetState {
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
