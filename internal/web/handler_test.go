package web

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"testing/fstest"
)

var assetReference = regexp.MustCompile(`(?:src|href)="/([^"?#]+)`)

func TestEmbeddedAssets(t *testing.T) {
	assets := Assets()
	for _, name := range []string{"index.html", "landing.html"} {
		page, err := fs.ReadFile(assets, name)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(page, []byte("/src/")) {
			t.Fatalf("embedded %s references Vite development source", name)
		}
		for _, match := range assetReference.FindAllSubmatch(page, -1) {
			if _, err := fs.Stat(assets, string(match[1])); err != nil {
				t.Errorf("embedded %s references missing asset %q: %v", name, match[1], err)
			}
		}
	}
	landing, err := fs.ReadFile(assets, "landing.html")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(landing, []byte("__ONBOARDD_SETUP_URL__")) {
		t.Fatal("embedded landing page is missing the runtime setup URL placeholder")
	}
}

func TestHandlerRoutesAPIAssetsAndHistoryFallback(t *testing.T) {
	api := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusTeapot)
		_, _ = response.Write([]byte(`{"api":true}`))
	})
	assets := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<h1>Setup</h1>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('setup')")},
	}
	handler, err := NewHandler(api, assets)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "api", path: "/api/v1/setup", wantStatus: http.StatusTeapot, wantBody: `{"api":true}`},
		{name: "asset", path: "/assets/app.js", wantStatus: http.StatusOK, wantBody: "console.log('setup')"},
		{name: "history", path: "/networks", wantStatus: http.StatusOK, wantBody: "<h1>Setup</h1>"},
		{name: "missing asset", path: "/assets/missing.js", wantStatus: http.StatusNotFound, wantBody: "404 page not found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://10.42.0.1"+test.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus || response.Body.String() != test.wantBody+newlineFor(test.wantStatus) {
				t.Fatalf("response = %d %q", response.Code, response.Body.String())
			}
			if test.name != "api" && response.Header().Get("Content-Security-Policy") == "" {
				t.Fatal("missing Content-Security-Policy")
			}
		})
	}
}

func TestNewHandlerRequiresIndex(t *testing.T) {
	_, err := NewHandler(http.NotFoundHandler(), fstest.MapFS{})
	if err == nil {
		t.Fatal("NewHandler() error = nil")
	}
	if !errors.Is(err, fs.ErrNotExist) && err.Error() != "frontend index.html is required" {
		t.Fatalf("NewHandler() error = %v", err)
	}
}

func newlineFor(status int) string {
	if status == http.StatusNotFound {
		return "\n"
	}
	return ""
}
