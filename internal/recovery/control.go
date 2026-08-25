package recovery

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	// ControlSocketPath is the local administrator endpoint used by
	// `onboardd recover` to reach the running appliance process.
	ControlSocketPath = "/run/onboardd/control.sock"
	controlTimeout    = 5 * time.Second
)

type requestAcceptor interface {
	Request() bool
}

// ControlServer accepts root-local manual recovery requests over a Unix socket.
type ControlServer struct {
	listener *net.UnixListener
	path     string
	requests requestAcceptor
	closed   atomic.Bool
	close    sync.Once
	closeErr error
}

// StartControlServer synchronously binds a private control socket. A stale
// socket left by a crashed process is replaced, but an active listener is never
// displaced.
func StartControlServer(path string, requests requestAcceptor) (*ControlServer, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("control socket path must be absolute")
	}
	if requests == nil {
		return nil, errors.New("recovery request acceptor is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create control socket directory: %w", err)
	}
	if err := prepareControlSocket(path); err != nil {
		return nil, err
	}

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("bind control socket: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, errors.Join(
			fmt.Errorf("protect control socket: %w", err),
			listener.Close(),
			removeSocket(path),
		)
	}
	return &ControlServer{listener: listener, path: path, requests: requests}, nil
}

func prepareControlSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect control socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("control socket path exists and is not a socket")
	}

	connection, dialErr := net.DialTimeout("unix", path, 100*time.Millisecond)
	if dialErr == nil {
		_ = connection.Close()
		return errors.New("control socket is already active")
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) {
		return fmt.Errorf("verify existing control socket: %w", dialErr)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale control socket: %w", err)
	}
	return nil
}

// Run accepts requests until cancellation or Close. Connections are handled
// synchronously because the root-only protocol is one response with no body.
func (s *ControlServer) Run(ctx context.Context) error {
	stopClose := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stopClose()
	defer s.Close()

	for {
		connection, err := s.listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil || s.closed.Load() || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept control request: %w", err)
		}
		// A client can disconnect after its request is accepted. That connection
		// failure is contained to the client and must not stop appliance recovery.
		if err := s.respond(connection); err != nil {
			continue
		}
	}
}

func (s *ControlServer) respond(connection *net.UnixConn) error {
	defer connection.Close()
	if err := connection.SetWriteDeadline(time.Now().Add(controlTimeout)); err != nil {
		return fmt.Errorf("set control response deadline: %w", err)
	}
	response := "pending\n"
	if s.requests.Request() {
		response = "accepted\n"
	}
	if _, err := io.WriteString(connection, response); err != nil {
		return fmt.Errorf("write control response: %w", err)
	}
	return nil
}

// Close stops the listener and removes its filesystem entry. It is idempotent.
func (s *ControlServer) Close() error {
	s.close.Do(func() {
		s.closed.Store(true)
		listenerErr := s.listener.Close()
		if errors.Is(listenerErr, net.ErrClosed) {
			listenerErr = nil
		}
		s.closeErr = errors.Join(listenerErr, removeSocket(s.path))
	})
	return s.closeErr
}

func removeSocket(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// RequestControl asks the already-running appliance process to enter temporary
// provisioning. The server's private socket permissions enforce local authority.
func RequestControl(ctx context.Context, path string) error {
	requestContext, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(requestContext, "unix", path)
	if err != nil {
		return fmt.Errorf("contact running onboardd process: %w", err)
	}
	defer connection.Close()
	deadline, ok := requestContext.Deadline()
	if ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return fmt.Errorf("set control request deadline: %w", err)
		}
	}
	response, err := bufio.NewReader(io.LimitReader(connection, 64)).ReadString('\n')
	if err != nil {
		return fmt.Errorf("read control response: %w", err)
	}
	switch strings.TrimSpace(response) {
	case "accepted", "pending":
		return nil
	default:
		return fmt.Errorf("unexpected control response %q", strings.TrimSpace(response))
	}
}
