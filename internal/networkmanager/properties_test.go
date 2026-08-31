package networkmanager

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// The typed accessors are only reachable over a live system bus, so the half
// that can go wrong without the compiler noticing -- the type assertion and the
// wording of its error -- is tested here directly.

func TestConvertPropertySucceeds(t *testing.T) {
	if got, err := convertProperty[string](dbus.MakeVariant("wlan0"), "i", "p", "string"); err != nil || got != "wlan0" {
		t.Fatalf("string = %q, err = %v", got, err)
	}
	if got, err := convertProperty[bool](dbus.MakeVariant(true), "i", "p", "bool"); err != nil || !got {
		t.Fatalf("bool = %t, err = %v", got, err)
	}
	if got, err := convertProperty[uint32](dbus.MakeVariant(uint32(100)), "i", "p", "uint32"); err != nil || got != 100 {
		t.Fatalf("uint32 = %d, err = %v", got, err)
	}
	if got, err := convertProperty[int64](dbus.MakeVariant(int64(-5)), "i", "p", "int64"); err != nil || got != -5 {
		t.Fatalf("int64 = %d, err = %v", got, err)
	}
	if got, err := convertProperty[uint8](dbus.MakeVariant(uint8(70)), "i", "p", "byte"); err != nil || got != 70 {
		t.Fatalf("byte = %d, err = %v", got, err)
	}
	got, err := convertProperty[[]byte](dbus.MakeVariant([]byte("ssid")), "i", "p", "byte array")
	if err != nil || string(got) != "ssid" {
		t.Fatalf("byte array = %q, err = %v", got, err)
	}
	path, err := convertProperty[dbus.ObjectPath](
		dbus.MakeVariant(dbus.ObjectPath("/org/x")), "i", "p", "object path",
	)
	if err != nil || path != "/org/x" {
		t.Fatalf("object path = %q, err = %v", path, err)
	}
}

// A mismatch must name the interface, the property, the type NetworkManager
// actually sent, and the type onboardd expected. These strings reach the
// journal, so they are pinned exactly.
func TestConvertPropertyReportsTypeMismatch(t *testing.T) {
	const iface = "org.freedesktop.NetworkManager.Device"

	tests := []struct {
		name     string
		convert  func() error
		expected string
	}{
		{
			name: "string",
			convert: func() error {
				_, err := convertProperty[string](dbus.MakeVariant(uint32(1)), iface, "Managed", "string")
				return err
			},
			expected: iface + ".Managed returned uint32, expected string",
		},
		{
			name: "bool",
			convert: func() error {
				_, err := convertProperty[bool](dbus.MakeVariant("yes"), iface, "Managed", "bool")
				return err
			},
			expected: iface + ".Managed returned string, expected bool",
		},
		{
			name: "uint32",
			convert: func() error {
				_, err := convertProperty[uint32](dbus.MakeVariant("100"), iface, "State", "uint32")
				return err
			},
			expected: iface + ".State returned string, expected uint32",
		},
		{
			name: "int64",
			convert: func() error {
				_, err := convertProperty[int64](dbus.MakeVariant(uint32(1)), iface, "LastScan", "int64")
				return err
			},
			expected: iface + ".LastScan returned uint32, expected int64",
		},
		{
			name: "byte",
			convert: func() error {
				_, err := convertProperty[uint8](dbus.MakeVariant(uint32(70)), iface, "Strength", "byte")
				return err
			},
			expected: iface + ".Strength returned uint32, expected byte",
		},
		{
			name: "byte array",
			convert: func() error {
				_, err := convertProperty[[]byte](dbus.MakeVariant("ssid"), iface, "Ssid", "byte array")
				return err
			},
			expected: iface + ".Ssid returned string, expected byte array",
		},
		{
			name: "object path",
			convert: func() error {
				_, err := convertProperty[dbus.ObjectPath](dbus.MakeVariant("/org/x"), iface, "Ip4Config", "object path")
				return err
			},
			expected: iface + ".Ip4Config returned string, expected object path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.convert()
			if err == nil {
				t.Fatal("expected a type mismatch error")
			}
			if err.Error() != test.expected {
				t.Fatalf("error = %q, expected %q", err.Error(), test.expected)
			}
		})
	}
}

// A mismatch must yield the zero value, not a partially converted one. bytes and
// object path are the cases where that is not obviously true.
func TestConvertPropertyReturnsZeroValueOnMismatch(t *testing.T) {
	if got, _ := convertProperty[[]byte](dbus.MakeVariant("ssid"), "i", "p", "byte array"); got != nil {
		t.Fatalf("byte array zero value = %v, expected nil", got)
	}
	if got, _ := convertProperty[dbus.ObjectPath](dbus.MakeVariant(uint32(1)), "i", "p", "object path"); got != "" {
		t.Fatalf("object path zero value = %q, expected empty", got)
	}
	if got, _ := convertProperty[string](dbus.MakeVariant(uint32(1)), "i", "p", "string"); got != "" {
		t.Fatalf("string zero value = %q, expected empty", got)
	}
	if got, _ := convertProperty[uint32](dbus.MakeVariant("x"), "i", "p", "uint32"); got != 0 {
		t.Fatalf("uint32 zero value = %d, expected 0", got)
	}
}
