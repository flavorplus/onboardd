// Package setup defines the product-facing setup workflow without exposing
// NetworkManager or captive-plumbing details to the HTTP layer.
package setup

import "time"

// Mode is the durable or temporary network mode expressed in product language.
type Mode string

const (
	ModeUnknown    Mode = "unknown"
	ModeSetup      Mode = "setup"
	ModeNetwork    Mode = "network"
	ModeStandalone Mode = "standalone"
)

// Capabilities controls which choices the setup experience may offer.
type Capabilities struct {
	Network    bool `json:"network"`
	Standalone bool `json:"standalone"`
}

// Network is one user-selectable Wi-Fi network.
type Network struct {
	SSID     string `json:"ssid"`
	Security string `json:"security"`
	Strength uint8  `json:"strength"`
}

// ConnectionRequest contains credentials for one infrastructure attempt. Password is
// deliberately absent from Operation and every response type.
type ConnectionRequest struct {
	SSID     string
	Password string
	Open     bool
	Hidden   bool
}

// OperationKind identifies the user choice being applied.
type OperationKind string

const (
	OperationConnect    OperationKind = "connect"
	OperationStandalone OperationKind = "standalone"
)

// OperationState is the lifecycle of one asynchronous network change.
type OperationState string

const (
	OperationPending   OperationState = "pending"
	OperationRunning   OperationState = "running"
	OperationSucceeded OperationState = "succeeded"
	OperationFailed    OperationState = "failed"
)

// Failure is safe to return to an untrusted browser.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Operation is the credential-free result retained across browser disconnects.
type Operation struct {
	ID         string         `json:"id"`
	Kind       OperationKind  `json:"kind"`
	State      OperationState `json:"state"`
	Network    string         `json:"network,omitempty"`
	Failure    *Failure       `json:"failure,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	FinishedAt *time.Time     `json:"finished_at,omitempty"`
}

// Bootstrap is the product-facing setup snapshot.
type Bootstrap struct {
	Capabilities Capabilities `json:"capabilities"`
	CurrentMode  Mode         `json:"current_mode"`
	Operation    *Operation   `json:"operation,omitempty"`
}

// PublicError lets a transition backend provide a stable browser-safe failure.
type PublicError struct {
	Failure Failure
}

func (err *PublicError) Error() string { return err.Failure.Code }

// NewPublicError creates an error whose code and message may be returned by the API.
func NewPublicError(code, message string) error {
	return &PublicError{Failure: Failure{Code: code, Message: message}}
}

// ConflictError reports the operation already using the single Wi-Fi transition slot.
type ConflictError struct {
	Operation Operation
}

func (err *ConflictError) Error() string { return "another setup operation is already running" }
