package captive

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ManagerOptions contains fixed policy for dynamically entering captive mode.
type ManagerOptions struct {
	Provisioning     ProvisioningOptions
	PublicHTTPPort   uint16
	ListenerHTTPPort uint16
	CleanupTimeout   time.Duration
}

// Manager owns the temporary resources that exist only in captive mode. The
// private HTTP server is intentionally owned by the application lifecycle instead.
type Manager struct {
	provisioner *Provisioner
	redirect    PortRedirector
	options     ManagerOptions

	// transitionMu serializes complete network resource transitions. Holding it
	// across I/O is intentional: entering and leaving captive mode must not overlap.
	transitionMu        sync.Mutex
	session             *ProvisioningSession
	redirectNeedsRemove bool
	active              bool
}

// NewManager validates dependencies and fixed captive-mode policy.
func NewManager(
	provisioner *Provisioner,
	redirect PortRedirector,
	options ManagerOptions,
) (*Manager, error) {
	if provisioner == nil {
		return nil, errors.New("provisioner is required")
	}
	if redirect == nil {
		return nil, errors.New("captive port redirector is required")
	}
	if err := validateProvisioningOptions(options.Provisioning); err != nil {
		return nil, err
	}
	if options.PublicHTTPPort == 0 || options.ListenerHTTPPort == 0 {
		return nil, errors.New("public and listener HTTP ports must be nonzero")
	}
	if options.PublicHTTPPort == options.ListenerHTTPPort {
		return nil, errors.New("public and listener HTTP ports must differ")
	}
	if options.CleanupTimeout <= 0 {
		return nil, errors.New("captive cleanup timeout must be positive")
	}
	return &Manager{provisioner: provisioner, redirect: redirect, options: options}, nil
}

// RecoverStartup removes owned captive resources that can survive an abrupt process
// exit. It must run on a newly constructed manager before reconciliation begins.
func (m *Manager) RecoverStartup(ctx context.Context) error {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	if m.active || m.session != nil || m.redirectNeedsRemove {
		return errors.New("startup recovery requires an unused captive manager")
	}
	redirectErr := m.redirect.Remove(ctx)
	if redirectErr != nil {
		redirectErr = fmt.Errorf("remove stale captive HTTP redirect: %w", redirectErr)
	}
	provisioningErr := m.provisioner.RecoverStartup(
		ctx,
		m.options.Provisioning.Interface,
	)
	return errors.Join(redirectErr, provisioningErr)
}

// EnterProvisioning activates the temporary AP, wildcard DNS, and public-port
// redirect. Calling it while captive mode is already active has no effect.
func (m *Manager) EnterProvisioning(ctx context.Context) error {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	if m.active {
		return nil
	}
	if m.session != nil || m.redirectNeedsRemove {
		if err := m.cleanupLocked(ctx); err != nil {
			return fmt.Errorf("finish previous captive cleanup: %w", err)
		}
	}

	session, err := m.provisioner.Start(ctx, m.options.Provisioning)
	if session != nil {
		m.session = session
	}
	if err != nil {
		return err
	}
	// Installation can fail after creating some rules, so removal remains
	// necessary until it succeeds even when Install returns an error.
	m.redirectNeedsRemove = true
	if err := m.redirect.Install(
		ctx,
		m.options.Provisioning.Interface,
		m.options.PublicHTTPPort,
		m.options.ListenerHTTPPort,
	); err != nil {
		cleanupContext, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			m.options.CleanupTimeout,
		)
		defer cancel()
		return errors.Join(
			fmt.Errorf("install captive HTTP redirect: %w", err),
			m.cleanupLocked(cleanupContext),
		)
	}
	m.active = true
	return nil
}

// LeaveProvisioning removes the redirect, temporary profile, and wildcard DNS.
// Calling it when captive mode is inactive has no effect.
func (m *Manager) LeaveProvisioning(ctx context.Context) error {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	if m.session == nil && !m.redirectNeedsRemove {
		return nil
	}
	m.active = false
	return m.cleanupLocked(ctx)
}

// ExitCaptive lets successful setup operations converge on the same cleanup path
// used by the appliance state controller.
func (m *Manager) ExitCaptive(ctx context.Context) error {
	return m.LeaveProvisioning(ctx)
}

func (m *Manager) cleanupLocked(ctx context.Context) error {
	var cleanupErrors []error
	if m.redirectNeedsRemove {
		if err := m.redirect.Remove(ctx); err != nil {
			cleanupErrors = append(
				cleanupErrors,
				fmt.Errorf("remove captive HTTP redirect: %w", err),
			)
		} else {
			m.redirectNeedsRemove = false
		}
	}
	if m.session != nil {
		if err := m.session.Stop(ctx); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		} else {
			m.session = nil
		}
	}
	return errors.Join(cleanupErrors...)
}
