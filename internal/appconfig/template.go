package appconfig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TemplateValues is the complete and deliberately small template namespace.
type TemplateValues struct {
	ProductName string
	DeviceName  string
	DeviceID    string
	Hostname    string
}

// RenderTemplates returns a copy with user-facing text and Wi-Fi names rendered. It
// supports field substitution only: functions, pipelines, loops, and code execution do
// not exist in this template language.
func RenderTemplates(config Config, identity Identity) (Config, error) {
	values := TemplateValues{
		ProductName: config.Product.Name,
		DeviceName:  config.Product.DeviceName,
		DeviceID:    identity.DeviceID,
		Hostname:    identity.Hostname,
	}
	if strings.TrimSpace(values.DeviceID) == "" {
		return Config{}, errors.New("template identity device ID must not be empty")
	}
	if strings.TrimSpace(values.Hostname) == "" {
		return Config{}, errors.New("template identity hostname must not be empty")
	}

	rendered := config
	fields := []struct {
		name   string
		target *string
	}{
		{"branding.text.title", &rendered.Branding.Text.Title},
		{"branding.text.subtitle", &rendered.Branding.Text.Subtitle},
		{"network.provisioning.ssid", &rendered.Network.Provisioning.SSID},
		{"handoff.application_label", &rendered.Handoff.ApplicationLabel},
		{"handoff.application_url", &rendered.Handoff.ApplicationURL},
		{"handoff.health_check_url", &rendered.Handoff.HealthCheckURL},
	}
	if rendered.Network.StandaloneEnabled {
		fields = append(fields, struct {
			name   string
			target *string
		}{"network.standalone.ssid", &rendered.Network.Standalone.SSID})
	}
	for _, field := range fields {
		value, err := renderTemplate(*field.target, values)
		if err != nil {
			return Config{}, fmt.Errorf("render %s: %w", field.name, err)
		}
		*field.target = value
	}
	if err := validateSSID("network.provisioning.ssid", rendered.Network.Provisioning.SSID); err != nil {
		return Config{}, err
	}
	if rendered.Network.StandaloneEnabled {
		if err := validateSSID("network.standalone.ssid", rendered.Network.Standalone.SSID); err != nil {
			return Config{}, err
		}
	}
	if err := validateHandoff(rendered.Handoff, true); err != nil {
		return Config{}, err
	}
	return rendered, nil
}

func renderTemplate(input string, values TemplateValues) (string, error) {
	lookup := map[string]string{
		"ProductName": values.ProductName,
		"DeviceName":  values.DeviceName,
		"DeviceID":    values.DeviceID,
		"Hostname":    values.Hostname,
	}

	var rendered strings.Builder
	remaining := input
	for {
		start := strings.Index(remaining, "{{")
		strayClose := strings.Index(remaining, "}}")
		if strayClose >= 0 && (start < 0 || strayClose < start) {
			return "", errors.New("unexpected closing template delimiter")
		}
		if start < 0 {
			rendered.WriteString(remaining)
			return rendered.String(), nil
		}
		rendered.WriteString(remaining[:start])
		afterOpen := remaining[start+2:]
		end := strings.Index(afterOpen, "}}")
		if end < 0 {
			return "", errors.New("unclosed template expression")
		}
		expression := strings.TrimSpace(afterOpen[:end])
		if !strings.HasPrefix(expression, ".") || len(expression) == 1 {
			return "", fmt.Errorf("unsupported template expression %q", expression)
		}
		name := strings.TrimPrefix(expression, ".")
		value, allowed := lookup[name]
		if !allowed {
			return "", fmt.Errorf("unknown template field %q", name)
		}
		rendered.WriteString(value)
		remaining = afterOpen[end+2:]
	}
}

func validateSSID(name, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	if len(value) == 0 || len(value) > 32 {
		return fmt.Errorf("%s must contain between 1 and 32 bytes after template rendering", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("%s must not contain control characters", name)
		}
	}
	return nil
}

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
