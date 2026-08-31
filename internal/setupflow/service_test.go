package setupflow

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

func TestServiceRunsKnownNetworkByServerResolvedName(t *testing.T) {
	const uuid = "0a3aeac5-3e46-4f46-b9b0-99b2f83d4cb1"
	backend := newFakeBackend()
	backend.knownNetworks = []KnownNetwork{{
		UUID: uuid, SSID: "Workshop", Managed: true, CanConnect: true,
	}}
	service, err := NewService(context.Background(), backend, Capabilities{Network: true})
	if err != nil {
		t.Fatal(err)
	}

	operation, err := service.StartKnownNetwork(context.Background(), uuid)
	if err != nil {
		t.Fatalf("StartKnownNetwork() error = %v", err)
	}
	if operation.Network != "Workshop" || operation.State != OperationPending {
		t.Fatalf("operation = %#v", operation)
	}
	if !service.BeginOperation(operation.ID) {
		t.Fatal("BeginOperation() = false")
	}
	close(backend.release)
	waitForOperation(t, service, operation.ID, OperationSucceeded)
	if backend.knownUUID != uuid {
		t.Fatalf("known network UUID = %q", backend.knownUUID)
	}
}

func TestServiceRefusesReadOnlyKnownNetwork(t *testing.T) {
	backend := newFakeBackend()
	backend.knownNetworks = []KnownNetwork{{
		UUID: "system", SSID: "System Wi-Fi", Managed: false, CanConnect: false,
	}}
	service, err := NewService(context.Background(), backend, Capabilities{Network: true})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.StartKnownNetwork(context.Background(), "system")
	var public *PublicError
	if !errors.As(err, &public) || public.Failure.Code != "network_read_only" {
		t.Fatalf("StartKnownNetwork() error = %v", err)
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
	err = service.ForgetKnownNetwork(context.Background(), "profile")
	if !errors.As(err, &conflict) || conflict.Operation.ID != first.ID {
		t.Fatalf("ForgetKnownNetwork() error = %v, want operation conflict", err)
	}
	close(backend.release)
	waitForOperation(t, service, first.ID, OperationSucceeded)
}

func TestServiceShutdownCancelsAndWaitsForActiveOperation(t *testing.T) {
	backend := newFakeBackend()
	backend.waitForCancellation = true
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
	<-backend.startedSignal

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		shutdownDone <- service.Shutdown(shutdownContext)
	}()
	<-backend.canceledSignal
	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown() returned before rollback completed: %v", err)
	default:
	}

	close(backend.release)
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	completed := waitForOperation(t, service, operation.ID, OperationFailed)
	if completed.Failure == nil || completed.Failure.Code != "operation_interrupted" {
		t.Fatalf("completed operation = %#v", completed)
	}
	if _, err := service.StartConnection(ConnectionRequest{SSID: "Other"}); err == nil {
		t.Fatal("StartConnection() after shutdown error = nil")
	}
}

func TestServiceShutdownCanRetryWaitAfterDeadline(t *testing.T) {
	backend := newFakeBackend()
	backend.waitForCancellation = true
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
	<-backend.startedSignal

	deadlineContext, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Shutdown(deadlineContext); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Shutdown() error = %v, want context cancellation", err)
	}
	<-backend.canceledSignal
	close(backend.release)
	if err := service.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown() error = %v", err)
	}
}

func TestServiceCancelPendingOperationReleasesTransitionSlot(t *testing.T) {
	backend := newFakeBackend()
	service, err := NewService(context.Background(), backend, Capabilities{Network: true})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := service.StartConnection(ConnectionRequest{SSID: "Office"})
	if err != nil {
		t.Fatal(err)
	}
	if !service.CancelPendingOperation(operation.ID) {
		t.Fatal("CancelPendingOperation() = false")
	}
	if backend.started {
		t.Fatal("backend started for canceled pending operation")
	}
	completed := waitForOperation(t, service, operation.ID, OperationFailed)
	if completed.Failure == nil || completed.Failure.Code != "operation_interrupted" {
		t.Fatalf("completed operation = %#v", completed)
	}
	if _, err := service.StartConnection(ConnectionRequest{SSID: "Other"}); err != nil {
		t.Fatalf("next StartConnection() error = %v", err)
	}
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

func TestServiceOrdersAndForgetsKnownNetworks(t *testing.T) {
	backend := newFakeBackend()
	backend.knownNetworks = []KnownNetwork{
		{UUID: "system", SSID: "Office", Automatic: true},
		{UUID: "managed", SSID: "Office", Managed: true, CanForget: true},
		{UUID: "active", SSID: "Workshop", Managed: true, Active: true, Automatic: true},
	}
	service, err := NewService(context.Background(), backend, Capabilities{Network: true})
	if err != nil {
		t.Fatal(err)
	}

	known, err := service.KnownNetworks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(known) != 3 || known[0].UUID != "active" ||
		known[1].UUID != "managed" || known[2].UUID != "system" {
		t.Fatalf("KnownNetworks() = %#v", known)
	}
	if err := service.ForgetKnownNetwork(context.Background(), "managed"); err != nil {
		t.Fatal(err)
	}
	if backend.forgottenUUID != "managed" {
		t.Fatalf("forgotten uuid = %q", backend.forgottenUUID)
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
	if _, err := standaloneOnly.KnownNetworks(context.Background()); !isModeUnavailable(err) {
		t.Fatalf("standalone-only known networks error = %v", err)
	}
	if err := standaloneOnly.ForgetKnownNetwork(context.Background(), "profile"); !isModeUnavailable(err) {
		t.Fatalf("standalone-only forget network error = %v", err)
	}
	if _, err := standaloneOnly.StartConnection(ConnectionRequest{SSID: "Office"}); !isModeUnavailable(err) {
		t.Fatalf("standalone-only connection error = %v", err)
	}
	if _, err := standaloneOnly.StartKnownNetwork(context.Background(), "profile"); !isModeUnavailable(err) {
		t.Fatalf("standalone-only known connection error = %v", err)
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
	mu                  sync.Mutex
	mode                Mode
	networks            []Network
	knownNetworks       []KnownNetwork
	forgottenUUID       string
	knownUUID           string
	connection          ConnectionRequest
	connectErr          error
	release             chan struct{}
	started             bool
	waitForCancellation bool
	startedSignal       chan struct{}
	canceledSignal      chan struct{}
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		mode:           ModeSetup,
		release:        make(chan struct{}),
		startedSignal:  make(chan struct{}),
		canceledSignal: make(chan struct{}),
	}
}

func (backend *fakeBackend) CurrentMode(context.Context) (Mode, error) { return backend.mode, nil }

func (backend *fakeBackend) Networks(context.Context) ([]Network, error) {
	return append([]Network(nil), backend.networks...), nil
}

func (backend *fakeBackend) KnownNetworks(context.Context) ([]KnownNetwork, error) {
	return append([]KnownNetwork(nil), backend.knownNetworks...), nil
}

func (backend *fakeBackend) ForgetKnownNetwork(_ context.Context, uuid string) error {
	backend.forgottenUUID = uuid
	return nil
}

func (backend *fakeBackend) Connect(ctx context.Context, request ConnectionRequest) error {
	backend.mu.Lock()
	backend.connection = request
	backend.started = true
	backend.mu.Unlock()
	close(backend.startedSignal)
	if backend.waitForCancellation {
		<-ctx.Done()
		close(backend.canceledSignal)
	}
	<-backend.release
	if backend.waitForCancellation {
		return ctx.Err()
	}
	return backend.connectErr
}

func (backend *fakeBackend) ConnectKnownNetwork(ctx context.Context, uuid string) error {
	backend.mu.Lock()
	backend.knownUUID = uuid
	backend.started = true
	backend.mu.Unlock()
	close(backend.startedSignal)
	if backend.waitForCancellation {
		<-ctx.Done()
		close(backend.canceledSignal)
	}
	<-backend.release
	if backend.waitForCancellation {
		return ctx.Err()
	}
	return backend.connectErr
}

func (backend *fakeBackend) Standalone(context.Context) error {
	<-backend.release
	return nil
}
