package observability

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/flavorplus/onboardd/internal/appliance"
)

func TestNewNotifier(t *testing.T) {
	const pid = 1234
	tests := []struct {
		name             string
		environment      map[string]string
		wantEnabled      bool
		wantWatchdog     time.Duration
		wantErrorContain string
	}{
		{
			name:        "foreground execution is disabled",
			environment: map[string]string{},
		},
		{
			name: "notification without watchdog",
			environment: map[string]string{
				notifySocketEnvironment: "/run/systemd/notify",
			},
			wantEnabled: true,
		},
		{
			name: "watchdog uses half the configured interval",
			environment: map[string]string{
				notifySocketEnvironment: "/run/systemd/notify",
				watchdogPIDEnvironment:  strconv.Itoa(pid),
				watchdogUsecEnvironment: "30000000",
			},
			wantEnabled:  true,
			wantWatchdog: 15 * time.Second,
		},
		{
			name: "watchdog for another process is ignored",
			environment: map[string]string{
				notifySocketEnvironment: "/run/systemd/notify",
				watchdogPIDEnvironment:  strconv.Itoa(pid + 1),
				watchdogUsecEnvironment: "30000000",
			},
			wantEnabled: true,
		},
		{
			name: "invalid socket",
			environment: map[string]string{
				notifySocketEnvironment: "relative.sock",
			},
			wantErrorContain: "NOTIFY_SOCKET",
		},
		{
			name: "invalid watchdog interval",
			environment: map[string]string{
				notifySocketEnvironment: "/run/systemd/notify",
				watchdogUsecEnvironment: "invalid",
			},
			wantErrorContain: "WATCHDOG_USEC",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			notifier, err := newNotifier(mapEnvironment(test.environment), pid)
			if test.wantErrorContain != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrorContain) {
					t.Fatalf("newNotifier() error = %v, want containing %q", err, test.wantErrorContain)
				}
				return
			}
			if err != nil {
				t.Fatalf("newNotifier() error = %v", err)
			}
			if notifier.Enabled() != test.wantEnabled {
				t.Fatalf("Enabled() = %t, want %t", notifier.Enabled(), test.wantEnabled)
			}
			if notifier.watchdogInterval != test.wantWatchdog {
				t.Fatalf(
					"watchdog interval = %s, want %s",
					notifier.watchdogInterval,
					test.wantWatchdog,
				)
			}
		})
	}
}

func TestNotifierRunReportsReadyWatchdogAndStopping(t *testing.T) {
	health := NewHealth()
	messages := make(chan string, 8)
	notifier := &Notifier{
		enabled: true,
		notify: func(message string) error {
			messages <- message
			return nil
		},
	}
	watchdog := make(chan time.Time, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- notifier.run(ctx, health, watchdog)
	}()

	wantMessage(t, messages, "STATUS=Starting")
	wantMessage(t, messages, "STATUS=Starting")
	health.ObserveNetworkState(
		1,
		appliance.StageInfrastructure,
		appliance.ModeInfrastructure,
		appliance.ReasonInfrastructureReady,
	)
	wantMessage(t, messages, "READY=1\nSTATUS=Ready")

	watchdog <- time.Now()
	wantMessage(t, messages, "WATCHDOG=1")
	cancel()
	wantMessage(t, messages, "STOPPING=1\nSTATUS=Stopping")
	if err := <-done; err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

func TestNotifierRunStopsOnDeliveryFailure(t *testing.T) {
	wantErr := errors.New("notification unavailable")
	notifier := &Notifier{
		enabled: true,
		notify: func(string) error {
			return wantErr
		},
	}
	err := notifier.run(context.Background(), NewHealth(), nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("run() error = %v, want wrapping %v", err, wantErr)
	}
}

func TestNotifierSendsUnixDatagram(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "onboardd-notify-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove socket directory: %v", err)
		}
	})
	socket := directory + "/notify.sock"
	listener, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close notification listener: %v", err)
		}
	})

	notifier, err := newNotifier(mapEnvironment(map[string]string{
		notifySocketEnvironment: socket,
	}), os.Getpid())
	if err != nil {
		t.Fatalf("newNotifier() error = %v", err)
	}
	if err := notifier.notify("READY=1"); err != nil {
		t.Fatalf("notify() error = %v", err)
	}
	buffer := make([]byte, 64)
	if err := listener.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	count, _, err := listener.ReadFromUnix(buffer)
	if err != nil {
		t.Fatalf("read notification: %v", err)
	}
	if got := string(buffer[:count]); got != "READY=1" {
		t.Fatalf("notification = %q, want READY=1", got)
	}
}

func mapEnvironment(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func wantMessage(t *testing.T, messages <-chan string, want string) {
	t.Helper()
	select {
	case got := <-messages:
		if got != want {
			t.Fatalf("notification = %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for notification %q", want)
	}
}
