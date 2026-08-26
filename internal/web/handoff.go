package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	appconfig "github.com/flavorplus/onboardd/internal/config"
)

const healthTimeout = 2 * time.Second

// Handoff contains the destinations shown after setup. Health and credential policy
// remain server-side and are never serialized directly to the browser.
type Handoff struct {
	SetupURL                  string
	Application               *ApplicationHandoff
	Standalone                *StandaloneHandoff
	HealthCheckURL            string
	ShowStandaloneCredentials bool
}

type ApplicationHandoff struct {
	Label string
	URL   string
}

type StandaloneHandoff struct {
	SSID     string
	Password string
}

func handoffFromConfig(config appconfig.Config, hostname string) (Handoff, error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" || strings.Contains(hostname, ".") {
		return Handoff{}, errors.New("Avahi hostname must be one non-empty label")
	}
	setupHost := hostname + ".local"
	if config.Portal.ListenerPort != 80 {
		setupHost = net.JoinHostPort(setupHost, strconv.Itoa(int(config.Portal.ListenerPort)))
	}
	info := Handoff{
		SetupURL:                  (&url.URL{Scheme: "http", Host: setupHost, Path: "/"}).String(),
		HealthCheckURL:            config.Handoff.HealthCheckURL,
		ShowStandaloneCredentials: config.Handoff.ShowStandaloneCredentials,
	}
	if config.Handoff.ApplicationURL != "" {
		info.Application = &ApplicationHandoff{
			Label: config.Handoff.ApplicationLabel,
			URL:   config.Handoff.ApplicationURL,
		}
	}
	if config.Network.StandaloneEnabled {
		info.Standalone = &StandaloneHandoff{SSID: config.Network.Standalone.SSID}
	}
	return info, nil
}

type readinessChecker interface {
	Ready(context.Context, string) bool
}

type healthChecker struct {
	client *http.Client
}

func newHealthChecker() *healthChecker {
	return &healthChecker{client: &http.Client{
		Timeout: healthTimeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}}
}

func (checker *healthChecker) Ready(ctx context.Context, endpoint string) bool {
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
