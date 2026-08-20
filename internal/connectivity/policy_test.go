package connectivity

import "testing"

func TestRequirementValidate(t *testing.T) {
	for _, requirement := range []Requirement{RequirementLocal, RequirementInternet} {
		if err := requirement.Validate(); err != nil {
			t.Fatalf("Validate(%q) error = %v", requirement, err)
		}
	}
	if err := Requirement("cloudish").Validate(); err == nil {
		t.Fatal("unknown requirement unexpectedly validated")
	}
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name        string
		requirement Requirement
		observation Observation
		accepted    bool
		reason      string
	}{
		{
			name:        "local accepts activated address without internet",
			requirement: RequirementLocal,
			observation: Observation{Activated: true, HasLocalAddress: true, Internet: InternetLimited},
			accepted:    true,
			reason:      "requirement-satisfied",
		},
		{
			name:        "internet requires full",
			requirement: RequirementInternet,
			observation: Observation{Activated: true, HasLocalAddress: true, Internet: InternetLimited},
			reason:      "internet-not-confirmed",
		},
		{
			name:        "internet accepts full",
			requirement: RequirementInternet,
			observation: Observation{Activated: true, HasLocalAddress: true, Internet: InternetFull},
			accepted:    true,
			reason:      "requirement-satisfied",
		},
		{
			name:        "local requires activation",
			requirement: RequirementLocal,
			observation: Observation{HasLocalAddress: true, Internet: InternetFull},
			reason:      "device-not-activated",
		},
		{
			name:        "local requires address",
			requirement: RequirementLocal,
			observation: Observation{Activated: true, Internet: InternetFull},
			reason:      "no-local-address",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := Evaluate(test.requirement, test.observation)
			if result.Accepted != test.accepted || result.Reason != test.reason {
				t.Fatalf("Evaluate() = %#v, want accepted=%t reason=%q", result, test.accepted, test.reason)
			}
		})
	}
}
