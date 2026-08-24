package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/flavorplus/onboardd/internal/connectivity"
)

const environmentPrefix = "ONBOARDD_"

// ResolveOptions describes the ordered configuration sources. Environment contains
// ordinary KEY=value entries, normally os.Environ().
type ResolveOptions struct {
	ConfigPath     string
	ConfigOptional bool
	Environment    []string
	Overrides      Overrides
}

// Overrides are the small operational subset exposed by the production CLI. Product
// branding and credentials remain configuration-file or environment concerns.
type Overrides struct {
	NetworkInterface      *string
	NetworkRequirement    *connectivity.Requirement
	InfrastructureEnabled *bool
	StandaloneEnabled     *bool
	ListenerPort          *uint16
}

// Resolve applies defaults, TOML, environment variables, and CLI overrides in that
// order, then validates the final result exactly once.
func Resolve(options ResolveOptions) (Config, error) {
	resolved := Defaults()
	if options.ConfigPath != "" {
		file, err := os.Open(options.ConfigPath)
		if err != nil {
			if !options.ConfigOptional || !errors.Is(err, os.ErrNotExist) {
				return Config{}, fmt.Errorf("open configuration %q: %w", options.ConfigPath, err)
			}
		} else {
			defer file.Close()
			if err := decodeInto(file, &resolved); err != nil {
				return Config{}, fmt.Errorf("load configuration %q: %w", options.ConfigPath, err)
			}
		}
	}
	if err := applyEnvironment(&resolved, options.Environment); err != nil {
		return Config{}, err
	}
	applyOverrides(&resolved, options.Overrides)
	if err := resolved.Validate(); err != nil {
		return Config{}, err
	}
	return resolved, nil
}

type environmentSetter func(*Config, string) error

var environmentSetters = map[string]environmentSetter{
	"ONBOARDD_PRODUCT_NAME": func(config *Config, value string) error {
		config.Product.Name = value
		return nil
	},
	"ONBOARDD_PRODUCT_DEVICE_NAME": func(config *Config, value string) error {
		config.Product.DeviceName = value
		return nil
	},
	"ONBOARDD_BRANDING_LOGO": func(config *Config, value string) error {
		config.Branding.Logo = value
		return nil
	},
	"ONBOARDD_BRANDING_PRIMARY_COLOR": func(config *Config, value string) error {
		config.Branding.PrimaryColor = value
		return nil
	},
	"ONBOARDD_BRANDING_BACKGROUND_COLOR": func(config *Config, value string) error {
		config.Branding.BackgroundColor = value
		return nil
	},
	"ONBOARDD_BRANDING_TEXT_TITLE": func(config *Config, value string) error {
		config.Branding.Text.Title = value
		return nil
	},
	"ONBOARDD_BRANDING_TEXT_SUBTITLE": func(config *Config, value string) error {
		config.Branding.Text.Subtitle = value
		return nil
	},
	"ONBOARDD_NETWORK_INTERFACE": func(config *Config, value string) error {
		config.Network.Interface = value
		return nil
	},
	"ONBOARDD_NETWORK_REQUIREMENT": func(config *Config, value string) error {
		config.Network.Requirement = connectivity.Requirement(value)
		return nil
	},
	"ONBOARDD_NETWORK_INFRASTRUCTURE_ENABLED": booleanEnvironment(func(config *Config, value bool) {
		config.Network.InfrastructureEnabled = value
	}),
	"ONBOARDD_NETWORK_STANDALONE_ENABLED": booleanEnvironment(func(config *Config, value bool) {
		config.Network.StandaloneEnabled = value
	}),
	"ONBOARDD_NETWORK_PROVISIONING_SSID": func(config *Config, value string) error {
		config.Network.Provisioning.SSID = value
		return nil
	},
	"ONBOARDD_NETWORK_PROVISIONING_PASSWORD_FILE": func(config *Config, value string) error {
		config.Network.Provisioning.PasswordFile = value
		return nil
	},
	"ONBOARDD_NETWORK_STANDALONE_SSID": func(config *Config, value string) error {
		config.Network.Standalone.SSID = value
		return nil
	},
	"ONBOARDD_NETWORK_STANDALONE_PASSWORD_FILE": func(config *Config, value string) error {
		config.Network.Standalone.PasswordFile = value
		return nil
	},
	"ONBOARDD_NETWORK_STANDALONE_ADDRESS": func(config *Config, value string) error {
		config.Network.Standalone.Address = value
		return nil
	},
	"ONBOARDD_PORTAL_LISTENER_PORT": func(config *Config, value string) error {
		port, err := strconv.ParseUint(value, 10, 16)
		if err != nil || port == 0 {
			return errors.New("must be an integer between 1 and 65535")
		}
		config.Portal.ListenerPort = uint16(port)
		return nil
	},
	"ONBOARDD_HANDOFF_APPLICATION_LABEL": func(config *Config, value string) error {
		config.Handoff.ApplicationLabel = value
		return nil
	},
	"ONBOARDD_HANDOFF_APPLICATION_URL": func(config *Config, value string) error {
		config.Handoff.ApplicationURL = value
		return nil
	},
	"ONBOARDD_HANDOFF_HEALTH_CHECK_URL": func(config *Config, value string) error {
		config.Handoff.HealthCheckURL = value
		return nil
	},
	"ONBOARDD_HANDOFF_SHOW_STANDALONE_CREDENTIALS": booleanEnvironment(func(config *Config, value bool) {
		config.Handoff.ShowStandaloneCredentials = value
	}),
}

func applyEnvironment(config *Config, environment []string) error {
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if !found || !strings.HasPrefix(name, environmentPrefix) {
			continue
		}
		setter, known := environmentSetters[name]
		if !known {
			return fmt.Errorf("unknown onboardd environment variable %s", name)
		}
		if err := setter(config, value); err != nil {
			return fmt.Errorf("invalid %s: %w", name, err)
		}
	}
	return nil
}

func booleanEnvironment(assign func(*Config, bool)) environmentSetter {
	return func(config *Config, value string) error {
		if value != "true" && value != "false" {
			return errors.New("must be true or false")
		}
		assign(config, value == "true")
		return nil
	}
}

func applyOverrides(config *Config, overrides Overrides) {
	if overrides.NetworkInterface != nil {
		config.Network.Interface = *overrides.NetworkInterface
	}
	if overrides.NetworkRequirement != nil {
		config.Network.Requirement = *overrides.NetworkRequirement
	}
	if overrides.InfrastructureEnabled != nil {
		config.Network.InfrastructureEnabled = *overrides.InfrastructureEnabled
	}
	if overrides.StandaloneEnabled != nil {
		config.Network.StandaloneEnabled = *overrides.StandaloneEnabled
	}
	if overrides.ListenerPort != nil {
		config.Portal.ListenerPort = *overrides.ListenerPort
	}
}
