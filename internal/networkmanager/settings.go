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
	ownerKey  = "onboardd.owner"
	roleKey   = "onboardd.role"
	schemaKey = "onboardd.schema"
	ownerName = "onboardd"
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
		"user": metadata(RoleInfrastructure),
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
		"802-11-wireless": wirelessVariants(wireless),
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
		"user": metadata(options.Role),
	}

	return settings, uuid, nil
}

func metadata(role Role) map[string]dbus.Variant {
	return variants(map[string]any{
		"data": map[string]string{
			ownerKey:  ownerName,
			roleKey:   string(role),
			schemaKey: "1",
		},
	})
}

func variants(values map[string]any) map[string]dbus.Variant {
	result := make(map[string]dbus.Variant, len(values))
	for key, value := range values {
		result[key] = dbus.MakeVariant(value)
	}
	return result
}

func wirelessVariants(values map[string]any) map[string]dbus.Variant {
	return variants(values)
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
