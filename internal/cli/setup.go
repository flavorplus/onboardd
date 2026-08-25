package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/flavorplus/onboardd/internal/captive"
	appconfig "github.com/flavorplus/onboardd/internal/config"
	"github.com/flavorplus/onboardd/internal/connectivity"
	"github.com/flavorplus/onboardd/internal/discovery"
	embeddedfrontend "github.com/flavorplus/onboardd/internal/frontend"
	"github.com/flavorplus/onboardd/internal/networkmanager"
	"github.com/flavorplus/onboardd/internal/recovery"
	setupflow "github.com/flavorplus/onboardd/internal/setup"
	webui "github.com/flavorplus/onboardd/internal/web"
)

const (
	defaultBand          = "bg"
	defaultDNSConfigPath = "/etc/NetworkManager/dnsmasq-shared.d/onboardd.conf"
)

func runSetup(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	defaults := appconfig.Defaults()
	flags := newFlagSet("setup", stderr)
	configPath := flags.String("config", appconfig.SystemPath, "TOML configuration file")
	operational := bindOperationalConfigFlags(flags, defaults)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}

	set := explicitlySetFlags(flags)
	overrides, err := operational.overrides(flags)
	if err != nil {
		return err
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
	avahiHostname, err := discovery.CurrentHostname(ctx)
	if err != nil {
		return fmt.Errorf("discover host mDNS name: %w", err)
	}
	identity, err := appconfig.LoadIdentity()
	if err != nil {
		return err
	}
	identity.Hostname = avahiHostname
	resolved, err = appconfig.RenderTemplates(resolved, identity)
	if err != nil {
		return err
	}
	options, err := configuredSetupOptions(resolved, avahiHostname)
	if err != nil {
		return err
	}
	return runInteractiveSetup(ctx, options, stdout)
}

func configuredSetupOptions(resolved appconfig.Config, hostname string) (interactiveSetupOptions, error) {
	branding, err := webui.OptionsFromConfig(resolved, hostname)
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
		if branding.Handoff != nil && branding.Handoff.Standalone != nil &&
			branding.Handoff.ShowStandaloneCredentials {
			branding.Handoff.Standalone.Password = standalonePassword
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
		Hostname:            hostname,
		RecoveryGPIO:        resolved.Recovery.GPIO,
	}, nil
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
	Hostname            string
	RecoveryGPIO        appconfig.RecoveryGPIO
}

func runInteractiveSetup(ctx context.Context, options interactiveSetupOptions, stdout io.Writer) error {
	// Validate both possible AP profiles before connecting to D-Bus or changing the
	// active interface. NewNetworkBackend repeats the standalone check at its boundary.
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
	landingPage, err := fs.ReadFile(options.Assets, "landing.html")
	if err != nil {
		return fmt.Errorf("frontend landing page: %w", err)
	}
	if options.Branding.Handoff == nil {
		return errors.New("resolved handoff configuration is required")
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
	publisher, err := discovery.Start(ctx, discovery.Options{
		ServiceName: options.ReadyLabel,
		Port:        options.ListenerHTTPPort,
	})
	if err != nil {
		return fmt.Errorf("start mDNS discovery: %w", err)
	}
	if !strings.EqualFold(publisher.Hostname(), options.Hostname) {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return errors.Join(
			fmt.Errorf("Avahi hostname changed from %q to %q while setup was starting", options.Hostname, publisher.Hostname()),
			publisher.Close(cleanupContext),
		)
	}
	portal := &swappableHandler{}
	portal.Set(http.FileServer(http.FS(options.Assets)))
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
		SetupURL:         options.Branding.Handoff.SetupURL,
		LandingPage:      landingPage,
	}, portal)
	if err != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return errors.Join(err, publisher.Close(cleanupContext))
	}
	cleanupAfterError := func(cause error) error {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return errors.Join(cause, publisher.Close(cleanupContext), session.Stop(cleanupContext))
	}
	if err := client.DeletePendingInfrastructureProfiles(ctx, options.Interface); err != nil {
		return cleanupAfterError(
			fmt.Errorf("recover interrupted infrastructure candidates: %w", err),
		)
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
	fmt.Fprintf(stdout, "Setup: %s\n", options.Branding.Handoff.SetupURL)
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
	operationContext, cancelOperations := context.WithTimeout(
		context.Background(),
		options.RestorationWait+applianceCleanupTimeout,
	)
	operationErr := service.Shutdown(operationContext)
	cancelOperations()
	cleanupContext, cancelCleanup := context.WithTimeout(
		context.Background(),
		applianceCleanupTimeout,
	)
	defer cancelCleanup()
	discoveryErr := publisher.Close(cleanupContext)
	stopErr := session.Stop(cleanupContext)
	if serveErr != nil || operationErr != nil || discoveryErr != nil || stopErr != nil {
		return errors.Join(serveErr, operationErr, discoveryErr, stopErr)
	}
	fmt.Fprintln(stdout, "interactive setup stopped and temporary resources were removed")
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
