package recovery

import "sync"

// Requests coalesces manual recovery triggers until the controller completes
// the pending request. A buffered notification wakes the controller without
// making trigger producers wait for network I/O.
type Requests struct {
	mu      sync.Mutex
	pending bool
	notify  chan struct{}
}

// NewRequests creates an empty manual recovery request set.
func NewRequests() *Requests {
	return &Requests{notify: make(chan struct{}, 1)}
}

// Request marks recovery as pending. It reports false when another pending
// request already represents the same desired outcome.
func (r *Requests) Request() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending {
		return false
	}
	r.pending = true
	select {
	case r.notify <- struct{}{}:
	default:
	}
	return true
}

// Pending reports whether the controller still needs to enter provisioning.
func (r *Requests) Pending() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pending
}

// Complete acknowledges a successful provisioning entry. It also removes an
// unread notification left by a controller restart that inspected Pending.
func (r *Requests) Complete() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = false
	select {
	case <-r.notify:
	default:
	}
}

// Notifications wakes a controller when a new request becomes pending.
func (r *Requests) Notifications() <-chan struct{} {
	return r.notify
}
