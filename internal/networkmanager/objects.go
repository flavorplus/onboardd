package networkmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/godbus/dbus/v5"
)

func (c *Client) device(ctx context.Context, path dbus.ObjectPath) (Device, error) {
	managed, err := c.boolProperty(ctx, path, deviceInterface, "Managed")
	if err != nil {
		return Device{}, err
	}
	stateValue, err := c.uint32Property(ctx, path, deviceInterface, "State")
	if err != nil {
		return Device{}, err
	}
	active, err := c.objectPathProperty(ctx, path, deviceInterface, "ActiveConnection")
	if err != nil {
		return Device{}, err
	}
	activeUUID := ""
	if active != noSpecificObject {
		activeUUID, err = c.stringProperty(ctx, active, activeConnectionIface, "Uuid")
		if err != nil {
			return Device{}, err
		}
	}
	ipv4Addresses, err := c.deviceIPv4Addresses(ctx, path)
	if err != nil {
		return Device{}, err
	}
	state := DeviceState(stateValue)
	return Device{
		Managed:          managed,
		State:            state,
		ActiveConnection: cleanRootPath(active),
		ActiveUUID:       activeUUID,
		IPv4Addresses:    ipv4Addresses,
	}, nil
}

func (c *Client) deviceIPv4Addresses(ctx context.Context, devicePath dbus.ObjectPath) ([]string, error) {
	configPath, err := c.objectPathProperty(ctx, devicePath, deviceInterface, "Ip4Config")
	if err != nil {
		return nil, err
	}
	if configPath == noSpecificObject {
		return nil, nil
	}
	value, err := c.property(
		ctx,
		configPath,
		"org.freedesktop.NetworkManager.IP4Config",
		"AddressData",
	)
	if err != nil {
		return nil, err
	}
	addressData, ok := value.Value().([]map[string]dbus.Variant)
	if !ok {
		return nil, propertyTypeError(
			"org.freedesktop.NetworkManager.IP4Config",
			"AddressData",
			"array of string/variant maps",
			value.Value(),
		)
	}
	addresses := make([]string, 0, len(addressData))
	for _, item := range addressData {
		if address := variantString(item["address"]); address != "" {
			addresses = append(addresses, address)
		}
	}
	return addresses, nil
}

func (c *Client) devicePath(ctx context.Context, interfaceName string) (dbus.ObjectPath, error) {
	if interfaceName == "" {
		return "", errors.New("interface name is required")
	}
	var path dbus.ObjectPath
	if err := c.object(managerPath).CallWithContext(
		ctx,
		managerInterface+".GetDeviceByIpIface",
		0,
		interfaceName,
	).Store(&path); err != nil {
		return "", fmt.Errorf("find NetworkManager interface %q: %w", interfaceName, err)
	}
	deviceType, err := c.uint32Property(ctx, path, deviceInterface, "DeviceType")
	if err != nil {
		return "", err
	}
	if deviceType != wifiDeviceType {
		return "", fmt.Errorf("interface %q is not a Wi-Fi device (type %d)", interfaceName, deviceType)
	}
	return path, nil
}

func (c *Client) profile(ctx context.Context, path dbus.ObjectPath) (Profile, error) {
	settings, err := c.connectionSettings(ctx, path)
	if err != nil {
		return Profile{}, err
	}
	filename, err := c.stringProperty(ctx, path, settingsConnectionIface, "Filename")
	if err != nil {
		return Profile{}, err
	}
	connection := settings["connection"]
	user := settings["user"]
	wireless := settings["802-11-wireless"]
	metadata := variantMapStringString(user["data"])
	ssidBytes := variantBytes(wireless["ssid"])
	owner := metadata[ownerKey] == ownerName
	return Profile{
		Path:        string(path),
		ID:          variantString(connection["id"]),
		UUID:        variantString(connection["uuid"]),
		Type:        variantString(connection["type"]),
		Interface:   variantString(connection["interface-name"]),
		SSID:        string(ssidBytes),
		Mode:        variantString(wireless["mode"]),
		Autoconnect: variantBoolDefault(connection["autoconnect"], true),
		Priority:    variantInt32(connection["autoconnect-priority"]),
		Owned:       owner,
		Role:        Role(metadata[roleKey]),
		Pending:     metadata[pendingKey] == "true",
		inMemory:    profileInMemory(filename),
	}, nil
}

// connectionSettings deliberately reads the raw call body instead of Call.Store.
// Store recursively reconstructs nested variants from their Go representation, which
// can lose the original signature of legacy tuple-valued NetworkManager settings.
func (c *Client) connectionSettings(ctx context.Context, path dbus.ObjectPath) (Settings, error) {
	call := c.object(path).CallWithContext(
		ctx,
		settingsConnectionIface+".GetSettings",
		0,
	)
	if call.Err != nil {
		return nil, call.Err
	}
	return settingsFromCallBody(call.Body)
}

func settingsFromCallBody(body []any) (Settings, error) {
	if len(body) != 1 {
		return nil, fmt.Errorf("GetSettings returned %d fields, expected one", len(body))
	}
	settings, ok := body[0].(map[string]map[string]dbus.Variant)
	if !ok {
		return nil, fmt.Errorf("GetSettings returned %T, expected a{sa{sv}}", body[0])
	}
	return Settings(settings), nil
}

func (c *Client) connectionSecrets(
	ctx context.Context,
	path dbus.ObjectPath,
	settingName string,
) (Settings, error) {
	call := c.object(path).CallWithContext(
		ctx,
		settingsConnectionIface+".GetSecrets",
		0,
		settingName,
	)
	if call.Err != nil {
		return nil, call.Err
	}
	return settingsFromCallBody(call.Body)
}

func isObjectGone(err error) bool {
	if err == nil {
		return false
	}
	var pointerError *dbus.Error
	if errors.As(err, &pointerError) {
		return objectGoneErrorName(pointerError.Name)
	}
	var valueError dbus.Error
	if errors.As(err, &valueError) {
		return objectGoneErrorName(valueError.Name)
	}
	return false
}

func objectGoneErrorName(name string) bool {
	switch name {
	case "org.freedesktop.DBus.Error.UnknownObject",
		"org.freedesktop.DBus.Error.UnknownInterface",
		"org.freedesktop.DBus.Error.UnknownMethod",
		"org.freedesktop.NetworkManager.UnknownConnection":
		return true
	default:
		return false
	}
}

func profileInMemory(filename string) bool {
	cleaned := strings.TrimSpace(filename)
	return cleaned == "" ||
		strings.HasPrefix(cleaned, "/run/") ||
		strings.HasPrefix(cleaned, "/var/run/")
}

func (c *Client) object(path dbus.ObjectPath) dbus.BusObject {
	return c.conn.Object(service, path)
}

func cleanRootPath(path dbus.ObjectPath) string {
	if path == noSpecificObject {
		return ""
	}
	return string(path)
}

func validateCheckpointPath(value string) (dbus.ObjectPath, error) {
	path := dbus.ObjectPath(value)
	if !path.IsValid() || !strings.HasPrefix(value, string(managerPath)+"/Checkpoint/") {
		return "", fmt.Errorf("invalid NetworkManager checkpoint path %q", value)
	}
	return path, nil
}
