package recovery

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// ButtonOptions identifies one active-low recovery button and its fixed input
// policy. ChipPath and Line come from product configuration; hold and debounce
// timings are internal appliance policy.
type ButtonOptions struct {
	ChipPath string
	Line     uint32
	Hold     time.Duration
	Debounce time.Duration
}

type buttonEvent uint8

const (
	buttonEventUnknown buttonEvent = iota
	buttonEventPressed
	buttonEventReleased
)

type buttonStream struct {
	events <-chan buttonEvent
	errors <-chan error
	done   <-chan struct{}
}

type buttonSource interface {
	InitialPressed() bool
	Run(context.Context) (buttonStream, error)
	Close() error
}

type buttonTimer interface {
	C() <-chan time.Time
	Stop() bool
}

type buttonClock interface {
	NewTimer(time.Duration) buttonTimer
}

// Button converts a debounced long press into the same coalesced request used
// by the local manual-recovery command.
type Button struct {
	source   buttonSource
	requests requestAcceptor
	options  ButtonOptions
	clock    buttonClock
}

// StartButton requests the configured GPIO line before returning so an invalid
// or busy pin prevents the appliance from announcing recovery readiness.
func StartButton(requests requestAcceptor, options ButtonOptions) (*Button, error) {
	if err := validateButtonOptions(requests, options); err != nil {
		return nil, err
	}
	source, err := openButtonSource(options)
	if err != nil {
		return nil, err
	}
	return &Button{
		source:   source,
		requests: requests,
		options:  options,
		clock:    systemButtonClock{},
	}, nil
}

func newButton(
	source buttonSource,
	requests requestAcceptor,
	options ButtonOptions,
) (*Button, error) {
	if source == nil {
		return nil, errors.New("button event source is required")
	}
	if err := validateButtonOptions(requests, options); err != nil {
		return nil, err
	}
	return &Button{
		source:   source,
		requests: requests,
		options:  options,
		clock:    systemButtonClock{},
	}, nil
}

func validateButtonOptions(requests requestAcceptor, options ButtonOptions) error {
	if requests == nil {
		return errors.New("recovery request acceptor is required")
	}
	if !filepath.IsAbs(options.ChipPath) {
		return errors.New("gpio chip path must be absolute")
	}
	if options.Hold <= 0 {
		return errors.New("gpio recovery hold duration must be positive")
	}
	if options.Debounce <= 0 {
		return errors.New("gpio recovery debounce duration must be positive")
	}
	return nil
}

// Run monitors the button until cancellation or an input failure.
func (b *Button) Run(ctx context.Context) (result error) {
	runContext, cancel := context.WithCancel(ctx)
	stream, err := b.source.Run(runContext)
	if err != nil {
		cancel()
		closeErr := b.source.Close()
		if closeErr != nil {
			closeErr = fmt.Errorf("close gpio recovery input: %w", closeErr)
		}
		return errors.Join(
			fmt.Errorf("start gpio recovery input: %w", err),
			closeErr,
		)
	}
	defer func() {
		cancel()
		closeErr := b.source.Close()
		<-stream.done
		if closeErr != nil {
			result = errors.Join(result, fmt.Errorf("close gpio recovery input: %w", closeErr))
		}
	}()

	isPressed := b.source.InitialPressed()
	isTriggered := false
	var holdTimer buttonTimer
	var hold <-chan time.Time
	if isPressed {
		holdTimer = b.clock.NewTimer(b.options.Hold)
		hold = holdTimer.C()
	}
	defer func() {
		if holdTimer != nil {
			holdTimer.Stop()
		}
	}()

	for stream.events != nil || stream.errors != nil {
		select {
		case <-runContext.Done():
			return nil
		case event, ok := <-stream.events:
			if !ok {
				stream.events = nil
				continue
			}
			switch event {
			case buttonEventPressed:
				if isPressed {
					continue
				}
				isPressed = true
				isTriggered = false
				holdTimer = b.clock.NewTimer(b.options.Hold)
				hold = holdTimer.C()
			case buttonEventReleased:
				isPressed = false
				isTriggered = false
				if holdTimer != nil {
					holdTimer.Stop()
				}
				holdTimer = nil
				hold = nil
			default:
				return fmt.Errorf("unsupported gpio button event %d", event)
			}
		case sourceErr, ok := <-stream.errors:
			if !ok {
				stream.errors = nil
				continue
			}
			if sourceErr != nil {
				return fmt.Errorf("monitor gpio recovery input: %w", sourceErr)
			}
		case <-hold:
			holdTimer = nil
			hold = nil
			if isPressed && !isTriggered {
				b.requests.Request()
				isTriggered = true
			}
		}
	}
	if runContext.Err() != nil {
		return nil
	}
	return errors.New("gpio recovery input stopped unexpectedly")
}

// Close releases a button created but not yet run. It is safe to call more than
// once when the platform source implements its required idempotent close.
func (b *Button) Close() error {
	return b.source.Close()
}

type systemButtonClock struct{}

func (systemButtonClock) NewTimer(duration time.Duration) buttonTimer {
	return systemButtonTimer{Timer: time.NewTimer(duration)}
}

type systemButtonTimer struct {
	*time.Timer
}

func (timer systemButtonTimer) C() <-chan time.Time { return timer.Timer.C }
