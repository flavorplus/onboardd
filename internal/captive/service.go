package captive

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

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

// HTTPService owns the current HTTP listener and rebinds it after an
// unexpected serving failure. StartHTTPService binds the initial socket before
// returning so callers cannot announce readiness before the listener exists.
type HTTPService struct {
	listen  ListenFunc
	handler http.Handler
	options HTTPServiceOptions
	current *HTTPServer
	waiter  serviceWaiter
}

// StartHTTPService validates the recovery policy and synchronously binds the
// initial listener.
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

	server, err := ListenHTTPServer(ctx, listen, options.Network, options.Address, handler)
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
			service.options.OnRetry(
				ctx,
				restarts+1,
				service.options.MaxRestarts,
			)
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
		server, err := ListenHTTPServer(
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
