// Package appliance coordinates normalized network state with long-running appliance
// resources. Platform-specific provisioning and serving remain behind small adapters.
package appliance

import (
	"context"
	"errors"
	"fmt"
	"time"

	stateengine "github.com/flavorplus/onboardd/internal/state"
)

// Config contains internal controller policy. These timings are implementation policy,
// not part of the product configuration contract.
type Config struct {
	ActionTimeout time.Duration
	Observer      LifecycleObserver
}

// LifecycleObserver receives only normalized controller events. Implementations must
// not receive or infer State.Detail from these callbacks.
type LifecycleObserver interface {
	ObserveNetworkState(
		context.Context,
		uint64,
		stateengine.Stage,
		stateengine.Mode,
		stateengine.Reason,
		stateengine.EventKind,
	)
	RecoveryRequested(context.Context)
	ProvisioningAction(context.Context, bool, bool)
}

type transitionSource interface {
	Run(context.Context) (<-chan stateengine.Transition, <-chan error, error)
}

type provisioningManager interface {
	EnterProvisioning(context.Context) error
	LeaveProvisioning(context.Context) error
}

type recoveryRequests interface {
	Pending() bool
	Complete()
	Notifications() <-chan struct{}
}

// Controller applies stable state-engine outcomes to temporary provisioning resources.
// Run is synchronous so its caller owns the complete controller lifetime.
type Controller struct {
	source       transitionSource
	provisioning provisioningManager
	recovery     recoveryRequests
	config       Config
}

// NewController validates dependencies and creates an appliance controller.
func NewController(
	source transitionSource,
	provisioning provisioningManager,
	recovery recoveryRequests,
	config Config,
) (*Controller, error) {
	if source == nil {
		return nil, errors.New("transition source is required")
	}
	if provisioning == nil {
		return nil, errors.New("provisioning manager is required")
	}
	if recovery == nil {
		return nil, errors.New("recovery request source is required")
	}
	if config.ActionTimeout <= 0 {
		return nil, errors.New("controller action timeout must be positive")
	}
	return &Controller{
		source:       source,
		provisioning: provisioning,
		recovery:     recovery,
		config:       config,
	}, nil
}

// Run consumes state transitions until cancellation, a terminal state, or an error.
func (controller *Controller) Run(ctx context.Context) error {
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()

	transitions, sourceErrors, err := controller.source.Run(runContext)
	if err != nil {
		return fmt.Errorf("start appliance reconciliation: %w", err)
	}

	current := actionNone
	awaitingProvisioning := false
	if controller.recovery.Pending() {
		controller.observeRecovery(runContext)
		if err := controller.applyRecovery(runContext, &current, &awaitingProvisioning); err != nil {
			return err
		}
	}
	for transitions != nil || sourceErrors != nil {
		select {
		case <-runContext.Done():
			return nil
		case transition, ok := <-transitions:
			if !ok {
				transitions = nil
				continue
			}
			controller.observeNetworkState(runContext, transition)
			if transition.Current.Stage == stateengine.StageStopped {
				return nil
			}
			next, actionErr := actionFor(transition.Current)
			if actionErr != nil {
				return actionErr
			}
			if awaitingProvisioning && next == actionEnterProvisioning {
				awaitingProvisioning = false
			}
			if awaitingProvisioning && next == actionLeaveProvisioning {
				continue
			}
			if next == actionNone || next == current {
				continue
			}
			if err := controller.apply(runContext, next); err != nil {
				if runContext.Err() != nil {
					return nil
				}
				return err
			}
			current = next
		case <-controller.recovery.Notifications():
			if !controller.recovery.Pending() {
				continue
			}
			controller.observeRecovery(runContext)
			if err := controller.applyRecovery(
				runContext,
				&current,
				&awaitingProvisioning,
			); err != nil {
				if runContext.Err() != nil {
					return nil
				}
				return err
			}
		case sourceErr, ok := <-sourceErrors:
			if !ok {
				sourceErrors = nil
				continue
			}
			if sourceErr != nil {
				return fmt.Errorf("observe appliance network state: %w", sourceErr)
			}
		}
	}
	if runContext.Err() != nil {
		return nil
	}
	return errors.New("appliance reconciliation stopped unexpectedly")
}

func (controller *Controller) applyRecovery(
	ctx context.Context,
	current *action,
	awaitingProvisioning *bool,
) error {
	if *current == actionEnterProvisioning {
		controller.recovery.Complete()
		return nil
	}
	if err := controller.apply(ctx, actionEnterProvisioning); err != nil {
		return err
	}
	controller.recovery.Complete()
	*current = actionEnterProvisioning
	*awaitingProvisioning = true
	return nil
}

type action uint8

const (
	actionNone action = iota
	actionEnterProvisioning
	actionLeaveProvisioning
)

func actionFor(current stateengine.State) (action, error) {
	switch current.Stage {
	case stateengine.StageBooting,
		stateengine.StageReconciling,
		stateengine.StageWaitingForConnectivity:
		return actionNone, nil
	case stateengine.StageProvisioning:
		return actionEnterProvisioning, nil
	case stateengine.StageInfrastructure, stateengine.StageStandalone:
		return actionLeaveProvisioning, nil
	case stateengine.StageFailed:
		var err error
		if current.Detail == "" {
			err = fmt.Errorf("appliance reconciliation failed: %s", current.Reason)
		} else {
			err = fmt.Errorf(
				"appliance reconciliation failed: %s: %s",
				current.Reason,
				current.Detail,
			)
		}
		if current.Reason == stateengine.ReasonObservationFailed {
			return actionNone, err
		}
		return actionNone, markTerminal(err)
	case stateengine.StageStopped:
		return actionNone, nil
	default:
		return actionNone, markTerminal(fmt.Errorf("unsupported appliance state %q", current.Stage))
	}
}

func (controller *Controller) apply(ctx context.Context, next action) error {
	actionContext, cancel := context.WithTimeout(ctx, controller.config.ActionTimeout)
	defer cancel()

	switch next {
	case actionEnterProvisioning:
		if err := controller.provisioning.EnterProvisioning(actionContext); err != nil {
			controller.observeProvisioningAction(ctx, true, false)
			return fmt.Errorf("enter temporary provisioning: %w", err)
		}
		controller.observeProvisioningAction(ctx, true, true)
	case actionLeaveProvisioning:
		if err := controller.provisioning.LeaveProvisioning(actionContext); err != nil {
			controller.observeProvisioningAction(ctx, false, false)
			return fmt.Errorf("leave temporary provisioning: %w", err)
		}
		controller.observeProvisioningAction(ctx, false, true)
	default:
		return fmt.Errorf("unsupported appliance action %d", next)
	}
	return nil
}

func (controller *Controller) observeNetworkState(
	ctx context.Context,
	transition stateengine.Transition,
) {
	if controller.config.Observer != nil {
		current := transition.Current
		controller.config.Observer.ObserveNetworkState(
			ctx,
			current.Sequence,
			current.Stage,
			current.Mode,
			current.Reason,
			transition.Trigger,
		)
	}
}

func (controller *Controller) observeRecovery(ctx context.Context) {
	if controller.config.Observer != nil {
		controller.config.Observer.RecoveryRequested(ctx)
	}
}

func (controller *Controller) observeProvisioningAction(
	ctx context.Context,
	entering bool,
	succeeded bool,
) {
	if controller.config.Observer != nil {
		controller.config.Observer.ProvisioningAction(ctx, entering, succeeded)
	}
}

// RetryConfig contains the bounded recovery policy for one supervised runner.
type RetryConfig struct {
	MaxRestarts  int
	RestartDelay time.Duration
	OnRetry      func(context.Context, int, int)
}

type runner interface {
	Run(context.Context) error
}

type retryWaiter interface {
	Wait(context.Context, time.Duration) error
}

// Supervisor restarts a long-running component after an unexpected failure.
// A normal return or parent cancellation ends supervision without a restart.
type Supervisor struct {
	runner runner
	config RetryConfig
	waiter retryWaiter
}

// NewSupervisor validates the bounded recovery policy and dependencies.
func NewSupervisor(component runner, config RetryConfig) (*Supervisor, error) {
	if component == nil {
		return nil, errors.New("supervised runner is required")
	}
	if config.MaxRestarts < 0 {
		return nil, errors.New("maximum restart count cannot be negative")
	}
	if config.RestartDelay <= 0 {
		return nil, errors.New("restart delay must be positive")
	}
	return &Supervisor{
		runner: component,
		config: config,
		waiter: timerWaiter{},
	}, nil
}

// Run owns the complete retry loop and returns after normal shutdown or after
// the recovery budget has been exhausted.
func (supervisor *Supervisor) Run(ctx context.Context) error {
	for restarts := 0; ; restarts++ {
		err := supervisor.runner.Run(ctx)
		if ctx.Err() != nil || err == nil {
			return nil
		}
		if isTerminal(err) {
			return err
		}
		if restarts >= supervisor.config.MaxRestarts {
			return fmt.Errorf(
				"appliance recovery exhausted after %d restarts: %w",
				supervisor.config.MaxRestarts,
				err,
			)
		}
		if supervisor.config.OnRetry != nil {
			supervisor.config.OnRetry(
				ctx,
				restarts+1,
				supervisor.config.MaxRestarts,
			)
		}
		if waitErr := supervisor.waiter.Wait(ctx, supervisor.config.RestartDelay); waitErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("wait to restart appliance reconciliation: %w", waitErr)
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

type terminalError struct {
	err error
}

func (err terminalError) Error() string { return err.err.Error() }
func (err terminalError) Unwrap() error { return err.err }

func markTerminal(err error) error {
	return terminalError{err: err}
}

func isTerminal(err error) bool {
	var terminal terminalError
	return errors.As(err, &terminal)
}

type timerWaiter struct{}

func (timerWaiter) Wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
