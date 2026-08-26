package networkmanager

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/godbus/dbus/v5"
)

// Scan requests a Wi-Fi scan, waits up to wait for LastScan to change, and returns the
// latest access-point list. A zero wait requests the scan and immediately returns the
// current list.
func (c *Client) Scan(ctx context.Context, interfaceName string, wait time.Duration) ([]AccessPoint, error) {
	devicePath, err := c.devicePath(ctx, interfaceName)
	if err != nil {
		return nil, err
	}
	before, err := c.int64Property(ctx, devicePath, wirelessDeviceInterface, "LastScan")
	if err != nil {
		return nil, err
	}

	if err := c.object(devicePath).CallWithContext(
		ctx,
		wirelessDeviceInterface+".RequestScan",
		0,
		map[string]dbus.Variant{},
	).Store(); err != nil {
		return nil, fmt.Errorf("request Wi-Fi scan on %s: %w", interfaceName, err)
	}

	if wait > 0 {
		deadline := time.NewTimer(wait)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer deadline.Stop()
		defer ticker.Stop()

	waitLoop:
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-deadline.C:
				break waitLoop
			case <-ticker.C:
				last, propertyErr := c.int64Property(ctx, devicePath, wirelessDeviceInterface, "LastScan")
				if propertyErr != nil {
					return nil, propertyErr
				}
				if last > before {
					break waitLoop
				}
			}
		}
	}

	var paths []dbus.ObjectPath
	if err := c.object(devicePath).CallWithContext(
		ctx,
		wirelessDeviceInterface+".GetAllAccessPoints",
		0,
	).Store(&paths); err != nil {
		return nil, fmt.Errorf("list Wi-Fi access points on %s: %w", interfaceName, err)
	}

	accessPoints := make([]AccessPoint, 0, len(paths))
	for _, path := range paths {
		accessPoint, readErr := c.accessPoint(ctx, path)
		if readErr != nil {
			return nil, fmt.Errorf("read access point %s: %w", path, readErr)
		}
		accessPoints = append(accessPoints, accessPoint)
	}
	sort.Slice(accessPoints, func(i, j int) bool {
		if accessPoints[i].Strength == accessPoints[j].Strength {
			return accessPoints[i].SSID < accessPoints[j].SSID
		}
		return accessPoints[i].Strength > accessPoints[j].Strength
	})
	return accessPoints, nil
}

func (c *Client) accessPoint(ctx context.Context, path dbus.ObjectPath) (AccessPoint, error) {
	ssid, err := c.bytesProperty(ctx, path, accessPointInterface, "Ssid")
	if err != nil {
		return AccessPoint{}, err
	}
	strength, err := c.byteProperty(ctx, path, accessPointInterface, "Strength")
	if err != nil {
		return AccessPoint{}, err
	}
	flags, err := c.uint32Property(ctx, path, accessPointInterface, "Flags")
	if err != nil {
		return AccessPoint{}, err
	}
	wpaFlags, err := c.uint32Property(ctx, path, accessPointInterface, "WpaFlags")
	if err != nil {
		return AccessPoint{}, err
	}
	rsnFlags, err := c.uint32Property(ctx, path, accessPointInterface, "RsnFlags")
	if err != nil {
		return AccessPoint{}, err
	}
	return AccessPoint{
		SSID:     string(ssid),
		Strength: strength,
		Security: accessPointSecurity(flags, wpaFlags, rsnFlags),
	}, nil
}

func accessPointSecurity(flags, wpaFlags, rsnFlags uint32) Security {
	switch {
	case rsnFlags != 0:
		return SecurityWPA2
	case wpaFlags != 0:
		return SecurityWPA
	case flags&0x1 != 0:
		return SecurityWEP
	default:
		return SecurityOpen
	}
}
