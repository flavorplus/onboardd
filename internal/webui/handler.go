package webui

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed dist
var compiledFrontend embed.FS

// Assets returns the compiled frontend rooted at index.html.
func Assets() fs.FS {
	assets, err := fs.Sub(compiledFrontend, "dist")
	if err != nil {
		panic(err)
	}
	return assets
}

const contentSecurityPolicy = "default-src 'self'; base-uri 'none'; form-action 'self'; " +
	"frame-ancestors 'none'; object-src 'none'; script-src 'self'; style-src 'self'; " +
	"connect-src 'self'; img-src 'self' data:"

// Handler combines the versioned API with frontend assets embedded in the binary.
type Handler struct {
	api        http.Handler
	appearance []byte
	logo       *Logo
	assets     fs.FS
	files      http.Handler
}

// NewHandler creates the complete setup HTTP handler.
func NewHandler(api http.Handler, assets fs.FS, options ...Options) (*Handler, error) {
	if api == nil {
		return nil, errors.New("setup API handler is required")
	}
	if assets == nil {
		return nil, errors.New("frontend asset filesystem is required")
	}
	if _, err := fs.Stat(assets, "index.html"); err != nil {
		return nil, errors.New("frontend index.html is required")
	}
	resolved, err := resolveOptions(options)
	if err != nil {
		return nil, err
	}
	appearance, err := json.Marshal(resolved.appearance)
	if err != nil {
		return nil, errors.New("encode public appearance")
	}
	appearance = append(appearance, '\n')
	return &Handler{
		api:        api,
		appearance: appearance,
		logo:       resolved.logo,
		assets:     assets,
		files:      http.FileServerFS(assets),
	}, nil
}

// ServeHTTP keeps API 404s separate from the browser application's history fallback.
func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path == appearanceURL {
		handler.serveAppearance(response, request)
		return
	}
	if request.URL.Path == logoURL {
		handler.serveLogo(response, request)
		return
	}
	if strings.HasPrefix(request.URL.Path, "/api/") {
		handler.api.ServeHTTP(response, request)
		return
	}
	response.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("X-Frame-Options", "DENY")

	name := strings.TrimPrefix(path.Clean(request.URL.Path), "/")
	if name == "." {
		name = "index.html"
	}
	if info, err := fs.Stat(handler.assets, name); err == nil && !info.IsDir() {
		handler.files.ServeHTTP(response, request)
		return
	}
	if path.Ext(name) != "" {
		http.NotFound(response, request)
		return
	}
	fallback := request.Clone(request.Context())
	urlCopy := *request.URL
	urlCopy.Path = "/"
	urlCopy.RawPath = ""
	fallback.URL = &urlCopy
	handler.files.ServeHTTP(response, fallback)
}

func (handler *Handler) serveAppearance(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		response.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = response.Write(handler.appearance)
	}
}

func (handler *Handler) serveLogo(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		response.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if handler.logo == nil {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Content-Type", handler.logo.contentType)
	response.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	response.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = response.Write(handler.logo.data)
	}
}
