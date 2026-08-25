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
	SetupURL         string
	LandingPage      []byte
}

// Lifecycle coordinates temporary AP, DNS, and HTTP resources.
type Lifecycle struct {
	provisioner *Provisioner
	redirect    PortRedirector
	listen      ListenFunc

	transitionMu        sync.Mutex
	pendingProvisioning *ProvisioningSession
	redirectNeedsRemove bool
}

// NewLifecycle validates and stores the Phase 3 platform dependencies.
func NewLifecycle(
	network networkManager,
	dns DNSConfigurer,
	redirect PortRedirector,
	listen ListenFunc,
) (*Lifecycle, error) {
	provisioner, err := NewProvisioner(network, dns)
	if err != nil {
		return nil, err
	}
	if redirect == nil {
		return nil, errors.New("captive port redirector is required")
	}
	if listen == nil {
		return nil, errors.New("HTTP listen function is required")
	}
	return &Lifecycle{provisioner: provisioner, redirect: redirect, listen: listen}, nil
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
	handler, err := NewHTTPHandler(
		options.PortalURL,
		options.SetupURL,
		options.ListenerHTTPPort,
		options.LandingPage,
		portal,
	)
	if err != nil {
		return nil, err
	}
	lifecycle.transitionMu.Lock()
	defer lifecycle.transitionMu.Unlock()
	if lifecycle.pendingProvisioning != nil || lifecycle.redirectNeedsRemove {
		if err := lifecycle.cleanupPendingLocked(ctx); err != nil {
			return nil, fmt.Errorf("finish previous captive cleanup: %w", err)
		}
	}
	provisioning, err := lifecycle.provisioner.Start(ctx, ProvisioningOptions{
		Interface: options.Interface,
		SSID:      options.SSID,
		Password:  options.Password,
		Address:   options.Address,
		Band:      options.Band,
		Wait:      options.Wait,
	})
	if err != nil {
		lifecycle.pendingProvisioning = provisioning
		return nil, err
	}

	listenAddress := net.JoinHostPort("0.0.0.0", strconv.Itoa(int(options.ListenerHTTPPort)))
	server, err := ListenHTTPServer(ctx, lifecycle.listen, "tcp4", listenAddress, handler)
	if err != nil {
		return nil, lifecycle.startFailure("bind captive HTTP listener", provisioning, err)
	}
	lifecycle.redirectNeedsRemove = true
	if err := lifecycle.redirect.Install(
		ctx,
		options.Interface,
		options.PublicHTTPPort,
		options.ListenerHTTPPort,
	); err != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = server.Shutdown(cleanupContext)
		lifecycle.pendingProvisioning = provisioning
		cleanupErr := lifecycle.cleanupPendingLocked(cleanupContext)
		cancel()
		return nil, errors.Join(
			fmt.Errorf("install captive HTTP redirect: %w", err),
			cleanupErr,
		)
	}
	lifecycle.redirectNeedsRemove = false

	return &Session{
		provisioning: provisioning,
		portalURL:    options.PortalURL,
		server:       server,
		redirect:     lifecycle.redirect,
	}, nil
}

func (lifecycle *Lifecycle) startFailure(
	stage string,
	provisioning *ProvisioningSession,
	cause error,
) error {
	cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lifecycle.pendingProvisioning = provisioning
	return errors.Join(
		fmt.Errorf("%s: %w", stage, cause),
		lifecycle.cleanupPendingLocked(cleanupContext),
	)
}

func (lifecycle *Lifecycle) cleanupPendingLocked(ctx context.Context) error {
	var cleanupErrors []error
	if lifecycle.redirectNeedsRemove {
		if err := lifecycle.redirect.Remove(ctx); err != nil {
			cleanupErrors = append(
				cleanupErrors,
				fmt.Errorf("remove captive HTTP redirect: %w", err),
			)
		} else {
			lifecycle.redirectNeedsRemove = false
		}
	}
	if lifecycle.pendingProvisioning != nil {
		if err := lifecycle.pendingProvisioning.Stop(ctx); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		} else {
			lifecycle.pendingProvisioning = nil
		}
	}
	return errors.Join(cleanupErrors...)
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

// Session is a fully active temporary provisioning lifecycle.
type Session struct {
	provisioning *ProvisioningSession
	portalURL    string
	server       *HTTPServer
	redirect     PortRedirector

	cleanupMu           sync.Mutex
	redirectRemoved     bool
	provisioningStopped bool
	serverStopped       bool
}

// Activation returns the in-memory NetworkManager profile details.
func (session *Session) Activation() networkmanager.Activation {
	return session.provisioning.Activation()
}

// PortalURL returns the fixed redirect target for this session.
func (session *Session) PortalURL() string { return session.portalURL }

// Done closes if the HTTP listener stops unexpectedly or during Stop.
func (session *Session) Done() <-chan struct{} { return session.server.Done() }

// Wait returns the captive HTTP listener's terminal result.
func (session *Session) Wait() error { return session.server.Wait() }

// ExitCaptive removes the interface redirect, temporary AP, and wildcard DNS while
// deliberately leaving the private HTTP listener available for progress and handoff.
func (session *Session) ExitCaptive(ctx context.Context) error {
	session.cleanupMu.Lock()
	defer session.cleanupMu.Unlock()
	return session.exitCaptiveLocked(ctx)
}

func (session *Session) exitCaptiveLocked(ctx context.Context) error {
	var exitErrors []error
	if !session.redirectRemoved {
		if err := session.redirect.Remove(ctx); err != nil {
			exitErrors = append(exitErrors, fmt.Errorf("remove captive HTTP redirect: %w", err))
		} else {
			session.redirectRemoved = true
		}
	}
	if !session.provisioningStopped {
		if err := session.provisioning.Stop(ctx); err != nil {
			exitErrors = append(exitErrors, err)
		} else {
			session.provisioningStopped = true
		}
	}
	return errors.Join(exitErrors...)
}

// Stop leaves captive mode and then shuts down the private HTTP listener.
func (session *Session) Stop(ctx context.Context) error {
	session.cleanupMu.Lock()
	defer session.cleanupMu.Unlock()
	exitErr := session.exitCaptiveLocked(ctx)
	var serverErr error
	if !session.serverStopped {
		serverErr = session.server.Shutdown(ctx)
		if serverErr != nil {
			serverErr = fmt.Errorf("stop captive HTTP listener: %w", serverErr)
		} else {
			session.serverStopped = true
		}
	}
	return errors.Join(exitErr, serverErr)
}
