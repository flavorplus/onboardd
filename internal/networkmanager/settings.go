package networkmanager

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/godbus/dbus/v5"
)

const (
	ownerKey   = "onboardd.owner"
	roleKey    = "onboardd.role"
	schemaKey  = "onboardd.schema"
	pendingKey = "onboardd.pending"
	ownerName  = "onboardd"
)

// Settings is NetworkManager's a{sa{sv}} connection representation.
type Settings map[string]map[string]dbus.Variant

// InfrastructureOptions configures a WPA Personal or explicitly open client profile.
type InfrastructureOptions struct {
	ID          string
	UUID        string
	Interface   string
	SSID        string
	Password    string
	Open        bool
	Hidden      bool
	Autoconnect bool
	Priority    int32
	Pending     bool
}

// AccessPointOptions configures a managed provisioning or standalone access point.
type AccessPointOptions struct {
	ID          string
	UUID        string
	Interface   string
	SSID        string
	Password    string
	Address     string
	Role        Role
	Autoconnect bool
	Priority    int32
	Band        string
}

// BuildInfrastructureSettings returns settings for an infrastructure Wi-Fi profile.
func BuildInfrastructureSettings(options InfrastructureOptions) (Settings, string, error) {
	if options.Interface == "" {
		return nil, "", errors.New("interface is required")
	}
	if err := validateSSID(options.SSID); err != nil {
		return nil, "", err
	}
	if options.Open && options.Password != "" {
		return nil, "", errors.New("open network cannot include a password")
	}
	if !options.Open {
		if err := validatePSK(options.Password); err != nil {
			return nil, "", err
		}
	}
	if options.Priority < -999 || options.Priority > 999 {
		return nil, "", errors.New("autoconnect priority must be between -999 and 999")
	}

	uuid, err := resolveUUID(options.UUID)
	if err != nil {
		return nil, "", err
	}
	id := options.ID
	if id == "" {
		id = "onboardd Wi-Fi " + options.SSID
	}

	settings := Settings{
		"connection": variants(map[string]any{
			"id":                   id,
			"uuid":                 uuid,
			"type":                 "802-11-wireless",
			"interface-name":       options.Interface,
			"autoconnect":          options.Autoconnect,
			"autoconnect-priority": options.Priority,
		}),
		"802-11-wireless": variants(map[string]any{
			"mode":   "infrastructure",
			"ssid":   []byte(options.SSID),
			"hidden": options.Hidden,
		}),
		"ipv4": variants(map[string]any{
			"method": "auto",
		}),
		"ipv6": variants(map[string]any{
			"method": "auto",
		}),
		"user": metadata(RoleInfrastructure, options.Pending),
	}

	if !options.Open {
		settings["802-11-wireless-security"] = variants(map[string]any{
			"key-mgmt": "wpa-psk",
			"psk":      options.Password,
		})
	}

	return settings, uuid, nil
}

// BuildAccessPointSettings returns settings for provisioning or standalone AP mode.
func BuildAccessPointSettings(options AccessPointOptions) (Settings, string, error) {
	if options.Role != RoleProvisioning && options.Role != RoleStandalone {
		return nil, "", errors.New("access point role must be provisioning or standalone")
	}
	if options.Interface == "" {
		return nil, "", errors.New("interface is required")
	}
	if err := validateSSID(options.SSID); err != nil {
		return nil, "", err
	}
	if err := validatePSK(options.Password); err != nil {
		return nil, "", err
	}
	if options.Priority < -999 || options.Priority > 999 {
		return nil, "", errors.New("autoconnect priority must be between -999 and 999")
	}
	if options.Band != "" && options.Band != "bg" && options.Band != "a" && options.Band != "6GHz" {
		return nil, "", errors.New("band must be bg, a, or 6GHz")
	}

	address, prefix, err := parseAddress(options.Address)
	if err != nil {
		return nil, "", err
	}
	uuid, err := resolveUUID(options.UUID)
	if err != nil {
		return nil, "", err
	}
	id := options.ID
	if id == "" {
		id = fmt.Sprintf("onboardd %s", options.Role)
	}

	wireless := map[string]any{
		"mode": "ap",
		"ssid": []byte(options.SSID),
	}
	if options.Band != "" {
		wireless["band"] = options.Band
	}

	settings := Settings{
		"connection": variants(map[string]any{
			"id":                   id,
			"uuid":                 uuid,
			"type":                 "802-11-wireless",
			"interface-name":       options.Interface,
			"autoconnect":          options.Autoconnect,
			"autoconnect-priority": options.Priority,
		}),
		"802-11-wireless": variants(wireless),
		"802-11-wireless-security": variants(map[string]any{
			"key-mgmt": "wpa-psk",
			"psk":      options.Password,
		}),
		"ipv4": variants(map[string]any{
			"method": "shared",
			"address-data": []map[string]dbus.Variant{
				{
					"address": dbus.MakeVariant(address),
					"prefix":  dbus.MakeVariant(uint32(prefix)),
				},
			},
		}),
		"ipv6": variants(map[string]any{
			"method": "disabled",
		}),
		"user": metadata(options.Role, false),
	}

	return settings, uuid, nil
}

// rebuildOwnedSettings creates a fresh, well-typed D-Bus settings map from the
// deliberately small profile schema onboardd owns. GetSettings also contains
// NetworkManager-generated legacy values that must not be blindly sent back
// through godbus because tuple values lose their concrete Go types on decode.
func rebuildOwnedSettings(
	profile Profile,
	current Settings,
	secrets Settings,
	autoconnect bool,
) (Settings, error) {
	wireless, ok := current["802-11-wireless"]
	if !ok {
		return nil, errors.New("missing 802-11-wireless settings")
	}

	switch profile.Role {
	case RoleInfrastructure:
		_, secured := current["802-11-wireless-security"]
		password := ""
		if secured {
			var err error
			password, err = profilePSK(current, secrets)
			if err != nil {
				return nil, err
			}
		}
		settings, _, err := BuildInfrastructureSettings(InfrastructureOptions{
			ID:          profile.ID,
			UUID:        profile.UUID,
			Interface:   profile.Interface,
			SSID:        profile.SSID,
			Password:    password,
			Open:        !secured,
			Hidden:      variantBoolDefault(wireless["hidden"], false),
			Autoconnect: autoconnect,
			Priority:    profile.Priority,
			// An autoconnect update is the durable commit point for a validated
			// candidate, so the rebuilt profile deliberately drops Pending.
			Pending: false,
		})
		return settings, err
	case RoleStandalone:
		password, err := profilePSK(current, secrets)
		if err != nil {
			return nil, err
		}
		address, err := profileAddress(current)
		if err != nil {
			return nil, err
		}
		settings, _, err := BuildAccessPointSettings(AccessPointOptions{
			ID:          profile.ID,
			UUID:        profile.UUID,
			Interface:   profile.Interface,
			SSID:        profile.SSID,
			Password:    password,
			Address:     address,
			Role:        RoleStandalone,
			Autoconnect: autoconnect,
			Priority:    profile.Priority,
			Band:        variantString(wireless["band"]),
		})
		return settings, err
	default:
		return nil, fmt.Errorf("cannot rebuild profile with role %q", profile.Role)
	}
}

func profilePSK(current, secrets Settings) (string, error) {
	for _, source := range []Settings{secrets, current} {
		if password := variantString(source["802-11-wireless-security"]["psk"]); password != "" {
			return password, nil
		}
	}
	return "", errors.New("WPA password was not available from NetworkManager")
}

func profileAddress(settings Settings) (string, error) {
	addressData, ok := settings["ipv4"]["address-data"].Value().([]map[string]dbus.Variant)
	if !ok || len(addressData) == 0 {
		return "", errors.New("standalone profile has no IPv4 address-data")
	}
	address := variantString(addressData[0]["address"])
	prefix, ok := addressData[0]["prefix"].Value().(uint32)
	if address == "" || !ok || prefix > 32 {
		return "", errors.New("standalone profile has invalid IPv4 address-data")
	}
	return fmt.Sprintf("%s/%d", address, prefix), nil
}

func metadata(role Role, pending bool) map[string]dbus.Variant {
	data := map[string]string{
		ownerKey:  ownerName,
		roleKey:   string(role),
		schemaKey: "1",
	}
	if pending {
		data[pendingKey] = "true"
	}
	return variants(map[string]any{"data": data})
}

func variants(values map[string]any) map[string]dbus.Variant {
	result := make(map[string]dbus.Variant, len(values))
	for key, value := range values {
		result[key] = dbus.MakeVariant(value)
	}
	return result
}

func validateSSID(ssid string) error {
	length := len([]byte(ssid))
	if length == 0 || length > 32 {
		return errors.New("SSID must contain between 1 and 32 bytes")
	}
	return nil
}

func validatePSK(password string) error {
	length := len(password)
	if length >= 8 && length <= 63 {
		return nil
	}
	if length == 64 {
		if _, err := hex.DecodeString(password); err == nil {
			return nil
		}
	}
	return errors.New("WPA password must be 8-63 bytes or 64 hexadecimal characters")
}

func parseAddress(cidr string) (string, int, error) {
	if cidr == "" {
		cidr = "10.42.0.1/24"
	}
	ip, network, err := net.ParseCIDR(cidr)
	if err != nil || ip.To4() == nil {
		return "", 0, fmt.Errorf("address must be a valid IPv4 CIDR: %q", cidr)
	}
	ones, bits := network.Mask.Size()
	if bits != 32 {
		return "", 0, fmt.Errorf("address must be IPv4: %q", cidr)
	}
	return ip.String(), ones, nil
}

func resolveUUID(value string) (string, error) {
	if value != "" {
		if !validUUID(value) {
			return "", errors.New("UUID must use the canonical 8-4-4-4-12 format")
		}
		return strings.ToLower(value), nil
	}

	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate UUID: %w", err)
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(data)
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[0:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:32]), nil
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	plain := strings.ReplaceAll(value, "-", "")
	_, err := hex.DecodeString(plain)
	return err == nil
}
