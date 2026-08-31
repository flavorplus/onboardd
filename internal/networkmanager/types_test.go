package networkmanager

import (
	"testing"

	"github.com/flavorplus/onboardd/internal/connectivity"
)

func TestDeviceStateString(t *testing.T) {
	tests := []struct {
		name     string
		state    DeviceState
		expected string
	}{
		{name: "unknown", state: DeviceStateUnknown, expected: "unknown"},
		{name: "unmanaged", state: DeviceStateUnmanaged, expected: "unmanaged"},
		{name: "unavailable", state: DeviceStateUnavailable, expected: "unavailable"},
		{name: "disconnected", state: DeviceStateDisconnected, expected: "disconnected"},
		{name: "prepare", state: DeviceStatePrepare, expected: "prepare"},
		{name: "config", state: DeviceStateConfig, expected: "config"},
		{name: "need auth", state: DeviceStateNeedAuth, expected: "need-auth"},
		{name: "ip config", state: DeviceStateIPConfig, expected: "ip-config"},
		{name: "ip check", state: DeviceStateIPCheck, expected: "ip-check"},
		{name: "secondaries", state: DeviceStateSecondaries, expected: "secondaries"},
		{name: "activated", state: DeviceStateActivated, expected: "activated"},
		{name: "deactivating", state: DeviceStateDeactivating, expected: "deactivating"},
		{name: "failed", state: DeviceStateFailed, expected: "failed"},
		// An unmapped value keeps its numeric form so an unexpected
		// NetworkManager state stays identifiable in the journal.
		{name: "unmapped value", state: DeviceState(5), expected: "unknown(5)"},
		{name: "unmapped high value", state: DeviceState(999), expected: "unknown(999)"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.state.String(); got != test.expected {
				t.Fatalf("DeviceState(%d).String() = %q, expected %q", test.state, got, test.expected)
			}
		})
	}
}

func TestDeviceStateReasonString(t *testing.T) {
	tests := []struct {
		name     string
		reason   DeviceStateReason
		expected string
	}{
		{name: "none", reason: DeviceStateReasonNone, expected: "none"},
		{name: "unknown", reason: DeviceStateReasonUnknown, expected: "unknown"},
		{name: "config failed", reason: DeviceStateReasonConfigFailed, expected: "config-failed"},
		{
			name:     "ip config unavailable",
			reason:   DeviceStateReasonIPConfigUnavailable,
			expected: "ip-config-unavailable",
		},
		{name: "no secrets", reason: DeviceStateReasonNoSecrets, expected: "no-secrets"},
		{
			name:     "supplicant config failed",
			reason:   DeviceStateReasonSupplicantConfigFailed,
			expected: "supplicant-config-failed",
		},
		{
			name:     "supplicant failed",
			reason:   DeviceStateReasonSupplicantFailed,
			expected: "supplicant-failed",
		},
		{
			name:     "supplicant timeout",
			reason:   DeviceStateReasonSupplicantTimeout,
			expected: "supplicant-timeout",
		},
		{name: "dhcp failed", reason: DeviceStateReasonDHCPFailed, expected: "dhcp-failed"},
		{
			name:     "shared start failed",
			reason:   DeviceStateReasonSharedStartFailed,
			expected: "shared-start-failed",
		},
		{name: "shared failed", reason: DeviceStateReasonSharedFailed, expected: "shared-failed"},
		{name: "ssid not found", reason: DeviceStateReasonSSIDNotFound, expected: "ssid-not-found"},
		// Unmapped reasons keep the numeric form; NetworkManager defines many
		// more than onboardd names.
		{name: "unmapped value", reason: DeviceStateReason(2), expected: "reason-2"},
		{name: "unmapped high value", reason: DeviceStateReason(64), expected: "reason-64"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.reason.String(); got != test.expected {
				t.Fatalf(
					"DeviceStateReason(%d).String() = %q, expected %q",
					test.reason,
					got,
					test.expected,
				)
			}
		})
	}
}

func TestConnectivityInternetState(t *testing.T) {
	tests := []struct {
		name     string
		value    Connectivity
		expected connectivity.InternetState
	}{
		{name: "unknown", value: ConnectivityUnknown, expected: connectivity.InternetUnknown},
		{name: "none", value: ConnectivityNone, expected: connectivity.InternetNone},
		{name: "portal", value: ConnectivityPortal, expected: connectivity.InternetPortal},
		{name: "limited", value: ConnectivityLimited, expected: connectivity.InternetLimited},
		{name: "full", value: ConnectivityFull, expected: connectivity.InternetFull},
		// An enum value NetworkManager may add later must not be read as usable
		// connectivity.
		{name: "unmapped value", value: Connectivity(99), expected: connectivity.InternetUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.value.InternetState(); got != test.expected {
				t.Fatalf("Connectivity(%d).InternetState() = %q, expected %q", test.value, got, test.expected)
			}
		})
	}
}

func TestStatusObservation(t *testing.T) {
	status := Status{
		Connectivity: ConnectivityFull,
		Device: Device{
			State:         DeviceStateActivated,
			IPv4Addresses: []string{"192.168.1.10"},
		},
	}
	observation := status.Observation()
	if !observation.Activated || !observation.HasLocalAddress ||
		observation.Internet != connectivity.InternetFull {
		t.Fatalf("activated status observation = %+v", observation)
	}

	// A device that is not activated, and one with no address, must both be
	// reported as such -- policy rejects each on its own.
	notActivated := Status{Device: Device{State: DeviceStateDisconnected}}.Observation()
	if notActivated.Activated || notActivated.HasLocalAddress {
		t.Fatalf("disconnected status observation = %+v", notActivated)
	}
	noAddress := Status{Device: Device{State: DeviceStateActivated}}.Observation()
	if !noAddress.Activated || noAddress.HasLocalAddress {
		t.Fatalf("addressless status observation = %+v", noAddress)
	}
}
