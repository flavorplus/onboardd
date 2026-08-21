package web

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const contentSecurityPolicy = "default-src 'self'; base-uri 'none'; form-action 'self'; " +
	"frame-ancestors 'none'; object-src 'none'; script-src 'self'; style-src 'self'; " +
	"connect-src 'self'; img-src 'self' data:"

// Handler combines the versioned API with built frontend assets. The fs.FS boundary is
// filesystem-backed in Phase 4 and becomes embed.FS in Phase 5 without changing routes.
type Handler struct {
	api    http.Handler
	assets fs.FS
	files  http.Handler
}

// NewHandler creates the complete setup HTTP handler.
func NewHandler(api http.Handler, assets fs.FS) (*Handler, error) {
	if api == nil {
		return nil, errors.New("setup API handler is required")
	}
	if assets == nil {
		return nil, errors.New("frontend asset filesystem is required")
	}
	if _, err := fs.Stat(assets, "index.html"); err != nil {
		return nil, errors.New("frontend index.html is required")
	}
	return &Handler{api: api, assets: assets, files: http.FileServerFS(assets)}, nil
}

// ServeHTTP keeps API 404s separate from the browser application's history fallback.
func (handler *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
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
