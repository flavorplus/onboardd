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

// persistence controls how NetworkManager stores a newly created profile.
type persistence uint32

const (
	persistenceDisk   persistence = 0x1
	persistenceMemory persistence = 0x2
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
	DeviceStateReasonNone                   DeviceStateReason = 0
	DeviceStateReasonUnknown                DeviceStateReason = 1
	DeviceStateReasonConfigFailed           DeviceStateReason = 4
	DeviceStateReasonIPConfigUnavailable    DeviceStateReason = 5
	DeviceStateReasonNoSecrets              DeviceStateReason = 7
	DeviceStateReasonSupplicantConfigFailed DeviceStateReason = 9
	DeviceStateReasonSupplicantFailed       DeviceStateReason = 10
	DeviceStateReasonSupplicantTimeout      DeviceStateReason = 11
	DeviceStateReasonDHCPFailed             DeviceStateReason = 17
	DeviceStateReasonSharedStartFailed      DeviceStateReason = 18
	DeviceStateReasonSharedFailed           DeviceStateReason = 19
	DeviceStateReasonSSIDNotFound           DeviceStateReason = 53
)

func (r DeviceStateReason) String() string {
	switch r {
	case DeviceStateReasonNone:
		return "none"
	case DeviceStateReasonUnknown:
		return "unknown"
	case DeviceStateReasonConfigFailed:
		return "config-failed"
	case DeviceStateReasonIPConfigUnavailable:
		return "ip-config-unavailable"
	case DeviceStateReasonNoSecrets:
		return "no-secrets"
	case DeviceStateReasonSupplicantConfigFailed:
		return "supplicant-config-failed"
	case DeviceStateReasonSupplicantFailed:
		return "supplicant-failed"
	case DeviceStateReasonSupplicantTimeout:
		return "supplicant-timeout"
	case DeviceStateReasonDHCPFailed:
		return "dhcp-failed"
	case DeviceStateReasonSharedStartFailed:
		return "shared-start-failed"
	case DeviceStateReasonSharedFailed:
		return "shared-failed"
	case DeviceStateReasonSSIDNotFound:
		return "ssid-not-found"
	default:
		return fmt.Sprintf("reason-%d", r)
	}
}

// Status contains only the NetworkManager state used by onboardd decisions.
type Status struct {
	Connectivity Connectivity
	Device       Device
}

// Device describes a NetworkManager device needed by onboardd.
type Device struct {
	Managed          bool
	State            DeviceState
	ActiveConnection string
	ActiveUUID       string
	IPv4Addresses    []string
}

// Profile is a safe summary of a saved or in-memory connection profile. Secrets are
// deliberately never returned.
type Profile struct {
	Path        string `json:"path"`
	ID          string `json:"id"`
	UUID        string `json:"uuid"`
	Type        string `json:"type"`
	Interface   string `json:"interface,omitempty"`
	SSID        string `json:"ssid,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Autoconnect bool   `json:"autoconnect"`
	Priority    int32  `json:"autoconnect_priority"`
	Owned       bool   `json:"owned_by_onboardd"`
	Role        Role   `json:"role,omitempty"`
	Pending     bool   `json:"pending"`
	inMemory    bool
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
	SSID     string
	Strength uint8
	Security Security
}

// Activation identifies a newly added profile and its active connection.
type Activation struct {
	ActivePath string
	UUID       string
}

// Event is a D-Bus property change reduced to the fields used for reconciliation.
type Event struct {
	Path        string         `json:"path"`
	Interface   string         `json:"interface"`
	Changed     map[string]any `json:"changed"`
	Invalidated []string       `json:"invalidated,omitempty"`
}
