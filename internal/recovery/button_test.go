package recovery

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestButtonRequestsRecoveryAfterLongPress(t *testing.T) {
	source := newFakeButtonSource(false)
	requests := NewRequests()
	button := newTestButton(t, source, requests)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- button.Run(ctx) }()

	source.events <- buttonEventPressed
	timer := button.clock.(*fakeButtonClock).next(t)
	timer.fire()
	select {
	case <-requests.Notifications():
	case <-time.After(time.Second):
		t.Fatal("long press did not request recovery")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestButtonIgnoresShortPress(t *testing.T) {
	source := newFakeButtonSource(false)
	requests := NewRequests()
	button := newTestButton(t, source, requests)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- button.Run(ctx) }()

	source.events <- buttonEventPressed
	timer := button.clock.(*fakeButtonClock).next(t)
	source.events <- buttonEventReleased
	<-timer.stopped
	timer.fire()
	select {
	case <-requests.Notifications():
		t.Fatal("short press requested recovery")
	case <-time.After(20 * time.Millisecond):
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestButtonStartsHoldTimerWhenAlreadyPressed(t *testing.T) {
	source := newFakeButtonSource(true)
	requests := NewRequests()
	button := newTestButton(t, source, requests)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- button.Run(ctx) }()

	timer := button.clock.(*fakeButtonClock).next(t)
	timer.fire()
	select {
	case <-requests.Notifications():
	case <-time.After(time.Second):
		t.Fatal("startup hold did not request recovery")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestButtonRequiresReleaseBeforeAnotherHold(t *testing.T) {
	source := newFakeButtonSource(false)
	requests := NewRequests()
	button := newTestButton(t, source, requests)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- button.Run(ctx) }()

	source.events <- buttonEventPressed
	button.clock.(*fakeButtonClock).next(t).fire()
	<-requests.Notifications()
	requests.Complete()
	source.events <- buttonEventPressed
	select {
	case <-button.clock.(*fakeButtonClock).timers:
		t.Fatal("repeated pressed edge started another hold")
	case <-time.After(20 * time.Millisecond):
	}
	source.events <- buttonEventReleased
	source.events <- buttonEventPressed
	button.clock.(*fakeButtonClock).next(t).fire()
	select {
	case <-requests.Notifications():
	case <-time.After(time.Second):
		t.Fatal("second hold did not request recovery")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestButtonReportsInputFailure(t *testing.T) {
	source := newFakeButtonSource(false)
	button := newTestButton(t, source, NewRequests())
	done := make(chan error, 1)
	go func() { done <- button.Run(context.Background()) }()
	source.errors <- errors.New("edge stream failed")
	err := <-done
	if err == nil || err.Error() != "monitor gpio recovery input: edge stream failed" {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestButtonClosesSourceWhenInputCannotStart(t *testing.T) {
	source := newFakeButtonSource(false)
	source.runErr = errors.New("input unavailable")
	button := newTestButton(t, source, NewRequests())

	err := button.Run(context.Background())
	if err == nil || err.Error() != "start gpio recovery input: input unavailable" {
		t.Fatalf("Run() error = %v", err)
	}
	select {
	case <-source.closed:
	default:
		t.Fatal("source was not closed after startup failure")
	}
}

func TestNewButtonValidatesPolicy(t *testing.T) {
	source := newFakeButtonSource(false)
	valid := ButtonOptions{
		ChipPath: "/dev/gpiochip0",
		Line:     17,
		Hold:     time.Second,
		Debounce: time.Millisecond,
	}
	if _, err := newButton(nil, NewRequests(), valid); err == nil {
		t.Fatal("nil event source accepted")
	}
	if _, err := newButton(source, nil, valid); err == nil {
		t.Fatal("nil request acceptor accepted")
	}
	valid.ChipPath = "gpiochip0"
	if _, err := newButton(source, NewRequests(), valid); err == nil {
		t.Fatal("relative chip path accepted")
	}
}

func newTestButton(t *testing.T, source *fakeButtonSource, requests *Requests) *Button {
	t.Helper()
	button, err := newButton(source, requests, ButtonOptions{
		ChipPath: "/dev/gpiochip0",
		Line:     17,
		Hold:     3 * time.Second,
		Debounce: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	button.clock = &fakeButtonClock{timers: make(chan *fakeButtonTimer, 4)}
	return button
}

type fakeButtonSource struct {
	initial bool
	events  chan buttonEvent
	errors  chan error
	closed  chan struct{}
	runErr  error
}

func newFakeButtonSource(initial bool) *fakeButtonSource {
	return &fakeButtonSource{
		initial: initial,
		events:  make(chan buttonEvent, 4),
		errors:  make(chan error, 1),
		closed:  make(chan struct{}),
	}
}

func (source *fakeButtonSource) InitialPressed() bool { return source.initial }

func (source *fakeButtonSource) Run(ctx context.Context) (buttonStream, error) {
	if source.runErr != nil {
		return buttonStream{}, source.runErr
	}
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(done)
	}()
	return buttonStream{events: source.events, errors: source.errors, done: done}, nil
}

func (source *fakeButtonSource) Close() error {
	select {
	case <-source.closed:
	default:
		close(source.closed)
	}
	return nil
}

type fakeButtonClock struct {
	timers chan *fakeButtonTimer
}

func (clock *fakeButtonClock) NewTimer(time.Duration) buttonTimer {
	timer := &fakeButtonTimer{
		ticks:   make(chan time.Time, 1),
		stopped: make(chan struct{}),
	}
	clock.timers <- timer
	return timer
}

func (clock *fakeButtonClock) next(t *testing.T) *fakeButtonTimer {
	t.Helper()
	select {
	case timer := <-clock.timers:
		return timer
	case <-time.After(time.Second):
		t.Fatal("button timer was not created")
		return nil
	}
}

type fakeButtonTimer struct {
	ticks     chan time.Time
	stopped   chan struct{}
	isStopped bool
}

func (timer *fakeButtonTimer) C() <-chan time.Time { return timer.ticks }
func (timer *fakeButtonTimer) Stop() bool {
	if timer.isStopped {
		return false
	}
	timer.isStopped = true
	close(timer.stopped)
	return true
}
func (timer *fakeButtonTimer) fire() {
	if !timer.isStopped {
		timer.ticks <- time.Now()
	}
}
