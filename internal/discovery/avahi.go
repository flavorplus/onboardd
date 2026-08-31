// Package discovery publishes onboardd's stable setup origin through the host's
// existing Avahi mDNS daemon.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	avahiService         = "org.freedesktop.Avahi"
	avahiServerPath      = dbus.ObjectPath("/")
	avahiServerInterface = "org.freedesktop.Avahi.Server"
	avahiGroupInterface  = "org.freedesktop.Avahi.EntryGroup"
	registrationWait     = 5 * time.Second
	pollInterval         = 100 * time.Millisecond

	avahiServerRunning    = int32(2)
	avahiServerCollision  = int32(3)
	avahiServerFailure    = int32(4)
	avahiGroupEstablished = int32(2)
	avahiGroupCollision   = int32(3)
	avahiGroupFailure     = int32(4)
)

// Options describes the stable setup service advertised on each active link.
type Options struct {
	ServiceName string
	Port        uint16
}

// Publisher owns one Avahi entry group. The hostname remains owned entirely by Avahi
// and the host operating system.
type Publisher struct {
	api      avahiAPI
	group    dbus.ObjectPath
	hostname string

	mu     sync.Mutex
	closed bool
}

type avahiAPI interface {
	HostName(context.Context) (string, error)
	ServerState(context.Context) (int32, error)
	NewEntryGroup(context.Context) (dbus.ObjectPath, error)
	AddHTTPService(context.Context, dbus.ObjectPath, string, uint16) error
	Commit(context.Context, dbus.ObjectPath) error
	GroupState(context.Context, dbus.ObjectPath) (int32, error)
	Free(context.Context, dbus.ObjectPath) error
	Close() error
}

// CurrentHostname reads the hostname currently managed by Avahi without changing it.
func CurrentHostname(ctx context.Context) (string, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return "", fmt.Errorf("connect to system D-Bus for mDNS: %w", err)
	}
	api := &dbusAvahi{conn: conn}
	hostname, hostnameErr := readHostname(ctx, api)
	closeErr := api.Close()
	if hostnameErr != nil || closeErr != nil {
		return "", errors.Join(hostnameErr, closeErr)
	}
	return hostname, nil
}

// Start connects to Avahi, publishes an HTTP setup service on its existing hostname,
// and waits until the registration is established.
func Start(ctx context.Context, options Options) (*Publisher, error) {
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("connect to system D-Bus for mDNS: %w", err)
	}
	publisher := &Publisher{api: &dbusAvahi{conn: conn}}
	if err := publisher.start(ctx, options); err != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), registrationWait)
		defer cancel()
		return nil, errors.Join(err, publisher.Close(cleanupContext))
	}
	return publisher, nil
}

func newPublisher(api avahiAPI) *Publisher {
	return &Publisher{api: api}
}

func (publisher *Publisher) start(ctx context.Context, options Options) error {
	if err := validateOptions(options); err != nil {
		return err
	}
	registrationContext, cancel := context.WithTimeout(ctx, registrationWait)
	defer cancel()

	hostname, err := readHostname(registrationContext, publisher.api)
	if err != nil {
		return err
	}
	publisher.hostname = hostname
	if err := waitForState(
		registrationContext,
		publisher.api.ServerState,
		avahiServerRunning,
		avahiServerCollision,
		avahiServerFailure,
		"Avahi server",
	); err != nil {
		return err
	}

	group, err := publisher.api.NewEntryGroup(registrationContext)
	if err != nil {
		return fmt.Errorf("create Avahi entry group: %w", err)
	}
	publisher.group = group
	if err := publisher.api.AddHTTPService(registrationContext, group, options.ServiceName, options.Port); err != nil {
		return fmt.Errorf("add Avahi HTTP service: %w", err)
	}
	if err := publisher.api.Commit(registrationContext, group); err != nil {
		return fmt.Errorf("commit Avahi entry group: %w", err)
	}
	if err := waitForState(
		registrationContext,
		func(ctx context.Context) (int32, error) { return publisher.api.GroupState(ctx, group) },
		avahiGroupEstablished,
		avahiGroupCollision,
		avahiGroupFailure,
		"Avahi entry group",
	); err != nil {
		return err
	}
	actual, err := publisher.api.HostName(registrationContext)
	if err != nil {
		return fmt.Errorf("confirm Avahi hostname: %w", err)
	}
	if !strings.EqualFold(actual, publisher.hostname) {
		return fmt.Errorf(
			"Avahi hostname changed from %q to %q while setup was starting",
			publisher.hostname,
			actual,
		)
	}
	return nil
}

// Hostname returns the host-managed name used by this publisher.
func (publisher *Publisher) Hostname() string {
	return publisher.hostname
}

// Close withdraws the service and closes the dedicated D-Bus connection. It never
// changes the host's Avahi hostname. Close is idempotent.
func (publisher *Publisher) Close(ctx context.Context) error {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if publisher.closed {
		return nil
	}
	publisher.closed = true

	var result error
	if publisher.group.IsValid() && publisher.group != "/" {
		if err := publisher.api.Free(ctx, publisher.group); err != nil {
			result = errors.Join(result, fmt.Errorf("withdraw Avahi service: %w", err))
		}
	}
	if err := publisher.api.Close(); err != nil {
		result = errors.Join(result, fmt.Errorf("close Avahi D-Bus connection: %w", err))
	}
	return result
}

func validateOptions(options Options) error {
	if strings.TrimSpace(options.ServiceName) == "" {
		return errors.New("mDNS service name is required")
	}
	if options.Port == 0 {
		return errors.New("mDNS service port is required")
	}
	return nil
}

func readHostname(ctx context.Context, api interface {
	HostName(context.Context) (string, error)
},
) (string, error) {
	hostname, err := api.HostName(ctx)
	if err != nil {
		return "", fmt.Errorf("read Avahi hostname: %w", err)
	}
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if hostname == "" || strings.Contains(hostname, ".") || len(hostname) > 63 {
		return "", fmt.Errorf("Avahi hostname %q must be one non-empty DNS label of at most 63 characters", hostname)
	}
	for index, character := range hostname {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			(character == '-' && index > 0 && index < len(hostname)-1) {
			continue
		}
		return "", fmt.Errorf("Avahi hostname %q is not a valid DNS label", hostname)
	}
	return hostname, nil
}

func waitForState(
	ctx context.Context,
	read func(context.Context) (int32, error),
	want, collision, failure int32,
	label string,
) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		state, err := read(ctx)
		if err != nil {
			return fmt.Errorf("read %s state: %w", label, err)
		}
		switch state {
		case want:
			return nil
		case collision:
			return fmt.Errorf("%s reported a name collision", label)
		case failure:
			return fmt.Errorf("%s reported a failure", label)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %s: %w", label, ctx.Err())
		case <-ticker.C:
		}
	}
}

type dbusAvahi struct {
	conn *dbus.Conn
}

func (api *dbusAvahi) HostName(ctx context.Context) (string, error) {
	var hostname string
	err := api.server().CallWithContext(ctx, avahiServerInterface+".GetHostName", 0).Store(&hostname)
	return hostname, err
}

func (api *dbusAvahi) ServerState(ctx context.Context) (int32, error) {
	var state int32
	err := api.server().CallWithContext(ctx, avahiServerInterface+".GetState", 0).Store(&state)
	return state, err
}

func (api *dbusAvahi) NewEntryGroup(ctx context.Context) (dbus.ObjectPath, error) {
	var path dbus.ObjectPath
	err := api.server().CallWithContext(ctx, avahiServerInterface+".EntryGroupNew", 0).Store(&path)
	return path, err
}

func (api *dbusAvahi) AddHTTPService(
	ctx context.Context,
	group dbus.ObjectPath,
	name string,
	port uint16,
) error {
	return api.group(group).CallWithContext(
		ctx,
		avahiGroupInterface+".AddService",
		0,
		int32(-1),
		int32(-1),
		uint32(0),
		name,
		"_http._tcp",
		"",
		"",
		port,
		[][]byte{[]byte("path=/"), []byte("product=onboardd")},
	).Err
}

func (api *dbusAvahi) Commit(ctx context.Context, group dbus.ObjectPath) error {
	return api.group(group).CallWithContext(ctx, avahiGroupInterface+".Commit", 0).Err
}

func (api *dbusAvahi) GroupState(ctx context.Context, group dbus.ObjectPath) (int32, error) {
	var state int32
	err := api.group(group).CallWithContext(ctx, avahiGroupInterface+".GetState", 0).Store(&state)
	return state, err
}

func (api *dbusAvahi) Free(ctx context.Context, group dbus.ObjectPath) error {
	return api.group(group).CallWithContext(ctx, avahiGroupInterface+".Free", 0).Err
}

func (api *dbusAvahi) Close() error {
	return api.conn.Close()
}

func (api *dbusAvahi) server() dbus.BusObject {
	return api.conn.Object(avahiService, avahiServerPath)
}

func (api *dbusAvahi) group(path dbus.ObjectPath) dbus.BusObject {
	return api.conn.Object(avahiService, path)
}
