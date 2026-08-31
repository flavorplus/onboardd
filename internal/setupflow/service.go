package setupflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const operationHistoryLimit = 10

// Backend supplies platform behavior while Service owns the browser-visible workflow.
type Backend interface {
	CurrentMode(context.Context) (Mode, error)
	Networks(context.Context) ([]Network, error)
	KnownNetworks(context.Context) ([]KnownNetwork, error)
	ForgetKnownNetwork(context.Context, string) error
	Connect(context.Context, ConnectionRequest) error
	ConnectKnownNetwork(context.Context, string) error
	Standalone(context.Context) error
}

// Service coordinates setup reads and asynchronous, single-flight mode changes.
type Service struct {
	ctx          context.Context
	cancel       context.CancelFunc
	backend      Backend
	capabilities Capabilities

	mu             sync.RWMutex
	operations     map[string]*Operation
	actions        map[string]func(context.Context) error
	order          []string
	isForgetting   bool
	isShuttingDown bool
	operationWG    sync.WaitGroup
	shutdownOnce   sync.Once
	shutdownDone   chan struct{}
}

// NewService creates a setup service whose operations live until ctx is cancelled.
func NewService(ctx context.Context, backend Backend, capabilities Capabilities) (*Service, error) {
	if ctx == nil {
		return nil, errors.New("setup context is required")
	}
	if backend == nil {
		return nil, errors.New("setup backend is required")
	}
	if !capabilities.Network && !capabilities.Standalone {
		return nil, errors.New("at least one setup mode must be enabled")
	}
	operationContext, cancel := context.WithCancel(ctx)
	return &Service{
		ctx:          operationContext,
		cancel:       cancel,
		backend:      backend,
		capabilities: capabilities,
		operations:   make(map[string]*Operation),
		actions:      make(map[string]func(context.Context) error),
		shutdownDone: make(chan struct{}),
	}, nil
}

// Bootstrap returns current durable intent and the most recent operation.
func (service *Service) Bootstrap(ctx context.Context) (Bootstrap, error) {
	mode, err := service.backend.CurrentMode(ctx)
	if err != nil {
		return Bootstrap{}, err
	}
	return Bootstrap{
		Capabilities: service.capabilities,
		CurrentMode:  mode,
		Operation:    service.latestOperation(),
	}, nil
}

// Networks returns visible networks ordered strongest first and then by SSID.
func (service *Service) Networks(ctx context.Context) ([]Network, error) {
	if !service.capabilities.Network {
		return nil, errModeUnavailableNetwork
	}
	networks, err := service.backend.Networks(ctx)
	if err != nil {
		return nil, err
	}
	unique := make(map[string]Network, len(networks))
	for _, network := range networks {
		if network.SSID == "" {
			continue
		}
		key := network.SSID + "\x00" + network.Security
		current, exists := unique[key]
		if !exists || network.Strength > current.Strength {
			unique[key] = network
		}
	}
	networks = networks[:0]
	for _, network := range unique {
		networks = append(networks, network)
	}
	sort.SliceStable(networks, func(left, right int) bool {
		if networks[left].Strength == networks[right].Strength {
			return networks[left].SSID < networks[right].SSID
		}
		return networks[left].Strength > networks[right].Strength
	})
	return networks, nil
}

// KnownNetworks returns saved Wi-Fi profiles ordered by current use, name, and
// ownership. Profiles managed outside onboardd remain visible but read-only.
func (service *Service) KnownNetworks(ctx context.Context) ([]KnownNetwork, error) {
	if !service.capabilities.Network {
		return nil, errModeUnavailableNetwork
	}
	known, err := service.backend.KnownNetworks(ctx)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(known, func(left, right int) bool {
		if known[left].Active != known[right].Active {
			return known[left].Active
		}
		if known[left].SSID != known[right].SSID {
			return known[left].SSID < known[right].SSID
		}
		if known[left].Managed != known[right].Managed {
			return known[left].Managed
		}
		return known[left].UUID < known[right].UUID
	})
	return known, nil
}

// ForgetKnownNetwork removes one inactive onboardd-owned infrastructure profile.
func (service *Service) ForgetKnownNetwork(ctx context.Context, uuid string) error {
	if !service.capabilities.Network {
		return errModeUnavailableNetwork
	}
	service.mu.Lock()
	if service.unavailableLocked() {
		service.mu.Unlock()
		return serviceStoppingError()
	}
	active := service.activeOperationLocked()
	if active != nil {
		operation := cloneOperation(active)
		service.mu.Unlock()
		return &ConflictError{Operation: operation}
	}
	if service.isForgetting {
		service.mu.Unlock()
		return errProfileChangeInProgress
	}
	service.isForgetting = true
	service.mu.Unlock()
	defer func() {
		service.mu.Lock()
		service.isForgetting = false
		service.mu.Unlock()
	}()
	return service.backend.ForgetKnownNetwork(ctx, uuid)
}

// StartConnection reserves the transition slot. BeginOperation starts the radio
// change after the HTTP layer has flushed the accepted response.
func (service *Service) StartConnection(request ConnectionRequest) (Operation, error) {
	if !service.capabilities.Network {
		return Operation{}, errModeUnavailableNetwork
	}
	return service.start(OperationConnect, request.SSID, func(ctx context.Context) error {
		return service.backend.Connect(ctx, request)
	})
}

// StartKnownNetwork resolves the server-owned display name, then reserves the same
// protected infrastructure transition slot used for newly entered credentials.
func (service *Service) StartKnownNetwork(ctx context.Context, uuid string) (Operation, error) {
	if !service.capabilities.Network {
		return Operation{}, errModeUnavailableNetwork
	}
	known, err := service.KnownNetworks(ctx)
	if err != nil {
		return Operation{}, err
	}
	for _, network := range known {
		if network.UUID != uuid {
			continue
		}
		if network.Active {
			return Operation{}, errActiveNetwork
		}
		if !network.CanConnect {
			return Operation{}, errNetworkReadOnly
		}
		return service.start(OperationConnect, network.SSID, func(ctx context.Context) error {
			return service.backend.ConnectKnownNetwork(ctx, uuid)
		})
	}
	return Operation{}, errKnownNetworkNotFound
}

// StartStandalone reserves the transition slot for server-configured standalone mode.
func (service *Service) StartStandalone() (Operation, error) {
	if !service.capabilities.Standalone {
		return Operation{}, errModeUnavailableStandalone
	}
	return service.start(OperationStandalone, "", service.backend.Standalone)
}

// Operation returns a credential-free snapshot by ID.
func (service *Service) Operation(id string) (Operation, bool) {
	service.mu.RLock()
	defer service.mu.RUnlock()
	operation, ok := service.operations[id]
	if !ok {
		return Operation{}, false
	}
	return cloneOperation(operation), true
}

// BeginOperation starts one reserved operation exactly once. The HTTP layer calls it
// only after flushing the 202 response, so radio disruption cannot overtake acceptance.
func (service *Service) BeginOperation(id string) bool {
	service.mu.Lock()
	operation, exists := service.operations[id]
	action, queued := service.actions[id]
	if !exists || !queued || operation.State != OperationPending {
		service.mu.Unlock()
		return false
	}
	if service.unavailableLocked() {
		service.cancelPendingOperationLocked(id)
		service.mu.Unlock()
		return false
	}
	operation.State = OperationRunning
	delete(service.actions, id)
	service.operationWG.Add(1)
	service.mu.Unlock()

	go service.run(id, action)
	return true
}

// CancelPendingOperation releases a reserved transition that could not be
// acknowledged to its caller. No backend work is started.
func (service *Service) CancelPendingOperation(id string) bool {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.cancelPendingOperationLocked(id)
}

// Shutdown prevents new mutations, cancels active work, and waits for every
// protected transition (including rollback) to return. A timed-out wait can be
// retried with a fresh context.
func (service *Service) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("shutdown context is required")
	}
	service.shutdownOnce.Do(func() {
		service.mu.Lock()
		service.isShuttingDown = true
		for id := range service.actions {
			service.cancelPendingOperationLocked(id)
		}
		service.cancel()
		service.mu.Unlock()
		go func() {
			service.operationWG.Wait()
			close(service.shutdownDone)
		}()
	})
	select {
	case <-service.shutdownDone:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for setup operations to stop: %w", ctx.Err())
	}
}

func (service *Service) start(
	kind OperationKind,
	network string,
	action func(context.Context) error,
) (Operation, error) {
	service.mu.Lock()
	if service.unavailableLocked() {
		service.mu.Unlock()
		return Operation{}, serviceStoppingError()
	}
	if service.isForgetting {
		service.mu.Unlock()
		return Operation{}, errProfileChangeInProgress
	}
	if active := service.activeOperationLocked(); active != nil {
		operation := cloneOperation(active)
		service.mu.Unlock()
		return Operation{}, &ConflictError{Operation: operation}
	}
	id, err := newOperationID()
	if err != nil {
		service.mu.Unlock()
		return Operation{}, err
	}
	operation := &Operation{
		ID:        id,
		Kind:      kind,
		State:     OperationPending,
		Network:   network,
		CreatedAt: time.Now().UTC(),
	}
	service.operations[id] = operation
	service.actions[id] = action
	service.order = append(service.order, id)
	service.trimHistoryLocked()
	snapshot := cloneOperation(operation)
	service.mu.Unlock()

	return snapshot, nil
}

func (service *Service) run(id string, action func(context.Context) error) {
	defer service.operationWG.Done()
	err := action(service.ctx)
	finished := time.Now().UTC()
	service.mu.Lock()
	defer service.mu.Unlock()
	operation, exists := service.operations[id]
	if !exists {
		return
	}
	operation.FinishedAt = &finished
	if err == nil {
		operation.State = OperationSucceeded
		return
	}
	operation.State = OperationFailed
	operation.Failure = publicFailure(err)
}

func (service *Service) cancelPendingOperationLocked(id string) bool {
	operation, exists := service.operations[id]
	if !exists || operation.State != OperationPending {
		return false
	}
	if _, queued := service.actions[id]; !queued {
		return false
	}
	finished := time.Now().UTC()
	operation.State = OperationFailed
	operation.FinishedAt = &finished
	operation.Failure = interruptedFailure()
	delete(service.actions, id)
	return true
}

func (service *Service) unavailableLocked() bool {
	return service.isShuttingDown || service.ctx.Err() != nil
}

func (service *Service) latestOperation() *Operation {
	service.mu.RLock()
	defer service.mu.RUnlock()
	if len(service.order) == 0 {
		return nil
	}
	operation := cloneOperation(service.operations[service.order[len(service.order)-1]])
	return &operation
}

func (service *Service) activeOperationLocked() *Operation {
	for index := len(service.order) - 1; index >= 0; index-- {
		operation := service.operations[service.order[index]]
		if operation.State == OperationPending || operation.State == OperationRunning {
			return operation
		}
	}
	return nil
}

func (service *Service) trimHistoryLocked() {
	for len(service.order) > operationHistoryLimit {
		oldestID := service.order[0]
		oldest := service.operations[oldestID]
		if oldest.State == OperationPending || oldest.State == OperationRunning {
			return
		}
		delete(service.operations, oldestID)
		delete(service.actions, oldestID)
		service.order = service.order[1:]
	}
}

func cloneOperation(operation *Operation) Operation {
	clone := *operation
	if operation.Failure != nil {
		failure := *operation.Failure
		clone.Failure = &failure
	}
	if operation.FinishedAt != nil {
		finished := *operation.FinishedAt
		clone.FinishedAt = &finished
	}
	return clone
}

func publicFailure(err error) *Failure {
	var public *PublicError
	if errors.As(err, &public) {
		failure := public.Failure
		return &failure
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return interruptedFailure()
	}
	return &Failure{
		Code:    "internal_failure",
		Message: "The network change could not be completed. Please try again.",
	}
}

func interruptedFailure() *Failure {
	return &Failure{
		Code:    "operation_interrupted",
		Message: "The network change was interrupted. Please try again.",
	}
}

func serviceStoppingError() error {
	return NewPublicError(
		"service_stopping",
		"Setup is stopping. Start it again before changing the network.",
	)
}

func newOperationID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
