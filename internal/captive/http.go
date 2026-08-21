// Package captive provides temporary captive-network plumbing without depending on
// the product-facing setup application.
package captive

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	cacheControlValue = "no-store, no-cache, must-revalidate, max-age=0"
)

// HTTPHandler redirects cleartext requests for arbitrary captive-probe hosts to one
// canonical portal URL. Requests already addressed to the portal host are delegated to
// the product-independent portal handler supplied by the caller.
type HTTPHandler struct {
	portalURL       *url.URL
	portalAuthority string
	listenerPort    string
	portal          http.Handler
}

// NewHTTPHandler validates the canonical portal URL and creates a captive HTTP handler.
// Phase 3 deliberately supports only cleartext HTTP: presenting an untrusted certificate
// for intercepted HTTPS traffic would be both unreliable and misleading.
func NewHTTPHandler(portalURL string, listenerPort uint16, portal http.Handler) (*HTTPHandler, error) {
	if portal == nil {
		return nil, errors.New("portal handler is required")
	}
	if listenerPort == 0 {
		return nil, errors.New("listener port is required")
	}
	parsed, err := url.Parse(portalURL)
	if err != nil {
		return nil, errors.New("parse portal URL: " + err.Error())
	}
	if parsed.Scheme != "http" {
		return nil, errors.New("portal URL scheme must be http")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return nil, errors.New("portal URL must include a host")
	}
	if parsed.User != nil {
		return nil, errors.New("portal URL must not include user information")
	}
	if parsed.Fragment != "" {
		return nil, errors.New("portal URL must not include a fragment")
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	}

	return &HTTPHandler{
		portalURL:       parsed,
		portalAuthority: normalizeAuthority(parsed.Host, parsed.Scheme),
		listenerPort:    strconv.Itoa(int(listenerPort)),
		portal:          portal,
	}, nil
}

// ServeHTTP delegates canonical-host traffic and redirects every other cleartext HTTP
// request. The redirect target is configured, never derived from untrusted request data.
func (handler *HTTPHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	setNoCacheHeaders(response.Header())
	response.Header().Set("X-Content-Type-Options", "nosniff")

	if normalizeAuthority(request.Host, "http") == handler.portalAuthority ||
		authorityPort(request.Host) == handler.listenerPort {
		handler.portal.ServeHTTP(response, request)
		return
	}

	response.Header().Set("Location", handler.portalURL.String())
	response.WriteHeader(http.StatusFound)
}

func authorityPort(authority string) string {
	return (&url.URL{Scheme: "http", Host: authority}).Port()
}

func setNoCacheHeaders(header http.Header) {
	header.Set("Cache-Control", cacheControlValue)
	header.Set("Pragma", "no-cache")
	header.Set("Expires", "0")
}

func normalizeAuthority(authority, scheme string) string {
	parsed := &url.URL{Scheme: scheme, Host: authority}
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port == "" {
		return hostname
	}
	return hostname + ":" + port
}
