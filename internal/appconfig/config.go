// Package appconfig loads and validates onboardd's product configuration.
package appconfig

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/flavorplus/onboardd/internal/connectivity"
)

const (
	// SchemaVersion is the only configuration schema understood by this build.
	SchemaVersion = 1
	// SystemPath is the default appliance configuration path.
	SystemPath = "/etc/onboardd/config.toml"

	// ProvisioningAddress is fixed so recovery instructions and captive clients always
	// have one predictable setup address.
	ProvisioningAddress = "10.42.0.1/24"
	// CaptivePublicPort is fixed because captive-detection clients expect ordinary HTTP.
	CaptivePublicPort uint16 = 80
)

var colorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// Config is the complete resolved onboardd configuration.
type Config struct {
	SchemaVersion int      `toml:"schema_version"`
	Product       Product  `toml:"product"`
	Branding      Branding `toml:"branding"`
	Network       Network  `toml:"network"`
	Portal        Portal   `toml:"portal"`
	Handoff       Handoff  `toml:"handoff"`
}

type Product struct {
	Name       string `toml:"name"`
	DeviceName string `toml:"device_name"`
}

type Branding struct {
	Logo            string       `toml:"logo"`
	PrimaryColor    string       `toml:"primary_color"`
	BackgroundColor string       `toml:"background_color"`
	Text            BrandingText `toml:"text"`
}

type BrandingText struct {
	Title    string `toml:"title"`
	Subtitle string `toml:"subtitle"`
}

type Network struct {
	Interface             string                   `toml:"interface"`
	Requirement           connectivity.Requirement `toml:"requirement"`
	InfrastructureEnabled bool                     `toml:"infrastructure_enabled"`
	StandaloneEnabled     bool                     `toml:"standalone_enabled"`
	Provisioning          Provisioning             `toml:"provisioning"`
	Standalone            Standalone               `toml:"standalone"`
}

type Provisioning struct {
	SSID         string `toml:"ssid"`
	PasswordFile string `toml:"password_file"`
}

type Standalone struct {
	SSID         string `toml:"ssid"`
	PasswordFile string `toml:"password_file"`
	Address      string `toml:"address"`
}

type Portal struct {
	ListenerPort uint16 `toml:"listener_port"`
	PasswordFile string `toml:"password_file"`
}

// Handoff configures the optional product application destination. The stable setup
// hostname is read from the host's Avahi daemon and is deliberately not configurable.
type Handoff struct {
	ApplicationLabel          string `toml:"application_label"`
	ApplicationURL            string `toml:"application_url"`
	HealthCheckURL            string `toml:"health_check_url"`
	ShowStandaloneCredentials bool   `toml:"show_standalone_credentials"`
}

// ResolveOptions describes the ordered configuration sources.
type ResolveOptions struct {
	ConfigPath     string
	ConfigOptional bool
	Overrides      Overrides
}

// Overrides are the small operational subset exposed by the production CLI.
type Overrides struct {
	NetworkInterface      *string
	NetworkRequirement    *connectivity.Requirement
	InfrastructureEnabled *bool
	StandaloneEnabled     *bool
	ListenerPort          *uint16
}

// Resolve applies defaults, TOML, and CLI overrides in that order, then validates
// the final result exactly once.
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
	applyOverrides(&resolved, options.Overrides)
	if err := resolved.Validate(); err != nil {
		if options.ConfigPath != "" {
			return Config{}, fmt.Errorf("validate configuration %q: %w", options.ConfigPath, err)
		}
		return Config{}, err
	}
	return resolved, nil
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

// Defaults returns the safe product-neutral values used before later sources overlay
// them. Password paths contain no secret data and are only opened by the runtime.
func Defaults() Config {
	return Config{
		SchemaVersion: SchemaVersion,
		Product: Product{
			Name:       "Device",
			DeviceName: "Device",
		},
		Branding: Branding{
			PrimaryColor:    "#cd2455",
			BackgroundColor: "#f8eff3",
			Text: BrandingText{
				Title:    "Set up your device",
				Subtitle: "Choose how this device should connect.",
			},
		},
		Network: Network{
			Interface:             "wlan0",
			Requirement:           connectivity.RequirementLocal,
			InfrastructureEnabled: true,
			StandaloneEnabled:     true,
			Provisioning: Provisioning{
				SSID:         "{{ .ProductName }}-Setup-{{ .DeviceID }}",
				PasswordFile: "/etc/onboardd/provisioning-password",
			},
			Standalone: Standalone{
				SSID:         "{{ .ProductName }}-{{ .DeviceID }}",
				PasswordFile: "/etc/onboardd/standalone-password",
				Address:      "10.42.0.1/24",
			},
		},
		Portal: Portal{
			ListenerPort: 18080,
			PasswordFile: "/etc/onboardd/admin-password",
		},
		Handoff: Handoff{},
	}
}

func decodeInto(reader io.Reader, resolved *Config) error {
	metadata, err := toml.NewDecoder(reader).Decode(resolved)
	if err != nil {
		return fmt.Errorf("decode TOML: %w", err)
	}
	if !metadata.IsDefined("schema_version") {
		return errors.New("schema_version is required")
	}
	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		sort.Strings(keys)
		return fmt.Errorf("unknown configuration keys: %s", strings.Join(keys, ", "))
	}
	return nil
}

// Validate checks cross-field and semantic constraints before any network or
// filesystem state is changed.
func (config Config) Validate() error {
	if config.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d; this build supports %d", config.SchemaVersion, SchemaVersion)
	}
	if strings.TrimSpace(config.Product.Name) == "" {
		return errors.New("product.name must not be empty")
	}
	if strings.TrimSpace(config.Product.DeviceName) == "" {
		return errors.New("product.device_name must not be empty")
	}
	if err := validateBranding(config.Branding); err != nil {
		return err
	}
	if err := validateNetwork(config.Network); err != nil {
		return err
	}
	if err := validatePortal(config.Portal); err != nil {
		return err
	}
	return validateHandoff(config.Handoff, false)
}

func validateBranding(branding Branding) error {
	colors := []struct {
		name  string
		value string
	}{
		{"branding.primary_color", branding.PrimaryColor},
		{"branding.background_color", branding.BackgroundColor},
	}
	for _, color := range colors {
		if !colorPattern.MatchString(color.value) {
			return fmt.Errorf("%s must be a six-digit hexadecimal color", color.name)
		}
	}
	if strings.TrimSpace(branding.Text.Title) == "" {
		return errors.New("branding.text.title must not be empty")
	}
	return nil
}

func validateNetwork(network Network) error {
	if strings.TrimSpace(network.Interface) == "" {
		return errors.New("network.interface must not be empty")
	}
	if err := network.Requirement.Validate(); err != nil {
		return fmt.Errorf("network.requirement: %w", err)
	}
	if !network.InfrastructureEnabled && !network.StandaloneEnabled {
		return errors.New("at least one production network mode must be enabled")
	}
	if strings.TrimSpace(network.Provisioning.SSID) == "" {
		return errors.New("network.provisioning.ssid must not be empty")
	}
	if strings.TrimSpace(network.Provisioning.PasswordFile) == "" {
		return errors.New("network.provisioning.password_file must not be empty")
	}
	if network.StandaloneEnabled {
		if strings.TrimSpace(network.Standalone.SSID) == "" {
			return errors.New("network.standalone.ssid must not be empty when standalone mode is enabled")
		}
		if strings.TrimSpace(network.Standalone.PasswordFile) == "" {
			return errors.New("network.standalone.password_file must not be empty when standalone mode is enabled")
		}
		if err := validateStandaloneAddress(network.Standalone.Address); err != nil {
			return err
		}
	}
	return nil
}

func validateStandaloneAddress(address string) error {
	prefix, err := netip.ParsePrefix(address)
	if err != nil || !prefix.Addr().Is4() {
		return errors.New("network.standalone.address must be a valid IPv4 host address and prefix")
	}
	if prefix.Bits() > 30 || prefix.Addr() == prefix.Masked().Addr() || !prefix.Contains(prefix.Addr().Next()) {
		return errors.New("network.standalone.address must identify a usable host in a subnet with client addresses")
	}
	return nil
}

func validatePortal(portal Portal) error {
	if portal.ListenerPort == 0 {
		return errors.New("portal.listener_port must be between 1 and 65535")
	}
	if portal.ListenerPort == CaptivePublicPort {
		return fmt.Errorf("portal.listener_port must differ from the fixed captive public port %d", CaptivePublicPort)
	}
	if strings.TrimSpace(portal.PasswordFile) == "" {
		return errors.New("portal.password_file must not be empty")
	}
	return nil
}

func validateHandoff(handoff Handoff, rendered bool) error {
	applicationLabel := strings.TrimSpace(handoff.ApplicationLabel)
	applicationURL := strings.TrimSpace(handoff.ApplicationURL)
	if (applicationLabel == "") != (applicationURL == "") {
		return errors.New("handoff.application_label and handoff.application_url must be configured together")
	}
	if strings.TrimSpace(handoff.HealthCheckURL) != "" && applicationURL == "" {
		return errors.New("handoff.health_check_url requires handoff.application_url")
	}
	if strings.ContainsAny(handoff.ApplicationLabel, "\r\n") {
		return errors.New("handoff.application_label must not contain line breaks")
	}
	if handoff.ApplicationURL != "" && (rendered || !strings.Contains(handoff.ApplicationURL, "{{")) {
		if err := validateHTTPURL("handoff.application_url", handoff.ApplicationURL); err != nil {
			return err
		}
	}
	if handoff.HealthCheckURL != "" && (rendered || !strings.Contains(handoff.HealthCheckURL, "{{")) {
		if err := validateHTTPURL("handoff.health_check_url", handoff.HealthCheckURL); err != nil {
			return err
		}
	}
	return nil
}

func validateHTTPURL(name, value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s must be an absolute HTTP or HTTPS URL", name)
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("%s must not contain credentials or a fragment", name)
	}
	return nil
}
