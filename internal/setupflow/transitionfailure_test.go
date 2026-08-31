package setupflow

import (
	"errors"
	"testing"
)

// publicTransitionFailure classifies a transition failure by substring-matching
// the error text produced in internal/recovery. That makes those messages a
// contract, not just diagnostics: rewording one silently changes the code the
// browser receives. This table pins the mapping, using the exact wording each
// recovery path produces today.
func TestPublicTransitionFailure(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		// Rollback and restoration failures mean setup may be unreachable.
		{
			name:     "checkpoint rollback failed",
			err:      errors.New("rollback NetworkManager checkpoint: bus error"),
			expected: "restoration_failed",
		},
		{
			name:     "previous connection not restored",
			err:      errors.New("confirm previous connection restoration: timed out"),
			expected: "restoration_failed",
		},

		// The candidate joined but did not provide the required access.
		{
			name:     "policy rejected the candidate",
			err:      errors.New("candidate does not satisfy internet requirement: internet-not-confirmed"),
			expected: "connectivity_unavailable",
		},
		{
			name:     "infrastructure status read failed",
			err:      errors.New("inspect candidate connectivity: bus error"),
			expected: "connectivity_unavailable",
		},

		// The standalone equivalent of the case above is worded without the
		// word "connectivity", so it classifies as a plain connection failure.
		// This asymmetry is existing behaviour, pinned here so a reword during
		// refactoring cannot change it unnoticed.
		{
			name:     "standalone status read failed",
			err:      errors.New("inspect standalone profile: bus error"),
			expected: "connection_failed",
		},

		// Everything else is a plain connection failure.
		{
			name:     "activate infrastructure profile",
			err:      errors.New("activate infrastructure profile: no secrets"),
			expected: "connection_failed",
		},
		{
			name:     "wait for candidate",
			err:      errors.New("wait for candidate infrastructure profile: timed out"),
			expected: "connection_failed",
		},
		{
			name:     "candidate not active",
			err:      errors.New("candidate profile abc is not active"),
			expected: "connection_failed",
		},
		{
			name:     "select infrastructure mode",
			err:      errors.New("select infrastructure mode: bus error"),
			expected: "connection_failed",
		},
		{
			name:     "commit infrastructure transition",
			err:      errors.New("commit infrastructure transition: bus error"),
			expected: "connection_failed",
		},
		{
			name:     "activate standalone profile",
			err:      errors.New("activate standalone profile: bus error"),
			expected: "connection_failed",
		},
		{
			name:     "wait for standalone profile",
			err:      errors.New("wait for standalone profile: timed out"),
			expected: "connection_failed",
		},
		{
			name:     "standalone not active",
			err:      errors.New("standalone profile abc is not active at 10.42.0.1"),
			expected: "connection_failed",
		},
		{
			name:     "select standalone mode",
			err:      errors.New("select standalone mode: bus error"),
			expected: "connection_failed",
		},
		{
			name:     "commit standalone transition",
			err:      errors.New("commit standalone transition: bus error"),
			expected: "connection_failed",
		},
		{
			name:     "checkpoint creation failed",
			err:      errors.New("protect setup AP with checkpoint: bus error"),
			expected: "connection_failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var public *PublicError
			if !errors.As(publicTransitionFailure(test.err), &public) {
				t.Fatal("expected a PublicError")
			}
			if public.Failure.Code != test.expected {
				t.Fatalf("code = %q, expected %q", public.Failure.Code, test.expected)
			}
		})
	}
}
