package captive

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/flavorplus/onboardd/internal/networkmanager"
)

type networkManager interface {
	StartAccessPoint(context.Context, networkmanager.AccessPointOptions) (networkmanager.Activation, error)
	WaitForActivation(context.Context, string, string, time.Duration) error
	Status(context.Context, string) (networkmanager.Status, error)
	FinalizeTransition(context.Context, string, networkmanager.Role, string, string) error
	DeleteOwnedProfile(context.Context, string) error
}

// ListenFunc binds the captive HTTP socket after the AP address is confirmed.
type ListenFunc func(context.Context, string, string) (net.Listener, error)

// StartOptions describes one temporary provisioning session.
type StartOptions struct {
	Interface        string
	SSID             string
	Password         string
	Address          netip.Prefix
	Band             string
	Wait             time.Duration
	PublicHTTPPort   uint16
	ListenerHTTPPort uint16
	PortalURL        string
}

// Lifecycle coordinates temporary AP, DNS, and HTTP resources.
type Lifecycle struct {
	network  networkManager
	dns      DNSConfigurer
	redirect PortRedirector
	listen   ListenFunc
}

// NewLifecycle validates and stores the Phase 3 platform dependencies.
func NewLifecycle(
	network networkManager,
	dns DNSConfigurer,
	redirect PortRedirector,
	listen ListenFunc,
) (*Lifecycle, error) {
	if network == nil {
		return nil, errors.New("NetworkManager client is required")
	}
	if dns == nil {
		return nil, errors.New("captive DNS configurer is required")
	}
	if redirect == nil {
		return nil, errors.New("captive port redirector is required")
	}
	if listen == nil {
		return nil, errors.New("HTTP listen function is required")
	}
	return &Lifecycle{network: network, dns: dns, redirect: redirect, listen: listen}, nil
}

// Start enters temporary provisioning and returns only after the portal is reachable.
func (lifecycle *Lifecycle) Start(
	ctx context.Context,
	options StartOptions,
	portal http.Handler,
) (*Session, error) {
	if err := validateStartOptions(options); err != nil {
		return nil, err
	}
	handler, err := NewHTTPHandler(options.PortalURL, options.ListenerHTTPPort, portal)
	if err != nil {
		return nil, err
	}
	if _, _, err := networkmanager.BuildAccessPointSettings(networkmanager.AccessPointOptions{
		Interface:   options.Interface,
		SSID:        options.SSID,
		Password:    options.Password,
		Address:     options.Address.String(),
		Role:        networkmanager.RoleProvisioning,
		Autoconnect: false,
		Band:        options.Band,
	}); err != nil {
		return nil, fmt.Errorf("validate provisioning AP: %w", err)
	}

	address := options.Address.Addr()
	if err := lifecycle.dns.Install(address); err != nil {
		return nil, fmt.Errorf("prepare captive DNS: %w", err)
	}

	activation, err := lifecycle.network.StartAccessPoint(ctx, networkmanager.AccessPointOptions{
		Interface:   options.Interface,
		SSID:        options.SSID,
		Password:    options.Password,
		Address:     options.Address.String(),
		Role:        networkmanager.RoleProvisioning,
		Autoconnect: false,
		Band:        options.Band,
	})
	if err != nil {
		return nil, lifecycle.startFailure("activate provisioning AP", "", err)
	}
	if err := lifecycle.network.WaitForActivation(ctx, activation.ActivePath, options.Interface, options.Wait); err != nil {
		return nil, lifecycle.startFailure("wait for provisioning AP", activation.UUID, err)
	}
	status, err := lifecycle.network.Status(ctx, options.Interface)
	if err != nil {
		return nil, lifecycle.startFailure("confirm provisioning AP address", activation.UUID, err)
	}
	if err := confirmProvisioningAddress(status, activation.UUID, address); err != nil {
		return nil, lifecycle.startFailure("confirm provisioning AP address", activation.UUID, err)
	}
	if err := lifecycle.network.FinalizeTransition(
		ctx,
		options.Interface,
		networkmanager.RoleProvisioning,
		options.SSID,
		activation.UUID,
	); err != nil {
		return nil, lifecycle.startFailure("finalize provisioning AP", activation.UUID, err)
	}

	listenAddress := net.JoinHostPort("0.0.0.0", strconv.Itoa(int(options.ListenerHTTPPort)))
	listener, err := lifecycle.listen(ctx, "tcp4", listenAddress)
	if err != nil {
		return nil, lifecycle.startFailure("bind captive HTTP listener", activation.UUID, err)
	}
	server, err := StartHTTPServer(listener, handler)
	if err != nil {
		_ = listener.Close()
		return nil, lifecycle.startFailure("start captive HTTP listener", activation.UUID, err)
	}
	if err := lifecycle.redirect.Install(
		ctx,
		options.Interface,
		options.PublicHTTPPort,
		options.ListenerHTTPPort,
	); err != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = server.Shutdown(cleanupContext)
		_ = lifecycle.redirect.Remove(cleanupContext)
		cancel()
		return nil, lifecycle.startFailure("install captive HTTP redirect", activation.UUID, err)
	}

	return &Session{
		activation: activation,
		portalURL:  options.PortalURL,
		server:     server,
		network:    lifecycle.network,
		dns:        lifecycle.dns,
		redirect:   lifecycle.redirect,
	}, nil
}

func (lifecycle *Lifecycle) startFailure(stage, uuid string, cause error) error {
	cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var cleanupErrors []error
	if uuid != "" {
		if err := lifecycle.network.DeleteOwnedProfile(cleanupContext, uuid); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete incomplete provisioning profile: %w", err))
		}
	}
	if err := lifecycle.dns.Remove(); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove captive DNS after failed start: %w", err))
	}
	return errors.Join(fmt.Errorf("%s: %w", stage, cause), errors.Join(cleanupErrors...))
}

func validateStartOptions(options StartOptions) error {
	if options.Interface == "" {
		return errors.New("interface is required")
	}
	if options.SSID == "" {
		return errors.New("SSID is required")
	}
	if options.Password == "" {
		return errors.New("password is required")
	}
	address := options.Address.Addr()
	if !options.Address.IsValid() || !address.Is4() || address.IsUnspecified() || address.IsMulticast() {
		return errors.New("address must be a usable IPv4 prefix")
	}
	if options.Wait <= 0 {
		return errors.New("activation wait must be positive")
	}
	if options.PublicHTTPPort == 0 || options.ListenerHTTPPort == 0 {
		return errors.New("public and listener HTTP ports must be nonzero")
	}
	if options.PublicHTTPPort == options.ListenerHTTPPort {
		return errors.New("public and listener HTTP ports must differ")
	}
	return nil
}

func confirmProvisioningAddress(
	status networkmanager.Status,
	uuid string,
	address netip.Addr,
) error {
	if status.Device.State != networkmanager.DeviceStateActivated {
		return fmt.Errorf("device state is %s, want activated", status.Device.StateName)
	}
	if status.Device.ActiveUUID != uuid {
		return fmt.Errorf("active profile is %q, want %q", status.Device.ActiveUUID, uuid)
	}
	for _, candidate := range status.Device.IPv4Addresses {
		parsed, err := netip.ParseAddr(candidate)
		if err == nil && parsed == address {
			return nil
		}
	}
	return fmt.Errorf("device does not have expected address %s", address)
}

// Session is a fully active temporary provisioning lifecycle.
type Session struct {
	activation networkmanager.Activation
	portalURL  string
	server     *HTTPServer
	network    networkManager
	dns        DNSConfigurer
	redirect   PortRedirector

	exitOnce sync.Once
	exitErr  error
	stopOnce sync.Once
	stopErr  error
}

// Activation returns the in-memory NetworkManager profile details.
func (session *Session) Activation() networkmanager.Activation { return session.activation }

// PortalURL returns the fixed redirect target for this session.
func (session *Session) PortalURL() string { return session.portalURL }

// Done closes if the HTTP listener stops unexpectedly or during Stop.
func (session *Session) Done() <-chan struct{} { return session.server.Done() }

// Wait returns the captive HTTP listener's terminal result.
func (session *Session) Wait() error { return session.server.Wait() }

// ExitCaptive removes the interface redirect, temporary AP, and wildcard DNS while
// deliberately leaving the private HTTP listener available for progress and handoff.
func (session *Session) ExitCaptive(ctx context.Context) error {
	session.exitOnce.Do(func() {
		var exitErrors []error
		if err := session.redirect.Remove(ctx); err != nil {
			exitErrors = append(exitErrors, fmt.Errorf("remove captive HTTP redirect: %w", err))
		}
		if err := session.network.DeleteOwnedProfile(ctx, session.activation.UUID); err != nil {
			exitErrors = append(exitErrors, fmt.Errorf("delete provisioning profile: %w", err))
		}
		if err := session.dns.Remove(); err != nil {
			exitErrors = append(exitErrors, fmt.Errorf("remove captive DNS: %w", err))
		}
		session.exitErr = errors.Join(exitErrors...)
	})
	return session.exitErr
}

// Stop leaves captive mode and then shuts down the private HTTP listener.
func (session *Session) Stop(ctx context.Context) error {
	session.stopOnce.Do(func() {
		exitErr := session.ExitCaptive(ctx)
		serverErr := session.server.Shutdown(ctx)
		if serverErr != nil {
			serverErr = fmt.Errorf("stop captive HTTP listener: %w", serverErr)
		}
		session.stopErr = errors.Join(exitErr, serverErr)
	})
	return session.stopErr
}
