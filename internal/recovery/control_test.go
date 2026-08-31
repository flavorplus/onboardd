package recovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestControlServerAcceptsRecoveryRequest(t *testing.T) {
	path := shortSocketPath(t)
	requests := NewRequests()
	server, err := StartControlServer(path, requests)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if permission := info.Mode().Perm(); permission != 0o600 {
		t.Fatalf("socket permission = %04o, want 0600", permission)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	if err := RequestControl(context.Background(), path); err != nil {
		t.Fatalf("RequestControl() error = %v", err)
	}
	if !requests.Pending() {
		t.Fatal("control request is not pending")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket remains after shutdown: %v", err)
	}
}

func TestControlServerCoalescesRepeatedRequests(t *testing.T) {
	path := shortSocketPath(t)
	requests := NewRequests()
	server, err := StartControlServer(path, requests)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()

	if err := RequestControl(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if err := RequestControl(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	select {
	case <-requests.Notifications():
	default:
		t.Fatal("request notification missing")
	}
	select {
	case <-requests.Notifications():
		t.Fatal("duplicate notification delivered")
	default:
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestStartControlServerRejectsActiveSocket(t *testing.T) {
	path := shortSocketPath(t)
	server, err := StartControlServer(path, NewRequests())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	_, err = StartControlServer(path, NewRequests())
	if err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("StartControlServer() error = %v", err)
	}
}

func TestRequestControlHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	err := RequestControl(ctx, filepath.Join(t.TempDir(), "missing.sock"))
	if err == nil {
		t.Fatal("RequestControl() unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("canceled request took %s", elapsed)
	}
}

func shortSocketPath(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "onboardd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove socket directory: %v", err)
		}
	})
	return filepath.Join(directory, "control.sock")
}

func TestRequestsCoalesceUntilCompleted(t *testing.T) {
	requests := NewRequests()
	if !requests.Request() {
		t.Fatal("first request was not accepted")
	}
	if requests.Request() {
		t.Fatal("duplicate request was not coalesced")
	}
	if !requests.Pending() {
		t.Fatal("request is not pending")
	}
	select {
	case <-requests.Notifications():
	default:
		t.Fatal("request notification was not delivered")
	}
	select {
	case <-requests.Notifications():
		t.Fatal("duplicate notification was delivered")
	default:
	}

	requests.Complete()
	if requests.Pending() {
		t.Fatal("completed request remained pending")
	}
	if !requests.Request() {
		t.Fatal("new request after completion was not accepted")
	}
}
