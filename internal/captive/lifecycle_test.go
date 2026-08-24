package captive

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/flavorplus/onboardd/internal/networkmanager"
)

func TestLifecycleStartAndStopOrdering(t *testing.T) {
	calls := []string{}
	network := &fakeNetworkManager{calls: &calls}
	dns := &fakeDNSConfigurer{calls: &calls}
	redirect := &fakePortRedirector{calls: &calls}
	listener := newMemoryListener()
	lifecycle, err := NewLifecycle(
		network,
		dns,
		redirect,
		func(_ context.Context, network, address string) (net.Listener, error) {
			calls = append(calls, "listen:"+network+":"+address)
			return listener, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	session, err := lifecycle.Start(
		context.Background(),
		validStartOptions(),
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got := session.PortalURL(); got != "http://10.42.0.1/" {
		t.Fatalf("PortalURL() = %q", got)
	}
	if got := session.Activation().UUID; got != "provisioning-uuid" {
		t.Fatalf("Activation().UUID = %q", got)
	}

	stopContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.ExitCaptive(stopContext); err != nil {
		t.Fatalf("ExitCaptive() error = %v", err)
	}
	select {
	case <-session.Done():
		t.Fatal("HTTP listener stopped while leaving captive mode")
	default:
	}
	want := []string{
		"dns-install:10.42.0.1",
		"start-ap",
		"wait",
		"status",
		"finalize",
		"listen:tcp4:0.0.0.0:18080",
		"redirect-install:wlan0:80:18080",
		"redirect-remove",
		"delete:provisioning-uuid",
		"dns-remove",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	if err := session.Stop(stopContext); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case <-session.Done():
	default:
		t.Fatal("HTTP listener remains active after Stop()")
	}
	if err := session.Stop(stopContext); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("second Stop() repeated cleanup: %#v", calls)
	}
}

func TestLifecycleStartUnwindsFailures(t *testing.T) {
	stages := []struct {
		name       string
		fail       string
		wantError  string
		wantDelete bool
		wantRemove bool
	}{
		{name: "DNS", fail: "dns-install", wantError: "prepare captive DNS"},
		{name: "AP", fail: "start-ap", wantError: "activate provisioning AP", wantRemove: true},
		{name: "activation wait", fail: "wait", wantError: "wait for provisioning AP", wantDelete: true, wantRemove: true},
		{name: "status", fail: "status", wantError: "confirm provisioning AP address", wantDelete: true, wantRemove: true},
		{name: "finalize", fail: "finalize", wantError: "finalize provisioning AP", wantDelete: true, wantRemove: true},
		{name: "listen", fail: "listen", wantError: "bind captive HTTP listener", wantDelete: true, wantRemove: true},
		{name: "redirect", fail: "redirect", wantError: "install captive HTTP redirect", wantDelete: true, wantRemove: true},
	}

	for _, test := range stages {
		t.Run(test.name, func(t *testing.T) {
			calls := []string{}
			network := &fakeNetworkManager{calls: &calls, fail: test.fail}
			dns := &fakeDNSConfigurer{calls: &calls, fail: test.fail}
			redirect := &fakePortRedirector{calls: &calls, fail: test.fail}
			lifecycle, err := NewLifecycle(
				network,
				dns,
				redirect,
				func(context.Context, string, string) (net.Listener, error) {
					if test.fail == "listen" {
						return nil, errors.New("test failure")
					}
					return newMemoryListener(), nil
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = lifecycle.Start(
				context.Background(),
				validStartOptions(),
				http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
			)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Start() error = %v, want containing %q", err, test.wantError)
			}
			if got := containsCall(calls, "delete:provisioning-uuid"); got != test.wantDelete {
				t.Errorf("delete called = %t, want %t; calls = %#v", got, test.wantDelete, calls)
			}
			if got := containsCall(calls, "dns-remove"); got != test.wantRemove {
				t.Errorf("DNS remove called = %t, want %t; calls = %#v", got, test.wantRemove, calls)
			}
		})
	}
}

func TestLifecycleRejectsInvalidOptionsBeforeSideEffects(t *testing.T) {
	calls := []string{}
	lifecycle, err := NewLifecycle(
		&fakeNetworkManager{calls: &calls},
		&fakeDNSConfigurer{calls: &calls},
		&fakePortRedirector{calls: &calls},
		func(context.Context, string, string) (net.Listener, error) { return newMemoryListener(), nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	options := validStartOptions()
	options.PublicHTTPPort = 0
	if _, err := lifecycle.Start(
		context.Background(),
		options,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	); err == nil || !strings.Contains(err.Error(), "HTTP port") {
		t.Fatalf("Start() error = %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("invalid options caused side effects: %#v", calls)
	}
}

func TestLifecycleRejectsInvalidAPSettingsBeforeSideEffects(t *testing.T) {
	calls := []string{}
	lifecycle, err := NewLifecycle(
		&fakeNetworkManager{calls: &calls},
		&fakeDNSConfigurer{calls: &calls},
		&fakePortRedirector{calls: &calls},
		func(context.Context, string, string) (net.Listener, error) { return newMemoryListener(), nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	options := validStartOptions()
	options.Password = "short"
	if _, err := lifecycle.Start(
		context.Background(),
		options,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	); err == nil || !strings.Contains(err.Error(), "validate provisioning AP") {
		t.Fatalf("Start() error = %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("invalid AP settings caused side effects: %#v", calls)
	}
}

func TestConfirmProvisioningAddress(t *testing.T) {
	status := networkmanager.Status{Device: networkmanager.Device{
		State:         networkmanager.DeviceStateActivated,
		StateName:     "activated",
		ActiveUUID:    "expected",
		IPv4Addresses: []string{"10.42.0.1"},
	}}
	if err := confirmProvisioningAddress(status, "expected", netip.MustParseAddr("10.42.0.1")); err != nil {
		t.Fatalf("confirmProvisioningAddress() error = %v", err)
	}
	status.Device.ActiveUUID = "other"
	if err := confirmProvisioningAddress(status, "expected", netip.MustParseAddr("10.42.0.1")); err == nil {
		t.Fatal("wrong active UUID unexpectedly accepted")
	}
}

func validStartOptions() StartOptions {
	return StartOptions{
		Interface:        "wlan0",
		SSID:             "Onboardd Setup",
		Password:         "test-password",
		Address:          netip.MustParsePrefix("10.42.0.1/24"),
		Band:             "bg",
		Wait:             30 * time.Second,
		PublicHTTPPort:   80,
		ListenerHTTPPort: 18080,
		PortalURL:        "http://10.42.0.1/",
		SetupURL:         "http://device.local:18080/",
		LandingPage:      testLandingPage,
	}
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

type fakeDNSConfigurer struct {
	calls *[]string
	fail  string
}

type fakePortRedirector struct {
	calls *[]string
	fail  string
}

func (redirect *fakePortRedirector) Install(
	_ context.Context,
	interfaceName string,
	publicPort uint16,
	listenerPort uint16,
) error {
	*redirect.calls = append(
		*redirect.calls,
		fmt.Sprintf("redirect-install:%s:%d:%d", interfaceName, publicPort, listenerPort),
	)
	if redirect.fail == "redirect" {
		return errors.New("test failure")
	}
	return nil
}

func (redirect *fakePortRedirector) Remove(context.Context) error {
	*redirect.calls = append(*redirect.calls, "redirect-remove")
	return nil
}

func (dns *fakeDNSConfigurer) Install(address netip.Addr) error {
	*dns.calls = append(*dns.calls, "dns-install:"+address.String())
	if dns.fail == "dns-install" {
		return errors.New("test failure")
	}
	return nil
}

func (dns *fakeDNSConfigurer) Remove() error {
	*dns.calls = append(*dns.calls, "dns-remove")
	return nil
}

type fakeNetworkManager struct {
	calls *[]string
	fail  string
}

func (network *fakeNetworkManager) StartAccessPoint(
	_ context.Context,
	options networkmanager.AccessPointOptions,
) (networkmanager.Activation, error) {
	*network.calls = append(*network.calls, "start-ap")
	if network.fail == "start-ap" {
		return networkmanager.Activation{}, errors.New("test failure")
	}
	if options.Role != networkmanager.RoleProvisioning || options.Autoconnect {
		return networkmanager.Activation{}, errors.New("invalid provisioning options")
	}
	return networkmanager.Activation{
		UUID:       "provisioning-uuid",
		ActivePath: "/org/freedesktop/NetworkManager/ActiveConnection/1",
		Role:       networkmanager.RoleProvisioning,
	}, nil
}

func (network *fakeNetworkManager) WaitForActivation(context.Context, string, string, time.Duration) error {
	*network.calls = append(*network.calls, "wait")
	if network.fail == "wait" {
		return errors.New("test failure")
	}
	return nil
}

func (network *fakeNetworkManager) Status(context.Context, string) (networkmanager.Status, error) {
	*network.calls = append(*network.calls, "status")
	if network.fail == "status" {
		return networkmanager.Status{}, errors.New("test failure")
	}
	return networkmanager.Status{Device: networkmanager.Device{
		State:         networkmanager.DeviceStateActivated,
		StateName:     "activated",
		ActiveUUID:    "provisioning-uuid",
		IPv4Addresses: []string{"10.42.0.1"},
	}}, nil
}

func (network *fakeNetworkManager) FinalizeTransition(
	context.Context,
	string,
	networkmanager.Role,
	string,
	string,
) error {
	*network.calls = append(*network.calls, "finalize")
	if network.fail == "finalize" {
		return errors.New("test failure")
	}
	return nil
}

func (network *fakeNetworkManager) DeleteOwnedProfile(_ context.Context, uuid string) error {
	*network.calls = append(*network.calls, "delete:"+uuid)
	return nil
}
