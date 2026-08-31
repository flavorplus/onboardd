// Package state reconciles transient onboardd orchestration state from NetworkManager
// observations. It deliberately persists nothing itself.
package appliance

import (
	"context"
	"time"

	"github.com/flavorplus/onboardd/internal/connectivity"
)

// Mode is the network operating mode inferred from NetworkManager.
type Mode string

const (
	ModeNone           Mode = "none"
	ModeInfrastructure Mode = "infrastructure"
	ModeStandalone     Mode = "standalone"
	ModeProvisioning   Mode = "provisioning"
)

// Stage is the current transient reconciliation stage.
type Stage string

const (
	StageBooting                Stage = "booting"
	StageReconciling            Stage = "reconciling"
	StageWaitingForConnectivity Stage = "waiting-for-connectivity"
	StageInfrastructure         Stage = "infrastructure"
	StageStandalone             Stage = "standalone"
	StageProvisioning           Stage = "provisioning"
	StageFailed                 Stage = "failed"
	StageStopped                Stage = "stopped"
)

// DeviceState is the small subset of device lifecycle needed by reconciliation.
type DeviceState string

const (
	DeviceUnknown      DeviceState = "unknown"
	DeviceDisconnected DeviceState = "disconnected"
	DeviceConnecting   DeviceState = "connecting"
	DeviceActivated    DeviceState = "activated"
	DeviceFailed       DeviceState = "failed"
)

// Reason explains why the machine entered a stage.
type Reason string

const (
	ReasonStarting               Reason = "starting"
	ReasonInspectingNetwork      Reason = "inspecting-network"
	ReasonInfrastructureReady    Reason = "infrastructure-ready"
	ReasonStandaloneActive       Reason = "standalone-active"
	ReasonProvisioningActive     Reason = "provisioning-active"
	ReasonNoCandidate            Reason = "no-infrastructure-candidate"
	ReasonWaitingForActivation   Reason = "waiting-for-activation"
	ReasonWaitingForConnectivity Reason = "waiting-for-connectivity"
	ReasonActivationFailed       Reason = "activation-failed"
	ReasonActivationTimedOut     Reason = "activation-timed-out"
	ReasonConnectivityTimedOut   Reason = "connectivity-timed-out"
	ReasonDeviceUnmanaged        Reason = "device-unmanaged"
	ReasonObservationFailed      Reason = "observation-failed"
	ReasonInterrupted            Reason = "interrupted"
)

// Profile is the persistent intent needed by the reconciler.
type Profile struct {
	UUID        string `json:"uuid"`
	Mode        Mode   `json:"mode"`
	Autoconnect bool   `json:"autoconnect"`
}

// Snapshot is a normalized point-in-time view of NetworkManager.
type Snapshot struct {
	DeviceManaged bool                     `json:"device_managed"`
	DeviceState   DeviceState              `json:"device_state"`
	ActiveUUID    string                   `json:"active_uuid,omitempty"`
	ActiveMode    Mode                     `json:"active_mode"`
	Connectivity  connectivity.Observation `json:"connectivity"`
	Profiles      []Profile                `json:"profiles"`
}

// State is derived transient state. Sequence exists only for the current process run.
type State struct {
	Sequence uint64 `json:"sequence"`
	Stage    Stage  `json:"stage"`
	Mode     Mode   `json:"mode"`
	Reason   Reason `json:"reason"`
	Detail   string `json:"detail,omitempty"`
}

// EventKind identifies the typed input that caused a transition.
type EventKind string

const (
	EventBoot           EventKind = "boot"
	EventNetworkChanged EventKind = "network-changed"
	EventGraceExpired   EventKind = "grace-expired"
	EventCancelled      EventKind = "cancelled"
)

// Transition is emitted whenever the observable state changes.
type Transition struct {
	Previous State     `json:"previous"`
	Current  State     `json:"current"`
	Trigger  EventKind `json:"trigger"`
}

// NetworkChange identifies the source of a request to inspect NetworkManager again.
type NetworkChange struct {
	Path      string `json:"path,omitempty"`
	Interface string `json:"interface,omitempty"`
}

// Observer supplies snapshots and change notifications. Implementations must treat
// context cancellation as normal shutdown.
type Observer interface {
	Snapshot(context.Context) (Snapshot, error)
	Watch(context.Context) (<-chan NetworkChange, <-chan error, error)
}

// EngineOptions controls transient reconciliation policy.
type EngineOptions struct {
	Requirement connectivity.Requirement
	GracePeriod time.Duration
}
