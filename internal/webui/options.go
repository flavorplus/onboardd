package webui

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/flavorplus/onboardd/internal/appconfig"
)

const (
	appearanceURL = "/appearance.json"
	logoURL       = "/appearance/logo"
)

var brandingColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// Branding is safe product copy and color data returned before authentication so the
// login screen can match the configured product.
type Branding struct {
	ProductName     string `json:"product_name"`
	DeviceName      string `json:"device_name"`
	Title           string `json:"title"`
	Subtitle        string `json:"subtitle"`
	PrimaryColor    string `json:"primary_color"`
	BackgroundColor string `json:"background_color"`
}

type appearanceResponse struct {
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

type resolvedOptions struct {
	appearance    appearanceResponse
	logo          *Logo
	handoff       *Handoff
	healthChecker readinessChecker
}

func resolveOptions(options []Options) (resolvedOptions, error) {
	if len(options) > 1 {
		return resolvedOptions{}, errors.New("only one setup options value is allowed")
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
		return resolvedOptions{}, errors.New("branding product and device names are required")
	}
	if strings.TrimSpace(branding.Title) == "" {
		return resolvedOptions{}, errors.New("branding title is required")
	}
	if !brandingColorPattern.MatchString(branding.PrimaryColor) ||
		!brandingColorPattern.MatchString(branding.BackgroundColor) {
		return resolvedOptions{}, errors.New("branding colors must be six-digit hexadecimal values")
	}
	response := appearanceResponse{Branding: branding}
	if logo != nil {
		response.LogoURL = logoURL
	}
	if handoffInfo != nil && handoffInfo.HealthCheckURL != "" && healthChecker == nil {
		healthChecker = newHealthChecker()
	}
	return resolvedOptions{
		appearance:    response,
		logo:          logo,
		handoff:       handoffInfo,
		healthChecker: healthChecker,
	}, nil
}

const maxLogoBytes = 512 * 1024

// Logo is a validated in-memory product logo. Its data remains private so callers
// cannot alter it after validation.
type Logo struct {
	data        []byte
	contentType string
}

// loadLogo validates a configured raster or SVG logo before any network transition.
func loadLogo(path string) (*Logo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open branding logo %q: %w", path, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxLogoBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read branding logo %q: %w", path, err)
	}
	if len(data) == 0 {
		return nil, errors.New("branding logo must not be empty")
	}
	if len(data) > maxLogoBytes {
		return nil, fmt.Errorf("branding logo exceeds the %d-byte limit", maxLogoBytes)
	}

	contentType := http.DetectContentType(data)
	switch contentType {
	case "image/png", "image/jpeg":
		if _, _, err := image.DecodeConfig(bytes.NewReader(data)); err != nil {
			return nil, fmt.Errorf("decode branding logo: %w", err)
		}
	case "text/xml; charset=utf-8", "text/plain; charset=utf-8", "application/xml":
		if err := validateSVG(data); err != nil {
			return nil, fmt.Errorf("validate branding logo: %w", err)
		}
		contentType = "image/svg+xml"
	default:
		return nil, fmt.Errorf("branding logo has unsupported media type %q", contentType)
	}
	return &Logo{data: append([]byte(nil), data...), contentType: contentType}, nil
}

func validateSVG(data []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	rootSeen := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("decode SVG XML: %w", err)
		}
		switch value := token.(type) {
		case xml.Directive:
			return errors.New("SVG directives are not allowed")
		case xml.StartElement:
			name := strings.ToLower(value.Name.Local)
			if !rootSeen {
				if name != "svg" {
					return errors.New("SVG root element is required")
				}
				rootSeen = true
			}
			switch name {
			case "script", "style", "foreignobject", "iframe", "object", "embed", "audio", "video":
				return fmt.Errorf("SVG element %q is not allowed", value.Name.Local)
			}
			for _, attribute := range value.Attr {
				attributeName := strings.ToLower(attribute.Name.Local)
				attributeValue := strings.TrimSpace(attribute.Value)
				if strings.HasPrefix(attributeName, "on") {
					return fmt.Errorf("SVG event attribute %q is not allowed", attribute.Name.Local)
				}
				if (attributeName == "href" || attributeName == "src") &&
					attributeValue != "" && !strings.HasPrefix(attributeValue, "#") {
					return fmt.Errorf("external SVG reference %q is not allowed", attribute.Value)
				}
				if attributeName == "style" && strings.Contains(strings.ToLower(attributeValue), "url(") {
					return errors.New("SVG style URLs are not allowed")
				}
			}
		}
	}
	if !rootSeen {
		return errors.New("SVG root element is required")
	}
	return nil
}

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
