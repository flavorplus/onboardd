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

// convertProperty asserts a D-Bus variant to T. The wanted description is passed
// in rather than derived from T because the messages predate this helper and do
// not match Go's type names: uint8 is reported as "byte", []byte as "byte array"
// and dbus.ObjectPath as "object path".
func convertProperty[T any](value dbus.Variant, interfaceName, propertyName, wanted string) (T, error) {
	result, ok := value.Value().(T)
	if !ok {
		var zero T
		return zero, propertyTypeError(interfaceName, propertyName, wanted, value.Value())
	}
	return result, nil
}

// clientProperty reads one property and converts it, so the typed accessors below
// stay one line each.
func clientProperty[T any](
	ctx context.Context,
	c *Client,
	path dbus.ObjectPath,
	interfaceName, propertyName, wanted string,
) (T, error) {
	value, err := c.property(ctx, path, interfaceName, propertyName)
	if err != nil {
		var zero T
		return zero, err
	}
	return convertProperty[T](value, interfaceName, propertyName, wanted)
}

func (c *Client) stringProperty(ctx context.Context, p dbus.ObjectPath, iface, name string) (string, error) {
	return clientProperty[string](ctx, c, p, iface, name, "string")
}

func (c *Client) boolProperty(ctx context.Context, p dbus.ObjectPath, iface, name string) (bool, error) {
	return clientProperty[bool](ctx, c, p, iface, name, "bool")
}

func (c *Client) uint32Property(ctx context.Context, p dbus.ObjectPath, iface, name string) (uint32, error) {
	return clientProperty[uint32](ctx, c, p, iface, name, "uint32")
}

func (c *Client) int64Property(ctx context.Context, p dbus.ObjectPath, iface, name string) (int64, error) {
	return clientProperty[int64](ctx, c, p, iface, name, "int64")
}

func (c *Client) byteProperty(ctx context.Context, p dbus.ObjectPath, iface, name string) (uint8, error) {
	return clientProperty[uint8](ctx, c, p, iface, name, "byte")
}

func (c *Client) bytesProperty(ctx context.Context, p dbus.ObjectPath, iface, name string) ([]byte, error) {
	return clientProperty[[]byte](ctx, c, p, iface, name, "byte array")
}

func (c *Client) objectPathProperty(ctx context.Context, p dbus.ObjectPath, iface, name string) (dbus.ObjectPath, error) {
	return clientProperty[dbus.ObjectPath](ctx, c, p, iface, name, "object path")
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
