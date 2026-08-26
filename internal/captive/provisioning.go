package captive

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
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

// ProvisioningOptions describes the NetworkManager and DNS resources needed for
// one temporary provisioning network. HTTP serving is deliberately independent.
type ProvisioningOptions struct {
	Interface string
	SSID      string
	Password  string
	Address   netip.Prefix
	Band      string
	Wait      time.Duration
}

// Provisioner creates independent temporary provisioning sessions. A stopped
// session does not prevent the same provisioner from creating another session.
type Provisioner struct {
	network networkManager
	dns     DNSConfigurer
}

// NewProvisioner validates the platform dependencies used by every session.
func NewProvisioner(network networkManager, dns DNSConfigurer) (*Provisioner, error) {
	if network == nil {
		return nil, errors.New("NetworkManager client is required")
	}
	if dns == nil {
		return nil, errors.New("captive DNS configurer is required")
	}
	return &Provisioner{network: network, dns: dns}, nil
}

// RecoverStartup removes externally visible provisioning state that may outlive an
// abruptly terminated process. NetworkManager deletion remains restricted to owned
// provisioning profiles on the configured interface.
func (p *Provisioner) RecoverStartup(ctx context.Context, interfaceName string) error {
	if interfaceName == "" {
		return errors.New("interface is required")
	}
	profileErr := p.network.FinalizeTransition(
		ctx,
		interfaceName,
		networkmanager.RoleProvisioning,
		"",
		"",
	)
	if profileErr != nil {
		profileErr = fmt.Errorf("remove stale provisioning profiles: %w", profileErr)
	}
	dnsErr := p.dns.Remove()
	if dnsErr != nil {
		dnsErr = fmt.Errorf("remove stale captive DNS: %w", dnsErr)
	}
	return errors.Join(profileErr, dnsErr)
}

// Start activates a temporary provisioning AP and wildcard DNS.
func (p *Provisioner) Start(
	ctx context.Context,
	options ProvisioningOptions,
) (*ProvisioningSession, error) {
	if err := validateProvisioningOptions(options); err != nil {
		return nil, err
	}
	apOptions := networkmanager.AccessPointOptions{
		Interface:   options.Interface,
		SSID:        options.SSID,
		Password:    options.Password,
		Address:     options.Address.String(),
		Role:        networkmanager.RoleProvisioning,
		Autoconnect: false,
		Band:        options.Band,
	}
	if _, _, err := networkmanager.BuildAccessPointSettings(apOptions); err != nil {
		return nil, fmt.Errorf("validate provisioning AP: %w", err)
	}

	address := options.Address.Addr()
	if err := p.dns.Install(address); err != nil {
		return nil, fmt.Errorf("prepare captive DNS: %w", err)
	}
	session := &ProvisioningSession{
		network:        p.network,
		dns:            p.dns,
		profileRemoved: true,
	}
	activation, err := p.network.StartAccessPoint(ctx, apOptions)
	if activation.UUID != "" {
		session.activation = activation
		session.profileRemoved = false
	}
	if err != nil {
		return p.startFailure("activate provisioning AP", session, err)
	}
	if err := p.network.WaitForActivation(
		ctx,
		activation.ActivePath,
		options.Interface,
		options.Wait,
	); err != nil {
		return p.startFailure("wait for provisioning AP", session, err)
	}
	status, err := p.network.Status(ctx, options.Interface)
	if err != nil {
		return p.startFailure("confirm provisioning AP address", session, err)
	}
	if err := confirmProvisioningAddress(status, activation.UUID, address); err != nil {
		return p.startFailure("confirm provisioning AP address", session, err)
	}
	if err := p.network.FinalizeTransition(
		ctx,
		options.Interface,
		networkmanager.RoleProvisioning,
		options.SSID,
		activation.UUID,
	); err != nil {
		return p.startFailure("finalize provisioning AP", session, err)
	}

	return session, nil
}

func (p *Provisioner) startFailure(
	stage string,
	session *ProvisioningSession,
	cause error,
) (*ProvisioningSession, error) {
	cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cleanupErr := session.Stop(cleanupContext)
	startErr := errors.Join(fmt.Errorf("%s: %w", stage, cause), cleanupErr)
	if cleanupErr != nil {
		return session, startErr
	}
	return nil, startErr
}

func validateProvisioningOptions(options ProvisioningOptions) error {
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
	return nil
}

func confirmProvisioningAddress(
	status networkmanager.Status,
	uuid string,
	address netip.Addr,
) error {
	if status.Device.State != networkmanager.DeviceStateActivated {
		return fmt.Errorf("device state is %s, want activated", status.Device.State)
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

// ProvisioningSession owns one activated provisioning AP and wildcard DNS.
type ProvisioningSession struct {
	activation networkmanager.Activation
	network    networkManager
	dns        DNSConfigurer

	stopMu         sync.Mutex
	profileRemoved bool
	dnsRemoved     bool
}

// Activation returns the temporary NetworkManager profile details.
func (s *ProvisioningSession) Activation() networkmanager.Activation {
	return s.activation
}

// Stop deletes the temporary profile and removes wildcard DNS. Successful
// cleanup steps are remembered while failed steps remain retryable.
func (s *ProvisioningSession) Stop(ctx context.Context) error {
	s.stopMu.Lock()
	defer s.stopMu.Unlock()
	stopErrors := []error{}
	if !s.profileRemoved {
		if err := s.network.DeleteOwnedProfile(ctx, s.activation.UUID); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("delete provisioning profile: %w", err))
		} else {
			s.profileRemoved = true
		}
	}
	if !s.dnsRemoved {
		if err := s.dns.Remove(); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("remove captive DNS: %w", err))
		} else {
			s.dnsRemoved = true
		}
	}
	return errors.Join(stopErrors...)
}
