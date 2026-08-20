// Package connectivity evaluates product connectivity requirements independently from
// NetworkManager transport details.
package connectivity

import "fmt"

// Requirement describes how much connectivity a product needs before setup is done.
type Requirement string

const (
	RequirementLocal    Requirement = "local"
	RequirementInternet Requirement = "internet"
)

// InternetState is the normalized result of an upstream connectivity check.
type InternetState string

const (
	InternetUnknown InternetState = "unknown"
	InternetNone    InternetState = "none"
	InternetPortal  InternetState = "portal"
	InternetLimited InternetState = "limited"
	InternetFull    InternetState = "full"
)

// Observation contains the facts used by connectivity policy. An activated device
// without a usable local address is not considered locally connected.
type Observation struct {
	Activated       bool          `json:"activated"`
	HasLocalAddress bool          `json:"has_local_address"`
	Internet        InternetState `json:"internet"`
}

// Result explains whether an observation satisfies a requirement.
type Result struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason"`
}

// Validate rejects unknown configuration values.
func (requirement Requirement) Validate() error {
	switch requirement {
	case RequirementLocal, RequirementInternet:
		return nil
	default:
		return fmt.Errorf("unknown connectivity requirement %q", requirement)
	}
}

// Evaluate applies product policy to normalized network facts.
func Evaluate(requirement Requirement, observation Observation) Result {
	if !observation.Activated {
		return Result{Reason: "device-not-activated"}
	}
	if !observation.HasLocalAddress {
		return Result{Reason: "no-local-address"}
	}
	if requirement == RequirementInternet && observation.Internet != InternetFull {
		return Result{Reason: "internet-not-confirmed"}
	}
	return Result{Accepted: true, Reason: "requirement-satisfied"}
}
