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
// state. It does not implement rollback; that belongs to the later recovery phase.
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

// Status retrieves global NetworkManager status and the requested interface.
func (c *Client) Status(ctx context.Context, interfaceName string) (Status, error) {
	devicePath, err := c.devicePath(ctx, interfaceName)
	if err != nil {
		return Status{}, err
	}

	version, err := c.stringProperty(ctx, managerPath, managerInterface, "Version")
	if err != nil {
		return Status{}, err
	}
	stateValue, err := c.uint32Property(ctx, managerPath, managerInterface, "State")
	if err != nil {
		return Status{}, err
	}
	connectivityValue, err := c.uint32Property(ctx, managerPath, managerInterface, "Connectivity")
	if err != nil {
		return Status{}, err
	}
	wirelessEnabled, err := c.boolProperty(ctx, managerPath, managerInterface, "WirelessEnabled")
	if err != nil {
		return Status{}, err
	}
	hardwareEnabled, err := c.boolProperty(ctx, managerPath, managerInterface, "WirelessHardwareEnabled")
	if err != nil {
		return Status{}, err
	}
	startup, err := c.boolProperty(ctx, managerPath, managerInterface, "Startup")
	if err != nil {
		return Status{}, err
	}
	checkAvailable, err := c.boolProperty(ctx, managerPath, managerInterface, "ConnectivityCheckAvailable")
	if err != nil {
		return Status{}, err
	}
	device, err := c.device(ctx, devicePath)
	if err != nil {
		return Status{}, err
	}

	state := State(stateValue)
	connectivity := Connectivity(connectivityValue)
	return Status{
		Version:                 version,
		State:                   state,
		StateName:               state.String(),
		Connectivity:            connectivity,
		ConnectivityName:        connectivity.String(),
		ConnectivityCheck:       checkAvailable,
		WirelessEnabled:         wirelessEnabled,
		WirelessHardwareEnabled: hardwareEnabled,
		Startup:                 startup,
		Device:                  device,
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
		if err := c.object(dbus.ObjectPath(profile.Path)).CallWithContext(
			ctx,
			settingsConnectionIface+".Delete",
			0,
		).Store(); err != nil {
			return fmt.Errorf("delete onboardd profile %s: %w", uuid, err)
		}
		return nil
	}
	return fmt.Errorf("profile %s was not found", uuid)
}

// CreateCheckpoint protects the requested Wi-Fi device with automatic rollback.
func (c *Client) CreateCheckpoint(
	ctx context.Context,
	interfaceName string,
	rollbackAfter time.Duration,
) (Checkpoint, error) {
	if rollbackAfter <= 0 || rollbackAfter%time.Second != 0 {
		return Checkpoint{}, errors.New("checkpoint rollback duration must be a positive whole number of seconds")
	}
	seconds := rollbackAfter / time.Second
	if seconds > math.MaxUint32 {
		return Checkpoint{}, errors.New("checkpoint rollback duration is too large")
	}
	devicePath, err := c.devicePath(ctx, interfaceName)
	if err != nil {
		return Checkpoint{}, err
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
		return Checkpoint{}, fmt.Errorf("create NetworkManager checkpoint: %w", err)
	}
	return Checkpoint{
		Path:            string(checkpointPath),
		Interface:       interfaceName,
		RollbackSeconds: uint32(seconds),
	}, nil
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
func (c *Client) RollbackCheckpoint(
	ctx context.Context,
	checkpointPath string,
) (RollbackResult, error) {
	path, err := validateCheckpointPath(checkpointPath)
	if err != nil {
		return RollbackResult{}, err
	}
	var raw map[string]uint32
	if err := c.object(managerPath).CallWithContext(
		ctx,
		managerInterface+".CheckpointRollback",
		0,
		path,
	).Store(&raw); err != nil {
		return RollbackResult{}, fmt.Errorf("rollback NetworkManager checkpoint: %w", err)
	}
	return RollbackResult{Checkpoint: checkpointPath, Devices: raw}, nil
}

// Scan requests a Wi-Fi scan, waits up to wait for LastScan to change, and returns the
// latest access-point list. A zero wait requests the scan and immediately returns the
// current list.
func (c *Client) Scan(ctx context.Context, interfaceName string, wait time.Duration) ([]AccessPoint, error) {
	devicePath, err := c.devicePath(ctx, interfaceName)
	if err != nil {
		return nil, err
	}
	before, err := c.int64Property(ctx, devicePath, wirelessDeviceInterface, "LastScan")
	if err != nil {
		return nil, err
	}

	if err := c.object(devicePath).CallWithContext(
		ctx,
		wirelessDeviceInterface+".RequestScan",
		0,
		map[string]dbus.Variant{},
	).Store(); err != nil {
		return nil, fmt.Errorf("request Wi-Fi scan on %s: %w", interfaceName, err)
	}

	if wait > 0 {
		deadline := time.NewTimer(wait)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer deadline.Stop()
		defer ticker.Stop()

	waitLoop:
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-deadline.C:
				break waitLoop
			case <-ticker.C:
				last, propertyErr := c.int64Property(ctx, devicePath, wirelessDeviceInterface, "LastScan")
				if propertyErr != nil {
					return nil, propertyErr
				}
				if last > before {
					break waitLoop
				}
			}
		}
	}

	var paths []dbus.ObjectPath
	if err := c.object(devicePath).CallWithContext(
		ctx,
		wirelessDeviceInterface+".GetAllAccessPoints",
		0,
	).Store(&paths); err != nil {
		return nil, fmt.Errorf("list Wi-Fi access points on %s: %w", interfaceName, err)
	}

	accessPoints := make([]AccessPoint, 0, len(paths))
	for _, path := range paths {
		accessPoint, readErr := c.accessPoint(ctx, path)
		if readErr != nil {
			return nil, fmt.Errorf("read access point %s: %w", path, readErr)
		}
		accessPoints = append(accessPoints, accessPoint)
	}
	sort.Slice(accessPoints, func(i, j int) bool {
		if accessPoints[i].Strength == accessPoints[j].Strength {
			return accessPoints[i].SSID < accessPoints[j].SSID
		}
		return accessPoints[i].Strength > accessPoints[j].Strength
	})
	return accessPoints, nil
}

// ConnectInfrastructure creates a persistent client profile and activates it.
func (c *Client) ConnectInfrastructure(
	ctx context.Context,
	options InfrastructureOptions,
) (Activation, error) {
	settings, uuid, err := BuildInfrastructureSettings(options)
	if err != nil {
		return Activation{}, err
	}
	return c.addAndActivate(
		ctx,
		options.Interface,
		settings,
		PersistenceDisk,
		uuid,
		RoleInfrastructure,
	)
}

// StartAccessPoint creates and activates a provisioning or standalone AP profile.
func (c *Client) StartAccessPoint(
	ctx context.Context,
	options AccessPointOptions,
) (Activation, error) {
	settings, uuid, err := BuildAccessPointSettings(options)
	if err != nil {
		return Activation{}, err
	}
	persistence := PersistenceMemory
	if options.Role == RoleStandalone {
		persistence = PersistenceDisk
	}
	return c.addAndActivate(ctx, options.Interface, settings, persistence, uuid, options.Role)
}

// FinalizeTransition updates durable mode intent and removes superseded profiles only
// after the caller has confirmed that activation succeeded.
func (c *Client) FinalizeTransition(
	ctx context.Context,
	interfaceName string,
	role Role,
	ssid string,
	keepUUID string,
) error {
	if role != RoleInfrastructure && role != RoleStandalone && role != RoleProvisioning {
		return fmt.Errorf("cannot finalize unknown network role %q", role)
	}
	scope := profileScope{interfaceName: interfaceName, role: role}
	if role == RoleInfrastructure {
		scope.ssid = ssid
	}
	if role == RoleProvisioning {
		if err := c.deleteSupersededProfiles(ctx, scope, keepUUID); err != nil {
			return fmt.Errorf("remove superseded %s profiles: %w", role, err)
		}
		return nil
	}
	// Record the new durable intent before deleting the old profile. If either
	// operation fails, at least one usable production profile remains eligible
	// for autoconnect on the next boot.
	if err := c.selectMode(ctx, interfaceName, role); err != nil {
		return err
	}
	if err := c.deleteSupersededProfiles(ctx, scope, keepUUID); err != nil {
		return fmt.Errorf("remove superseded %s profiles: %w", role, err)
	}
	return nil
}

func (c *Client) selectMode(ctx context.Context, interfaceName string, role Role) error {
	profiles, err := c.Profiles(ctx)
	if err != nil {
		return err
	}
	updates := autoconnectUpdates(profiles, interfaceName, role)
	for _, update := range updates {
		if err := c.updateAutoconnect(ctx, update.Profile, update.Enabled); err != nil {
			if isObjectGone(err) {
				continue
			}
			return err
		}
	}
	return nil
}

type autoconnectUpdate struct {
	Profile Profile
	Enabled bool
}

func autoconnectUpdates(profiles []Profile, interfaceName string, selected Role) []autoconnectUpdate {
	var updates []autoconnectUpdate
	for _, profile := range profiles {
		if !profile.Owned || profile.Interface != interfaceName {
			continue
		}
		if profile.Role != RoleInfrastructure && profile.Role != RoleStandalone {
			continue
		}
		enabled := profile.Role == selected
		if profile.Autoconnect != enabled {
			updates = append(updates, autoconnectUpdate{Profile: profile, Enabled: enabled})
		}
	}
	// Enable the selected mode first. A failure can then leave both modes
	// eligible, but never leaves a device with every production mode disabled.
	sort.SliceStable(updates, func(left, right int) bool {
		return updates[left].Enabled && !updates[right].Enabled
	})
	return updates
}

func (c *Client) updateAutoconnect(ctx context.Context, profile Profile, enabled bool) error {
	path := dbus.ObjectPath(profile.Path)
	current, err := c.connectionSettings(ctx, path)
	if err != nil {
		return fmt.Errorf("read profile %s for autoconnect update: %w", profile.UUID, err)
	}
	secrets := Settings{}
	if _, secured := current["802-11-wireless-security"]; secured {
		secrets, err = c.connectionSecrets(ctx, path, "802-11-wireless-security")
		if err != nil {
			return fmt.Errorf("read profile %s secrets for autoconnect update: %w", profile.UUID, err)
		}
	}
	settings, err := rebuildOwnedSettings(profile, current, secrets, enabled)
	if err != nil {
		return fmt.Errorf("rebuild profile %s for autoconnect update: %w", profile.UUID, err)
	}
	persistence := PersistenceDisk
	if profile.Persistence == "memory" {
		persistence = PersistenceMemory
	}
	var result map[string]dbus.Variant
	if err := c.object(path).CallWithContext(
		ctx,
		settingsConnectionIface+".Update2",
		0,
		settings,
		uint32(persistence),
		map[string]dbus.Variant{},
	).Store(&result); err != nil {
		return fmt.Errorf("set autoconnect=%t on profile %s: %w", enabled, profile.UUID, err)
	}
	return nil
}

// deleteSupersededProfiles makes creating an onboardd profile idempotent.
// It deliberately considers only profiles carrying onboardd ownership metadata, and
// never deletes the newly-created profile.
func (c *Client) deleteSupersededProfiles(
	ctx context.Context,
	scope profileScope,
	keepUUID string,
) error {
	profiles, err := c.Profiles(ctx)
	if err != nil {
		return err
	}
	for _, profile := range supersededProfiles(profiles, scope, keepUUID) {
		if err := c.object(dbus.ObjectPath(profile.Path)).CallWithContext(
			ctx,
			settingsConnectionIface+".Delete",
			0,
		).Store(); err != nil {
			if isObjectGone(err) {
				continue
			}
			return fmt.Errorf("delete superseded profile %s: %w", profile.UUID, err)
		}
	}
	return nil
}

type profileScope struct {
	interfaceName string
	role          Role
	ssid          string
}

func (scope profileScope) matches(profile Profile) bool {
	if profile.Interface != scope.interfaceName || profile.Role != scope.role {
		return false
	}
	if scope.role == RoleInfrastructure {
		return profile.SSID == scope.ssid
	}
	return true
}

func supersededProfiles(profiles []Profile, scope profileScope, keepUUID string) []Profile {
	var superseded []Profile
	for _, profile := range profiles {
		if profile.Owned && profile.UUID != keepUUID && scope.matches(profile) {
			superseded = append(superseded, profile)
		}
	}
	return superseded
}

func (c *Client) addAndActivate(
	ctx context.Context,
	interfaceName string,
	settings Settings,
	persistence Persistence,
	uuid string,
	role Role,
) (Activation, error) {
	devicePath, err := c.devicePath(ctx, interfaceName)
	if err != nil {
		return Activation{}, err
	}

	var profilePath dbus.ObjectPath
	var result map[string]dbus.Variant
	if err := c.object(settingsPath).CallWithContext(
		ctx,
		settingsInterface+".AddConnection2",
		0,
		settings,
		uint32(persistence),
		map[string]dbus.Variant{},
	).Store(&profilePath, &result); err != nil {
		return Activation{}, fmt.Errorf("add %s profile: %w", role, err)
	}

	var activePath dbus.ObjectPath
	if err := c.object(managerPath).CallWithContext(
		ctx,
		managerInterface+".ActivateConnection",
		0,
		profilePath,
		devicePath,
		noSpecificObject,
	).Store(&activePath); err != nil {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelCleanup()
		deleteErr := c.object(profilePath).CallWithContext(
			cleanupCtx,
			settingsConnectionIface+".Delete",
			0,
		).Store()
		if deleteErr != nil {
			return Activation{}, fmt.Errorf(
				"activate %s profile: %w (cleanup also failed: %v)",
				role,
				err,
				deleteErr,
			)
		}
		return Activation{}, fmt.Errorf("activate %s profile: %w", role, err)
	}

	persistenceName := "memory"
	if persistence == PersistenceDisk {
		persistenceName = "disk"
	}
	return Activation{
		ProfilePath: string(profilePath),
		ActivePath:  string(activePath),
		UUID:        uuid,
		Role:        role,
		Persistence: persistenceName,
	}, nil
}

func (c *Client) device(ctx context.Context, path dbus.ObjectPath) (Device, error) {
	interfaceName, err := c.stringProperty(ctx, path, deviceInterface, "Interface")
	if err != nil {
		return Device{}, err
	}
	deviceType, err := c.uint32Property(ctx, path, deviceInterface, "DeviceType")
	if err != nil {
		return Device{}, err
	}
	managed, err := c.boolProperty(ctx, path, deviceInterface, "Managed")
	if err != nil {
		return Device{}, err
	}
	stateValue, err := c.uint32Property(ctx, path, deviceInterface, "State")
	if err != nil {
		return Device{}, err
	}
	active, err := c.objectPathProperty(ctx, path, deviceInterface, "ActiveConnection")
	if err != nil {
		return Device{}, err
	}
	activeUUID := ""
	if active != noSpecificObject {
		activeUUID, err = c.stringProperty(ctx, active, activeConnectionIface, "Uuid")
		if err != nil {
			return Device{}, err
		}
	}
	ipv4Addresses, err := c.deviceIPv4Addresses(ctx, path)
	if err != nil {
		return Device{}, err
	}
	state := DeviceState(stateValue)
	return Device{
		Path:             string(path),
		Interface:        interfaceName,
		Type:             deviceType,
		Managed:          managed,
		State:            state,
		StateName:        state.String(),
		ActiveConnection: cleanRootPath(active),
		ActiveUUID:       activeUUID,
		IPv4Addresses:    ipv4Addresses,
	}, nil
}

func (c *Client) deviceIPv4Addresses(ctx context.Context, devicePath dbus.ObjectPath) ([]string, error) {
	configPath, err := c.objectPathProperty(ctx, devicePath, deviceInterface, "Ip4Config")
	if err != nil {
		return nil, err
	}
	if configPath == noSpecificObject {
		return nil, nil
	}
	value, err := c.property(
		ctx,
		configPath,
		"org.freedesktop.NetworkManager.IP4Config",
		"AddressData",
	)
	if err != nil {
		return nil, err
	}
	addressData, ok := value.Value().([]map[string]dbus.Variant)
	if !ok {
		return nil, propertyTypeError(
			"org.freedesktop.NetworkManager.IP4Config",
			"AddressData",
			"array of string/variant maps",
			value.Value(),
		)
	}
	addresses := make([]string, 0, len(addressData))
	for _, item := range addressData {
		if address := variantString(item["address"]); address != "" {
			addresses = append(addresses, address)
		}
	}
	return addresses, nil
}

func (c *Client) devicePath(ctx context.Context, interfaceName string) (dbus.ObjectPath, error) {
	if interfaceName == "" {
		return "", errors.New("interface name is required")
	}
	var path dbus.ObjectPath
	if err := c.object(managerPath).CallWithContext(
		ctx,
		managerInterface+".GetDeviceByIpIface",
		0,
		interfaceName,
	).Store(&path); err != nil {
		return "", fmt.Errorf("find NetworkManager interface %q: %w", interfaceName, err)
	}
	deviceType, err := c.uint32Property(ctx, path, deviceInterface, "DeviceType")
	if err != nil {
		return "", err
	}
	if deviceType != wifiDeviceType {
		return "", fmt.Errorf("interface %q is not a Wi-Fi device (type %d)", interfaceName, deviceType)
	}
	return path, nil
}

func (c *Client) profile(ctx context.Context, path dbus.ObjectPath) (Profile, error) {
	settings, err := c.connectionSettings(ctx, path)
	if err != nil {
		return Profile{}, err
	}
	unsaved, err := c.boolProperty(ctx, path, settingsConnectionIface, "Unsaved")
	if err != nil {
		return Profile{}, err
	}
	filename, err := c.stringProperty(ctx, path, settingsConnectionIface, "Filename")
	if err != nil {
		return Profile{}, err
	}
	flags, err := c.uint32Property(ctx, path, settingsConnectionIface, "Flags")
	if err != nil {
		return Profile{}, err
	}

	connection := settings["connection"]
	user := settings["user"]
	wireless := settings["802-11-wireless"]
	metadata := variantMapStringString(user["data"])
	ssidBytes := variantBytes(wireless["ssid"])
	owner := metadata[ownerKey] == ownerName
	return Profile{
		Path:           string(path),
		ID:             variantString(connection["id"]),
		UUID:           variantString(connection["uuid"]),
		Type:           variantString(connection["type"]),
		Interface:      variantString(connection["interface-name"]),
		SSID:           string(ssidBytes),
		Mode:           variantString(wireless["mode"]),
		Autoconnect:    variantBoolDefault(connection["autoconnect"], true),
		Priority:       variantInt32(connection["autoconnect-priority"]),
		Owned:          owner,
		Role:           Role(metadata[roleKey]),
		MetadataSchema: metadata[schemaKey],
		Persistence:    profilePersistence(filename),
		Filename:       filename,
		Unsaved:        unsaved,
		Flags:          flags,
	}, nil
}

// connectionSettings deliberately reads the raw call body instead of Call.Store.
// Store recursively reconstructs nested variants from their Go representation, which
// can lose the original signature of legacy tuple-valued NetworkManager settings.
func (c *Client) connectionSettings(ctx context.Context, path dbus.ObjectPath) (Settings, error) {
	call := c.object(path).CallWithContext(
		ctx,
		settingsConnectionIface+".GetSettings",
		0,
	)
	if call.Err != nil {
		return nil, call.Err
	}
	return settingsFromCallBody(call.Body)
}

func settingsFromCallBody(body []any) (Settings, error) {
	if len(body) != 1 {
		return nil, fmt.Errorf("GetSettings returned %d fields, expected one", len(body))
	}
	settings, ok := body[0].(map[string]map[string]dbus.Variant)
	if !ok {
		return nil, fmt.Errorf("GetSettings returned %T, expected a{sa{sv}}", body[0])
	}
	return Settings(settings), nil
}

func (c *Client) connectionSecrets(
	ctx context.Context,
	path dbus.ObjectPath,
	settingName string,
) (Settings, error) {
	call := c.object(path).CallWithContext(
		ctx,
		settingsConnectionIface+".GetSecrets",
		0,
		settingName,
	)
	if call.Err != nil {
		return nil, call.Err
	}
	return settingsFromCallBody(call.Body)
}

func isObjectGone(err error) bool {
	if err == nil {
		return false
	}
	var pointerError *dbus.Error
	if errors.As(err, &pointerError) {
		return objectGoneErrorName(pointerError.Name)
	}
	var valueError dbus.Error
	if errors.As(err, &valueError) {
		return objectGoneErrorName(valueError.Name)
	}
	return false
}

func objectGoneErrorName(name string) bool {
	switch name {
	case "org.freedesktop.DBus.Error.UnknownObject",
		"org.freedesktop.DBus.Error.UnknownInterface",
		"org.freedesktop.DBus.Error.UnknownMethod",
		"org.freedesktop.NetworkManager.UnknownConnection":
		return true
	default:
		return false
	}
}

func profilePersistence(filename string) string {
	cleaned := strings.TrimSpace(filename)
	if cleaned == "" ||
		strings.HasPrefix(cleaned, "/run/") ||
		strings.HasPrefix(cleaned, "/var/run/") {
		return "memory"
	}
	return "disk"
}

func (c *Client) accessPoint(ctx context.Context, path dbus.ObjectPath) (AccessPoint, error) {
	ssid, err := c.bytesProperty(ctx, path, accessPointInterface, "Ssid")
	if err != nil {
		return AccessPoint{}, err
	}
	bssid, err := c.stringProperty(ctx, path, accessPointInterface, "HwAddress")
	if err != nil {
		return AccessPoint{}, err
	}
	strength, err := c.byteProperty(ctx, path, accessPointInterface, "Strength")
	if err != nil {
		return AccessPoint{}, err
	}
	frequency, err := c.uint32Property(ctx, path, accessPointInterface, "Frequency")
	if err != nil {
		return AccessPoint{}, err
	}
	flags, err := c.uint32Property(ctx, path, accessPointInterface, "Flags")
	if err != nil {
		return AccessPoint{}, err
	}
	wpaFlags, err := c.uint32Property(ctx, path, accessPointInterface, "WpaFlags")
	if err != nil {
		return AccessPoint{}, err
	}
	rsnFlags, err := c.uint32Property(ctx, path, accessPointInterface, "RsnFlags")
	if err != nil {
		return AccessPoint{}, err
	}
	return AccessPoint{
		Path:      string(path),
		SSID:      string(ssid),
		Hidden:    len(ssid) == 0,
		BSSID:     bssid,
		Strength:  strength,
		Frequency: frequency,
		Security:  accessPointSecurity(flags, wpaFlags, rsnFlags),
	}, nil
}

func accessPointSecurity(flags, wpaFlags, rsnFlags uint32) Security {
	switch {
	case rsnFlags != 0:
		return SecurityWPA2
	case wpaFlags != 0:
		return SecurityWPA
	case flags&0x1 != 0:
		return SecurityWEP
	default:
		return SecurityOpen
	}
}

func (c *Client) object(path dbus.ObjectPath) dbus.BusObject {
	return c.conn.Object(service, path)
}

func cleanRootPath(path dbus.ObjectPath) string {
	if path == noSpecificObject {
		return ""
	}
	return string(path)
}

func validateCheckpointPath(value string) (dbus.ObjectPath, error) {
	path := dbus.ObjectPath(value)
	if !path.IsValid() || !strings.HasPrefix(value, string(managerPath)+"/Checkpoint/") {
		return "", fmt.Errorf("invalid NetworkManager checkpoint path %q", value)
	}
	return path, nil
}
