package appliance

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSupervisorRetriesUntilRunnerRecovers(t *testing.T) {
	runner := &fakeRunner{results: []error{
		errors.New("observer unavailable"),
		errors.New("provisioning unavailable"),
		nil,
	}}
	waiter := &fakeWaiter{}
	var attempts []int
	supervisor, err := NewSupervisor(runner, RetryConfig{
		MaxRestarts:  2,
		RestartDelay: time.Second,
		OnRetry: func(_ context.Context, attempt, maximum int) {
			if maximum != 2 {
				t.Errorf("maximum = %d, want 2", maximum)
			}
			attempts = append(attempts, attempt)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	supervisor.waiter = waiter

	if err := supervisor.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if runner.calls != 3 {
		t.Fatalf("runner calls = %d, want 3", runner.calls)
	}
	if len(waiter.delays) != 2 {
		t.Fatalf("retry waits = %v, want two", waiter.delays)
	}
	if len(attempts) != 2 || attempts[0] != 1 || attempts[1] != 2 {
		t.Fatalf("retry attempts = %v, want [1 2]", attempts)
	}
}

func TestSupervisorReportsExhaustedRecoveryBudget(t *testing.T) {
	runner := &fakeRunner{fallback: errors.New("D-Bus unavailable")}
	supervisor, err := NewSupervisor(runner, RetryConfig{
		MaxRestarts:  2,
		RestartDelay: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	supervisor.waiter = &fakeWaiter{}

	err = supervisor.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "recovery exhausted after 2 restarts") ||
		!strings.Contains(err.Error(), "D-Bus unavailable") {
		t.Fatalf("Run() error = %v", err)
	}
	if runner.calls != 3 {
		t.Fatalf("runner calls = %d, want 3", runner.calls)
	}
}

func TestSupervisorCancellationInterruptsRetryDelay(t *testing.T) {
	runner := &fakeRunner{fallback: errors.New("temporary failure")}
	waiter := &blockingWaiter{started: make(chan struct{})}
	supervisor, err := NewSupervisor(runner, RetryConfig{
		MaxRestarts:  3,
		RestartDelay: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	supervisor.waiter = waiter

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx) }()
	<-waiter.started
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
}

func TestSupervisorDoesNotRestartTerminalFailure(t *testing.T) {
	runner := &fakeRunner{fallback: markTerminal(errors.New("device is unmanaged"))}
	supervisor, err := NewSupervisor(runner, RetryConfig{
		MaxRestarts:  3,
		RestartDelay: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	supervisor.waiter = &fakeWaiter{}

	err = supervisor.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "device is unmanaged") {
		t.Fatalf("Run() error = %v", err)
	}
	if runner.calls != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.calls)
	}
}

func TestNewSupervisorValidatesPolicy(t *testing.T) {
	runner := &fakeRunner{}
	if _, err := NewSupervisor(nil, RetryConfig{RestartDelay: time.Second}); err == nil {
		t.Fatal("nil runner unexpectedly accepted")
	}
	if _, err := NewSupervisor(runner, RetryConfig{MaxRestarts: -1, RestartDelay: time.Second}); err == nil {
		t.Fatal("negative restart count unexpectedly accepted")
	}
	if _, err := NewSupervisor(runner, RetryConfig{MaxRestarts: 1}); err == nil {
		t.Fatal("zero restart delay unexpectedly accepted")
	}
}

type fakeRunner struct {
	results  []error
	fallback error
	calls    int
}

func (runner *fakeRunner) Run(context.Context) error {
	index := runner.calls
	runner.calls++
	if index < len(runner.results) {
		return runner.results[index]
	}
	return runner.fallback
}

type fakeWaiter struct {
	delays []time.Duration
}

func (waiter *fakeWaiter) Wait(_ context.Context, delay time.Duration) error {
	waiter.delays = append(waiter.delays, delay)
	return nil
}

type blockingWaiter struct {
	started chan struct{}
}

func (waiter *blockingWaiter) Wait(ctx context.Context, _ time.Duration) error {
	close(waiter.started)
	<-ctx.Done()
	return ctx.Err()
}
