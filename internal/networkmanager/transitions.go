package networkmanager

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

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
		persistenceDisk,
		uuid,
		RoleInfrastructure,
	)
}

// ActivateProfile explicitly activates an existing profile by UUID on the requested
// interface. It does not modify the profile or require onboardd ownership; recovery may
// need to restore the exact connection that was active before a transition.
func (c *Client) ActivateProfile(
	ctx context.Context,
	interfaceName string,
	uuid string,
) (Activation, error) {
	if !validUUID(uuid) {
		return Activation{}, errors.New("UUID must use the canonical 8-4-4-4-12 format")
	}
	devicePath, err := c.devicePath(ctx, interfaceName)
	if err != nil {
		return Activation{}, err
	}
	var profilePath dbus.ObjectPath
	if err := c.object(settingsPath).CallWithContext(
		ctx,
		settingsInterface+".GetConnectionByUuid",
		0,
		strings.ToLower(uuid),
	).Store(&profilePath); err != nil {
		return Activation{}, fmt.Errorf("find profile %s for activation: %w", uuid, err)
	}
	if !profilePath.IsValid() || profilePath == noSpecificObject {
		return Activation{}, fmt.Errorf("NetworkManager returned invalid path %q for profile %s", profilePath, uuid)
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
		return Activation{}, fmt.Errorf("activate existing profile %s: %w", uuid, err)
	}
	return Activation{
		ActivePath: string(activePath),
		UUID:       strings.ToLower(uuid),
	}, nil
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
	persistence := persistenceMemory
	if options.Role == RoleStandalone {
		persistence = persistenceDisk
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
	persistence := persistenceDisk
	if profile.inMemory {
		persistence = persistenceMemory
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
	persistence persistence,
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
	partialActivation := Activation{
		UUID: uuid,
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
			return partialActivation, fmt.Errorf(
				"activate %s profile: %w (cleanup also failed: %v)",
				role,
				err,
				deleteErr,
			)
		}
		return Activation{}, fmt.Errorf("activate %s profile: %w", role, err)
	}

	return Activation{
		ActivePath: string(activePath),
		UUID:       uuid,
	}, nil
}
