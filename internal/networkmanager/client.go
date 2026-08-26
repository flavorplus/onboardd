package networkmanager

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	service                 = "org.freedesktop.NetworkManager"
	managerPath             = dbus.ObjectPath("/org/freedesktop/NetworkManager")
	settingsPath            = dbus.ObjectPath("/org/freedesktop/NetworkManager/Settings")
	managerInterface        = "org.freedesktop.NetworkManager"
	deviceInterface         = "org.freedesktop.NetworkManager.Device"
	wirelessDeviceInterface = "org.freedesktop.NetworkManager.Device.Wireless"
	accessPointInterface    = "org.freedesktop.NetworkManager.AccessPoint"
	settingsInterface       = "org.freedesktop.NetworkManager.Settings"
	settingsConnectionIface = "org.freedesktop.NetworkManager.Settings.Connection"
	activeConnectionIface   = "org.freedesktop.NetworkManager.Connection.Active"
	propertiesInterface     = "org.freedesktop.DBus.Properties"
	wifiDeviceType          = uint32(2)
	noSpecificObject        = dbus.ObjectPath("/")
)

// Client is a direct NetworkManager D-Bus client.
type Client struct {
	conn *dbus.Conn
}

// ConnectSystemBus connects to the host system bus. NetworkManager normally runs on the
// system bus rather than the per-user session bus.
func ConnectSystemBus() (*Client, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("connect to system D-Bus: %w", err)
	}
	return &Client{conn: conn}, nil
}

// Close releases the D-Bus connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// WaitForActivation waits for an active connection to reach NetworkManager's activated
// state. Checkpoint rollback is coordinated by the recovery package.
func (c *Client) WaitForActivation(
	ctx context.Context,
	activePath string,
	interfaceName string,
	wait time.Duration,
) error {
	if !dbus.ObjectPath(activePath).IsValid() || activePath == "/" {
		return fmt.Errorf("invalid active connection path %q", activePath)
	}
	if wait <= 0 {
		return nil
	}
	devicePath, err := c.devicePath(ctx, interfaceName)
	if err != nil {
		return err
	}

	deadline := time.NewTimer(wait)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()

	for {
		state, err := c.uint32Property(
			ctx,
			dbus.ObjectPath(activePath),
			activeConnectionIface,
			"State",
		)
		if err != nil {
			if isObjectGone(err) {
				return c.activationFailure(ctx, devicePath, interfaceName)
			}
			return err
		}
		switch state {
		case 2:
			return nil
		case 4:
			return c.activationFailure(ctx, devicePath, interfaceName)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("connection did not activate within %s", wait)
		case <-ticker.C:
		}
	}
}

func (c *Client) activationFailure(
	ctx context.Context,
	devicePath dbus.ObjectPath,
	interfaceName string,
) error {
	state, reason, err := c.deviceStateReason(ctx, devicePath)
	if err != nil {
		return errors.New("NetworkManager removed the active connection before it became ready")
	}
	return fmt.Errorf(
		"NetworkManager rejected the connection on %s: device state %s, reason %s (%d)",
		interfaceName,
		state,
		reason,
		reason,
	)
}

// Status retrieves the NetworkManager state needed by onboardd for one interface.
func (c *Client) Status(ctx context.Context, interfaceName string) (Status, error) {
	devicePath, err := c.devicePath(ctx, interfaceName)
	if err != nil {
		return Status{}, err
	}

	connectivityValue, err := c.uint32Property(ctx, managerPath, managerInterface, "Connectivity")
	if err != nil {
		return Status{}, err
	}
	device, err := c.device(ctx, devicePath)
	if err != nil {
		return Status{}, err
	}

	return Status{
		Connectivity: Connectivity(connectivityValue),
		Device:       device,
	}, nil
}

// CheckConnectivity asks NetworkManager to run its configured connectivity check now
// instead of relying on the most recently cached global result.
func (c *Client) CheckConnectivity(ctx context.Context) (Connectivity, error) {
	var value uint32
	if err := c.object(managerPath).CallWithContext(
		ctx,
		managerInterface+".CheckConnectivity",
		0,
	).Store(&value); err != nil {
		return ConnectivityUnknown, fmt.Errorf("request NetworkManager connectivity check: %w", err)
	}
	return Connectivity(value), nil
}

// Profiles lists all profiles visible to the D-Bus caller without requesting secrets.
func (c *Client) Profiles(ctx context.Context) ([]Profile, error) {
	var paths []dbus.ObjectPath
	if err := c.object(settingsPath).CallWithContext(
		ctx,
		settingsInterface+".ListConnections",
		0,
	).Store(&paths); err != nil {
		return nil, fmt.Errorf("list NetworkManager profiles: %w", err)
	}

	profiles := make([]Profile, 0, len(paths))
	for _, path := range paths {
		profile, err := c.profile(ctx, path)
		if err != nil {
			if isObjectGone(err) {
				continue
			}
			return nil, fmt.Errorf("read profile %s: %w", path, err)
		}
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].ID == profiles[j].ID {
			return profiles[i].UUID < profiles[j].UUID
		}
		return profiles[i].ID < profiles[j].ID
	})
	return profiles, nil
}

// DeleteOwnedProfile deletes a profile only after verifying onboardd ownership metadata.
// An already-absent UUID is success so interrupted cleanup remains idempotent.
func (c *Client) DeleteOwnedProfile(ctx context.Context, uuid string) error {
	if !validUUID(uuid) {
		return errors.New("UUID must use the canonical 8-4-4-4-12 format")
	}
	profiles, err := c.Profiles(ctx)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		if profile.UUID != strings.ToLower(uuid) {
			continue
		}
		if !profile.Owned {
			return fmt.Errorf("refusing to delete profile %s because it is not owned by onboardd", uuid)
		}
		return c.deleteProfile(ctx, profile)
	}
	return nil
}

// DeleteOwnedInfrastructureProfile deletes only an onboardd-owned Wi-Fi client
// profile bound to interfaceName. This narrower operation is the authorization
// boundary used by the product-facing Known networks API.
func (c *Client) DeleteOwnedInfrastructureProfile(
	ctx context.Context,
	interfaceName string,
	uuid string,
) error {
	if interfaceName == "" {
		return errors.New("interface name is required")
	}
	if !validUUID(uuid) {
		return errors.New("uuid must use the canonical 8-4-4-4-12 format")
	}
	normalizedUUID := strings.ToLower(uuid)
	status, err := c.Status(ctx, interfaceName)
	if err != nil {
		return err
	}
	if status.Device.ActiveUUID == normalizedUUID {
		return fmt.Errorf("refusing to delete active profile %s", normalizedUUID)
	}
	profiles, err := c.Profiles(ctx)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		if profile.UUID != normalizedUUID {
			continue
		}
		if !profile.Owned || profile.Role != RoleInfrastructure ||
			profile.Interface != interfaceName || !profile.IsInfrastructureWiFi() {
			return fmt.Errorf(
				"refusing to delete profile %s because it is not an onboardd-owned infrastructure profile on %s",
				uuid,
				interfaceName,
			)
		}
		return c.deleteProfile(ctx, profile)
	}
	return fmt.Errorf("profile %s was not found", uuid)
}

func (c *Client) deleteProfile(ctx context.Context, profile Profile) error {
	if err := c.object(dbus.ObjectPath(profile.Path)).CallWithContext(
		ctx,
		settingsConnectionIface+".Delete",
		0,
	).Store(); err != nil {
		return fmt.Errorf("delete onboardd profile %s: %w", profile.UUID, err)
	}
	return nil
}

// DeletePendingInfrastructureProfiles removes only uncommitted profiles created by an
// interrupted onboardd transition on the exact interface. Committed and unmanaged
// profiles are outside this recovery boundary.
func (c *Client) DeletePendingInfrastructureProfiles(
	ctx context.Context,
	interfaceName string,
) error {
	if interfaceName == "" {
		return errors.New("interface name is required")
	}
	profiles, err := c.Profiles(ctx)
	if err != nil {
		return fmt.Errorf("list profiles for pending candidate cleanup: %w", err)
	}
	for _, profile := range pendingProfiles(profiles, interfaceName) {
		if err := c.deleteProfile(ctx, profile); err != nil {
			if isObjectGone(err) {
				continue
			}
			return fmt.Errorf("delete pending infrastructure candidate: %w", err)
		}
	}
	return nil
}

func pendingProfiles(profiles []Profile, interfaceName string) []Profile {
	var pending []Profile
	for _, profile := range profiles {
		if profile.Owned && profile.Pending &&
			profile.Interface == interfaceName &&
			profile.Role == RoleInfrastructure {
			pending = append(pending, profile)
		}
	}
	return pending
}

// CreateCheckpoint protects the requested Wi-Fi device with automatic rollback.
func (c *Client) CreateCheckpoint(
	ctx context.Context,
	interfaceName string,
	rollbackAfter time.Duration,
) (string, error) {
	if rollbackAfter <= 0 || rollbackAfter%time.Second != 0 {
		return "", errors.New("checkpoint rollback duration must be a positive whole number of seconds")
	}
	seconds := rollbackAfter / time.Second
	if seconds > math.MaxUint32 {
		return "", errors.New("checkpoint rollback duration is too large")
	}
	devicePath, err := c.devicePath(ctx, interfaceName)
	if err != nil {
		return "", err
	}
	var checkpointPath dbus.ObjectPath
	if err := c.object(managerPath).CallWithContext(
		ctx,
		managerInterface+".CheckpointCreate",
		0,
		[]dbus.ObjectPath{devicePath},
		uint32(seconds),
		uint32(0),
	).Store(&checkpointPath); err != nil {
		return "", fmt.Errorf("create NetworkManager checkpoint: %w", err)
	}
	return string(checkpointPath), nil
}

// CommitCheckpoint accepts the current network configuration by destroying its rollback
// checkpoint.
func (c *Client) CommitCheckpoint(ctx context.Context, checkpointPath string) error {
	path, err := validateCheckpointPath(checkpointPath)
	if err != nil {
		return err
	}
	if err := c.object(managerPath).CallWithContext(
		ctx,
		managerInterface+".CheckpointDestroy",
		0,
		path,
	).Store(); err != nil {
		return fmt.Errorf("commit NetworkManager checkpoint: %w", err)
	}
	return nil
}

// RollbackCheckpoint immediately restores a checkpoint.
func (c *Client) RollbackCheckpoint(ctx context.Context, checkpointPath string) error {
	path, err := validateCheckpointPath(checkpointPath)
	if err != nil {
		return err
	}
	var raw map[string]uint32
	if err := c.object(managerPath).CallWithContext(
		ctx,
		managerInterface+".CheckpointRollback",
		0,
		path,
	).Store(&raw); err != nil {
		return fmt.Errorf("rollback NetworkManager checkpoint: %w", err)
	}
	return nil
}
