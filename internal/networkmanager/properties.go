package networkmanager

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"
)

func (c *Client) property(
	ctx context.Context,
	path dbus.ObjectPath,
	interfaceName string,
	propertyName string,
) (dbus.Variant, error) {
	var value dbus.Variant
	err := c.object(path).CallWithContext(
		ctx,
		propertiesInterface+".Get",
		0,
		interfaceName,
		propertyName,
	).Store(&value)
	if err != nil {
		return dbus.Variant{}, fmt.Errorf("read %s.%s: %w", interfaceName, propertyName, err)
	}
	return value, nil
}

func (c *Client) stringProperty(
	ctx context.Context,
	path dbus.ObjectPath,
	interfaceName string,
	propertyName string,
) (string, error) {
	value, err := c.property(ctx, path, interfaceName, propertyName)
	if err != nil {
		return "", err
	}
	result, ok := value.Value().(string)
	if !ok {
		return "", propertyTypeError(interfaceName, propertyName, "string", value.Value())
	}
	return result, nil
}

func (c *Client) boolProperty(
	ctx context.Context,
	path dbus.ObjectPath,
	interfaceName string,
	propertyName string,
) (bool, error) {
	value, err := c.property(ctx, path, interfaceName, propertyName)
	if err != nil {
		return false, err
	}
	result, ok := value.Value().(bool)
	if !ok {
		return false, propertyTypeError(interfaceName, propertyName, "bool", value.Value())
	}
	return result, nil
}

func (c *Client) uint32Property(
	ctx context.Context,
	path dbus.ObjectPath,
	interfaceName string,
	propertyName string,
) (uint32, error) {
	value, err := c.property(ctx, path, interfaceName, propertyName)
	if err != nil {
		return 0, err
	}
	result, ok := value.Value().(uint32)
	if !ok {
		return 0, propertyTypeError(interfaceName, propertyName, "uint32", value.Value())
	}
	return result, nil
}

func (c *Client) int64Property(
	ctx context.Context,
	path dbus.ObjectPath,
	interfaceName string,
	propertyName string,
) (int64, error) {
	value, err := c.property(ctx, path, interfaceName, propertyName)
	if err != nil {
		return 0, err
	}
	result, ok := value.Value().(int64)
	if !ok {
		return 0, propertyTypeError(interfaceName, propertyName, "int64", value.Value())
	}
	return result, nil
}

func (c *Client) byteProperty(
	ctx context.Context,
	path dbus.ObjectPath,
	interfaceName string,
	propertyName string,
) (uint8, error) {
	value, err := c.property(ctx, path, interfaceName, propertyName)
	if err != nil {
		return 0, err
	}
	result, ok := value.Value().(uint8)
	if !ok {
		return 0, propertyTypeError(interfaceName, propertyName, "byte", value.Value())
	}
	return result, nil
}

func (c *Client) bytesProperty(
	ctx context.Context,
	path dbus.ObjectPath,
	interfaceName string,
	propertyName string,
) ([]byte, error) {
	value, err := c.property(ctx, path, interfaceName, propertyName)
	if err != nil {
		return nil, err
	}
	result, ok := value.Value().([]byte)
	if !ok {
		return nil, propertyTypeError(interfaceName, propertyName, "byte array", value.Value())
	}
	return result, nil
}

func (c *Client) objectPathProperty(
	ctx context.Context,
	path dbus.ObjectPath,
	interfaceName string,
	propertyName string,
) (dbus.ObjectPath, error) {
	value, err := c.property(ctx, path, interfaceName, propertyName)
	if err != nil {
		return "", err
	}
	result, ok := value.Value().(dbus.ObjectPath)
	if !ok {
		return "", propertyTypeError(interfaceName, propertyName, "object path", value.Value())
	}
	return result, nil
}

func (c *Client) deviceStateReason(
	ctx context.Context,
	path dbus.ObjectPath,
) (DeviceState, DeviceStateReason, error) {
	value, err := c.property(ctx, path, deviceInterface, "StateReason")
	if err != nil {
		return DeviceStateUnknown, DeviceStateReasonUnknown, err
	}
	return parseDeviceStateReason(value.Value())
}

func parseDeviceStateReason(value any) (DeviceState, DeviceStateReason, error) {
	fields, ok := value.([]any)
	if !ok || len(fields) != 2 {
		return DeviceStateUnknown, DeviceStateReasonUnknown, propertyTypeError(
			deviceInterface,
			"StateReason",
			"(uint32, uint32)",
			value,
		)
	}
	state, stateOK := fields[0].(uint32)
	reason, reasonOK := fields[1].(uint32)
	if !stateOK || !reasonOK {
		return DeviceStateUnknown, DeviceStateReasonUnknown, propertyTypeError(
			deviceInterface,
			"StateReason",
			"(uint32, uint32)",
			value,
		)
	}
	return DeviceState(state), DeviceStateReason(reason), nil
}

func propertyTypeError(interfaceName, propertyName, wanted string, actual any) error {
	return fmt.Errorf(
		"%s.%s returned %T, expected %s",
		interfaceName,
		propertyName,
		actual,
		wanted,
	)
}

func variantString(value dbus.Variant) string {
	result, _ := value.Value().(string)
	return result
}

func variantInt32(value dbus.Variant) int32 {
	result, _ := value.Value().(int32)
	return result
}

func variantBoolDefault(value dbus.Variant, fallback bool) bool {
	if value.Signature().Empty() {
		return fallback
	}
	result, ok := value.Value().(bool)
	if !ok {
		return fallback
	}
	return result
}

func variantBytes(value dbus.Variant) []byte {
	result, _ := value.Value().([]byte)
	return result
}

func variantMapStringString(value dbus.Variant) map[string]string {
	result, _ := value.Value().(map[string]string)
	return result
}
