package captive

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHTTPServiceRestartsFailedListener(t *testing.T) {
	listened := make(chan struct{}, 2)
	listeners := []net.Listener{
		&failingListener{err: errors.New("accept failed")},
		newMemoryListener(),
	}
	listenCalls := 0
	var attempts []int
	var recovered []int
	service, err := StartHTTPService(
		context.Background(),
		func(context.Context, string, string) (net.Listener, error) {
			listener := listeners[listenCalls]
			listenCalls++
			listened <- struct{}{}
			return listener, nil
		},
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		HTTPServiceOptions{
			Network:         "tcp4",
			Address:         "0.0.0.0:18080",
			MaxRestarts:     2,
			RestartDelay:    time.Second,
			ShutdownTimeout: time.Second,
			OnRetry: func(_ context.Context, attempt, maximum int) {
				if maximum != 2 {
					t.Errorf("maximum = %d, want 2", maximum)
				}
				attempts = append(attempts, attempt)
			},
			OnRecovered: func(_ context.Context, attempt int) {
				recovered = append(recovered, attempt)
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	service.waiter = &serviceFakeWaiter{}
	<-listened

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	<-listened
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if listenCalls != 2 {
		t.Fatalf("listen calls = %d, want 2", listenCalls)
	}
	if len(attempts) != 1 || attempts[0] != 1 {
		t.Fatalf("retry attempts = %v, want [1]", attempts)
	}
	if len(recovered) != 1 || recovered[0] != 1 {
		t.Fatalf("recoveries = %v, want [1]", recovered)
	}
}

func TestHTTPServiceReportsExhaustedRecoveryBudget(t *testing.T) {
	listenCalls := 0
	service, err := StartHTTPService(
		context.Background(),
		func(context.Context, string, string) (net.Listener, error) {
			listenCalls++
			return &failingListener{err: errors.New("accept failed")}, nil
		},
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		HTTPServiceOptions{
			Network:         "tcp4",
			Address:         "0.0.0.0:18080",
			MaxRestarts:     2,
			RestartDelay:    time.Second,
			ShutdownTimeout: time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	service.waiter = &serviceFakeWaiter{}

	err = service.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "recovery exhausted after 2 restarts") ||
		!strings.Contains(err.Error(), "accept failed") {
		t.Fatalf("Run() error = %v", err)
	}
	if listenCalls != 3 {
		t.Fatalf("listen calls = %d, want 3", listenCalls)
	}
}

func TestHTTPServiceRecoversFromRebindFailure(t *testing.T) {
	listened := make(chan struct{}, 3)
	listenCalls := 0
	service, err := StartHTTPService(
		context.Background(),
		func(context.Context, string, string) (net.Listener, error) {
			listenCalls++
			listened <- struct{}{}
			switch listenCalls {
			case 1:
				return &failingListener{err: errors.New("accept failed")}, nil
			case 2:
				return nil, errors.New("address temporarily unavailable")
			default:
				return newMemoryListener(), nil
			}
		},
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		HTTPServiceOptions{
			Network:         "tcp4",
			Address:         "0.0.0.0:18080",
			MaxRestarts:     3,
			RestartDelay:    time.Second,
			ShutdownTimeout: time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	service.waiter = &serviceFakeWaiter{}
	<-listened

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	<-listened
	<-listened
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if listenCalls != 3 {
		t.Fatalf("listen calls = %d, want 3", listenCalls)
	}
}

func TestHTTPServiceCancellationInterruptsRestartDelay(t *testing.T) {
	service, err := StartHTTPService(
		context.Background(),
		func(context.Context, string, string) (net.Listener, error) {
			return &failingListener{err: errors.New("accept failed")}, nil
		},
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		HTTPServiceOptions{
			Network:         "tcp4",
			Address:         "0.0.0.0:18080",
			MaxRestarts:     2,
			RestartDelay:    time.Minute,
			ShutdownTimeout: time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	waiter := &serviceBlockingWaiter{started: make(chan struct{})}
	service.waiter = waiter

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	<-waiter.started
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestStartHTTPServiceValidatesPolicy(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	valid := HTTPServiceOptions{
		Network:         "tcp4",
		Address:         "0.0.0.0:18080",
		MaxRestarts:     2,
		RestartDelay:    time.Second,
		ShutdownTimeout: time.Second,
	}
	if _, err := StartHTTPService(context.Background(), nil, handler, valid); err == nil {
		t.Fatal("nil listen function unexpectedly accepted")
	}
	if _, err := StartHTTPService(
		context.Background(),
		func(context.Context, string, string) (net.Listener, error) {
			return newMemoryListener(), nil
		},
		handler,
		HTTPServiceOptions{},
	); err == nil {
		t.Fatal("empty policy unexpectedly accepted")
	}
}

type serviceFakeWaiter struct{}

func (*serviceFakeWaiter) Wait(context.Context, time.Duration) error { return nil }

type serviceBlockingWaiter struct {
	started chan struct{}
}

func (waiter *serviceBlockingWaiter) Wait(ctx context.Context, _ time.Duration) error {
	close(waiter.started)
	<-ctx.Done()
	return ctx.Err()
}
