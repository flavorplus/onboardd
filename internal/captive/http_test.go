package captive

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPHandlerRedirectsCaptiveProbeRequests(t *testing.T) {
	portal := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		t.Fatal("probe request unexpectedly reached portal handler")
	})
	handler, err := NewHTTPHandler("http://setup.local/", 18080, portal)
	if err != nil {
		t.Fatalf("NewHTTPHandler() error = %v", err)
	}

	tests := []struct {
		name string
		host string
		path string
	}{
		{name: "Android generate 204", host: "connectivitycheck.gstatic.com", path: "/generate_204"},
		{name: "Android alternate 204", host: "connectivitycheck.android.com", path: "/generate_204"},
		{name: "Apple hotspot", host: "captive.apple.com", path: "/hotspot-detect.html"},
		{name: "Windows connect test", host: "www.msftconnecttest.com", path: "/connecttest.txt"},
		{name: "legacy Windows NCSI", host: "www.msftncsi.com", path: "/ncsi.txt"},
		{name: "Firefox canonical", host: "detectportal.firefox.com", path: "/canonical.html"},
		{name: "arbitrary browser URL", host: "example.com", path: "/products/example?q=1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://"+test.host+test.path, nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusFound {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusFound)
			}
			if got := response.Header().Get("Location"); got != "http://setup.local/" {
				t.Fatalf("Location = %q, want canonical portal URL", got)
			}
			assertNoCacheHeaders(t, response.Header())
			if response.Body.Len() != 0 {
				t.Fatalf("redirect body = %q, want empty", response.Body.String())
			}
		})
	}
}

func TestHTTPHandlerDelegatesCanonicalPortalHost(t *testing.T) {
	var receivedPath string
	portal := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		receivedPath = request.URL.RequestURI()
		response.Header().Set("Content-Type", "text/plain")
		response.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(response, "setup")
	})
	handler, err := NewHTTPHandler("http://Setup.Local:80/", 18080, portal)
	if err != nil {
		t.Fatalf("NewHTTPHandler() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://setup.local./networks?page=2", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "setup" {
		t.Fatalf("portal response = status %d body %q", response.Code, response.Body.String())
	}
	if receivedPath != "/networks?page=2" {
		t.Fatalf("portal received path = %q", receivedPath)
	}
	assertNoCacheHeaders(t, response.Header())
}

func TestHTTPHandlerDelegatesDirectListenerAddresses(t *testing.T) {
	portal := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(response, "setup")
	})
	handler, err := NewHTTPHandler("http://10.42.0.1/", 18080, portal)
	if err != nil {
		t.Fatalf("NewHTTPHandler() error = %v", err)
	}

	for _, address := range []string{
		"http://10.42.0.1:18080/",
		"http://192.0.2.10:18080/",
	} {
		t.Run(address, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, address, nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK || response.Body.String() != "setup" {
				t.Fatalf("direct response = status %d body %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestNewHTTPHandlerValidatesConfiguration(t *testing.T) {
	portal := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	tests := []struct {
		name      string
		portalURL string
		portal    http.Handler
		want      string
	}{
		{name: "missing handler", portalURL: "http://setup.local/", want: "portal handler is required"},
		{name: "missing listener port", portalURL: "http://setup.local/", portal: portal, want: "listener port is required"},
		{name: "relative URL", portalURL: "/setup", portal: portal, want: "scheme must be http"},
		{name: "HTTPS interception", portalURL: "https://setup.local/", portal: portal, want: "scheme must be http"},
		{name: "missing host", portalURL: "http:///setup", portal: portal, want: "must include a host"},
		{name: "user information", portalURL: "http://user@setup.local/", portal: portal, want: "must not include user information"},
		{name: "fragment", portalURL: "http://setup.local/#setup", portal: portal, want: "must not include a fragment"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			listenerPort := uint16(18080)
			if test.name == "missing listener port" {
				listenerPort = 0
			}
			_, err := NewHTTPHandler(test.portalURL, listenerPort, test.portal)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewHTTPHandler() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestHTTPHandlerPreservesHEADSemantics(t *testing.T) {
	handler, err := NewHTTPHandler(
		"http://setup.local/",
		18080,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodHead, "http://example.com/", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusFound || response.Body.Len() != 0 {
		t.Fatalf("HEAD response = status %d body %q", response.Code, response.Body.String())
	}
}

func assertNoCacheHeaders(t *testing.T, header http.Header) {
	t.Helper()
	if got := header.Get("Cache-Control"); got != cacheControlValue {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := header.Get("Pragma"); got != "no-cache" {
		t.Errorf("Pragma = %q", got)
	}
	if got := header.Get("Expires"); got != "0" {
		t.Errorf("Expires = %q", got)
	}
}
