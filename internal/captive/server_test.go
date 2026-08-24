package captive

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStartHTTPServerServesAndShutsDown(t *testing.T) {
	listener := newMemoryListener()
	portal := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(response, "setup")
	})
	handler, err := NewHTTPHandler(
		"http://10.42.0.1/",
		"http://device.local:18080/",
		18080,
		testLandingPage,
		portal,
	)
	if err != nil {
		t.Fatal(err)
	}
	server, err := StartHTTPServer(listener, handler)
	if err != nil {
		t.Fatal(err)
	}

	serverConnection, clientConnection := net.Pipe()
	defer clientConnection.Close()
	listener.connections <- serverConnection
	requestWritten := make(chan error, 1)
	go func() {
		_, requestErr := fmt.Fprint(
			clientConnection,
			"GET /generate_204 HTTP/1.1\r\nHost: connectivitycheck.gstatic.com\r\nConnection: close\r\n\r\n",
		)
		requestWritten <- requestErr
	}()
	response, err := http.ReadResponse(bufio.NewReader(clientConnection), nil)
	if err != nil {
		t.Fatalf("read probe response error = %v", err)
	}
	defer response.Body.Close()
	if err := <-requestWritten; err != nil {
		t.Fatalf("write probe request error = %v", err)
	}
	if response.StatusCode != http.StatusFound {
		t.Fatalf("probe status = %d, want %d", response.StatusCode, http.StatusFound)
	}
	if got := response.Header.Get("Location"); got != "http://10.42.0.1/" {
		t.Fatalf("Location = %q", got)
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case <-server.Done():
	default:
		t.Fatal("Done() remains open after Shutdown()")
	}
	if err := server.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestHTTPServerForceClosesActiveRequestAfterGracefulShutdownExpires(t *testing.T) {
	listener := newMemoryListener()
	requestStarted := make(chan struct{})
	requestStopped := make(chan struct{})
	server, err := StartHTTPServer(listener, http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		<-request.Context().Done()
		close(requestStopped)
	}))
	if err != nil {
		t.Fatal(err)
	}

	serverConnection, clientConnection := net.Pipe()
	defer clientConnection.Close()
	listener.connections <- serverConnection
	go func() {
		_, _ = fmt.Fprint(
			clientConnection,
			"GET / HTTP/1.1\r\nHost: 10.42.0.1\r\n\r\n",
		)
	}()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach handler")
	}

	shutdownContext, cancel := context.WithCancel(context.Background())
	cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case <-requestStopped:
	case <-time.After(2 * time.Second):
		t.Fatal("active request remains open after forced shutdown")
	}
	select {
	case <-server.Done():
	default:
		t.Fatal("Done() remains open after forced shutdown")
	}
}

func TestStartHTTPServerValidatesDependencies(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if _, err := StartHTTPServer(nil, handler); err == nil || !strings.Contains(err.Error(), "listener") {
		t.Fatalf("nil listener error = %v", err)
	}

	listener := newMemoryListener()
	defer listener.Close()
	if _, err := StartHTTPServer(listener, nil); err == nil || !strings.Contains(err.Error(), "handler") {
		t.Fatalf("nil handler error = %v", err)
	}
}

func TestHTTPServerReportsUnexpectedListenerFailure(t *testing.T) {
	wantErr := errors.New("accept failed")
	listener := &failingListener{err: wantErr}
	server, err := StartHTTPServer(listener, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Wait(); !errors.Is(err, wantErr) {
		t.Fatalf("Wait() error = %v, want %v", err, wantErr)
	}
}

type memoryListener struct {
	connections chan net.Conn
	closed      chan struct{}
	closeOnce   sync.Once
}

func newMemoryListener() *memoryListener {
	return &memoryListener{
		connections: make(chan net.Conn, 1),
		closed:      make(chan struct{}),
	}
}

func (listener *memoryListener) Accept() (net.Conn, error) {
	select {
	case connection := <-listener.connections:
		return connection, nil
	case <-listener.closed:
		return nil, net.ErrClosed
	}
}

func (listener *memoryListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.closed) })
	return nil
}

func (*memoryListener) Addr() net.Addr {
	return memoryAddress("memory")
}

type memoryAddress string

func (address memoryAddress) Network() string { return string(address) }
func (address memoryAddress) String() string  { return string(address) }

type failingListener struct {
	err error
}

func (listener *failingListener) Accept() (net.Conn, error) { return nil, listener.err }
func (*failingListener) Close() error                       { return nil }
func (*failingListener) Addr() net.Addr                     { return memoryAddress("failed") }
