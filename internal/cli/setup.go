package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	appconfig "github.com/flavorplus/onboardd/internal/config"
	"github.com/flavorplus/onboardd/internal/connectivity"
	"github.com/flavorplus/onboardd/internal/discovery"
	"github.com/flavorplus/onboardd/internal/networkmanager"
	"github.com/flavorplus/onboardd/internal/recovery"
	setupflow "github.com/flavorplus/onboardd/internal/setup"
	webui "github.com/flavorplus/onboardd/internal/web"
)

const (
	defaultBand          = "bg"
	defaultDNSConfigPath = "/etc/NetworkManager/dnsmasq-shared.d/onboardd.conf"
)

// operationalConfigFlags is the deliberately small override surface. Product
// identity, branding, SSIDs, and secrets stay out of process arguments.
type operationalConfigFlags struct {
	interfaceName        *string
	requirementText      *string
	infrastructureEnable *bool
	standaloneEnable     *bool
	listenerPort         *uint
}

func bindOperationalConfigFlags(flags *flag.FlagSet, defaults appconfig.Config) operationalConfigFlags {
	return operationalConfigFlags{
		interfaceName: flags.String(
			"network-interface", defaults.Network.Interface,
			"NetworkManager Wi-Fi interface override",
		),
		requirementText: flags.String(
			"network-requirement", string(defaults.Network.Requirement),
			"connectivity requirement override: local or internet",
		),
		infrastructureEnable: flags.Bool(
			"infrastructure-enabled", defaults.Network.InfrastructureEnabled,
			"infrastructure-mode policy override",
		),
		standaloneEnable: flags.Bool(
			"standalone-enabled", defaults.Network.StandaloneEnabled,
			"standalone-mode policy override",
		),
		listenerPort: flags.Uint(
			"listener-port", uint(defaults.Portal.ListenerPort),
			"private portal listener port override",
		),
	}
}

func resolveConfig(
	flags *flag.FlagSet,
	configPath string,
	values operationalConfigFlags,
) (appconfig.Config, error) {
	set := explicitlySetFlags(flags)
	overrides, err := values.overrides(set)
	if err != nil {
		return appconfig.Config{}, err
	}
	return appconfig.Resolve(appconfig.ResolveOptions{
		ConfigPath:     configPath,
		ConfigOptional: !set["config"],
		Overrides:      overrides,
	})
}

func (values operationalConfigFlags) overrides(set map[string]bool) (appconfig.Overrides, error) {
	overrides := appconfig.Overrides{}
	if set["network-interface"] {
		overrides.NetworkInterface = values.interfaceName
	}
	if set["network-requirement"] {
		requirement := connectivity.Requirement(*values.requirementText)
		overrides.NetworkRequirement = &requirement
	}
	if set["infrastructure-enabled"] {
		overrides.InfrastructureEnabled = values.infrastructureEnable
	}
	if set["standalone-enabled"] {
		overrides.StandaloneEnabled = values.standaloneEnable
	}
	if set["listener-port"] {
		if *values.listenerPort == 0 || *values.listenerPort > 65535 {
			return appconfig.Overrides{}, errors.New("--listener-port must be between 1 and 65535")
		}
		port := uint16(*values.listenerPort)
		overrides.ListenerPort = &port
	}
	return overrides, nil
}

func explicitlySetFlags(flags *flag.FlagSet) map[string]bool {
	set := make(map[string]bool)
	flags.Visit(func(flag *flag.Flag) {
		set[flag.Name] = true
	})
	return set
}

func loadSetupOptions(
	ctx context.Context,
	command string,
	args []string,
	stderr io.Writer,
) (setupOptions, error) {
	defaults := appconfig.Defaults()
	flags := newFlagSet(command, stderr)
	configPath := flags.String("config", appconfig.SystemPath, "TOML configuration file")
	operational := bindOperationalConfigFlags(flags, defaults)
	if err := flags.Parse(args); err != nil {
		return setupOptions{}, err
	}
	if err := requireNoArgs(flags); err != nil {
		return setupOptions{}, err
	}

	resolved, err := resolveConfig(flags, *configPath, operational)
	if err != nil {
		return setupOptions{}, err
	}
	avahiHostname, err := discovery.CurrentHostname(ctx)
	if err != nil {
		return setupOptions{}, fmt.Errorf("discover host mDNS name: %w", err)
	}
	identity, err := appconfig.LoadIdentity()
	if err != nil {
		return setupOptions{}, err
	}
	identity.Hostname = avahiHostname
	resolved, err = appconfig.RenderTemplates(resolved, identity)
	if err != nil {
		return setupOptions{}, err
	}
	return setupOptionsFromConfig(resolved, avahiHostname)
}

func setupOptionsFromConfig(resolved appconfig.Config, hostname string) (setupOptions, error) {
	branding, err := webui.OptionsFromConfig(resolved, hostname)
	if err != nil {
		return setupOptions{}, err
	}
	adminPassword, err := readSecurePasswordFile(resolved.Portal.PasswordFile)
	if err != nil {
		return setupOptions{}, fmt.Errorf("admin password: %w", err)
	}
	portalPassword, err := readSecurePasswordFile(resolved.Network.Provisioning.PasswordFile)
	if err != nil {
		return setupOptions{}, fmt.Errorf("provisioning password: %w", err)
	}
	standalonePassword := ""
	if resolved.Network.StandaloneEnabled {
		standalonePassword, err = readSecurePasswordFile(resolved.Network.Standalone.PasswordFile)
		if err != nil {
			return setupOptions{}, fmt.Errorf("standalone password: %w", err)
		}
		if branding.Handoff != nil && branding.Handoff.Standalone != nil &&
			branding.Handoff.ShowStandaloneCredentials {
			branding.Handoff.Standalone.Password = standalonePassword
		}
	}
	address, err := netip.ParsePrefix(appconfig.ProvisioningAddress)
	if err != nil {
		return setupOptions{}, fmt.Errorf("invalid built-in provisioning address: %w", err)
	}
	canonicalURL := portalURLFor(address, appconfig.CaptivePublicPort)
	origin, err := portalOrigin(canonicalURL)
	if err != nil {
		return setupOptions{}, err
	}

	return setupOptions{
		Interface:           resolved.Network.Interface,
		ProvisioningSSID:    resolved.Network.Provisioning.SSID,
		ProvisioningPSK:     portalPassword,
		ProvisioningAddress: address,
		Band:                defaultBand,
		PublicHTTPPort:      appconfig.CaptivePublicPort,
		ListenerHTTPPort:    resolved.Portal.ListenerPort,
		PortalURL:           canonicalURL,
		PortalOrigin:        origin,
		AdminPassword:       adminPassword,
		DNSConfigPath:       defaultDNSConfigPath,
		Assets:              webui.Assets(),
		Branding:            branding,
		NetworkEnabled:      resolved.Network.InfrastructureEnabled,
		StandaloneEnabled:   resolved.Network.StandaloneEnabled,
		StandaloneSSID:      resolved.Network.Standalone.SSID,
		StandalonePSK:       standalonePassword,
		StandaloneAddress:   resolved.Network.Standalone.Address,
		Requirement:         resolved.Network.Requirement,
		ScanWait:            5 * time.Second,
		ActivationWait:      30 * time.Second,
		RollbackAfter:       90 * time.Second,
		RestorationWait:     30 * time.Second,
		ReadyLabel:          resolved.Product.Name + " setup",
		Hostname:            hostname,
	}, nil
}

type setupOptions struct {
	Interface           string
	ProvisioningSSID    string
	ProvisioningPSK     string
	ProvisioningAddress netip.Prefix
	Band                string
	PublicHTTPPort      uint16
	ListenerHTTPPort    uint16
	PortalURL           string
	PortalOrigin        string
	AdminPassword       string
	DNSConfigPath       string
	Assets              fs.FS
	Branding            webui.Options
	NetworkEnabled      bool
	StandaloneEnabled   bool
	StandaloneSSID      string
	StandalonePSK       string
	StandaloneAddress   string
	Requirement         connectivity.Requirement
	ScanWait            time.Duration
	ActivationWait      time.Duration
	RollbackAfter       time.Duration
	RestorationWait     time.Duration
	ReadyLabel          string
	Hostname            string
}

func validateSetupOptions(options setupOptions) error {
	if err := webui.ValidateAdminPassword(options.AdminPassword); err != nil {
		return err
	}
	if _, _, err := networkmanager.BuildAccessPointSettings(networkmanager.AccessPointOptions{
		Interface: options.Interface,
		SSID:      options.ProvisioningSSID,
		Password:  options.ProvisioningPSK,
		Address:   options.ProvisioningAddress.String(),
		Role:      networkmanager.RoleProvisioning,
		Band:      options.Band,
	}); err != nil {
		return fmt.Errorf("invalid provisioning network: %w", err)
	}
	if options.StandaloneEnabled {
		if _, _, err := networkmanager.BuildAccessPointSettings(networkmanager.AccessPointOptions{
			Interface: options.Interface,
			SSID:      options.StandaloneSSID,
			Password:  options.StandalonePSK,
			Address:   options.StandaloneAddress,
			Role:      networkmanager.RoleStandalone,
			Priority:  999,
			Band:      options.Band,
		}); err != nil {
			return fmt.Errorf("invalid standalone network: %w", err)
		}
	}
	if _, err := fs.Stat(options.Assets, "index.html"); err != nil {
		return fmt.Errorf("frontend assets: %w", err)
	}
	if _, err := fs.Stat(options.Assets, "landing.html"); err != nil {
		return fmt.Errorf("frontend landing page: %w", err)
	}
	if options.Branding.Handoff == nil {
		return errors.New("resolved handoff configuration is required")
	}
	return nil
}

func newNetworkBackend(
	client *networkmanager.Client,
	captiveExiter setupflow.CaptiveExiter,
	options setupOptions,
) (*setupflow.NetworkBackend, error) {
	infrastructureTransition, err := recovery.NewInfrastructure(client)
	if err != nil {
		return nil, err
	}
	standaloneTransition, err := recovery.NewStandalone(client)
	if err != nil {
		return nil, err
	}
	return setupflow.NewNetworkBackend(
		client,
		infrastructureTransition,
		standaloneTransition,
		captiveExiter,
		setupflow.NetworkOptions{
			Interface:         options.Interface,
			Requirement:       options.Requirement,
			ScanWait:          options.ScanWait,
			ActivationWait:    options.ActivationWait,
			RollbackAfter:     options.RollbackAfter,
			RestorationWait:   options.RestorationWait,
			StandaloneEnabled: options.StandaloneEnabled,
			Standalone: networkmanager.AccessPointOptions{
				SSID:     options.StandaloneSSID,
				Password: options.StandalonePSK,
				Address:  options.StandaloneAddress,
				Band:     options.Band,
				Priority: 999,
			},
		},
	)
}

func portalOrigin(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("portal URL must be an absolute URL")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func portalURLFor(address netip.Prefix, port uint16) string {
	host := address.Addr().String()
	if port != 80 {
		host = net.JoinHostPort(host, fmt.Sprint(port))
	}
	return "http://" + host + "/"
}

func readSecurePasswordFile(path string) (string, error) {
	if path == "" {
		return "", errors.New("password file path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect password file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("password file %q must be a regular file", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("password file %q must not be readable or writable by group or other users (use mode 0600)", path)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read password file %q: %w", path, err)
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(contents), "\n"), "\r"), nil
}
