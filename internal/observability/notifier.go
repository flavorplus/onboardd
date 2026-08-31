// Package systemd connects onboardd's transport-neutral health state to the
// service manager notification protocol.
package observability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	notifySocketEnvironment = "NOTIFY_SOCKET"
	watchdogPIDEnvironment  = "WATCHDOG_PID"
	watchdogUsecEnvironment = "WATCHDOG_USEC"
)

// Notifier reports readiness, normalized status, and watchdog heartbeats when
// onboardd was launched by a notification-aware service manager. It is disabled
// when NOTIFY_SOCKET is absent, preserving ordinary foreground execution.
type Notifier struct {
	enabled          bool
	watchdogInterval time.Duration
	notify           func(string) error
}

// NewNotifier reads the service-manager environment once, before onboardd starts
// concurrent runtime components. Invalid notification settings fail startup instead
// of silently advertising a watchdog contract that the process cannot honor.
func NewNotifier() (*Notifier, error) {
	return newNotifier(os.Getenv, os.Getpid())
}

func newNotifier(getenv func(string) string, pid int) (*Notifier, error) {
	socket := getenv(notifySocketEnvironment)
	if socket == "" {
		return &Notifier{}, nil
	}
	if !strings.HasPrefix(socket, "/") && !strings.HasPrefix(socket, "@") {
		return nil, errors.New("NOTIFY_SOCKET must be an absolute or abstract Unix socket")
	}

	interval, err := watchdogInterval(getenv, pid)
	if err != nil {
		return nil, err
	}
	return &Notifier{
		enabled:          true,
		watchdogInterval: interval,
		notify: func(message string) error {
			return send(socket, message)
		},
	}, nil
}

func watchdogInterval(getenv func(string) string, pid int) (time.Duration, error) {
	value := getenv(watchdogUsecEnvironment)
	if value == "" {
		return 0, nil
	}
	configuredPID := getenv(watchdogPIDEnvironment)
	if configuredPID != "" {
		parsedPID, err := strconv.Atoi(configuredPID)
		if err != nil || parsedPID <= 0 {
			return 0, errors.New("WATCHDOG_PID must be a positive process ID")
		}
		if parsedPID != pid {
			return 0, nil
		}
	}

	microseconds, err := strconv.ParseUint(value, 10, 64)
	if err != nil || microseconds == 0 {
		return 0, errors.New("WATCHDOG_USEC must be a positive integer")
	}
	if microseconds > uint64(math.MaxInt64/int64(time.Microsecond)) {
		return 0, errors.New("WATCHDOG_USEC exceeds the supported duration")
	}
	return time.Duration(microseconds) * time.Microsecond / 2, nil
}

// Enabled reports whether service-manager notifications were requested by the
// process environment.
func (notifier *Notifier) Enabled() bool {
	return notifier != nil && notifier.enabled
}

// Run reports health changes until cancellation. A notification delivery failure
// ends the component so the appliance runtime can shut down cleanly and let the
// service manager apply its restart policy.
func (notifier *Notifier) Run(ctx context.Context, health *Health) error {
	if !notifier.Enabled() {
		return nil
	}
	if health == nil {
		return errors.New("systemd notifier requires appliance health")
	}

	var watchdog <-chan time.Time
	var ticker *time.Ticker
	if notifier.watchdogInterval > 0 {
		ticker = time.NewTicker(notifier.watchdogInterval)
		watchdog = ticker.C
		defer ticker.Stop()
	}
	return notifier.run(ctx, health, watchdog)
}

func (notifier *Notifier) run(
	ctx context.Context,
	health *Health,
	watchdog <-chan time.Time,
) error {
	if err := notifier.notify("STATUS=Starting"); err != nil {
		return fmt.Errorf("send systemd startup status: %w", err)
	}
	readySent := false
	for {
		select {
		case <-ctx.Done():
			// A failed stopping notification must not turn an otherwise graceful
			// cancellation into a runtime failure.
			if err := notifier.notify("STOPPING=1\nSTATUS=Stopping"); err != nil {
				return nil
			}
			return nil
		case snapshot := <-health.Changes():
			message := "STATUS=" + statusText(snapshot)
			if snapshot.Ready && !readySent {
				message = "READY=1\n" + message
				readySent = true
			}
			if err := notifier.notify(message); err != nil {
				return fmt.Errorf("send systemd health status: %w", err)
			}
		case <-watchdog:
			if !health.Snapshot().Healthy {
				continue
			}
			if err := notifier.notify("WATCHDOG=1"); err != nil {
				return fmt.Errorf("send systemd watchdog heartbeat: %w", err)
			}
		}
	}
}

func statusText(snapshot Snapshot) string {
	switch snapshot.Status {
	case StatusReady:
		return "Ready"
	case StatusReconciling:
		return "Reconciling network state"
	case StatusRecovering:
		return "Recovering runtime component"
	case StatusStopping:
		return "Stopping"
	case StatusStopped:
		return "Stopped"
	case StatusFailed:
		return "Failed"
	default:
		return "Starting"
	}
}

func send(socket, message string) (result error) {
	if strings.HasPrefix(socket, "@") {
		socket = "\x00" + strings.TrimPrefix(socket, "@")
	}
	connection, err := net.DialUnix(
		"unixgram",
		nil,
		&net.UnixAddr{Name: socket, Net: "unixgram"},
	)
	if err != nil {
		return fmt.Errorf("connect notification socket: %w", err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close notification socket: %w", err))
		}
	}()
	written, err := connection.Write([]byte(message))
	if err != nil {
		return fmt.Errorf("write notification: %w", err)
	}
	if written != len(message) {
		return io.ErrShortWrite
	}
	return nil
}
