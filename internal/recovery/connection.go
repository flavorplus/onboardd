package recovery

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/flavorplus/onboardd/internal/networkmanager"
)

type statusReader interface {
	Status(context.Context, string) (networkmanager.Status, error)
}

type connectionRestorer interface {
	statusReader
	ActivateProfile(context.Context, string, string) (networkmanager.Activation, error)
	WaitForActivation(context.Context, string, string, time.Duration) error
}

func restorePreviousConnection(
	ctx context.Context,
	network connectionRestorer,
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
	network statusReader,
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
