// Package cli implements onboardd's small standard-library command-line interface.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/flavorplus/onboardd/internal/buildinfo"
	"github.com/flavorplus/onboardd/internal/captive"
	appconfig "github.com/flavorplus/onboardd/internal/config"
	"github.com/flavorplus/onboardd/internal/connectivity"
	embeddedfrontend "github.com/flavorplus/onboardd/internal/frontend"
	"github.com/flavorplus/onboardd/internal/networkmanager"
	"github.com/flavorplus/onboardd/internal/recovery"
	setupflow "github.com/flavorplus/onboardd/internal/setup"
	stateengine "github.com/flavorplus/onboardd/internal/state"
	webui "github.com/flavorplus/onboardd/internal/web"
)

const (
	defaultInterface     = "wlan0"
	defaultBand          = "bg"
	defaultDNSConfigPath = "/etc/NetworkManager/dnsmasq-shared.d/onboardd.conf"
)

// Run executes the onboardd command line.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printRootHelp(stdout)
		return nil
	}
	if args[0] == "debug" {
		return runDebug(ctx, args[1:], stdout, stderr)
	}
	if args[0] == "setup" {
		err := runSetup(ctx, args[1:], stdout, stderr)
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	root := flag.NewFlagSet("onboardd", flag.ContinueOnError)
	root.SetOutput(stderr)
	showVersion := root.Bool("version", false, "print version information")
	if err := root.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Fprintf(stdout, "onboardd %s\n", buildinfo.Version)
		return nil
	}
	if root.NArg() > 0 && root.Arg(0) == "help" {
		printRootHelp(stdout)
		return nil
	}
	return fmt.Errorf("unknown command %q; run 'onboardd help'", strings.Join(args, " "))
}

// runSetup starts the product-configured setup experience. The command deliberately
// does not expose low-level radio, DNS, timing, or public-port options; those remain
// debug-only implementation controls.
func runSetup(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	defaults := appconfig.Defaults()
	flags := newFlagSet("setup", stderr)
	configPath := flags.String("config", appconfig.SystemPath, "TOML configuration file")
	interfaceName := flags.String("network-interface", defaults.Network.Interface, "NetworkManager Wi-Fi interface override")
	requirementText := flags.String("network-requirement", string(defaults.Network.Requirement), "connectivity requirement override: local or internet")
	infrastructureEnabled := flags.Bool("infrastructure-enabled", defaults.Network.InfrastructureEnabled, "infrastructure-mode policy override")
	standaloneEnabled := flags.Bool("standalone-enabled", defaults.Network.StandaloneEnabled, "standalone-mode policy override")
	listenerPort := flags.Uint("listener-port", uint(defaults.Portal.ListenerPort), "private portal listener port override")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}

	set := make(map[string]bool)
	flags.Visit(func(flag *flag.Flag) {
		set[flag.Name] = true
	})
	overrides := appconfig.Overrides{}
	if set["network-interface"] {
		overrides.NetworkInterface = interfaceName
	}
	if set["network-requirement"] {
		requirement := connectivity.Requirement(*requirementText)
		overrides.NetworkRequirement = &requirement
	}
	if set["infrastructure-enabled"] {
		overrides.InfrastructureEnabled = infrastructureEnabled
	}
	if set["standalone-enabled"] {
		overrides.StandaloneEnabled = standaloneEnabled
	}
	if set["listener-port"] {
		if *listenerPort == 0 || *listenerPort > 65535 {
			return errors.New("--listener-port must be between 1 and 65535")
		}
		port := uint16(*listenerPort)
		overrides.ListenerPort = &port
	}

	resolved, err := appconfig.Resolve(appconfig.ResolveOptions{
		ConfigPath:     *configPath,
		ConfigOptional: !set["config"],
		Environment:    os.Environ(),
		Overrides:      overrides,
	})
	if err != nil {
		return err
	}
	identity, err := appconfig.LoadIdentity()
	if err != nil {
		return err
	}
	resolved, err = appconfig.RenderTemplates(resolved, identity)
	if err != nil {
		return err
	}
	options, err := configuredSetupOptions(resolved)
	if err != nil {
		return err
	}
	return runInteractiveSetup(ctx, options, stdout)
}

func configuredSetupOptions(resolved appconfig.Config) (interactiveSetupOptions, error) {
	branding, err := webui.OptionsFromConfig(resolved)
	if err != nil {
		return interactiveSetupOptions{}, err
	}
	portalPassword, err := readSecurePasswordFile(resolved.Network.Provisioning.PasswordFile)
	if err != nil {
		return interactiveSetupOptions{}, fmt.Errorf("provisioning password: %w", err)
	}
	standalonePassword := ""
	if resolved.Network.StandaloneEnabled {
		standalonePassword, err = readSecurePasswordFile(resolved.Network.Standalone.PasswordFile)
		if err != nil {
			return interactiveSetupOptions{}, fmt.Errorf("standalone password: %w", err)
		}
	}
	address, err := netip.ParsePrefix(appconfig.ProvisioningAddress)
	if err != nil {
		return interactiveSetupOptions{}, fmt.Errorf("invalid built-in provisioning address: %w", err)
	}
	canonicalURL := portalURLFor(address, appconfig.CaptivePublicPort)
	origin, err := portalOrigin(canonicalURL)
	if err != nil {
		return interactiveSetupOptions{}, err
	}

	return interactiveSetupOptions{
		Interface:           resolved.Network.Interface,
		ProvisioningSSID:    resolved.Network.Provisioning.SSID,
		ProvisioningPSK:     portalPassword,
		ProvisioningAddress: address,
		Band:                defaultBand,
		PublicHTTPPort:      appconfig.CaptivePublicPort,
		ListenerHTTPPort:    resolved.Portal.ListenerPort,
		PortalURL:           canonicalURL,
		PortalOrigin:        origin,
		DNSConfigPath:       defaultDNSConfigPath,
		Assets:              embeddedfrontend.Assets(),
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
	}, nil
}

func runDebug(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" {
		printDebugHelp(stdout)
		return nil
	}

	switch args[0] {
	case "config":
		return debugConfig(args[1:], stdout, stderr)
	case "status":
		return debugStatus(ctx, args[1:], stdout, stderr)
	case "profiles":
		return debugProfiles(ctx, args[1:], stdout, stderr)
	case "profile-delete":
		return debugProfileDelete(ctx, args[1:], stdout, stderr)
	case "scan":
		return debugScan(ctx, args[1:], stdout, stderr)
	case "connect":
		return debugConnect(ctx, args[1:], stdout, stderr)
	case "provisioning-start":
		return debugAccessPoint(ctx, args[1:], stdout, stderr, networkmanager.RoleProvisioning)
	case "standalone-start":
		return debugAccessPoint(ctx, args[1:], stdout, stderr, networkmanager.RoleStandalone)
	case "captive-start":
		return debugCaptiveStart(ctx, args[1:], stdout, stderr)
	case "setup-start":
		return debugSetupStart(ctx, args[1:], stdout, stderr)
	case "connect-protected":
		return debugConnectProtected(ctx, args[1:], stdout, stderr)
	case "watch":
		return debugWatch(ctx, args[1:], stdout, stderr)
	case "reconcile":
		return debugReconcile(ctx, args[1:], stdout, stderr)
	case "checkpoint-create":
		return debugCheckpointCreate(ctx, args[1:], stdout, stderr)
	case "checkpoint-commit":
		return debugCheckpointCommit(ctx, args[1:], stdout, stderr)
	case "checkpoint-rollback":
		return debugCheckpointRollback(ctx, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown debug command %q; run 'onboardd debug help'", args[0])
	}
}

func debugConfig(args []string, stdout, stderr io.Writer) error {
	defaults := appconfig.Defaults()
	flags := newFlagSet("debug config", stderr)
	configPath := flags.String("config", appconfig.SystemPath, "TOML configuration file")
	interfaceName := flags.String("network-interface", defaults.Network.Interface, "NetworkManager Wi-Fi interface override")
	requirementText := flags.String("network-requirement", string(defaults.Network.Requirement), "connectivity requirement override: local or internet")
	infrastructureEnabled := flags.Bool("infrastructure-enabled", defaults.Network.InfrastructureEnabled, "infrastructure-mode policy override")
	standaloneEnabled := flags.Bool("standalone-enabled", defaults.Network.StandaloneEnabled, "standalone-mode policy override")
	listenerPort := flags.Uint("listener-port", uint(defaults.Portal.ListenerPort), "private portal listener port override")
	render := flags.Bool("render", false, "render text and SSID templates in the output")
	deviceID := flags.String("device-id", "", "debug-only device ID used with --render")
	hostname := flags.String("hostname", "", "debug-only hostname used with --render")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}

	set := make(map[string]bool)
	flags.Visit(func(flag *flag.Flag) {
		set[flag.Name] = true
	})
	overrides := appconfig.Overrides{}
	if set["network-interface"] {
		overrides.NetworkInterface = interfaceName
	}
	if set["network-requirement"] {
		requirement := connectivity.Requirement(*requirementText)
		overrides.NetworkRequirement = &requirement
	}
	if set["infrastructure-enabled"] {
		overrides.InfrastructureEnabled = infrastructureEnabled
	}
	if set["standalone-enabled"] {
		overrides.StandaloneEnabled = standaloneEnabled
	}
	if set["listener-port"] {
		if *listenerPort == 0 || *listenerPort > 65535 {
			return errors.New("--listener-port must be between 1 and 65535")
		}
		port := uint16(*listenerPort)
		overrides.ListenerPort = &port
	}

	resolved, err := appconfig.Resolve(appconfig.ResolveOptions{
		ConfigPath:     *configPath,
		ConfigOptional: !set["config"],
		Environment:    os.Environ(),
		Overrides:      overrides,
	})
	if err != nil {
		return err
	}
	if !*render && (*deviceID != "" || *hostname != "") {
		return errors.New("--device-id and --hostname require --render")
	}
	if *render {
		identity := appconfig.Identity{DeviceID: *deviceID, Hostname: *hostname}
		if identity.DeviceID == "" || identity.Hostname == "" {
			detected, err := appconfig.LoadIdentity()
			if err != nil {
				return err
			}
			if identity.DeviceID == "" {
				identity.DeviceID = detected.DeviceID
			}
			if identity.Hostname == "" {
				identity.Hostname = detected.Hostname
			}
		}
		resolved, err = appconfig.RenderTemplates(resolved, identity)
		if err != nil {
			return err
		}
	}
	if err := toml.NewEncoder(stdout).Encode(resolved); err != nil {
		return fmt.Errorf("print resolved configuration: %w", err)
	}
	return nil
}

func debugConnectProtected(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug connect-protected", stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	ssid := flags.String("ssid", "", "target Wi-Fi SSID")
	passwordFile := flags.String("password-file", "", "file containing the Wi-Fi password")
	open := flags.Bool("open", false, "connect to an explicitly open network")
	hidden := flags.Bool("hidden", false, "target network hides its SSID")
	id := flags.String("id", "", "optional human-readable NetworkManager profile ID")
	priority := flags.Int("priority", 0, "autoconnect priority from -999 to 999")
	requirementText := flags.String("requirement", "local", "connectivity requirement: local or internet")
	activationWait := flags.Duration("wait", 30*time.Second, "maximum time to confirm candidate activation")
	rollbackAfter := flags.Duration("rollback-after", 90*time.Second, "automatic checkpoint rollback duration")
	restorationWait := flags.Duration("restoration-wait", 30*time.Second, "maximum time to confirm AP restoration")
	provisioningUUID := flags.String("provisioning-uuid", "", "active provisioning profile UUID")
	provisioningAddressText := flags.String("provisioning-address", "10.42.0.1", "provisioning AP IPv4 address")
	yes := flags.Bool("yes", false, "confirm the disruptive checkpoint-backed Wi-Fi change")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if !*yes {
		return errors.New("protected connect changes the active Wi-Fi interface; repeat with --yes while captive-start provides the recovery path")
	}
	if *priority < -999 || *priority > 999 {
		return errors.New("--priority must be between -999 and 999")
	}
	requirement := connectivity.Requirement(*requirementText)
	if err := requirement.Validate(); err != nil {
		return err
	}
	provisioningAddress, err := netip.ParseAddr(*provisioningAddressText)
	if err != nil {
		return fmt.Errorf("parse --provisioning-address: %w", err)
	}
	password, err := readPassword(*passwordFile, *open)
	if err != nil {
		return err
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	transition, err := recovery.NewInfrastructure(client)
	if err != nil {
		return err
	}
	activation, err := transition.Attempt(ctx, recovery.InfrastructureOptions{
		Interface: *interfaceName,
		Candidate: networkmanager.InfrastructureOptions{
			ID:       *id,
			SSID:     *ssid,
			Password: password,
			Open:     *open,
			Hidden:   *hidden,
			Priority: int32(*priority),
		},
		Requirement:         requirement,
		ActivationWait:      *activationWait,
		RollbackAfter:       *rollbackAfter,
		RestorationWait:     *restorationWait,
		PreviousUUID:        *provisioningUUID,
		PreviousIPv4Address: provisioningAddress,
	})
	if err != nil {
		return err
	}
	return writeActivation(stdout, activation, *jsonOutput)
}

func debugCaptiveStart(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug captive-start", stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	ssid := flags.String("ssid", "", "provisioning access-point SSID")
	passwordFile := flags.String("password-file", "", "file containing the access-point password")
	addressText := flags.String("address", "10.42.0.1/24", "access-point IPv4 address and prefix")
	band := flags.String("band", "bg", "Wi-Fi band: bg, a, or 6GHz")
	wait := flags.Duration("wait", 30*time.Second, "maximum time to confirm AP activation")
	httpPort := flags.Uint("http-port", 80, "captive HTTP port")
	listenerHTTPPort := flags.Uint("listener-http-port", 18080, "private onboardd HTTP listener port")
	portalURL := flags.String("portal-url", "", "canonical cleartext portal URL (defaults to the AP address)")
	dnsConfigPath := flags.String(
		"dns-config",
		"/etc/NetworkManager/dnsmasq-shared.d/onboardd.conf",
		"NetworkManager dnsmasq-shared fragment path",
	)
	yes := flags.Bool("yes", false, "confirm the disruptive Wi-Fi change")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if !*yes {
		return errors.New("starting captive provisioning changes the active Wi-Fi interface; repeat with --yes after ensuring a recovery path")
	}
	if *wait <= 0 {
		return errors.New("--wait must be positive")
	}
	if *httpPort == 0 || *httpPort > 65535 {
		return errors.New("--http-port must be between 1 and 65535")
	}
	if *listenerHTTPPort == 0 || *listenerHTTPPort > 65535 {
		return errors.New("--listener-http-port must be between 1 and 65535")
	}
	if *httpPort == *listenerHTTPPort {
		return errors.New("--http-port and --listener-http-port must differ")
	}
	address, err := netip.ParsePrefix(*addressText)
	if err != nil {
		return fmt.Errorf("parse --address: %w", err)
	}
	password, err := readRequiredPassword(*passwordFile)
	if err != nil {
		return err
	}
	canonicalURL := *portalURL
	if canonicalURL == "" {
		host := address.Addr().String()
		if *httpPort != 80 {
			host = net.JoinHostPort(host, fmt.Sprint(*httpPort))
		}
		canonicalURL = "http://" + host + "/"
	}

	dns, err := captive.NewDNSConfigFile(*dnsConfigPath)
	if err != nil {
		return err
	}
	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	redirect, err := captive.NewNFTRedirect("nft")
	if err != nil {
		return err
	}
	listenConfig := &net.ListenConfig{}
	lifecycle, err := captive.NewLifecycle(client, dns, redirect, listenConfig.Listen)
	if err != nil {
		return err
	}
	portal := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(
			response,
			"<!doctype html><html><head><meta name=\"viewport\" content=\"width=device-width\"></head>"+
				"<body><main><h1>onboardd setup</h1><p>The Phase 3 captive portal is reachable.</p>"+
				"</main></body></html>",
		)
	})
	session, err := lifecycle.Start(ctx, captive.StartOptions{
		Interface:        *interfaceName,
		SSID:             *ssid,
		Password:         password,
		Address:          address,
		Band:             *band,
		Wait:             *wait,
		PublicHTTPPort:   uint16(*httpPort),
		ListenerHTTPPort: uint16(*listenerHTTPPort),
		PortalURL:        canonicalURL,
	}, portal)
	if err != nil {
		return err
	}

	activation := session.Activation()
	fmt.Fprintln(stdout, "captive provisioning is ready")
	fmt.Fprintf(stdout, "SSID: %s\n", *ssid)
	fmt.Fprintf(stdout, "Portal: %s\n", session.PortalURL())
	fmt.Fprintf(stdout, "UUID: %s\n", activation.UUID)
	fmt.Fprintln(stdout, "Press Ctrl+C to stop and remove the temporary provisioning AP.")

	var serveErr error
	select {
	case <-ctx.Done():
	case <-session.Done():
		serveErr = session.Wait()
		if serveErr == nil {
			serveErr = errors.New("captive HTTP listener stopped unexpectedly")
		}
	}
	cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelCleanup()
	stopErr := session.Stop(cleanupContext)
	if serveErr != nil || stopErr != nil {
		return errors.Join(serveErr, stopErr)
	}
	fmt.Fprintln(stdout, "captive provisioning stopped and temporary resources were removed")
	return nil
}

func debugSetupStart(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug setup-start", stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	ssid := flags.String("ssid", "", "provisioning access-point SSID")
	passwordFile := flags.String("password-file", "", "provisioning access-point password file")
	addressText := flags.String("address", "10.42.0.1/24", "provisioning IPv4 address and prefix")
	band := flags.String("band", "bg", "Wi-Fi band: bg, a, or 6GHz")
	httpPort := flags.Uint("http-port", 80, "captive public HTTP port")
	listenerHTTPPort := flags.Uint("listener-http-port", 18080, "private onboardd HTTP listener port")
	portalURL := flags.String("portal-url", "", "canonical cleartext portal URL")
	dnsConfigPath := flags.String(
		"dns-config",
		"/etc/NetworkManager/dnsmasq-shared.d/onboardd.conf",
		"NetworkManager dnsmasq-shared fragment path",
	)
	frontendDirectory := flags.String("frontend-dir", "frontend/dist", "built frontend asset directory")
	networkEnabled := flags.Bool("network-enabled", true, "offer connection to an existing Wi-Fi network")
	standaloneEnabled := flags.Bool("standalone-enabled", true, "offer standalone mode")
	standaloneSSID := flags.String("standalone-ssid", "", "standalone access-point SSID")
	standalonePasswordFile := flags.String("standalone-password-file", "", "standalone access-point password file")
	standaloneAddress := flags.String("standalone-address", "10.42.0.1/24", "standalone IPv4 address and prefix")
	requirementText := flags.String("requirement", "local", "connectivity requirement: local or internet")
	scanWait := flags.Duration("scan-wait", 5*time.Second, "maximum wait for a fresh Wi-Fi scan")
	activationWait := flags.Duration("activation-wait", 30*time.Second, "maximum candidate activation wait")
	rollbackAfter := flags.Duration("rollback-after", 90*time.Second, "automatic checkpoint rollback duration")
	restorationWait := flags.Duration("restoration-wait", 30*time.Second, "maximum previous-connection restoration wait")
	yes := flags.Bool("yes", false, "confirm the disruptive setup lifecycle")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if !*yes {
		return errors.New("starting setup changes the active Wi-Fi interface; repeat with --yes after ensuring a recovery path")
	}
	if !*networkEnabled && !*standaloneEnabled {
		return errors.New("at least one of --network-enabled or --standalone-enabled must be true")
	}
	if *httpPort == 0 || *httpPort > 65535 || *listenerHTTPPort == 0 || *listenerHTTPPort > 65535 {
		return errors.New("HTTP ports must be between 1 and 65535")
	}
	if *httpPort == *listenerHTTPPort {
		return errors.New("--http-port and --listener-http-port must differ")
	}
	if *scanWait < 0 || *activationWait <= 0 || *restorationWait <= 0 {
		return errors.New("scan wait cannot be negative and activation/restoration waits must be positive")
	}
	if *rollbackAfter <= 0 || *rollbackAfter%time.Second != 0 {
		return errors.New("--rollback-after must be a positive whole number of seconds")
	}
	requirement := connectivity.Requirement(*requirementText)
	if err := requirement.Validate(); err != nil {
		return err
	}
	address, err := netip.ParsePrefix(*addressText)
	if err != nil {
		return fmt.Errorf("parse --address: %w", err)
	}
	portalPassword, err := readRequiredPassword(*passwordFile)
	if err != nil {
		return err
	}
	standalonePassword := ""
	if *standaloneEnabled {
		if *standaloneSSID == "" {
			return errors.New("--standalone-ssid is required when standalone mode is enabled")
		}
		standalonePassword, err = readRequiredPassword(*standalonePasswordFile)
		if err != nil {
			return err
		}
	}
	if err := validateBuiltFrontend(*frontendDirectory); err != nil {
		return err
	}
	canonicalURL := *portalURL
	if canonicalURL == "" {
		host := address.Addr().String()
		if *httpPort != 80 {
			host = net.JoinHostPort(host, fmt.Sprint(*httpPort))
		}
		canonicalURL = "http://" + host + "/"
	}
	origin, err := portalOrigin(canonicalURL)
	if err != nil {
		return err
	}

	return runInteractiveSetup(ctx, interactiveSetupOptions{
		Interface:           *interfaceName,
		ProvisioningSSID:    *ssid,
		ProvisioningPSK:     portalPassword,
		ProvisioningAddress: address,
		Band:                *band,
		PublicHTTPPort:      uint16(*httpPort),
		ListenerHTTPPort:    uint16(*listenerHTTPPort),
		PortalURL:           canonicalURL,
		PortalOrigin:        origin,
		DNSConfigPath:       *dnsConfigPath,
		Assets:              os.DirFS(*frontendDirectory),
		Branding:            webui.Options{Branding: webui.DefaultBranding()},
		NetworkEnabled:      *networkEnabled,
		StandaloneEnabled:   *standaloneEnabled,
		StandaloneSSID:      *standaloneSSID,
		StandalonePSK:       standalonePassword,
		StandaloneAddress:   *standaloneAddress,
		Requirement:         requirement,
		ScanWait:            *scanWait,
		ActivationWait:      *activationWait,
		RollbackAfter:       *rollbackAfter,
		RestorationWait:     *restorationWait,
		ReadyLabel:          "interactive setup",
	}, stdout)
}

type interactiveSetupOptions struct {
	Interface           string
	ProvisioningSSID    string
	ProvisioningPSK     string
	ProvisioningAddress netip.Prefix
	Band                string
	PublicHTTPPort      uint16
	ListenerHTTPPort    uint16
	PortalURL           string
	PortalOrigin        string
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
}

func runInteractiveSetup(ctx context.Context, options interactiveSetupOptions, stdout io.Writer) error {
	// Validate both possible AP profiles before connecting to D-Bus or changing the
	// active interface. NewNetworkBackend repeats the standalone check as a guard at
	// its own boundary.
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

	dns, err := captive.NewDNSConfigFile(options.DNSConfigPath)
	if err != nil {
		return err
	}
	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	redirect, err := captive.NewNFTRedirect("nft")
	if err != nil {
		return err
	}
	listenConfig := &net.ListenConfig{}
	lifecycle, err := captive.NewLifecycle(client, dns, redirect, listenConfig.Listen)
	if err != nil {
		return err
	}
	portal := &swappableHandler{}
	session, err := lifecycle.Start(ctx, captive.StartOptions{
		Interface:        options.Interface,
		SSID:             options.ProvisioningSSID,
		Password:         options.ProvisioningPSK,
		Address:          options.ProvisioningAddress,
		Band:             options.Band,
		Wait:             options.ActivationWait,
		PublicHTTPPort:   options.PublicHTTPPort,
		ListenerHTTPPort: options.ListenerHTTPPort,
		PortalURL:        options.PortalURL,
	}, portal)
	if err != nil {
		return err
	}
	cleanupAfterError := func(cause error) error {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return errors.Join(cause, session.Stop(cleanupContext))
	}
	infrastructureTransition, err := recovery.NewInfrastructure(client)
	if err != nil {
		return cleanupAfterError(err)
	}
	standaloneTransition, err := recovery.NewStandalone(client)
	if err != nil {
		return cleanupAfterError(err)
	}
	backend, err := setupflow.NewNetworkBackend(
		client,
		infrastructureTransition,
		standaloneTransition,
		session,
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
	if err != nil {
		return cleanupAfterError(err)
	}
	service, err := setupflow.NewService(ctx, backend, setupflow.Capabilities{
		Network:    options.NetworkEnabled,
		Standalone: options.StandaloneEnabled,
	})
	if err != nil {
		return cleanupAfterError(err)
	}
	api, err := webui.NewAPI(service, options.PortalOrigin, options.Branding)
	if err != nil {
		return cleanupAfterError(err)
	}
	handler, err := webui.NewHandler(api, options.Assets)
	if err != nil {
		return cleanupAfterError(err)
	}
	portal.Set(handler)

	activation := session.Activation()
	fmt.Fprintf(stdout, "%s is ready\n", options.ReadyLabel)
	fmt.Fprintf(stdout, "SSID: %s\n", options.ProvisioningSSID)
	fmt.Fprintf(stdout, "Portal: %s\n", session.PortalURL())
	fmt.Fprintf(stdout, "UUID: %s\n", activation.UUID)
	fmt.Fprintln(stdout, "Press Ctrl+C to stop setup and remove temporary resources.")

	var serveErr error
	select {
	case <-ctx.Done():
	case <-session.Done():
		serveErr = session.Wait()
		if serveErr == nil {
			serveErr = errors.New("setup HTTP listener stopped unexpectedly")
		}
	}
	cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelCleanup()
	stopErr := session.Stop(cleanupContext)
	if serveErr != nil || stopErr != nil {
		return errors.Join(serveErr, stopErr)
	}
	fmt.Fprintln(stdout, "interactive setup stopped and temporary resources were removed")
	return nil
}

func validateBuiltFrontend(directory string) error {
	indexPath := filepath.Join(directory, "index.html")
	contents, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Errorf("find built frontend index: %w", err)
	}
	if bytes.Contains(contents, []byte("/src/main.ts")) {
		return fmt.Errorf(
			"frontend directory %q contains Vite development source; use the compiled frontend/dist directory",
			directory,
		)
	}
	return nil
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

type swappableHandler struct {
	mu      sync.RWMutex
	handler http.Handler
}

func (handler *swappableHandler) Set(next http.Handler) {
	handler.mu.Lock()
	handler.handler = next
	handler.mu.Unlock()
}

func (handler *swappableHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	handler.mu.RLock()
	current := handler.handler
	handler.mu.RUnlock()
	if current == nil {
		response.Header().Set("Cache-Control", "no-store")
		http.Error(response, "Setup is starting. Try again in a moment.", http.StatusServiceUnavailable)
		return
	}
	current.ServeHTTP(response, request)
}

func debugProfileDelete(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug profile-delete", stderr)
	uuid := flags.String("uuid", "", "UUID of an onboardd-owned profile")
	yes := flags.Bool("yes", false, "confirm profile deletion")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if !*yes {
		return errors.New("profile deletion requires --yes")
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.DeleteOwnedProfile(ctx, *uuid); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Deleted onboardd-owned profile %s\n", *uuid)
	return nil
}

func debugStatus(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug status", stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	status, err := client.Status(ctx, *interfaceName)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, status)
	}

	fmt.Fprintf(stdout, "NetworkManager %s\n", status.Version)
	fmt.Fprintf(stdout, "State: %s\n", status.StateName)
	fmt.Fprintf(stdout, "Connectivity: %s (check available: %t)\n", status.ConnectivityName, status.ConnectivityCheck)
	fmt.Fprintf(stdout, "Wireless: enabled=%t hardware-enabled=%t\n", status.WirelessEnabled, status.WirelessHardwareEnabled)
	fmt.Fprintf(stdout, "Startup: %t\n", status.Startup)
	fmt.Fprintf(
		stdout,
		"Device: %s state=%s managed=%t active=%s\n",
		status.Device.Interface,
		status.Device.StateName,
		status.Device.Managed,
		emptyAsDash(status.Device.ActiveConnection),
	)
	return nil
}

func debugProfiles(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug profiles", stderr)
	onlyOwned := flags.Bool("owned", false, "show only profiles owned by onboardd")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	profiles, err := client.Profiles(ctx)
	if err != nil {
		return err
	}
	if *onlyOwned {
		filtered := profiles[:0]
		for _, profile := range profiles {
			if profile.Owned {
				filtered = append(filtered, profile)
			}
		}
		profiles = filtered
	}
	if *jsonOutput {
		return writeJSON(stdout, profiles)
	}

	table := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tUUID\tROLE\tOWNED\tAUTO\tPRIORITY\tSTORAGE\tUNSAVED\tSSID")
	for _, profile := range profiles {
		fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%t\t%t\t%d\t%s\t%t\t%s\n",
			profile.ID,
			profile.UUID,
			emptyAsDash(string(profile.Role)),
			profile.Owned,
			profile.Autoconnect,
			profile.Priority,
			profile.Persistence,
			profile.Unsaved,
			emptyAsDash(profile.SSID),
		)
	}
	return table.Flush()
}

func debugScan(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug scan", stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	wait := flags.Duration("wait", 5*time.Second, "maximum time to wait for a fresh scan")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	accessPoints, err := client.Scan(ctx, *interfaceName, *wait)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, accessPoints)
	}

	table := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "SSID\tSIGNAL\tSECURITY\tFREQUENCY\tBSSID")
	for _, accessPoint := range accessPoints {
		ssid := accessPoint.SSID
		if accessPoint.Hidden {
			ssid = "<hidden>"
		}
		fmt.Fprintf(
			table,
			"%s\t%d%%\t%s\t%d MHz\t%s\n",
			ssid,
			accessPoint.Strength,
			accessPoint.Security,
			accessPoint.Frequency,
			accessPoint.BSSID,
		)
	}
	return table.Flush()
}

func debugConnect(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug connect", stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	ssid := flags.String("ssid", "", "target Wi-Fi SSID")
	passwordFile := flags.String("password-file", "", "file containing the Wi-Fi password")
	open := flags.Bool("open", false, "connect to an explicitly open network")
	hidden := flags.Bool("hidden", false, "target network hides its SSID")
	id := flags.String("id", "", "optional human-readable NetworkManager profile ID")
	priority := flags.Int("priority", 0, "autoconnect priority from -999 to 999")
	wait := flags.Duration("wait", 30*time.Second, "maximum time to confirm activation")
	yes := flags.Bool("yes", false, "confirm the disruptive Wi-Fi change")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if !*yes {
		return errors.New("connect changes the active Wi-Fi interface; repeat with --yes after ensuring a recovery path")
	}
	if *priority < -999 || *priority > 999 {
		return errors.New("--priority must be between -999 and 999")
	}
	if *wait <= 0 {
		return errors.New("--wait must be positive so activation can be confirmed before selecting infrastructure mode")
	}
	password, err := readPassword(*passwordFile, *open)
	if err != nil {
		return err
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	activation, err := client.ConnectInfrastructure(ctx, networkmanager.InfrastructureOptions{
		ID:          *id,
		Interface:   *interfaceName,
		SSID:        *ssid,
		Password:    password,
		Open:        *open,
		Hidden:      *hidden,
		Autoconnect: false,
		Priority:    int32(*priority),
	})
	if err != nil {
		return err
	}
	if err := client.WaitForActivation(ctx, activation.ActivePath, *interfaceName, *wait); err != nil {
		return fmt.Errorf("profile %s was created but activation failed: %w", activation.UUID, err)
	}
	if err := client.FinalizeTransition(
		ctx,
		*interfaceName,
		networkmanager.RoleInfrastructure,
		*ssid,
		activation.UUID,
	); err != nil {
		return fmt.Errorf("profile %s activated but mode selection failed: %w", activation.UUID, err)
	}
	return writeActivation(stdout, activation, *jsonOutput)
}

func debugAccessPoint(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	role networkmanager.Role,
) error {
	commandName := "debug " + string(role) + "-start"
	flags := newFlagSet(commandName, stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	ssid := flags.String("ssid", "", "access-point SSID")
	passwordFile := flags.String("password-file", "", "file containing the access-point password")
	address := flags.String("address", "10.42.0.1/24", "access-point IPv4 address and prefix")
	band := flags.String("band", "bg", "Wi-Fi band: bg, a, or 6GHz")
	id := flags.String("id", "", "optional human-readable NetworkManager profile ID")
	wait := flags.Duration("wait", 30*time.Second, "maximum time to confirm activation")
	yes := flags.Bool("yes", false, "confirm the disruptive Wi-Fi change")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if !*yes {
		return errors.New("starting an access point changes the active Wi-Fi interface; repeat with --yes after ensuring a recovery path")
	}
	if *wait <= 0 {
		return errors.New("--wait must be positive so activation can be confirmed before selecting access-point mode")
	}
	password, err := readRequiredPassword(*passwordFile)
	if err != nil {
		return err
	}

	priority := int32(0)
	if role == networkmanager.RoleStandalone {
		priority = 999
	}
	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	activation, err := client.StartAccessPoint(ctx, networkmanager.AccessPointOptions{
		ID:          *id,
		Interface:   *interfaceName,
		SSID:        *ssid,
		Password:    password,
		Address:     *address,
		Role:        role,
		Autoconnect: false,
		Priority:    priority,
		Band:        *band,
	})
	if err != nil {
		return err
	}
	if err := client.WaitForActivation(ctx, activation.ActivePath, *interfaceName, *wait); err != nil {
		return fmt.Errorf("profile %s was created but activation failed: %w", activation.UUID, err)
	}
	if err := client.FinalizeTransition(ctx, *interfaceName, role, *ssid, activation.UUID); err != nil {
		return fmt.Errorf("profile %s activated but mode selection failed: %w", activation.UUID, err)
	}
	return writeActivation(stdout, activation, *jsonOutput)
}

func debugWatch(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug watch", stderr)
	jsonOutput := flags.Bool("json", false, "print one JSON object per line")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	events, watchErrors, err := client.WatchProperties(ctx)
	if err != nil {
		return err
	}
	if !*jsonOutput {
		fmt.Fprintln(stdout, "Watching NetworkManager property changes; press Ctrl+C to stop.")
	}
	for events != nil || watchErrors != nil {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if *jsonOutput {
				if err := json.NewEncoder(stdout).Encode(event); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(stdout, "%s %s changed=%v invalidated=%v\n", event.Path, event.Interface, event.Changed, event.Invalidated)
			}
		case watchErr, ok := <-watchErrors:
			if !ok {
				watchErrors = nil
				continue
			}
			if watchErr != nil {
				return watchErr
			}
		}
	}
	return nil
}

func debugReconcile(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug reconcile", stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	requirementValue := flags.String("requirement", string(connectivity.RequirementLocal), "connectivity requirement: local or internet")
	gracePeriod := flags.Duration("grace-period", 30*time.Second, "connectivity and activation grace period")
	watch := flags.Bool("watch", false, "continue reconciling until interrupted")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	requirement := connectivity.Requirement(*requirementValue)
	if err := requirement.Validate(); err != nil {
		return err
	}
	if *gracePeriod <= 0 {
		return errors.New("--grace-period must be positive")
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	observer := stateengine.NewNetworkManagerObserver(client, *interfaceName)
	engine, err := stateengine.New(observer, stateengine.Config{
		Requirement: requirement,
		GracePeriod: *gracePeriod,
	})
	if err != nil {
		return err
	}
	if !*watch {
		current, inspectErr := engine.Inspect(ctx)
		if inspectErr != nil {
			return inspectErr
		}
		return writeReconciliationState(stdout, current, *jsonOutput)
	}

	transitions, engineErrors, err := engine.Run(ctx)
	if err != nil {
		return err
	}
	ctxDone := ctx.Done()
	for transitions != nil || engineErrors != nil {
		select {
		case <-ctxDone:
			// Let the engine publish its final stopped transition and close its
			// channels before the D-Bus client is closed by this function.
			ctxDone = nil
		case transition, ok := <-transitions:
			if !ok {
				transitions = nil
				continue
			}
			if *jsonOutput {
				if err := writeJSON(stdout, transition); err != nil {
					return err
				}
				continue
			}
			fmt.Fprintf(
				stdout,
				"%s\t%d\t%s\t%s\t%s\t%s\n",
				time.Now().UTC().Format(time.RFC3339),
				transition.Current.Sequence,
				transition.Current.Stage,
				transition.Current.Mode,
				transition.Current.Reason,
				emptyAsDash(transition.Current.Detail),
			)
		case engineErr, ok := <-engineErrors:
			if !ok {
				engineErrors = nil
				continue
			}
			fmt.Fprintf(stderr, "onboardd reconcile: %v\n", engineErr)
		}
	}
	return nil
}

func writeReconciliationState(stdout io.Writer, current stateengine.State, jsonOutput bool) error {
	if jsonOutput {
		return writeJSON(stdout, current)
	}
	fmt.Fprintf(stdout, "Stage: %s\n", current.Stage)
	fmt.Fprintf(stdout, "Mode: %s\n", current.Mode)
	fmt.Fprintf(stdout, "Reason: %s\n", current.Reason)
	if current.Detail != "" {
		fmt.Fprintf(stdout, "Detail: %s\n", current.Detail)
	}
	return nil
}

func debugCheckpointCreate(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug checkpoint-create", stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	rollbackAfter := flags.Duration("rollback-after", 90*time.Second, "automatic rollback duration")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	checkpoint, err := client.CreateCheckpoint(ctx, *interfaceName, *rollbackAfter)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, checkpoint)
	}
	fmt.Fprintf(stdout, "Checkpoint: %s\n", checkpoint.Path)
	fmt.Fprintf(stdout, "Interface: %s\n", checkpoint.Interface)
	fmt.Fprintf(stdout, "Automatic rollback: %d seconds\n", checkpoint.RollbackSeconds)
	return nil
}

func debugCheckpointCommit(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug checkpoint-commit", stderr)
	path := flags.String("path", "", "NetworkManager checkpoint object path")
	yes := flags.Bool("yes", false, "confirm removal of rollback protection")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if !*yes {
		return errors.New("checkpoint commit removes rollback protection; repeat with --yes")
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.CommitCheckpoint(ctx, *path); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Committed checkpoint %s\n", *path)
	return nil
}

func debugCheckpointRollback(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug checkpoint-rollback", stderr)
	path := flags.String("path", "", "NetworkManager checkpoint object path")
	yes := flags.Bool("yes", false, "confirm disruptive network rollback")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if !*yes {
		return errors.New("checkpoint rollback changes the active network configuration; repeat with --yes")
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	result, err := client.RollbackCheckpoint(ctx, *path)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "Rolled back checkpoint %s\n", result.Checkpoint)
	for device, code := range result.Devices {
		fmt.Fprintf(stdout, "Device %s: result %d\n", device, code)
	}
	return nil
}

func openClient() (*networkmanager.Client, error) {
	client, err := networkmanager.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("NetworkManager debug commands require a Linux system bus: %w", err)
	}
	return client, nil
}

func readPassword(path string, open bool) (string, error) {
	if open {
		if path != "" {
			return "", errors.New("--open and --password-file cannot be used together")
		}
		return "", nil
	}
	return readRequiredPassword(path)
}

func readRequiredPassword(path string) (string, error) {
	if path == "" {
		return "", errors.New("--password-file is required; passwords are intentionally not accepted as command-line values")
	}
	return readPasswordFile(path)
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
	return readPasswordFile(path)
}

func readPasswordFile(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read password file %q: %w", path, err)
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(contents), "\n"), "\r"), nil
}

func writeActivation(stdout io.Writer, activation networkmanager.Activation, jsonOutput bool) error {
	if jsonOutput {
		return writeJSON(stdout, activation)
	}
	fmt.Fprintf(stdout, "%s profile activated\n", activation.Role)
	fmt.Fprintf(stdout, "UUID: %s\n", activation.UUID)
	fmt.Fprintf(stdout, "Persistence: %s\n", activation.Persistence)
	fmt.Fprintf(stdout, "Profile: %s\n", activation.ProfilePath)
	fmt.Fprintf(stdout, "Active connection: %s\n", activation.ActivePath)
	return nil
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func requireNoArgs(flags *flag.FlagSet) error {
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	return nil
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

func emptyAsDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func printRootHelp(writer io.Writer) {
	fmt.Fprintln(writer, "onboardd - headless appliance network onboarding")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Usage:")
	fmt.Fprintln(writer, "  onboardd --version")
	fmt.Fprintln(writer, "  onboardd setup [--config /etc/onboardd/config.toml] [operational overrides]")
	fmt.Fprintln(writer, "  onboardd debug <command> [options]")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "The setup command loads TOML, environment variables, and CLI overrides, then starts")
	fmt.Fprintln(writer, "the embedded setup portal. Run 'onboardd setup -h' for its options.")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Run 'onboardd debug help' for NetworkManager and reconciliation tools.")
}

func printDebugHelp(writer io.Writer) {
	fmt.Fprintln(writer, "NetworkManager D-Bus and reconciliation diagnostics")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Read-only commands:")
	fmt.Fprintln(writer, "  onboardd debug config [--config FILE] [--render] [operational overrides]")
	fmt.Fprintln(writer, "  onboardd debug status [--interface wlan0] [--json]")
	fmt.Fprintln(writer, "  onboardd debug profiles [--owned] [--json]")
	fmt.Fprintln(writer, "  onboardd debug scan [--interface wlan0] [--wait 5s] [--json]")
	fmt.Fprintln(writer, "  onboardd debug watch [--json]")
	fmt.Fprintln(writer, "  onboardd debug reconcile [--requirement local|internet] [--watch] [--json]")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Checkpoint commands:")
	fmt.Fprintln(writer, "  onboardd debug checkpoint-create [--interface wlan0] [--rollback-after 90s]")
	fmt.Fprintln(writer, "  onboardd debug checkpoint-commit --path OBJECT_PATH --yes")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Disruptive commands (require --yes and a recovery path):")
	fmt.Fprintln(writer, "  onboardd debug connect --ssid NAME (--password-file FILE | --open) --yes")
	fmt.Fprintln(writer, "  onboardd debug connect-protected --ssid NAME --provisioning-uuid UUID --yes")
	fmt.Fprintln(writer, "  onboardd debug provisioning-start --ssid NAME --password-file FILE --yes")
	fmt.Fprintln(writer, "  onboardd debug standalone-start --ssid NAME --password-file FILE --yes")
	fmt.Fprintln(writer, "  onboardd debug captive-start --ssid NAME --password-file FILE --yes")
	fmt.Fprintln(writer, "  onboardd debug setup-start --ssid NAME --password-file FILE --frontend-dir DIR --yes")
	fmt.Fprintln(writer, "  onboardd debug profile-delete --uuid UUID --yes")
	fmt.Fprintln(writer, "  onboardd debug checkpoint-rollback --path OBJECT_PATH --yes")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Passwords are accepted only through files and are never printed.")
}
