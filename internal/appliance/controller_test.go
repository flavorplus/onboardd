package appliance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	recoveryinput "github.com/flavorplus/onboardd/internal/recovery"
	stateengine "github.com/flavorplus/onboardd/internal/state"
)

func TestControllerAppliesOnlyStableStateChanges(t *testing.T) {
	source := newFakeTransitionSource(
		stateengine.StageBooting,
		stateengine.StageReconciling,
		stateengine.StageWaitingForConnectivity,
		stateengine.StageProvisioning,
		stateengine.StageProvisioning,
		stateengine.StageWaitingForConnectivity,
		stateengine.StageInfrastructure,
		stateengine.StageInfrastructure,
		stateengine.StageStandalone,
		stateengine.StageStopped,
	)
	provisioning := &fakeProvisioningManager{}
	controller, err := NewController(
		source,
		provisioning,
		recoveryinput.NewRequests(),
		Config{ActionTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := []string{"enter", "leave"}
	if strings.Join(provisioning.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("provisioning calls = %v, want %v", provisioning.calls, want)
	}
}

func TestControllerReportsNormalizedLifecycleEvents(t *testing.T) {
	source := newFakeTransitionSourceWithStates(
		stateengine.State{
			Sequence: 1,
			Stage:    stateengine.StageProvisioning,
			Detail:   "raw D-Bus detail",
		},
		stateengine.State{Sequence: 2, Stage: stateengine.StageInfrastructure},
		stateengine.State{Sequence: 3, Stage: stateengine.StageStopped},
	)
	observer := &fakeLifecycleObserver{}
	controller, err := NewController(
		source,
		&fakeProvisioningManager{},
		recoveryinput.NewRequests(),
		Config{ActionTimeout: time.Second, Observer: observer},
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(observer.states) != 3 {
		t.Fatalf("observed states = %d, want 3", len(observer.states))
	}
	wantActions := []string{"enter:succeeded", "leave:succeeded"}
	if strings.Join(observer.actions, ",") != strings.Join(wantActions, ",") {
		t.Fatalf("actions = %v, want %v", observer.actions, wantActions)
	}
}

func TestControllerReportsRecoveryRequestAndFailedAction(t *testing.T) {
	requests := recoveryinput.NewRequests()
	requests.Request()
	observer := &fakeLifecycleObserver{}
	controller, err := NewController(
		newFakeTransitionSource(stateengine.StageStopped),
		&fakeProvisioningManager{enterErr: errors.New("secret platform failure")},
		requests,
		Config{ActionTimeout: time.Second, Observer: observer},
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := controller.Run(context.Background()); err == nil {
		t.Fatal("Run() unexpectedly succeeded")
	}
	if observer.recoveryRequests != 1 {
		t.Fatalf("recovery requests = %d, want 1", observer.recoveryRequests)
	}
	if strings.Join(observer.actions, ",") != "enter:failed" {
		t.Fatalf("actions = %v, want failed enter", observer.actions)
	}
}

func TestControllerEntersManualRecoveryAndIgnoresStaleProductionState(t *testing.T) {
	source := newFakeTransitionSource(
		stateengine.StageInfrastructure,
		stateengine.StageInfrastructure,
		stateengine.StageProvisioning,
		stateengine.StageInfrastructure,
		stateengine.StageStopped,
	)
	provisioning := &fakeProvisioningManager{}
	requests := recoveryinput.NewRequests()
	requests.Request()
	controller, err := NewController(
		source,
		provisioning,
		requests,
		Config{ActionTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := controller.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := []string{"enter", "leave"}
	if strings.Join(provisioning.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("provisioning calls = %v, want %v", provisioning.calls, want)
	}
	if requests.Pending() {
		t.Fatal("successful manual recovery remained pending")
	}
}

func TestControllerRetainsFailedManualRecoveryForRestart(t *testing.T) {
	requests := recoveryinput.NewRequests()
	requests.Request()
	provisioning := &fakeProvisioningManager{enterErr: errors.New("AP unavailable")}
	first, err := NewController(
		newFakeTransitionSource(stateengine.StageStopped),
		provisioning,
		requests,
		Config{ActionTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Run(context.Background()); err == nil {
		t.Fatal("first Run() unexpectedly succeeded")
	}
	if !requests.Pending() {
		t.Fatal("failed manual recovery request was discarded")
	}

	provisioning.enterErr = nil
	second, err := NewController(
		newFakeTransitionSource(stateengine.StageProvisioning, stateengine.StageStopped),
		provisioning,
		requests,
		Config{ActionTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Run(context.Background()); err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if requests.Pending() {
		t.Fatal("retried manual recovery remained pending")
	}
	want := []string{"enter", "enter"}
	if strings.Join(provisioning.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("provisioning calls = %v, want %v", provisioning.calls, want)
	}
}

func TestControllerReportsSourceAndActionFailures(t *testing.T) {
	tests := []struct {
		name         string
		source       *fakeTransitionSource
		provisioning *fakeProvisioningManager
		want         string
	}{
		{
			name:         "source startup",
			source:       &fakeTransitionSource{startErr: errors.New("source unavailable")},
			provisioning: &fakeProvisioningManager{},
			want:         "start appliance reconciliation: source unavailable",
		},
		{
			name:         "source observation",
			source:       newFakeTransitionSourceWithError(errors.New("signal failure")),
			provisioning: &fakeProvisioningManager{},
			want:         "observe appliance network state: signal failure",
		},
		{
			name:         "enter provisioning",
			source:       newFakeTransitionSource(stateengine.StageProvisioning),
			provisioning: &fakeProvisioningManager{enterErr: errors.New("AP failure")},
			want:         "enter temporary provisioning: AP failure",
		},
		{
			name:         "leave provisioning",
			source:       newFakeTransitionSource(stateengine.StageInfrastructure),
			provisioning: &fakeProvisioningManager{leaveErr: errors.New("cleanup failure")},
			want:         "leave temporary provisioning: cleanup failure",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			controller, err := NewController(
				test.source,
				test.provisioning,
				recoveryinput.NewRequests(),
				Config{ActionTimeout: time.Second},
			)
			if err != nil {
				t.Fatal(err)
			}
			err = controller.Run(context.Background())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestControllerReportsTerminalFailure(t *testing.T) {
	source := newFakeTransitionSourceWithStates(stateengine.State{
		Stage:  stateengine.StageFailed,
		Reason: stateengine.ReasonObservationFailed,
		Detail: "D-Bus unavailable",
	})
	controller, err := NewController(
		source,
		&fakeProvisioningManager{},
		recoveryinput.NewRequests(),
		Config{ActionTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}

	err = controller.Run(context.Background())
	if err == nil || !strings.Contains(
		err.Error(),
		"appliance reconciliation failed: observation-failed: D-Bus unavailable",
	) {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestControllerClassifiesUnmanagedDeviceAsTerminal(t *testing.T) {
	source := newFakeTransitionSourceWithStates(stateengine.State{
		Stage:  stateengine.StageFailed,
		Reason: stateengine.ReasonDeviceUnmanaged,
	})
	controller, err := NewController(
		source,
		&fakeProvisioningManager{},
		recoveryinput.NewRequests(),
		Config{ActionTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}

	err = controller.Run(context.Background())
	if err == nil || !isTerminal(err) {
		t.Fatalf("Run() error = %v, want terminal failure", err)
	}
}

func TestControllerClassifiesObservationFailureAsRestartable(t *testing.T) {
	source := newFakeTransitionSourceWithStates(stateengine.State{
		Stage:  stateengine.StageFailed,
		Reason: stateengine.ReasonObservationFailed,
		Detail: "D-Bus unavailable",
	})
	controller, err := NewController(
		source,
		&fakeProvisioningManager{},
		recoveryinput.NewRequests(),
		Config{ActionTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}

	err = controller.Run(context.Background())
	if err == nil || isTerminal(err) {
		t.Fatalf("Run() error = %v, want restartable failure", err)
	}
}

func TestControllerCancellationIsNormalShutdown(t *testing.T) {
	transitions := make(chan stateengine.Transition)
	errorsOut := make(chan error)
	source := &fakeTransitionSource{transitions: transitions, errors: errorsOut}
	controller, err := NewController(
		source,
		&fakeProvisioningManager{},
		recoveryinput.NewRequests(),
		Config{ActionTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := controller.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestControllerCancelsSourceAfterFailure(t *testing.T) {
	source := &contextTransitionSource{
		sourceErr: errors.New("observer failed"),
		canceled:  make(chan struct{}),
	}
	controller, err := NewController(
		source,
		&fakeProvisioningManager{},
		recoveryinput.NewRequests(),
		Config{ActionTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := controller.Run(context.Background()); err == nil {
		t.Fatal("Run() unexpectedly succeeded")
	}
	select {
	case <-source.canceled:
	case <-time.After(time.Second):
		t.Fatal("source context was not canceled after controller failure")
	}
}

func TestNewControllerValidatesDependencies(t *testing.T) {
	source := newFakeTransitionSource(stateengine.StageStopped)
	provisioning := &fakeProvisioningManager{}
	requests := recoveryinput.NewRequests()
	if _, err := NewController(nil, provisioning, requests, Config{ActionTimeout: time.Second}); err == nil {
		t.Fatal("nil transition source accepted")
	}
	if _, err := NewController(source, nil, requests, Config{ActionTimeout: time.Second}); err == nil {
		t.Fatal("nil provisioning manager accepted")
	}
	if _, err := NewController(source, provisioning, nil, Config{ActionTimeout: time.Second}); err == nil {
		t.Fatal("nil recovery request source accepted")
	}
	if _, err := NewController(source, provisioning, requests, Config{}); err == nil {
		t.Fatal("zero action timeout accepted")
	}
}

type fakeTransitionSource struct {
	transitions <-chan stateengine.Transition
	errors      <-chan error
	startErr    error
}

type contextTransitionSource struct {
	sourceErr error
	canceled  chan struct{}
}

func (source *contextTransitionSource) Run(
	ctx context.Context,
) (<-chan stateengine.Transition, <-chan error, error) {
	transitions := make(chan stateengine.Transition)
	close(transitions)
	errorsOut := make(chan error, 1)
	errorsOut <- source.sourceErr
	close(errorsOut)
	go func() {
		<-ctx.Done()
		close(source.canceled)
	}()
	return transitions, errorsOut, nil
}

func newFakeTransitionSource(stages ...stateengine.Stage) *fakeTransitionSource {
	states := make([]stateengine.State, 0, len(stages))
	for _, stage := range stages {
		states = append(states, stateengine.State{Stage: stage})
	}
	return newFakeTransitionSourceWithStates(states...)
}

func newFakeTransitionSourceWithStates(states ...stateengine.State) *fakeTransitionSource {
	transitions := make(chan stateengine.Transition, len(states))
	for _, current := range states {
		transitions <- stateengine.Transition{Current: current}
	}
	close(transitions)
	errorsOut := make(chan error)
	close(errorsOut)
	return &fakeTransitionSource{transitions: transitions, errors: errorsOut}
}

func newFakeTransitionSourceWithError(sourceErr error) *fakeTransitionSource {
	transitions := make(chan stateengine.Transition)
	close(transitions)
	errorsOut := make(chan error, 1)
	errorsOut <- sourceErr
	close(errorsOut)
	return &fakeTransitionSource{transitions: transitions, errors: errorsOut}
}

func (source *fakeTransitionSource) Run(
	context.Context,
) (<-chan stateengine.Transition, <-chan error, error) {
	return source.transitions, source.errors, source.startErr
}

type fakeProvisioningManager struct {
	calls    []string
	enterErr error
	leaveErr error
}

type fakeLifecycleObserver struct {
	states           []string
	recoveryRequests int
	actions          []string
}

func (observer *fakeLifecycleObserver) ObserveNetworkState(
	_ context.Context,
	sequence uint64,
	stage stateengine.Stage,
	_ stateengine.Mode,
	_ stateengine.Reason,
	_ stateengine.EventKind,
) {
	observer.states = append(observer.states, fmt.Sprintf("%d:%s", sequence, stage))
}

func (observer *fakeLifecycleObserver) RecoveryRequested(context.Context) {
	observer.recoveryRequests++
}

func (observer *fakeLifecycleObserver) ProvisioningAction(
	_ context.Context,
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
	observer.actions = append(observer.actions, action+":"+outcome)
}

func (manager *fakeProvisioningManager) EnterProvisioning(context.Context) error {
	manager.calls = append(manager.calls, "enter")
	return manager.enterErr
}

func (manager *fakeProvisioningManager) LeaveProvisioning(context.Context) error {
	manager.calls = append(manager.calls, "leave")
	return manager.leaveErr
}
