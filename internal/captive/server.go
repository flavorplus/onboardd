package captive

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
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

// startHTTPServer begins serving immediately and returns without blocking.
func startHTTPServer(listener net.Listener, handler http.Handler) (*HTTPServer, error) {
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

// listenHTTPServer binds a socket and begins serving immediately. It closes the
// socket if server construction fails so callers never inherit a partial listener.
func listenHTTPServer(
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
	server, err := startHTTPServer(listener, handler)
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

// HTTPServiceOptions contains the listener address and bounded recovery policy.
type HTTPServiceOptions struct {
	Network         string
	Address         string
	MaxRestarts     int
	RestartDelay    time.Duration
	ShutdownTimeout time.Duration
	OnRetry         func(context.Context, int, int)
	OnRecovered     func(context.Context, int)
}

type serviceWaiter interface {
	Wait(context.Context, time.Duration) error
}

// HTTPService owns the current HTTP listener and rebinds it after an unexpected
// serving failure.
type HTTPService struct {
	listen  ListenFunc
	handler http.Handler
	options HTTPServiceOptions
	current *HTTPServer
	waiter  serviceWaiter
}

// StartHTTPService synchronously binds the initial listener so callers cannot
// announce readiness before the socket exists.
func StartHTTPService(
	ctx context.Context,
	listen ListenFunc,
	handler http.Handler,
	options HTTPServiceOptions,
) (*HTTPService, error) {
	if listen == nil {
		return nil, errors.New("HTTP listen function is required")
	}
	if handler == nil {
		return nil, errors.New("HTTP handler is required")
	}
	if strings.TrimSpace(options.Network) == "" {
		return nil, errors.New("HTTP listener network is required")
	}
	if strings.TrimSpace(options.Address) == "" {
		return nil, errors.New("HTTP listener address is required")
	}
	if options.MaxRestarts < 0 {
		return nil, errors.New("maximum HTTP listener restart count cannot be negative")
	}
	if options.RestartDelay <= 0 {
		return nil, errors.New("HTTP listener restart delay must be positive")
	}
	if options.ShutdownTimeout <= 0 {
		return nil, errors.New("HTTP listener shutdown timeout must be positive")
	}

	server, err := listenHTTPServer(ctx, listen, options.Network, options.Address, handler)
	if err != nil {
		return nil, fmt.Errorf("bind initial HTTP listener: %w", err)
	}
	return &HTTPService{
		listen:  listen,
		handler: handler,
		options: options,
		current: server,
		waiter:  timerWaiter{},
	}, nil
}

// Run supervises the listener until cancellation or recovery exhaustion.
func (service *HTTPService) Run(ctx context.Context) error {
	current := service.current
	restarts := 0
	var lastErr error

	for {
		if ctx.Err() != nil {
			if current == nil {
				return nil
			}
			return service.shutdown(ctx, current)
		}

		if current != nil {
			select {
			case <-ctx.Done():
				return service.shutdown(ctx, current)
			case <-current.Done():
				lastErr = current.Wait()
				if lastErr == nil {
					lastErr = errors.New("HTTP listener stopped unexpectedly")
				}
				current = nil
			}
		}

		if ctx.Err() != nil {
			return nil
		}
		if restarts >= service.options.MaxRestarts {
			return fmt.Errorf(
				"HTTP listener recovery exhausted after %d restarts: %w",
				service.options.MaxRestarts,
				lastErr,
			)
		}
		if service.options.OnRetry != nil {
			service.options.OnRetry(ctx, restarts+1, service.options.MaxRestarts)
		}
		if err := service.waiter.Wait(ctx, service.options.RestartDelay); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("wait to restart HTTP listener: %w", err)
		}
		if ctx.Err() != nil {
			return nil
		}

		restarts++
		server, err := listenHTTPServer(
			ctx,
			service.listen,
			service.options.Network,
			service.options.Address,
			service.handler,
		)
		if err != nil {
			lastErr = fmt.Errorf("rebind HTTP listener: %w", err)
			continue
		}
		current = server
		if service.options.OnRecovered != nil {
			service.options.OnRecovered(ctx, restarts)
		}
	}
}

func (service *HTTPService) shutdown(ctx context.Context, server *HTTPServer) error {
	cleanupContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		service.options.ShutdownTimeout,
	)
	defer cancel()
	if err := server.Shutdown(cleanupContext); err != nil {
		return fmt.Errorf("stop HTTP listener: %w", err)
	}
	return nil
}

type timerWaiter struct{}

func (timerWaiter) Wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
