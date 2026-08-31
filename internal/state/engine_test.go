package state

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/flavorplus/onboardd/internal/connectivity"
	"github.com/flavorplus/onboardd/internal/networkmanager"
)

func TestSuccessfulStartup(t *testing.T) {
	observer := newFakeObserver(readyInfrastructureSnapshot())
	engine, _ := New(observer, Config{
		Requirement: connectivity.RequirementLocal,
		GracePeriod: time.Minute,
	})

	ctx, cancel := context.WithCancel(context.Background())
	transitions, errorsOut, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state := waitForStage(t, transitions, StageInfrastructure)
	if state.Mode != ModeInfrastructure || state.Reason != ReasonInfrastructureReady {
		t.Fatalf("state = %#v", state)
	}
	cancelAndDrain(t, cancel, transitions, errorsOut)
}

func TestFailedActivationFallsBackToProvisioning(t *testing.T) {
	snapshot := Snapshot{
		DeviceManaged: true,
		DeviceState:   DeviceFailed,
		Profiles: []Profile{
			{UUID: "office", Mode: ModeInfrastructure, Autoconnect: true},
		},
	}
	observer := newFakeObserver(snapshot)
	engine, _ := New(observer, Config{
		Requirement: connectivity.RequirementLocal,
		GracePeriod: time.Minute,
	})

	ctx, cancel := context.WithCancel(context.Background())
	transitions, errorsOut, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state := waitForStage(t, transitions, StageProvisioning)
	if state.Reason != ReasonActivationFailed {
		t.Fatalf("reason = %q, want %q", state.Reason, ReasonActivationFailed)
	}
	cancelAndDrain(t, cancel, transitions, errorsOut)
}

func TestInternetConnectivityFailureExpiresGracePeriod(t *testing.T) {
	snapshot := readyInfrastructureSnapshot()
	snapshot.Connectivity.Internet = connectivity.InternetLimited
	observer := newFakeObserver(snapshot)
	manualClock := newFakeClock()
	engine, _ := New(observer, Config{
		Requirement: connectivity.RequirementInternet,
		GracePeriod: time.Minute,
	})
	engine.clock = manualClock

	ctx, cancel := context.WithCancel(context.Background())
	transitions, errorsOut, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	waitForStage(t, transitions, StageWaitingForConnectivity)
	manualClock.nextTimer(t).fire()
	state := waitForStage(t, transitions, StageProvisioning)
	if state.Reason != ReasonConnectivityTimedOut || state.Detail != "internet-not-confirmed" {
		t.Fatalf("state = %#v", state)
	}
	cancelAndDrain(t, cancel, transitions, errorsOut)
}

func TestStandaloneIntentSurvivesProcessRestart(t *testing.T) {
	snapshot := Snapshot{
		DeviceManaged: true,
		DeviceState:   DeviceActivated,
		ActiveUUID:    "standalone",
		ActiveMode:    ModeStandalone,
		Profiles: []Profile{
			{UUID: "standalone", Mode: ModeStandalone, Autoconnect: true},
		},
	}

	for run := 1; run <= 2; run++ {
		observer := newFakeObserver(snapshot)
		engine, _ := New(observer, Config{
			Requirement: connectivity.RequirementInternet,
			GracePeriod: time.Minute,
		})
		ctx, cancel := context.WithCancel(context.Background())
		transitions, errorsOut, err := engine.Run(ctx)
		if err != nil {
			t.Fatalf("run %d: Run() error = %v", run, err)
		}
		state := waitForStage(t, transitions, StageStandalone)
		if state.Reason != ReasonStandaloneActive {
			t.Fatalf("run %d: state = %#v", run, state)
		}
		cancelAndDrain(t, cancel, transitions, errorsOut)
	}
}

func TestDisconnectionStartsGraceThenProvisioning(t *testing.T) {
	observer := newFakeObserver(readyInfrastructureSnapshot())
	manualClock := newFakeClock()
	engine, _ := New(observer, Config{
		Requirement: connectivity.RequirementLocal,
		GracePeriod: time.Minute,
	})
	engine.clock = manualClock

	ctx, cancel := context.WithCancel(context.Background())
	transitions, errorsOut, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	waitForStage(t, transitions, StageInfrastructure)

	disconnected := Snapshot{
		DeviceManaged: true,
		DeviceState:   DeviceDisconnected,
		Profiles: []Profile{
			{UUID: "office", Mode: ModeInfrastructure, Autoconnect: true},
		},
	}
	observer.setSnapshot(disconnected)
	observer.notify()
	waitForStage(t, transitions, StageWaitingForConnectivity)
	manualClock.nextTimer(t).fire()
	state := waitForStage(t, transitions, StageProvisioning)
	if state.Reason != ReasonActivationTimedOut {
		t.Fatalf("reason = %q, want %q", state.Reason, ReasonActivationTimedOut)
	}
	cancelAndDrain(t, cancel, transitions, errorsOut)
}

func TestInterruptedTransitionStopsTimer(t *testing.T) {
	snapshot := Snapshot{
		DeviceManaged: true,
		DeviceState:   DeviceConnecting,
		Profiles: []Profile{
			{UUID: "office", Mode: ModeInfrastructure, Autoconnect: true},
		},
	}
	observer := newFakeObserver(snapshot)
	manualClock := newFakeClock()
	engine, _ := New(observer, Config{
		Requirement: connectivity.RequirementLocal,
		GracePeriod: time.Minute,
	})
	engine.clock = manualClock

	ctx, cancel := context.WithCancel(context.Background())
	transitions, errorsOut, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	waitForStage(t, transitions, StageWaitingForConnectivity)
	timer := manualClock.nextTimer(t)
	cancel()
	state := waitForStage(t, transitions, StageStopped)
	if state.Reason != ReasonInterrupted {
		t.Fatalf("reason = %q, want %q", state.Reason, ReasonInterrupted)
	}
	if !timer.isStopped() {
		t.Fatal("grace timer was not stopped during cancellation")
	}
	assertNoErrors(t, errorsOut)
}

func TestStandaloneActivationExpiresGracePeriod(t *testing.T) {
	snapshot := Snapshot{
		DeviceManaged: true,
		DeviceState:   DeviceDisconnected,
		Profiles: []Profile{
			{UUID: "standalone", Mode: ModeStandalone, Autoconnect: true},
		},
	}
	observer := newFakeObserver(snapshot)
	manualClock := newFakeClock()
	engine, _ := New(observer, Config{
		Requirement: connectivity.RequirementInternet,
		GracePeriod: time.Minute,
	})
	engine.clock = manualClock

	ctx, cancel := context.WithCancel(context.Background())
	transitions, errorsOut, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	waiting := waitForStage(t, transitions, StageWaitingForConnectivity)
	if waiting.Mode != ModeStandalone || waiting.Reason != ReasonWaitingForActivation {
		t.Fatalf("waiting state = %#v", waiting)
	}
	manualClock.nextTimer(t).fire()
	timedOut := waitForStage(t, transitions, StageProvisioning)
	if timedOut.Reason != ReasonActivationTimedOut {
		t.Fatalf("timed-out state = %#v", timedOut)
	}
	cancelAndDrain(t, cancel, transitions, errorsOut)
}

func TestActiveStandaloneDoesNotRequireInternet(t *testing.T) {
	snapshot := Snapshot{
		DeviceManaged: true,
		DeviceState:   DeviceActivated,
		ActiveUUID:    "standalone",
		ActiveMode:    ModeStandalone,
		Connectivity: connectivity.Observation{
			Internet: connectivity.InternetNone,
		},
		Profiles: []Profile{
			{UUID: "standalone", Mode: ModeStandalone, Autoconnect: true},
		},
	}
	state := evaluate(snapshot, connectivity.RequirementInternet, false)
	if state.Stage != StageStandalone || state.Reason != ReasonStandaloneActive {
		t.Fatalf("evaluate() = %#v", state)
	}
}

func TestNewValidatesConfiguration(t *testing.T) {
	observer := newFakeObserver(Snapshot{})
	if _, err := New(nil, Config{Requirement: connectivity.RequirementLocal, GracePeriod: time.Second}); err == nil {
		t.Fatal("nil observer unexpectedly accepted")
	}
	if _, err := New(observer, Config{Requirement: "invalid", GracePeriod: time.Second}); err == nil {
		t.Fatal("invalid requirement unexpectedly accepted")
	}
	if _, err := New(observer, Config{Requirement: connectivity.RequirementLocal}); err == nil {
		t.Fatal("zero grace period unexpectedly accepted")
	}
}

func readyInfrastructureSnapshot() Snapshot {
	return Snapshot{
		DeviceManaged: true,
		DeviceState:   DeviceActivated,
		ActiveUUID:    "office",
		ActiveMode:    ModeInfrastructure,
		Connectivity: connectivity.Observation{
			Activated:       true,
			HasLocalAddress: true,
			Internet:        connectivity.InternetFull,
		},
		Profiles: []Profile{
			{UUID: "office", Mode: ModeInfrastructure, Autoconnect: true},
		},
	}
}

type fakeObserver struct {
	mu       sync.RWMutex
	snapshot Snapshot
	changes  chan NetworkChange
	errors   chan error
}

func newFakeObserver(snapshot Snapshot) *fakeObserver {
	return &fakeObserver{
		snapshot: snapshot,
		changes:  make(chan NetworkChange, 8),
		errors:   make(chan error, 1),
	}
}

func (observer *fakeObserver) Snapshot(context.Context) (Snapshot, error) {
	observer.mu.RLock()
	defer observer.mu.RUnlock()
	return observer.snapshot, nil
}

func (observer *fakeObserver) Watch(context.Context) (<-chan NetworkChange, <-chan error, error) {
	return observer.changes, observer.errors, nil
}

func (observer *fakeObserver) setSnapshot(snapshot Snapshot) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.snapshot = snapshot
}

func (observer *fakeObserver) notify() {
	observer.changes <- NetworkChange{Path: "/fake/network"}
}

type fakeClock struct {
	created chan *fakeTimer
}

func newFakeClock() *fakeClock {
	return &fakeClock{created: make(chan *fakeTimer, 8)}
}

func (clock *fakeClock) NewTimer(time.Duration) timer {
	timer := &fakeTimer{ticks: make(chan time.Time, 1)}
	clock.created <- timer
	return timer
}

func (clock *fakeClock) nextTimer(t *testing.T) *fakeTimer {
	t.Helper()
	select {
	case timer := <-clock.created:
		return timer
	case <-time.After(time.Second):
		t.Fatal("engine did not create a grace timer")
		return nil
	}
}

type fakeTimer struct {
	mu      sync.Mutex
	ticks   chan time.Time
	stopped bool
}

func (timer *fakeTimer) C() <-chan time.Time { return timer.ticks }

func (timer *fakeTimer) Stop() bool {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	wasActive := !timer.stopped
	timer.stopped = true
	return wasActive
}

func (timer *fakeTimer) fire() {
	timer.ticks <- time.Unix(1, 0)
}

func (timer *fakeTimer) isStopped() bool {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	return timer.stopped
}

func waitForStage(t *testing.T, transitions <-chan Transition, stage Stage) State {
	t.Helper()
	for {
		select {
		case transition, ok := <-transitions:
			if !ok {
				t.Fatalf("transition stream closed before stage %q", stage)
			}
			if transition.Current.Stage == stage {
				return transition.Current
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for stage %q", stage)
		}
	}
}

func cancelAndDrain(
	t *testing.T,
	cancel context.CancelFunc,
	transitions <-chan Transition,
	errorsOut <-chan error,
) {
	t.Helper()
	cancel()
	waitForStage(t, transitions, StageStopped)
	assertNoErrors(t, errorsOut)
}

func assertNoErrors(t *testing.T, errorsOut <-chan error) {
	t.Helper()
	for err := range errorsOut {
		if err != nil {
			t.Fatalf("unexpected engine error: %v", err)
		}
	}
}

func TestNetworkManagerObserverNormalizesSnapshot(t *testing.T) {
	client := &fakeNetworkManagerClient{
		status: networkmanager.Status{
			Connectivity: networkmanager.ConnectivityLimited,
			Device: networkmanager.Device{
				Managed:       true,
				State:         networkmanager.DeviceStateActivated,
				ActiveUUID:    "office",
				IPv4Addresses: []string{"192.0.2.10"},
			},
		},
		profiles: []networkmanager.Profile{
			{
				UUID:        "office",
				Interface:   "wlan0",
				Mode:        "infrastructure",
				Autoconnect: true,
			},
			{
				UUID:        "standalone",
				Interface:   "wlan0",
				Role:        networkmanager.RoleStandalone,
				Mode:        "ap",
				Autoconnect: false,
			},
			{
				UUID:        "other-interface",
				Interface:   "wlan1",
				Mode:        "infrastructure",
				Autoconnect: true,
			},
		},
	}

	observer := NewNetworkManagerObserver(client, "wlan0")
	snapshot, err := observer.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.ActiveMode != ModeInfrastructure || snapshot.ActiveUUID != "office" {
		t.Fatalf("active snapshot = %#v", snapshot)
	}
	if snapshot.DeviceState != DeviceActivated || !snapshot.Connectivity.HasLocalAddress {
		t.Fatalf("device snapshot = %#v", snapshot)
	}
	if snapshot.Connectivity.Internet != connectivity.InternetLimited {
		t.Fatalf("internet = %q", snapshot.Connectivity.Internet)
	}
	if len(snapshot.Profiles) != 2 {
		t.Fatalf("profiles = %#v, want two wlan0 profiles", snapshot.Profiles)
	}
}

type fakeNetworkManagerClient struct {
	status   networkmanager.Status
	profiles []networkmanager.Profile
}

func (client *fakeNetworkManagerClient) Status(context.Context, string) (networkmanager.Status, error) {
	return client.status, nil
}

func (client *fakeNetworkManagerClient) Profiles(context.Context) ([]networkmanager.Profile, error) {
	return client.profiles, nil
}

func (client *fakeNetworkManagerClient) WatchProperties(
	context.Context,
) (<-chan networkmanager.Event, <-chan error, error) {
	return make(chan networkmanager.Event), make(chan error), nil
}
