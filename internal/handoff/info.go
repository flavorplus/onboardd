// Package handoff derives stable browser and application destinations from resolved
// product configuration. Discovery and health checking build on this contract.
package handoff

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	appconfig "github.com/flavorplus/onboardd/internal/config"
)

// Application is the optional product destination shown after setup.
type Application struct {
	Label string
	URL   string
}

// Standalone contains the final AP details used before and after the radio transition.
// Password is populated only when product policy explicitly permits browser exposure.
type Standalone struct {
	SSID     string
	Password string
}

// Info contains resolved handoff policy. HealthCheckURL and credential policy remain
// server-side and are never serialized directly to the browser.
type Info struct {
	SetupURL                  string
	Application               *Application
	Standalone                *Standalone
	HealthCheckURL            string
	ShowStandaloneCredentials bool
}

// FromConfig derives the stable .local setup URL from a rendered configuration and
// the hostname read from Avahi at startup.
func FromConfig(config appconfig.Config, hostname string) (Info, error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" || strings.Contains(hostname, ".") {
		return Info{}, errors.New("Avahi hostname must be one non-empty label")
	}
	setupHost := strings.ToLower(hostname) + ".local"
	if config.Portal.ListenerPort != 80 {
		setupHost = net.JoinHostPort(setupHost, strconv.Itoa(int(config.Portal.ListenerPort)))
	}
	setupURL := (&url.URL{Scheme: "http", Host: setupHost, Path: "/"}).String()
	info := Info{
		SetupURL:                  setupURL,
		HealthCheckURL:            config.Handoff.HealthCheckURL,
		ShowStandaloneCredentials: config.Handoff.ShowStandaloneCredentials,
	}
	if config.Handoff.ApplicationURL != "" {
		info.Application = &Application{
			Label: config.Handoff.ApplicationLabel,
			URL:   config.Handoff.ApplicationURL,
		}
	}
	if config.Network.StandaloneEnabled {
		info.Standalone = &Standalone{SSID: config.Network.Standalone.SSID}
	}
	return info, nil
}
