package config

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

var defaultMachineIDPaths = []string{
	"/etc/machine-id",
	"/var/lib/dbus/machine-id",
}

const deviceIDNamespace = "github.com/flavorplus/onboardd/device-id/v1"

// Identity contains the only operating-system identity values exposed to templates.
// DeviceID is application-specific and does not reveal the raw machine ID.
type Identity struct {
	DeviceID string
	Hostname string
}

// LoadIdentity reads the supported Linux machine ID and the current hostname.
func LoadIdentity() (Identity, error) {
	return loadIdentity(defaultMachineIDPaths, os.Hostname)
}

func loadIdentity(machineIDPaths []string, hostname func() (string, error)) (Identity, error) {
	name, err := hostname()
	if err != nil {
		return Identity{}, fmt.Errorf("read hostname: %w", err)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Identity{}, errors.New("hostname must not be empty")
	}

	for _, path := range machineIDPaths {
		contents, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return Identity{}, fmt.Errorf("read machine ID %q: %w", path, err)
		}
		deviceID, err := deriveDeviceID(string(contents))
		if err != nil {
			return Identity{}, fmt.Errorf("read machine ID %q: %w", path, err)
		}
		return Identity{DeviceID: deviceID, Hostname: name}, nil
	}
	return Identity{}, fmt.Errorf("machine ID not found in %s", strings.Join(machineIDPaths, " or "))
}

func deriveDeviceID(machineID string) (string, error) {
	normalized := strings.TrimSpace(machineID)
	if len(normalized) != 32 {
		return "", errors.New("machine ID must contain exactly 32 hexadecimal characters")
	}
	decoded, err := hex.DecodeString(normalized)
	if err != nil {
		return "", errors.New("machine ID must contain exactly 32 hexadecimal characters")
	}

	mac := hmac.New(sha256.New, decoded)
	_, _ = mac.Write([]byte(deviceIDNamespace))
	digest := mac.Sum(nil)
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:5]), nil
}
