package captive

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// ListenFunc binds an HTTP socket selected by the application entry point.
type ListenFunc func(context.Context, string, string) (net.Listener, error)

// HTTPServer owns one already-bound captive HTTP listener. Accepting a listener keeps
// privileged binding and interface selection outside this package and makes lifecycle
// behavior deterministic in tests.
type HTTPServer struct {
	server *http.Server
	done   chan struct{}

	mu       sync.RWMutex
	serveErr error
}

// StartHTTPServer begins serving immediately and returns without blocking.
func StartHTTPServer(listener net.Listener, handler http.Handler) (*HTTPServer, error) {
	if listener == nil {
		return nil, errors.New("HTTP listener is required")
	}
	if handler == nil {
		return nil, errors.New("HTTP handler is required")
	}

	lifecycle := &HTTPServer{
		server: &http.Server{
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
		done: make(chan struct{}),
	}
	go func() {
		err := lifecycle.server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		lifecycle.mu.Lock()
		lifecycle.serveErr = err
		lifecycle.mu.Unlock()
		close(lifecycle.done)
	}()
	return lifecycle, nil
}

// ListenHTTPServer binds a socket and begins serving immediately. It closes the
// socket if server construction fails so callers never inherit a partial listener.
func ListenHTTPServer(
	ctx context.Context,
	listen ListenFunc,
	network string,
	address string,
	handler http.Handler,
) (*HTTPServer, error) {
	if listen == nil {
		return nil, errors.New("HTTP listen function is required")
	}
	listener, err := listen(ctx, network, address)
	if err != nil {
		return nil, err
	}
	server, err := StartHTTPServer(listener, handler)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	return server, nil
}

// Done closes after the listener stops accepting requests.
func (server *HTTPServer) Done() <-chan struct{} {
	return server.done
}

// Wait waits for the listener to stop and returns any unexpected serving error.
func (server *HTTPServer) Wait() error {
	<-server.done
	server.mu.RLock()
	defer server.mu.RUnlock()
	return server.serveErr
}

// Shutdown gracefully stops the listener and waits for its serving goroutine.
// If a client keeps a request active until the caller's deadline, it force-closes
// the remaining connections so shutdown can still complete deterministically.
func (server *HTTPServer) Shutdown(ctx context.Context) error {
	if err := server.server.Shutdown(ctx); err != nil {
		if ctx.Err() == nil || !errors.Is(err, ctx.Err()) {
			return err
		}
		if closeErr := server.server.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			return errors.Join(err, closeErr)
		}
	}
	return server.Wait()
}
