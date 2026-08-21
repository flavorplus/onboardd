package setup

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestServiceRunsConnectionWithoutRetainingPassword(t *testing.T) {
	backend := newFakeBackend()
	service, err := NewService(context.Background(), backend, Capabilities{Network: true})
	if err != nil {
		t.Fatal(err)
	}

	operation, err := service.StartConnection(ConnectionRequest{
		SSID: "Office", Password: "private-password",
	})
	if err != nil {
		t.Fatalf("StartConnection() error = %v", err)
	}
	if operation.ID == "" || operation.Network != "Office" || operation.State != OperationPending {
		t.Fatalf("operation = %#v", operation)
	}
	if operation.Failure != nil {
		t.Fatalf("operation retained unexpected failure: %#v", operation.Failure)
	}
	if backend.started {
		t.Fatal("backend started before the accepted response could be flushed")
	}
	if !service.BeginOperation(operation.ID) {
		t.Fatal("BeginOperation() = false")
	}

	close(backend.release)
	completed := waitForOperation(t, service, operation.ID, OperationSucceeded)
	if completed.Network != "Office" || completed.Failure != nil {
		t.Fatalf("completed operation = %#v", completed)
	}
	backend.mu.Lock()
	request := backend.connection
	backend.mu.Unlock()
	if request.Password != "private-password" {
		t.Fatalf("backend password = %q", request.Password)
	}
}

func TestServiceRejectsConcurrentOperation(t *testing.T) {
	backend := newFakeBackend()
	service, err := NewService(context.Background(), backend, Capabilities{Network: true, Standalone: true})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.StartConnection(ConnectionRequest{SSID: "Office", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if !service.BeginOperation(first.ID) {
		t.Fatal("BeginOperation() = false")
	}

	_, err = service.StartStandalone()
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("StartStandalone() error = %v, want ConflictError", err)
	}
	if conflict.Operation.ID != first.ID {
		t.Fatalf("conflict operation = %#v, want %s", conflict.Operation, first.ID)
	}
	close(backend.release)
	waitForOperation(t, service, first.ID, OperationSucceeded)
}

func TestServiceMapsOnlyPublicFailures(t *testing.T) {
	for _, test := range []struct {
		name       string
		backendErr error
		wantCode   string
	}{
		{
			name: "public",
			backendErr: NewPublicError(
				"credentials_rejected",
				"The password was not accepted. Check it and try again.",
			),
			wantCode: "credentials_rejected",
		},
		{name: "internal", backendErr: errors.New("secret D-Bus detail"), wantCode: "internal_failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeBackend()
			backend.connectErr = test.backendErr
			service, err := NewService(context.Background(), backend, Capabilities{Network: true})
			if err != nil {
				t.Fatal(err)
			}
			operation, err := service.StartConnection(ConnectionRequest{SSID: "Office"})
			if err != nil {
				t.Fatal(err)
			}
			if !service.BeginOperation(operation.ID) {
				t.Fatal("BeginOperation() = false")
			}
			close(backend.release)
			completed := waitForOperation(t, service, operation.ID, OperationFailed)
			if completed.Failure == nil || completed.Failure.Code != test.wantCode {
				t.Fatalf("failure = %#v, want %q", completed.Failure, test.wantCode)
			}
			if completed.Failure.Message == "secret D-Bus detail" {
				t.Fatal("internal error leaked through operation")
			}
		})
	}
}

func TestServiceBootstrapAndNetworkOrder(t *testing.T) {
	backend := newFakeBackend()
	backend.mode = ModeSetup
	backend.networks = []Network{
		{SSID: "Weak", Strength: 20, Security: "protected"},
		{SSID: "Strong B", Strength: 90, Security: "open"},
		{SSID: "Strong A", Strength: 90, Security: "protected"},
		{SSID: "Strong A", Strength: 40, Security: "protected"},
		{SSID: "", Strength: 100, Security: "protected"},
	}
	service, err := NewService(context.Background(), backend, Capabilities{Network: true, Standalone: true})
	if err != nil {
		t.Fatal(err)
	}

	bootstrap, err := service.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.CurrentMode != ModeSetup || !bootstrap.Capabilities.Standalone || bootstrap.Operation != nil {
		t.Fatalf("bootstrap = %#v", bootstrap)
	}
	networks, err := service.Networks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(networks) != 3 || networks[0].SSID != "Strong A" ||
		networks[1].SSID != "Strong B" || networks[2].SSID != "Weak" {
		t.Fatalf("networks = %#v", networks)
	}
}

func TestNewServiceRequiresEnabledMode(t *testing.T) {
	if _, err := NewService(context.Background(), newFakeBackend(), Capabilities{}); err == nil {
		t.Fatal("NewService() error = nil")
	}
}

func TestServiceEnforcesModeCapabilities(t *testing.T) {
	networkOnly, err := NewService(
		context.Background(),
		newFakeBackend(),
		Capabilities{Network: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := networkOnly.StartStandalone(); !isModeUnavailable(err) {
		t.Fatalf("network-only standalone error = %v", err)
	}

	standaloneOnly, err := NewService(
		context.Background(),
		newFakeBackend(),
		Capabilities{Standalone: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := standaloneOnly.Networks(context.Background()); !isModeUnavailable(err) {
		t.Fatalf("standalone-only networks error = %v", err)
	}
	if _, err := standaloneOnly.StartConnection(ConnectionRequest{SSID: "Office"}); !isModeUnavailable(err) {
		t.Fatalf("standalone-only connection error = %v", err)
	}
}

func isModeUnavailable(err error) bool {
	var public *PublicError
	return errors.As(err, &public) && public.Failure.Code == "mode_unavailable"
}

func waitForOperation(t *testing.T, service *Service, id string, state OperationState) Operation {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		operation, ok := service.Operation(id)
		if !ok {
			t.Fatalf("operation %s disappeared", id)
		}
		if operation.State == state {
			return operation
		}
		select {
		case <-deadline.C:
			t.Fatalf("operation state = %s, want %s", operation.State, state)
		case <-ticker.C:
		}
	}
}

type fakeBackend struct {
	mu         sync.Mutex
	mode       Mode
	networks   []Network
	connection ConnectionRequest
	connectErr error
	release    chan struct{}
	started    bool
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{mode: ModeSetup, release: make(chan struct{})}
}

func (backend *fakeBackend) CurrentMode(context.Context) (Mode, error) { return backend.mode, nil }

func (backend *fakeBackend) Networks(context.Context) ([]Network, error) {
	return append([]Network(nil), backend.networks...), nil
}

func (backend *fakeBackend) Connect(_ context.Context, request ConnectionRequest) error {
	backend.mu.Lock()
	backend.connection = request
	backend.started = true
	backend.mu.Unlock()
	<-backend.release
	return backend.connectErr
}

func (backend *fakeBackend) Standalone(context.Context) error {
	<-backend.release
	return nil
}
