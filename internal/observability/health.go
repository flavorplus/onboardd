// Package observability provides redaction-safe lifecycle events and health state for
// the long-running appliance process.
package observability

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	stateengine "github.com/flavorplus/onboardd/internal/state"
)

// Status is the process-level health phase exposed to operators and supervisors.
type Status string

const (
	StatusStarting    Status = "starting"
	StatusReconciling Status = "reconciling"
	StatusReady       Status = "ready"
	StatusRecovering  Status = "recovering"
	StatusStopping    Status = "stopping"
	StatusStopped     Status = "stopped"
	StatusFailed      Status = "failed"
)

// Component is a fixed, low-cardinality runtime component name.
type Component string

const (
	ComponentRuntime    Component = "runtime"
	ComponentReconciler Component = "reconciler"
	ComponentHTTP       Component = "setup-http"
	ComponentControl    Component = "recovery-control"
	ComponentSystemd    Component = "systemd-notify"
)

// Failure is a safe failure classification. It deliberately carries no error text.
type Failure string

const (
	FailureStartup     Failure = "startup"
	FailureOperational Failure = "operational"
	FailureExhausted   Failure = "recovery-exhausted"
	FailureUnexpected  Failure = "unexpected-stop"
)

// Snapshot is safe to expose over HTTP. Raw observer details and resource identifiers
// never enter this type.
type Snapshot struct {
	Status    Status             `json:"status"`
	Healthy   bool               `json:"healthy"`
	Ready     bool               `json:"ready"`
	Sequence  uint64             `json:"sequence,omitempty"`
	Stage     stateengine.Stage  `json:"stage,omitempty"`
	Mode      stateengine.Mode   `json:"mode,omitempty"`
	Reason    stateengine.Reason `json:"reason,omitempty"`
	UpdatedAt time.Time          `json:"updated_at"`
}

// Health owns the concurrency-safe lifecycle snapshot and a coalesced change signal.
// Changes is transport-neutral so the systemd notifier stays outside the controller.
type Health struct {
	mu       sync.RWMutex
	snapshot Snapshot
	degraded map[Component]struct{}
	changes  chan Snapshot
}

// NewHealth creates a live but not-yet-ready process health signal.
func NewHealth() *Health {
	health := &Health{
		degraded: make(map[Component]struct{}),
		changes:  make(chan Snapshot, 1),
	}
	health.snapshot = Snapshot{
		Status:    StatusStarting,
		Healthy:   true,
		UpdatedAt: time.Now().UTC(),
	}
	health.publishLocked()
	return health
}

// Snapshot returns a point-in-time copy suitable for concurrent readers.
func (health *Health) Snapshot() Snapshot {
	health.mu.RLock()
	defer health.mu.RUnlock()
	return health.snapshot
}

// Changes returns the latest coalesced health update. The channel remains owned by
// Health for the process lifetime and is never closed.
func (health *Health) Changes() <-chan Snapshot {
	return health.changes
}

// ObserveNetworkState records only normalized state fields. Its signature cannot accept
// State.Detail, D-Bus paths, profile identifiers, or raw errors.
func (health *Health) ObserveNetworkState(
	sequence uint64,
	stage stateengine.Stage,
	mode stateengine.Mode,
	reason stateengine.Reason,
) {
	health.mu.Lock()
	defer health.mu.Unlock()

	health.snapshot.Sequence = sequence
	health.snapshot.Stage = stage
	health.snapshot.Mode = mode
	health.snapshot.Reason = reason

	switch stage {
	case stateengine.StageInfrastructure,
		stateengine.StageStandalone,
		stateengine.StageProvisioning:
		delete(health.degraded, ComponentReconciler)
	case stateengine.StageFailed:
		health.degraded[ComponentReconciler] = struct{}{}
	}
	health.deriveLocked()
}

// ComponentRetry marks a bounded component recovery attempt.
func (health *Health) ComponentRetry(component Component, _, _ int) {
	health.mu.Lock()
	defer health.mu.Unlock()
	health.degraded[component] = struct{}{}
	health.deriveLocked()
}

// ComponentRecovered clears a component's bounded recovery state.
func (health *Health) ComponentRecovered(component Component) {
	health.mu.Lock()
	defer health.mu.Unlock()
	delete(health.degraded, component)
	health.deriveLocked()
}

// Stopping marks the point at which new work is no longer accepted.
func (health *Health) Stopping() {
	health.setTerminal(StatusStopping)
}

// Stopped marks a completed graceful shutdown.
func (health *Health) Stopped() {
	health.setTerminal(StatusStopped)
}

// Failed marks a terminal runtime failure without retaining its raw error.
func (health *Health) Failed(Component, Failure) {
	health.setTerminal(StatusFailed)
}

func (health *Health) setTerminal(status Status) {
	health.mu.Lock()
	defer health.mu.Unlock()
	health.snapshot.Status = status
	health.snapshot.Healthy = false
	health.snapshot.Ready = false
	health.snapshot.UpdatedAt = time.Now().UTC()
	health.publishLocked()
}

func (health *Health) deriveLocked() {
	health.snapshot.Healthy = true
	health.snapshot.Ready = false
	switch {
	case len(health.degraded) > 0:
		health.snapshot.Status = StatusRecovering
	case health.snapshot.Stage == stateengine.StageInfrastructure,
		health.snapshot.Stage == stateengine.StageStandalone,
		health.snapshot.Stage == stateengine.StageProvisioning:
		health.snapshot.Status = StatusReady
		health.snapshot.Ready = true
	case health.snapshot.Stage == stateengine.StageBooting,
		health.snapshot.Stage == stateengine.StageReconciling,
		health.snapshot.Stage == stateengine.StageWaitingForConnectivity:
		health.snapshot.Status = StatusReconciling
	default:
		health.snapshot.Status = StatusStarting
	}
	health.snapshot.UpdatedAt = time.Now().UTC()
	health.publishLocked()
}

func (health *Health) publishLocked() {
	select {
	case <-health.changes:
	default:
	}
	select {
	case health.changes <- health.snapshot:
	default:
	}
}

// ServeHTTP exposes liveness and readiness without exposing configuration or raw
// platform errors. Healthy startup and bounded recovery return 200; terminal states
// return 503. Consumers should inspect Ready when they need readiness specifically.
func (health *Health) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	snapshot := health.Snapshot()
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	status := http.StatusOK
	if !snapshot.Healthy {
		status = http.StatusServiceUnavailable
	}
	writer.WriteHeader(status)
	if request.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(writer).Encode(snapshot)
}
