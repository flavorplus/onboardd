package recovery

import "testing"

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
