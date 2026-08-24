package handoff

import (
	"context"
	"net/http"
	"time"
)

const healthTimeout = 2 * time.Second

// ReadinessChecker decides whether a configured application is ready to receive the
// user. Implementations must treat the URL as server-side policy and never expose it.
type ReadinessChecker interface {
	Ready(context.Context, string) bool
}

// HealthChecker performs a bounded HTTP GET against the configured readiness endpoint.
type HealthChecker struct {
	client *http.Client
}

// NewHealthChecker creates the production readiness checker.
func NewHealthChecker() *HealthChecker {
	return &HealthChecker{client: &http.Client{
		Timeout: healthTimeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}}
}

// Ready returns true only for an HTTP 2xx response. Network errors, timeouts,
// redirects that are not followed, and every other status mean not ready.
func (checker *HealthChecker) Ready(ctx context.Context, endpoint string) bool {
	if checker == nil || checker.client == nil || endpoint == "" {
		return false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	request.Header.Set("User-Agent", "onboardd-health/1")
	response, err := checker.client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
}
