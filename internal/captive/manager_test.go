package captive

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/flavorplus/onboardd/internal/networkmanager"
)

func TestManagerEntersLeavesAndReentersProvisioning(t *testing.T) {
	manager, fakes := newTestManager(t)

	for range 2 {
		if err := manager.EnterProvisioning(context.Background()); err != nil {
			t.Fatalf("EnterProvisioning() error = %v", err)
		}
		if err := manager.EnterProvisioning(context.Background()); err != nil {
			t.Fatalf("duplicate EnterProvisioning() error = %v", err)
		}
		if err := manager.LeaveProvisioning(context.Background()); err != nil {
			t.Fatalf("LeaveProvisioning() error = %v", err)
		}
		if err := manager.LeaveProvisioning(context.Background()); err != nil {
			t.Fatalf("duplicate LeaveProvisioning() error = %v", err)
		}
	}

	wantCycle := []string{
		"dns-install:10.42.0.1",
		"start-ap",
		"wait",
		"status",
		"finalize",
		"redirect-install:wlan0:80:18080",
		"redirect-remove",
		"delete:provisioning-uuid",
		"dns-remove",
	}
	want := append(append([]string{}, wantCycle...), wantCycle...)
	if !reflect.DeepEqual(*fakes.calls, want) {
		t.Fatalf("calls = %#v, want %#v", *fakes.calls, want)
	}
}

func TestManagerRecoversStaleCaptiveResourcesBeforeReconciliation(t *testing.T) {
	manager, fakes := newTestManager(t)

	if err := manager.RecoverStartup(context.Background()); err != nil {
		t.Fatalf("RecoverStartup() error = %v", err)
	}
	want := []string{"redirect-remove", "finalize", "dns-remove"}
	if !reflect.DeepEqual(*fakes.calls, want) {
		t.Fatalf("calls = %#v, want %#v", *fakes.calls, want)
	}
	if fakes.network.finalizedRole != networkmanager.RoleProvisioning ||
		fakes.network.finalizedInterface != "wlan0" || fakes.network.finalizedKeepUUID != "" {
		t.Fatalf(
			"finalized stale profile scope = interface %q, role %q, keep %q",
			fakes.network.finalizedInterface,
			fakes.network.finalizedRole,
			fakes.network.finalizedKeepUUID,
		)
	}
}

func TestManagerStartupRecoveryAttemptsEveryOwnedResource(t *testing.T) {
	manager, fakes := newTestManager(t)
	fakes.network.fail = "finalize"
	fakes.dns.removeFailures = 1
	fakes.redirect.removeFailures = 1

	err := manager.RecoverStartup(context.Background())
	if err == nil {
		t.Fatal("RecoverStartup() error = nil")
	}
	for _, call := range []string{"redirect-remove", "finalize", "dns-remove"} {
		if !containsCall(*fakes.calls, call) {
			t.Errorf("startup cleanup is missing %q: %#v", call, *fakes.calls)
		}
	}
}

func TestManagerUnwindsProvisioningWhenRedirectFails(t *testing.T) {
	manager, fakes := newTestManager(t)
	fakes.redirect.fail = "redirect"

	err := manager.EnterProvisioning(context.Background())
	if err == nil || !strings.Contains(err.Error(), "install captive HTTP redirect") {
		t.Fatalf("EnterProvisioning() error = %v", err)
	}
	for _, want := range []string{"redirect-remove", "delete:provisioning-uuid", "dns-remove"} {
		if !containsCall(*fakes.calls, want) {
			t.Errorf("cleanup is missing %q: %#v", want, *fakes.calls)
		}
	}
}

func TestManagerRetriesOnlyIncompleteCaptiveCleanup(t *testing.T) {
	for _, test := range []struct {
		name             string
		configure        func(*fakeNetworkManager, *fakeDNSConfigurer, *fakePortRedirector)
		wantRepeatedCall string
		wantSingleCalls  []string
	}{
		{
			name: "redirect removal",
			configure: func(_ *fakeNetworkManager, _ *fakeDNSConfigurer, redirect *fakePortRedirector) {
				redirect.removeFailures = 1
			},
			wantRepeatedCall: "redirect-remove",
			wantSingleCalls:  []string{"delete:provisioning-uuid", "dns-remove"},
		},
		{
			name: "profile deletion",
			configure: func(network *fakeNetworkManager, _ *fakeDNSConfigurer, _ *fakePortRedirector) {
				network.deleteFailures = 1
			},
			wantRepeatedCall: "delete:provisioning-uuid",
			wantSingleCalls:  []string{"redirect-remove", "dns-remove"},
		},
		{
			name: "DNS removal",
			configure: func(_ *fakeNetworkManager, dns *fakeDNSConfigurer, _ *fakePortRedirector) {
				dns.removeFailures = 1
			},
			wantRepeatedCall: "dns-remove",
			wantSingleCalls:  []string{"redirect-remove", "delete:provisioning-uuid"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, fakes := newTestManager(t)
			test.configure(fakes.network, fakes.dns, fakes.redirect)
			if err := manager.EnterProvisioning(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := manager.LeaveProvisioning(context.Background()); err == nil {
				t.Fatal("first LeaveProvisioning() error = nil")
			}
			if err := manager.LeaveProvisioning(context.Background()); err != nil {
				t.Fatalf("second LeaveProvisioning() error = %v", err)
			}
			if got := countCalls(*fakes.calls, test.wantRepeatedCall); got != 2 {
				t.Fatalf("%s calls = %d, want 2; calls = %#v", test.wantRepeatedCall, got, *fakes.calls)
			}
			for _, call := range test.wantSingleCalls {
				if got := countCalls(*fakes.calls, call); got != 1 {
					t.Fatalf("%s calls = %d, want 1; calls = %#v", call, got, *fakes.calls)
				}
			}
		})
	}
}

func TestManagerRetriesFailedEntryCleanupBeforeReentering(t *testing.T) {
	manager, fakes := newTestManager(t)
	fakes.redirect.fail = "redirect"
	fakes.redirect.removeFailures = 1
	if err := manager.EnterProvisioning(context.Background()); err == nil {
		t.Fatal("first EnterProvisioning() error = nil")
	}
	fakes.redirect.fail = ""
	if err := manager.EnterProvisioning(context.Background()); err != nil {
		t.Fatalf("second EnterProvisioning() error = %v", err)
	}
	if got := countCalls(*fakes.calls, "start-ap"); got != 2 {
		t.Fatalf("start-ap calls = %d, want 2; calls = %#v", got, *fakes.calls)
	}
	if got := countCalls(*fakes.calls, "redirect-remove"); got != 2 {
		t.Fatalf("redirect-remove calls = %d, want 2; calls = %#v", got, *fakes.calls)
	}
}

func TestManagerRetriesIncompleteProvisionerStartBeforeReentering(t *testing.T) {
	manager, fakes := newTestManager(t)
	fakes.network.fail = "wait"
	fakes.network.deleteFailures = 1
	if err := manager.EnterProvisioning(context.Background()); err == nil {
		t.Fatal("first EnterProvisioning() error = nil")
	}
	fakes.network.fail = ""
	if err := manager.EnterProvisioning(context.Background()); err != nil {
		t.Fatalf("second EnterProvisioning() error = %v", err)
	}
	if got := countCalls(*fakes.calls, "delete:provisioning-uuid"); got != 2 {
		t.Fatalf("profile delete calls = %d, want 2; calls = %#v", got, *fakes.calls)
	}
	if got := countCalls(*fakes.calls, "start-ap"); got != 2 {
		t.Fatalf("start-ap calls = %d, want 2; calls = %#v", got, *fakes.calls)
	}
}

func TestNewManagerValidatesDependenciesAndOptions(t *testing.T) {
	calls := []string{}
	provisioner, err := NewProvisioner(
		&fakeNetworkManager{calls: &calls},
		&fakeDNSConfigurer{calls: &calls},
	)
	if err != nil {
		t.Fatal(err)
	}
	redirect := &fakePortRedirector{calls: &calls}
	if _, err := NewManager(nil, redirect, validManagerOptions()); err == nil {
		t.Fatal("nil provisioner unexpectedly accepted")
	}
	if _, err := NewManager(provisioner, nil, validManagerOptions()); err == nil {
		t.Fatal("nil redirector unexpectedly accepted")
	}
	options := validManagerOptions()
	options.ListenerHTTPPort = options.PublicHTTPPort
	if _, err := NewManager(provisioner, redirect, options); err == nil {
		t.Fatal("colliding HTTP ports unexpectedly accepted")
	}
}

func validManagerOptions() ManagerOptions {
	return ManagerOptions{
		Provisioning:     validProvisioningOptions(),
		PublicHTTPPort:   80,
		ListenerHTTPPort: 18080,
		CleanupTimeout:   time.Second,
	}
}

func countCalls(calls []string, want string) int {
	count := 0
	for _, call := range calls {
		if call == want {
			count++
		}
	}
	return count
}

func containsCall(calls []string, want string) bool {
	return countCalls(calls, want) != 0
}

type fakeDNSConfigurer struct {
	calls          *[]string
	fail           string
	removeFailures int
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
	if dns.removeFailures > 0 {
		dns.removeFailures--
		return errors.New("test removal failure")
	}
	return nil
}

type fakePortRedirector struct {
	calls          *[]string
	fail           string
	removeFailures int
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
	if redirect.removeFailures > 0 {
		redirect.removeFailures--
		return errors.New("test removal failure")
	}
	return nil
}

type fakeNetworkManager struct {
	calls              *[]string
	fail               string
	deleteFailures     int
	finalizedInterface string
	finalizedRole      networkmanager.Role
	finalizedKeepUUID  string
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
		ActiveUUID:    "provisioning-uuid",
		IPv4Addresses: []string{"10.42.0.1"},
	}}, nil
}

func (network *fakeNetworkManager) FinalizeTransition(
	_ context.Context,
	interfaceName string,
	role networkmanager.Role,
	_ string,
	keepUUID string,
) error {
	*network.calls = append(*network.calls, "finalize")
	network.finalizedInterface = interfaceName
	network.finalizedRole = role
	network.finalizedKeepUUID = keepUUID
	if network.fail == "finalize" {
		return errors.New("test failure")
	}
	return nil
}

func (network *fakeNetworkManager) DeleteOwnedProfile(_ context.Context, uuid string) error {
	*network.calls = append(*network.calls, "delete:"+uuid)
	if network.deleteFailures > 0 {
		network.deleteFailures--
		return errors.New("test deletion failure")
	}
	return nil
}

// captiveFakes are the three collaborators a Manager is built from. Their
// failure fields are read when the method is called, not when the fake is
// constructed, so a test may configure them after newTestManager returns.
type captiveFakes struct {
	calls    *[]string
	network  *fakeNetworkManager
	dns      *fakeDNSConfigurer
	redirect *fakePortRedirector
}

func newTestManager(t *testing.T) (*Manager, captiveFakes) {
	t.Helper()
	calls := []string{}
	fakes := captiveFakes{
		calls:    &calls,
		network:  &fakeNetworkManager{calls: &calls},
		dns:      &fakeDNSConfigurer{calls: &calls},
		redirect: &fakePortRedirector{calls: &calls},
	}
	provisioner, err := NewProvisioner(fakes.network, fakes.dns)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(provisioner, fakes.redirect, validManagerOptions())
	if err != nil {
		t.Fatal(err)
	}
	return manager, fakes
}
