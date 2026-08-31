package discovery

import (
	"context"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestPublisherRegistersServiceWithoutChangingHostname(t *testing.T) {
	api := &fakeAvahi{
		hostname:    "display-player",
		serverState: avahiServerRunning,
		groupState:  avahiGroupEstablished,
		group:       "/Client1/EntryGroup1",
	}
	publisher := newPublisher(api)
	if err := publisher.start(context.Background(), Options{
		ServiceName: "Display Player setup",
		Port:        18080,
	}); err != nil {
		t.Fatal(err)
	}
	if publisher.Hostname() != "display-player" || api.serviceName != "Display Player setup" || api.port != 18080 {
		t.Fatalf("published hostname=%q service=%q port=%d", api.hostname, api.serviceName, api.port)
	}
	if !api.committed {
		t.Fatal("entry group was not committed")
	}
	if err := publisher.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if api.hostname != "display-player" || api.freed != api.group || !api.closed {
		t.Fatalf("cleanup hostname=%q freed=%q closed=%t", api.hostname, api.freed, api.closed)
	}
	if err := publisher.Close(context.Background()); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
}

func TestPublisherReportsEntryGroupCollision(t *testing.T) {
	api := &fakeAvahi{
		hostname:    "display",
		serverState: avahiServerRunning,
		groupState:  avahiGroupCollision,
		group:       "/Client1/EntryGroup3",
	}
	err := newPublisher(api).start(context.Background(), Options{
		ServiceName: "Display setup",
		Port:        18080,
	})
	if err == nil || !strings.Contains(err.Error(), "name collision") {
		t.Fatalf("error = %v", err)
	}
}

func TestPublisherDetectsHostnameChangeDuringStartup(t *testing.T) {
	api := &fakeAvahi{
		hostname:     "display",
		nextHostname: "display-2",
		serverState:  avahiServerRunning,
		groupState:   avahiGroupEstablished,
		group:        "/Client1/EntryGroup4",
	}
	err := newPublisher(api).start(context.Background(), Options{
		ServiceName: "Display setup",
		Port:        18080,
	})
	if err == nil || !strings.Contains(err.Error(), `changed from "display" to "display-2"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestPublisherRejectsInvalidAvahiHostname(t *testing.T) {
	api := &fakeAvahi{hostname: "display.local"}
	err := newPublisher(api).start(context.Background(), Options{
		ServiceName: "Display setup",
		Port:        18080,
	})
	if err == nil || !strings.Contains(err.Error(), "one non-empty DNS label") || api.hostNameReads != 1 {
		t.Fatalf("error = %v, hostname reads = %d", err, api.hostNameReads)
	}
}

func TestReadHostnameNormalizesAvahiName(t *testing.T) {
	api := &fakeAvahi{hostname: "Display-Player"}
	hostname, err := readHostname(context.Background(), api)
	if err != nil || hostname != "display-player" {
		t.Fatalf("hostname = %q, error = %v", hostname, err)
	}
}

type fakeAvahi struct {
	hostname      string
	nextHostname  string
	serverState   int32
	groupState    int32
	group         dbus.ObjectPath
	serviceName   string
	port          uint16
	hostNameReads int
	committed     bool
	freed         dbus.ObjectPath
	closed        bool
	err           error
}

func (api *fakeAvahi) HostName(context.Context) (string, error) {
	api.hostNameReads++
	if api.hostNameReads > 1 && api.nextHostname != "" {
		return api.nextHostname, api.err
	}
	return api.hostname, api.err
}

func (api *fakeAvahi) ServerState(context.Context) (int32, error) {
	return api.serverState, api.err
}

func (api *fakeAvahi) NewEntryGroup(context.Context) (dbus.ObjectPath, error) {
	return api.group, api.err
}

func (api *fakeAvahi) AddHTTPService(
	_ context.Context,
	_ dbus.ObjectPath,
	name string,
	port uint16,
) error {
	api.serviceName = name
	api.port = port
	return api.err
}

func (api *fakeAvahi) Commit(context.Context, dbus.ObjectPath) error {
	api.committed = true
	return api.err
}

func (api *fakeAvahi) GroupState(context.Context, dbus.ObjectPath) (int32, error) {
	return api.groupState, api.err
}

func (api *fakeAvahi) Free(_ context.Context, path dbus.ObjectPath) error {
	api.freed = path
	return api.err
}

func (api *fakeAvahi) Close() error {
	api.closed = true
	return api.err
}

var _ avahiAPI = (*fakeAvahi)(nil)

func TestValidateOptions(t *testing.T) {
	tests := []struct {
		name        string
		options     Options
		expectedErr string
	}{
		{
			name:    "service name and port",
			options: Options{ServiceName: "Display setup", Port: 18080},
		},
		{
			name:        "missing service name",
			options:     Options{Port: 18080},
			expectedErr: "mDNS service name is required",
		},
		{
			name:        "blank service name",
			options:     Options{ServiceName: "   ", Port: 18080},
			expectedErr: "mDNS service name is required",
		},
		{
			name:        "missing port",
			options:     Options{ServiceName: "Display setup"},
			expectedErr: "mDNS service port is required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateOptions(test.options)
			if test.expectedErr == "" {
				if err != nil {
					t.Fatalf("validateOptions() error = %v, expected none", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.expectedErr) {
				t.Fatalf("validateOptions() error = %v, expected %q", err, test.expectedErr)
			}
		})
	}
}
