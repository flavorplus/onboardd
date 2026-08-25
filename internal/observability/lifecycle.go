package observability

import (
	"context"
	"errors"
	"io"
	"log/slog"

	stateengine "github.com/flavorplus/onboardd/internal/state"
)

const lifecycleMessage = "onboardd lifecycle"

// Lifecycle emits structured events and updates the matching process health signal.
// Its public methods accept only normalized fields so callers cannot accidentally log
// credentials, resource identifiers, raw errors, or State.Detail.
type Lifecycle struct {
	logger *slog.Logger
	health *Health
}

// NewLifecycle writes one JSON object per line to output.
func NewLifecycle(output io.Writer) *Lifecycle {
	if output == nil {
		output = io.Discard
	}
	return &Lifecycle{
		logger: slog.New(slog.NewJSONHandler(output, nil)),
		health: NewHealth(),
	}
}

// Health returns the lifecycle's shared health state.
func (lifecycle *Lifecycle) Health() *Health {
	return lifecycle.health
}

// Starting records process runtime initialization.
func (lifecycle *Lifecycle) Starting(ctx context.Context) {
	lifecycle.logger.InfoContext(ctx, lifecycleMessage, "event", "runtime_starting")
}

// ObserveNetworkState records the normalized state-machine outcome and updates health.
// Raw transition detail cannot be supplied through this API.
func (lifecycle *Lifecycle) ObserveNetworkState(
	ctx context.Context,
	sequence uint64,
	stage stateengine.Stage,
	mode stateengine.Mode,
	reason stateengine.Reason,
	trigger stateengine.EventKind,
) {
	lifecycle.health.ObserveNetworkState(sequence, stage, mode, reason)
	lifecycle.logger.InfoContext(
		ctx,
		lifecycleMessage,
		"event", "network_state_changed",
		"sequence", sequence,
		"stage", stage,
		"mode", mode,
		"reason", reason,
		"trigger", trigger,
	)
}

// RecoveryRequested records a coalesced local recovery request.
func (lifecycle *Lifecycle) RecoveryRequested(ctx context.Context) {
	lifecycle.logger.InfoContext(ctx, lifecycleMessage, "event", "recovery_requested")
}

// ProvisioningAction records only the direction and outcome of a captive-resource
// change. No profile or network identifiers are accepted.
func (lifecycle *Lifecycle) ProvisioningAction(
	ctx context.Context,
	entering bool,
	succeeded bool,
) {
	action := "leave"
	if entering {
		action = "enter"
	}
	outcome := "failed"
	if succeeded {
		outcome = "succeeded"
	}
	method := lifecycle.logger.WarnContext
	if succeeded {
		method = lifecycle.logger.InfoContext
	}
	method(
		ctx,
		lifecycleMessage,
		"event", "provisioning_action",
		"action", action,
		"outcome", outcome,
	)
}

// ComponentRetry records a bounded retry and marks the runtime as recovering.
func (lifecycle *Lifecycle) ComponentRetry(
	ctx context.Context,
	component Component,
	attempt int,
	maximum int,
) {
	lifecycle.health.ComponentRetry(component, attempt, maximum)
	lifecycle.logger.WarnContext(
		ctx,
		lifecycleMessage,
		"event", "component_retry",
		"component", component,
		"attempt", attempt,
		"maximum", maximum,
	)
}

// ComponentRecovered records a successful rebind or reconciliation recovery.
func (lifecycle *Lifecycle) ComponentRecovered(ctx context.Context, component Component) {
	lifecycle.health.ComponentRecovered(component)
	lifecycle.logger.InfoContext(
		ctx,
		lifecycleMessage,
		"event", "component_recovered",
		"component", component,
	)
}

// Stopping records the beginning of bounded shutdown.
func (lifecycle *Lifecycle) Stopping(ctx context.Context) {
	lifecycle.health.Stopping()
	lifecycle.logger.InfoContext(ctx, lifecycleMessage, "event", "runtime_stopping")
}

// Stopped records a completed graceful shutdown.
func (lifecycle *Lifecycle) Stopped(ctx context.Context) {
	lifecycle.health.Stopped()
	lifecycle.logger.InfoContext(ctx, lifecycleMessage, "event", "runtime_stopped")
}

// Failed records a terminal classification without accepting the underlying error.
func (lifecycle *Lifecycle) Failed(
	ctx context.Context,
	component Component,
	failure Failure,
) {
	lifecycle.health.Failed(component, failure)
	lifecycle.logger.ErrorContext(
		ctx,
		lifecycleMessage,
		"event", "runtime_failed",
		"component", component,
		"failure", failure,
	)
}

type runtimeError struct {
	cause error
}

func (err runtimeError) Error() string { return "appliance runtime failed" }
func (err runtimeError) Unwrap() error { return err.cause }

// RedactRuntimeError preserves an internal error chain for programmatic inspection
// while keeping the CLI/journal message free from raw platform diagnostics.
func RedactRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	var redacted runtimeError
	if errors.As(err, &redacted) {
		return err
	}
	return runtimeError{cause: err}
}
