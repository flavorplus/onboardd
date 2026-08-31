package networkmanager

import "testing"

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
