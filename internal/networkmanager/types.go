// Package networkmanager provides a narrow, product-focused adapter for NetworkManager's
// D-Bus API. D-Bus-specific types remain inside this package.
package networkmanager

import "fmt"

// Role identifies why onboardd created a NetworkManager profile.
type Role string

const (
	RoleInfrastructure Role = "infrastructure"
	RoleProvisioning   Role = "provisioning"
	RoleStandalone     Role = "standalone"
)

// Persistence controls how NetworkManager stores a newly created profile.
type Persistence uint32

const (
	PersistenceDisk   Persistence = 0x1
	PersistenceMemory Persistence = 0x2
)

// Connectivity is NetworkManager's view of upstream connectivity.
type Connectivity uint32

const (
	ConnectivityUnknown Connectivity = 0
	ConnectivityNone    Connectivity = 1
	ConnectivityPortal  Connectivity = 2
	ConnectivityLimited Connectivity = 3
	ConnectivityFull    Connectivity = 4
)

func (c Connectivity) String() string {
	switch c {
	case ConnectivityUnknown:
		return "unknown"
	case ConnectivityNone:
		return "none"
	case ConnectivityPortal:
		return "portal"
	case ConnectivityLimited:
		return "limited"
	case ConnectivityFull:
		return "full"
	default:
		return fmt.Sprintf("unknown(%d)", c)
	}
}

// State is NetworkManager's global state.
type State uint32

const (
	StateUnknown         State = 0
	StateAsleep          State = 10
	StateDisconnected    State = 20
	StateDisconnecting   State = 30
	StateConnecting      State = 40
	StateConnectedLocal  State = 50
	StateConnectedSite   State = 60
	StateConnectedGlobal State = 70
)

func (s State) String() string {
	switch s {
	case StateUnknown:
		return "unknown"
	case StateAsleep:
		return "asleep"
	case StateDisconnected:
		return "disconnected"
	case StateDisconnecting:
		return "disconnecting"
	case StateConnecting:
		return "connecting"
	case StateConnectedLocal:
		return "connected-local"
	case StateConnectedSite:
		return "connected-site"
	case StateConnectedGlobal:
		return "connected-global"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

// DeviceState is the lifecycle state of a NetworkManager device.
type DeviceState uint32

const (
	DeviceStateUnknown      DeviceState = 0
	DeviceStateUnmanaged    DeviceState = 10
	DeviceStateUnavailable  DeviceState = 20
	DeviceStateDisconnected DeviceState = 30
	DeviceStatePrepare      DeviceState = 40
	DeviceStateConfig       DeviceState = 50
	DeviceStateNeedAuth     DeviceState = 60
	DeviceStateIPConfig     DeviceState = 70
	DeviceStateIPCheck      DeviceState = 80
	DeviceStateSecondaries  DeviceState = 90
	DeviceStateActivated    DeviceState = 100
	DeviceStateDeactivating DeviceState = 110
	DeviceStateFailed       DeviceState = 120
)

func (s DeviceState) String() string {
	switch s {
	case DeviceStateUnknown:
		return "unknown"
	case DeviceStateUnmanaged:
		return "unmanaged"
	case DeviceStateUnavailable:
		return "unavailable"
	case DeviceStateDisconnected:
		return "disconnected"
	case DeviceStatePrepare:
		return "prepare"
	case DeviceStateConfig:
		return "config"
	case DeviceStateNeedAuth:
		return "need-auth"
	case DeviceStateIPConfig:
		return "ip-config"
	case DeviceStateIPCheck:
		return "ip-check"
	case DeviceStateSecondaries:
		return "secondaries"
	case DeviceStateActivated:
		return "activated"
	case DeviceStateDeactivating:
		return "deactivating"
	case DeviceStateFailed:
		return "failed"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

// DeviceStateReason explains why NetworkManager changed a device's state.
type DeviceStateReason uint32

const (
	DeviceStateReasonNone                     DeviceStateReason = 0
	DeviceStateReasonUnknown                  DeviceStateReason = 1
	DeviceStateReasonConfigFailed             DeviceStateReason = 4
	DeviceStateReasonIPConfigUnavailable      DeviceStateReason = 5
	DeviceStateReasonIPConfigExpired          DeviceStateReason = 6
	DeviceStateReasonNoSecrets                DeviceStateReason = 7
	DeviceStateReasonSupplicantDisconnect     DeviceStateReason = 8
	DeviceStateReasonSupplicantConfigFailed   DeviceStateReason = 9
	DeviceStateReasonSupplicantFailed         DeviceStateReason = 10
	DeviceStateReasonSupplicantTimeout        DeviceStateReason = 11
	DeviceStateReasonDHCPStartFailed          DeviceStateReason = 15
	DeviceStateReasonDHCPError                DeviceStateReason = 16
	DeviceStateReasonDHCPFailed               DeviceStateReason = 17
	DeviceStateReasonSharedStartFailed        DeviceStateReason = 18
	DeviceStateReasonSharedFailed             DeviceStateReason = 19
	DeviceStateReasonFirmwareMissing          DeviceStateReason = 35
	DeviceStateReasonRemoved                  DeviceStateReason = 36
	DeviceStateReasonSleeping                 DeviceStateReason = 37
	DeviceStateReasonConnectionRemoved        DeviceStateReason = 38
	DeviceStateReasonUserRequested            DeviceStateReason = 39
	DeviceStateReasonCarrier                  DeviceStateReason = 40
	DeviceStateReasonDependencyFailed         DeviceStateReason = 50
	DeviceStateReasonSSIDNotFound             DeviceStateReason = 53
	DeviceStateReasonNewActivation            DeviceStateReason = 60
	DeviceStateReasonIPAddressDuplicate       DeviceStateReason = 64
	DeviceStateReasonIPMethodUnsupported      DeviceStateReason = 65
	DeviceStateReasonDeviceHandlerFailed      DeviceStateReason = 68
	DeviceStateReasonUnmanagedByDefault       DeviceStateReason = 69
	DeviceStateReasonUnmanagedExternalDown    DeviceStateReason = 70
	DeviceStateReasonUnmanagedLinkNotInit     DeviceStateReason = 71
	DeviceStateReasonUnmanagedQuitting        DeviceStateReason = 72
	DeviceStateReasonUnmanagedManagerDisabled DeviceStateReason = 73
	DeviceStateReasonUnmanagedUserConfig      DeviceStateReason = 74
	DeviceStateReasonUnmanagedUserExplicit    DeviceStateReason = 75
	DeviceStateReasonUnmanagedUserSettings    DeviceStateReason = 76
	DeviceStateReasonUnmanagedUserUdev        DeviceStateReason = 77
	DeviceStateReasonNetworkingOff            DeviceStateReason = 78
)

func (r DeviceStateReason) String() string {
	names := map[DeviceStateReason]string{
		DeviceStateReasonNone:                     "none",
		DeviceStateReasonUnknown:                  "unknown",
		DeviceStateReasonConfigFailed:             "config-failed",
		DeviceStateReasonIPConfigUnavailable:      "ip-config-unavailable",
		DeviceStateReasonIPConfigExpired:          "ip-config-expired",
		DeviceStateReasonNoSecrets:                "no-secrets",
		DeviceStateReasonSupplicantDisconnect:     "supplicant-disconnect",
		DeviceStateReasonSupplicantConfigFailed:   "supplicant-config-failed",
		DeviceStateReasonSupplicantFailed:         "supplicant-failed",
		DeviceStateReasonSupplicantTimeout:        "supplicant-timeout",
		DeviceStateReasonDHCPStartFailed:          "dhcp-start-failed",
		DeviceStateReasonDHCPError:                "dhcp-error",
		DeviceStateReasonDHCPFailed:               "dhcp-failed",
		DeviceStateReasonSharedStartFailed:        "shared-start-failed",
		DeviceStateReasonSharedFailed:             "shared-failed",
		DeviceStateReasonFirmwareMissing:          "firmware-missing",
		DeviceStateReasonRemoved:                  "removed",
		DeviceStateReasonSleeping:                 "sleeping",
		DeviceStateReasonConnectionRemoved:        "connection-removed",
		DeviceStateReasonUserRequested:            "user-requested",
		DeviceStateReasonCarrier:                  "carrier",
		DeviceStateReasonDependencyFailed:         "dependency-failed",
		DeviceStateReasonSSIDNotFound:             "ssid-not-found",
		DeviceStateReasonNewActivation:            "new-activation",
		DeviceStateReasonIPAddressDuplicate:       "ip-address-duplicate",
		DeviceStateReasonIPMethodUnsupported:      "ip-method-unsupported",
		DeviceStateReasonDeviceHandlerFailed:      "device-handler-failed",
		DeviceStateReasonUnmanagedByDefault:       "unmanaged-by-default",
		DeviceStateReasonUnmanagedExternalDown:    "unmanaged-external-down",
		DeviceStateReasonUnmanagedLinkNotInit:     "unmanaged-link-not-init",
		DeviceStateReasonUnmanagedQuitting:        "unmanaged-quitting",
		DeviceStateReasonUnmanagedManagerDisabled: "unmanaged-manager-disabled",
		DeviceStateReasonUnmanagedUserConfig:      "unmanaged-user-config",
		DeviceStateReasonUnmanagedUserExplicit:    "unmanaged-user-explicit",
		DeviceStateReasonUnmanagedUserSettings:    "unmanaged-user-settings",
		DeviceStateReasonUnmanagedUserUdev:        "unmanaged-user-udev",
		DeviceStateReasonNetworkingOff:            "networking-off",
	}
	if name, ok := names[r]; ok {
		return name
	}
	return fmt.Sprintf("unknown(%d)", r)
}

// Status summarizes the NetworkManager daemon and requested interface.
type Status struct {
	Version                 string       `json:"version"`
	State                   State        `json:"state"`
	StateName               string       `json:"state_name"`
	Connectivity            Connectivity `json:"connectivity"`
	ConnectivityName        string       `json:"connectivity_name"`
	ConnectivityCheck       bool         `json:"connectivity_check_available"`
	WirelessEnabled         bool         `json:"wireless_enabled"`
	WirelessHardwareEnabled bool         `json:"wireless_hardware_enabled"`
	Startup                 bool         `json:"startup"`
	Device                  Device       `json:"device"`
}

// Device describes a NetworkManager device needed by onboardd.
type Device struct {
	Path             string      `json:"path"`
	Interface        string      `json:"interface"`
	Type             uint32      `json:"type"`
	Managed          bool        `json:"managed"`
	State            DeviceState `json:"state"`
	StateName        string      `json:"state_name"`
	ActiveConnection string      `json:"active_connection,omitempty"`
	ActiveUUID       string      `json:"active_uuid,omitempty"`
	IPv4Addresses    []string    `json:"ipv4_addresses,omitempty"`
}

// Profile is a safe summary of a saved or in-memory connection profile. Secrets are
// deliberately never returned.
type Profile struct {
	Path           string `json:"path"`
	ID             string `json:"id"`
	UUID           string `json:"uuid"`
	Type           string `json:"type"`
	Interface      string `json:"interface,omitempty"`
	SSID           string `json:"ssid,omitempty"`
	Mode           string `json:"mode,omitempty"`
	Autoconnect    bool   `json:"autoconnect"`
	Priority       int32  `json:"autoconnect_priority"`
	Owned          bool   `json:"owned_by_onboardd"`
	Role           Role   `json:"role,omitempty"`
	MetadataSchema string `json:"metadata_schema,omitempty"`
	Pending        bool   `json:"pending"`
	Persistence    string `json:"persistence"`
	Filename       string `json:"filename,omitempty"`
	Unsaved        bool   `json:"unsaved"`
	Flags          uint32 `json:"flags"`
}

// IsInfrastructureWiFi reports whether the profile describes a Wi-Fi client
// connection rather than an access point or another NetworkManager connection type.
func (profile Profile) IsInfrastructureWiFi() bool {
	return profile.Type == "802-11-wireless" &&
		(profile.Mode == "" || profile.Mode == "infrastructure")
}

// AppliesTo reports whether NetworkManager may use the profile on the requested
// interface. An empty interface binding means the profile is not device-specific.
func (profile Profile) AppliesTo(interfaceName string) bool {
	return profile.Interface == "" || profile.Interface == interfaceName
}

// Security is a user-facing summary of access-point security capabilities.
type Security string

const (
	SecurityOpen Security = "open"
	SecurityWEP  Security = "wep"
	SecurityWPA  Security = "wpa"
	SecurityWPA2 Security = "wpa2-or-wpa3"
)

// AccessPoint is one visible Wi-Fi access point.
type AccessPoint struct {
	Path      string   `json:"path"`
	SSID      string   `json:"ssid"`
	Hidden    bool     `json:"hidden"`
	BSSID     string   `json:"bssid"`
	Strength  uint8    `json:"strength"`
	Frequency uint32   `json:"frequency_mhz"`
	Security  Security `json:"security"`
}

// Activation identifies a newly added profile and its active connection.
type Activation struct {
	ProfilePath string `json:"profile_path"`
	ActivePath  string `json:"active_connection_path"`
	UUID        string `json:"uuid"`
	Role        Role   `json:"role"`
	Persistence string `json:"persistence"`
}

// Checkpoint identifies a NetworkManager checkpoint and its automatic rollback window.
type Checkpoint struct {
	Path            string `json:"path"`
	Interface       string `json:"interface"`
	RollbackSeconds uint32 `json:"rollback_seconds"`
}

// RollbackResult reports NetworkManager's per-device rollback result codes.
type RollbackResult struct {
	Checkpoint string            `json:"checkpoint"`
	Devices    map[string]uint32 `json:"devices"`
}

// Event is a D-Bus signal translated into a small diagnostic representation.
type Event struct {
	Path        string         `json:"path"`
	Interface   string         `json:"interface"`
	Changed     map[string]any `json:"changed"`
	Invalidated []string       `json:"invalidated,omitempty"`
}
