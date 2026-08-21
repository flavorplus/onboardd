package setup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

const operationHistoryLimit = 10

// Backend supplies platform behavior while Service owns the browser-visible workflow.
type Backend interface {
	CurrentMode(context.Context) (Mode, error)
	Networks(context.Context) ([]Network, error)
	Connect(context.Context, ConnectionRequest) error
	Standalone(context.Context) error
}

// Service coordinates setup reads and asynchronous, single-flight mode changes.
type Service struct {
	ctx          context.Context
	backend      Backend
	capabilities Capabilities

	mu         sync.RWMutex
	operations map[string]*Operation
	actions    map[string]func(context.Context) error
	order      []string
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
	return &Service{
		ctx:          ctx,
		backend:      backend,
		capabilities: capabilities,
		operations:   make(map[string]*Operation),
		actions:      make(map[string]func(context.Context) error),
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
		return nil, NewPublicError("mode_unavailable", "Connecting to a Wi-Fi network is not available.")
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

// StartConnection reserves the transition slot. BeginOperation starts the radio
// change after the HTTP layer has flushed the accepted response.
func (service *Service) StartConnection(request ConnectionRequest) (Operation, error) {
	if !service.capabilities.Network {
		return Operation{}, NewPublicError("mode_unavailable", "Connecting to a Wi-Fi network is not available.")
	}
	return service.start(OperationConnect, request.SSID, func(ctx context.Context) error {
		return service.backend.Connect(ctx, request)
	})
}

// StartStandalone reserves the transition slot for server-configured standalone mode.
func (service *Service) StartStandalone() (Operation, error) {
	if !service.capabilities.Standalone {
		return Operation{}, NewPublicError("mode_unavailable", "Standalone mode is not available.")
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
	operation.State = OperationRunning
	delete(service.actions, id)
	service.mu.Unlock()

	go service.run(id, action)
	return true
}

func (service *Service) start(
	kind OperationKind,
	network string,
	action func(context.Context) error,
) (Operation, error) {
	service.mu.Lock()
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
		return &Failure{
			Code:    "operation_interrupted",
			Message: "The network change was interrupted. Please try again.",
		}
	}
	return &Failure{
		Code:    "internal_failure",
		Message: "The network change could not be completed. Please try again.",
	}
}

func newOperationID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
