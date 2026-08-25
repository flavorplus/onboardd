package appliance

import (
	"context"
	"errors"
	"fmt"
	"time"
)

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
