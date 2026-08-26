package web

import (
	"errors"
	"regexp"
	"strings"

	appconfig "github.com/flavorplus/onboardd/internal/config"
)

const logoURL = "/api/v1/branding/logo"

var brandingColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// Branding is safe product copy and color data returned to the setup frontend.
type Branding struct {
	ProductName     string `json:"product_name"`
	DeviceName      string `json:"device_name"`
	Title           string `json:"title"`
	Subtitle        string `json:"subtitle"`
	PrimaryColor    string `json:"primary_color"`
	BackgroundColor string `json:"background_color"`
}

type brandingResponse struct {
	Branding
	LogoURL string `json:"logo_url,omitempty"`
}

// Options supplies product presentation without coupling the HTTP package to the TOML
// loader. The production startup path builds this after template rendering.
type Options struct {
	Branding      Branding
	Logo          *Logo
	Handoff       *Handoff
	HealthChecker readinessChecker
}

// OptionsFromConfig converts an already resolved and rendered product configuration
// into HTTP options and loads its optional logo before the server starts.
func OptionsFromConfig(config appconfig.Config, hostname string) (Options, error) {
	options := Options{Branding: Branding{
		ProductName:     config.Product.Name,
		DeviceName:      config.Product.DeviceName,
		Title:           config.Branding.Text.Title,
		Subtitle:        config.Branding.Text.Subtitle,
		PrimaryColor:    config.Branding.PrimaryColor,
		BackgroundColor: config.Branding.BackgroundColor,
	}}
	handoffInfo, err := handoffFromConfig(config, hostname)
	if err != nil {
		return Options{}, err
	}
	options.Handoff = &handoffInfo
	options.HealthChecker = newHealthChecker()
	if config.Branding.Logo == "" {
		return options, nil
	}
	logo, err := loadLogo(config.Branding.Logo)
	if err != nil {
		return Options{}, err
	}
	options.Logo = logo
	return options, nil
}

func defaultBranding() Branding {
	return Branding{
		ProductName:     "Device",
		DeviceName:      "Device",
		Title:           "How should this device connect?",
		Subtitle:        "Choose Wi-Fi for normal network access, or keep this device available as its own network.",
		PrimaryColor:    "#cd2455",
		BackgroundColor: "#f8eff3",
	}
}

func resolveAPIOptions(
	options []Options,
) (brandingResponse, *Logo, *Handoff, readinessChecker, error) {
	if len(options) > 1 {
		return brandingResponse{}, nil, nil, nil, errors.New("only one setup API options value is allowed")
	}
	branding := defaultBranding()
	var logo *Logo
	var handoffInfo *Handoff
	var healthChecker readinessChecker
	if len(options) == 1 {
		branding = options[0].Branding
		logo = options[0].Logo
		handoffInfo = options[0].Handoff
		healthChecker = options[0].HealthChecker
	}
	if strings.TrimSpace(branding.ProductName) == "" || strings.TrimSpace(branding.DeviceName) == "" {
		return brandingResponse{}, nil, nil, nil, errors.New("branding product and device names are required")
	}
	if strings.TrimSpace(branding.Title) == "" {
		return brandingResponse{}, nil, nil, nil, errors.New("branding title is required")
	}
	if !brandingColorPattern.MatchString(branding.PrimaryColor) ||
		!brandingColorPattern.MatchString(branding.BackgroundColor) {
		return brandingResponse{}, nil, nil, nil, errors.New("branding colors must be six-digit hexadecimal values")
	}
	response := brandingResponse{Branding: branding}
	if logo != nil {
		response.LogoURL = logoURL
	}
	if handoffInfo != nil && handoffInfo.HealthCheckURL != "" && healthChecker == nil {
		healthChecker = newHealthChecker()
	}
	return response, logo, handoffInfo, healthChecker, nil
}
