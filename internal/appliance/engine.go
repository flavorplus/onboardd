package appliance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/flavorplus/onboardd/internal/connectivity"
	"github.com/flavorplus/onboardd/internal/networkmanager"
)

type timer interface {
	C() <-chan time.Time
	Stop() bool
}

type clock interface {
	NewTimer(time.Duration) timer
}

type systemClock struct{}

func (systemClock) NewTimer(duration time.Duration) timer {
	return systemTimer{Timer: time.NewTimer(duration)}
}

type systemTimer struct {
	*time.Timer
}

func (timer systemTimer) C() <-chan time.Time { return timer.Timer.C }

// Engine reconciles transient state from an Observer.
type Engine struct {
	observer Observer
	config   EngineOptions
	clock    clock
}

// NewEngine validates configuration and creates a reconciliation engine.
func NewEngine(observer Observer, config EngineOptions) (*Engine, error) {
	if observer == nil {
		return nil, errors.New("state observer is required")
	}
	if err := config.Requirement.Validate(); err != nil {
		return nil, err
	}
	if config.GracePeriod <= 0 {
		return nil, errors.New("connectivity grace period must be positive")
	}
	return &Engine{observer: observer, config: config, clock: systemClock{}}, nil
}

// Run watches NetworkManager until the context is cancelled. Cancellation emits a final
// stopped state and is not reported as an operational error.
func (engine *Engine) Run(
	ctx context.Context,
) (<-chan Transition, <-chan error, error) {
	changes, watchErrors, err := engine.observer.Watch(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("watch network state: %w", err)
	}

	transitions := make(chan Transition, 32)
	errorsOut := make(chan error, 8)
	go engine.run(ctx, changes, watchErrors, transitions, errorsOut)
	return transitions, errorsOut, nil
}

func (engine *Engine) run(
	ctx context.Context,
	changes <-chan NetworkChange,
	watchErrors <-chan error,
	transitions chan<- Transition,
	errorsOut chan<- error,
) {
	defer close(transitions)
	defer close(errorsOut)

	current := State{}
	sequence := uint64(0)
	var graceTimer timer
	var grace <-chan time.Time

	emit := func(next State, kind EventKind) bool {
		if sameState(current, next) {
			return true
		}
		sequence++
		next.Sequence = sequence
		transition := Transition{Previous: current, Current: next, Trigger: kind}
		select {
		case transitions <- transition:
			current = next
			return true
		case <-ctx.Done():
			return false
		}
	}

	stopGrace := func() {
		if graceTimer != nil {
			graceTimer.Stop()
		}
		graceTimer = nil
		grace = nil
	}

	reconcile := func(kind EventKind, timedOut bool) bool {
		if kind == EventBoot {
			if !emit(State{Stage: StageReconciling, Mode: current.Mode, Reason: ReasonInspectingNetwork}, kind) {
				return false
			}
		}
		snapshot, snapshotErr := engine.observer.Snapshot(ctx)
		if snapshotErr != nil {
			if ctx.Err() != nil {
				return false
			}
			stopGrace()
			emit(State{
				Stage:  StageFailed,
				Mode:   ModeNone,
				Reason: ReasonObservationFailed,
				Detail: snapshotErr.Error(),
			}, kind)
			select {
			case errorsOut <- fmt.Errorf("inspect network state: %w", snapshotErr):
			case <-ctx.Done():
			}
			return true
		}

		next := evaluate(snapshot, engine.config.Requirement, timedOut)
		if next.Stage == StageWaitingForConnectivity {
			if graceTimer == nil {
				graceTimer = engine.clock.NewTimer(engine.config.GracePeriod)
				grace = graceTimer.C()
			}
		} else {
			stopGrace()
		}
		return emit(next, kind)
	}

	if !emit(State{Stage: StageBooting, Mode: ModeNone, Reason: ReasonStarting}, EventBoot) {
		return
	}
	if !reconcile(EventBoot, false) && ctx.Err() == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			stopGrace()
			sequence++
			stopped := State{
				Sequence: sequence,
				Stage:    StageStopped,
				Mode:     current.Mode,
				Reason:   ReasonInterrupted,
			}
			select {
			case transitions <- Transition{Previous: current, Current: stopped, Trigger: EventCancelled}:
			default:
			}
			return
		case _, ok := <-changes:
			if !ok {
				changes = nil
				continue
			}
			if !reconcile(EventNetworkChanged, false) && ctx.Err() == nil {
				return
			}
		case watchErr, ok := <-watchErrors:
			if !ok {
				watchErrors = nil
				continue
			}
			if watchErr == nil {
				continue
			}
			select {
			case errorsOut <- fmt.Errorf("observe network state: %w", watchErr):
			case <-ctx.Done():
			}
		case <-grace:
			graceTimer = nil
			grace = nil
			if !reconcile(EventGraceExpired, true) && ctx.Err() == nil {
				return
			}
		}
	}
}

func evaluate(
	snapshot Snapshot,
	requirement connectivity.Requirement,
	timedOut bool,
) State {
	if !snapshot.DeviceManaged {
		return State{Stage: StageFailed, Mode: ModeNone, Reason: ReasonDeviceUnmanaged}
	}
	if snapshot.ActiveMode == ModeProvisioning {
		return State{Stage: StageProvisioning, Mode: ModeProvisioning, Reason: ReasonProvisioningActive}
	}
	if snapshot.ActiveMode == ModeStandalone {
		return State{Stage: StageStandalone, Mode: ModeStandalone, Reason: ReasonStandaloneActive}
	}
	if hasAutoconnectProfile(snapshot.Profiles, ModeStandalone) {
		if snapshot.DeviceState == DeviceFailed {
			return State{Stage: StageProvisioning, Mode: ModeProvisioning, Reason: ReasonActivationFailed}
		}
		if timedOut {
			return State{Stage: StageProvisioning, Mode: ModeProvisioning, Reason: ReasonActivationTimedOut}
		}
		return State{
			Stage:  StageWaitingForConnectivity,
			Mode:   ModeStandalone,
			Reason: ReasonWaitingForActivation,
		}
	}

	hasInfrastructure := snapshot.ActiveMode == ModeInfrastructure ||
		hasAutoconnectProfile(snapshot.Profiles, ModeInfrastructure)
	if !hasInfrastructure {
		return State{Stage: StageProvisioning, Mode: ModeProvisioning, Reason: ReasonNoCandidate}
	}
	if snapshot.DeviceState == DeviceFailed {
		return State{Stage: StageProvisioning, Mode: ModeProvisioning, Reason: ReasonActivationFailed}
	}

	result := connectivity.Evaluate(requirement, snapshot.Connectivity)
	if snapshot.ActiveMode == ModeInfrastructure && result.Accepted {
		return State{Stage: StageInfrastructure, Mode: ModeInfrastructure, Reason: ReasonInfrastructureReady}
	}
	if timedOut {
		if snapshot.ActiveMode == ModeInfrastructure {
			return State{
				Stage:  StageProvisioning,
				Mode:   ModeProvisioning,
				Reason: ReasonConnectivityTimedOut,
				Detail: result.Reason,
			}
		}
		return State{Stage: StageProvisioning, Mode: ModeProvisioning, Reason: ReasonActivationTimedOut}
	}
	if snapshot.ActiveMode == ModeInfrastructure {
		return State{
			Stage:  StageWaitingForConnectivity,
			Mode:   ModeInfrastructure,
			Reason: ReasonWaitingForConnectivity,
			Detail: result.Reason,
		}
	}
	return State{
		Stage:  StageWaitingForConnectivity,
		Mode:   ModeInfrastructure,
		Reason: ReasonWaitingForActivation,
	}
}

func hasAutoconnectProfile(profiles []Profile, mode Mode) bool {
	for _, profile := range profiles {
		if profile.Mode == mode && profile.Autoconnect {
			return true
		}
	}
	return false
}

func sameState(left, right State) bool {
	return left.Stage == right.Stage &&
		left.Mode == right.Mode &&
		left.Reason == right.Reason &&
		left.Detail == right.Detail
}

type networkManagerClient interface {
	Status(context.Context, string) (networkmanager.Status, error)
	Profiles(context.Context) ([]networkmanager.Profile, error)
	WatchProperties(context.Context) (<-chan networkmanager.Event, <-chan error, error)
}

// NetworkManagerObserver translates the D-Bus adapter into state-engine observations.
type NetworkManagerObserver struct {
	client        networkManagerClient
	interfaceName string
}

// NewNetworkManagerObserver creates a normalized observer for one Wi-Fi interface.
func NewNetworkManagerObserver(client networkManagerClient, interfaceName string) *NetworkManagerObserver {
	return &NetworkManagerObserver{client: client, interfaceName: interfaceName}
}

func (observer *NetworkManagerObserver) Snapshot(ctx context.Context) (Snapshot, error) {
	status, err := observer.client.Status(ctx, observer.interfaceName)
	if err != nil {
		return Snapshot{}, err
	}
	profiles, err := observer.client.Profiles(ctx)
	if err != nil {
		return Snapshot{}, err
	}

	normalizedProfiles := make([]Profile, 0, len(profiles))
	activeMode := ModeNone
	for _, profile := range profiles {
		mode := profileMode(profile)
		if profile.Interface == observer.interfaceName && mode != ModeNone {
			normalizedProfiles = append(normalizedProfiles, Profile{
				UUID:        profile.UUID,
				Mode:        mode,
				Autoconnect: profile.Autoconnect,
			})
		}
		if profile.UUID == status.Device.ActiveUUID {
			activeMode = mode
		}
	}

	return Snapshot{
		DeviceManaged: status.Device.Managed,
		DeviceState:   normalizedDeviceState(status.Device.State),
		ActiveUUID:    status.Device.ActiveUUID,
		ActiveMode:    activeMode,
		Connectivity:  status.Observation(),
		Profiles:      normalizedProfiles,
	}, nil
}

func (observer *NetworkManagerObserver) Watch(
	ctx context.Context,
) (<-chan NetworkChange, <-chan error, error) {
	events, sourceErrors, err := observer.client.WatchProperties(ctx)
	if err != nil {
		return nil, nil, err
	}
	changes := make(chan NetworkChange, 1)
	errorsOut := make(chan error, 1)
	go func() {
		defer close(changes)
		defer close(errorsOut)
		for events != nil || sourceErrors != nil {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				change := NetworkChange{Path: event.Path, Interface: event.Interface}
				select {
				case changes <- change:
				default:
				}
			case sourceErr, ok := <-sourceErrors:
				if !ok {
					sourceErrors = nil
					continue
				}
				select {
				case errorsOut <- sourceErr:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return changes, errorsOut, nil
}

func profileMode(profile networkmanager.Profile) Mode {
	switch profile.Role {
	case networkmanager.RoleInfrastructure:
		return ModeInfrastructure
	case networkmanager.RoleStandalone:
		return ModeStandalone
	case networkmanager.RoleProvisioning:
		return ModeProvisioning
	}
	if profile.Mode == "infrastructure" {
		return ModeInfrastructure
	}
	return ModeNone
}

func normalizedDeviceState(value networkmanager.DeviceState) DeviceState {
	switch value {
	case networkmanager.DeviceStateDisconnected:
		return DeviceDisconnected
	case networkmanager.DeviceStateActivated:
		return DeviceActivated
	case networkmanager.DeviceStateFailed:
		return DeviceFailed
	case networkmanager.DeviceStatePrepare,
		networkmanager.DeviceStateConfig,
		networkmanager.DeviceStateNeedAuth,
		networkmanager.DeviceStateIPConfig,
		networkmanager.DeviceStateIPCheck,
		networkmanager.DeviceStateSecondaries:
		return DeviceConnecting
	default:
		return DeviceUnknown
	}
}
