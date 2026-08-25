package captive

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/flavorplus/onboardd/internal/networkmanager"
)

func TestManagerEntersLeavesAndReentersProvisioning(t *testing.T) {
	calls := []string{}
	provisioner, err := NewProvisioner(
		&fakeNetworkManager{calls: &calls},
		&fakeDNSConfigurer{calls: &calls},
	)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(
		provisioner,
		&fakePortRedirector{calls: &calls},
		validManagerOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := manager.EnterProvisioning(context.Background()); err != nil {
			t.Fatalf("EnterProvisioning() error = %v", err)
		}
		if err := manager.EnterProvisioning(context.Background()); err != nil {
			t.Fatalf("duplicate EnterProvisioning() error = %v", err)
		}
		if err := manager.ExitCaptive(context.Background()); err != nil {
			t.Fatalf("ExitCaptive() error = %v", err)
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
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestManagerRecoversStaleCaptiveResourcesBeforeReconciliation(t *testing.T) {
	calls := []string{}
	network := &fakeNetworkManager{calls: &calls}
	provisioner, err := NewProvisioner(network, &fakeDNSConfigurer{calls: &calls})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(
		provisioner,
		&fakePortRedirector{calls: &calls},
		validManagerOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := manager.RecoverStartup(context.Background()); err != nil {
		t.Fatalf("RecoverStartup() error = %v", err)
	}
	want := []string{"redirect-remove", "finalize", "dns-remove"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	if network.finalizedRole != networkmanager.RoleProvisioning ||
		network.finalizedInterface != "wlan0" || network.finalizedKeepUUID != "" {
		t.Fatalf(
			"finalized stale profile scope = interface %q, role %q, keep %q",
			network.finalizedInterface,
			network.finalizedRole,
			network.finalizedKeepUUID,
		)
	}
}

func TestManagerStartupRecoveryAttemptsEveryOwnedResource(t *testing.T) {
	calls := []string{}
	network := &fakeNetworkManager{calls: &calls, fail: "finalize"}
	dns := &fakeDNSConfigurer{calls: &calls, removeFailures: 1}
	provisioner, err := NewProvisioner(network, dns)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(
		provisioner,
		&fakePortRedirector{calls: &calls, removeFailures: 1},
		validManagerOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}

	err = manager.RecoverStartup(context.Background())
	if err == nil {
		t.Fatal("RecoverStartup() error = nil")
	}
	for _, call := range []string{"redirect-remove", "finalize", "dns-remove"} {
		if !containsCall(calls, call) {
			t.Errorf("startup cleanup is missing %q: %#v", call, calls)
		}
	}
}

func TestManagerUnwindsProvisioningWhenRedirectFails(t *testing.T) {
	calls := []string{}
	provisioner, err := NewProvisioner(
		&fakeNetworkManager{calls: &calls},
		&fakeDNSConfigurer{calls: &calls},
	)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(
		provisioner,
		&fakePortRedirector{calls: &calls, fail: "redirect"},
		validManagerOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}

	err = manager.EnterProvisioning(context.Background())
	if err == nil || !strings.Contains(err.Error(), "install captive HTTP redirect") {
		t.Fatalf("EnterProvisioning() error = %v", err)
	}
	for _, want := range []string{"redirect-remove", "delete:provisioning-uuid", "dns-remove"} {
		if !containsCall(calls, want) {
			t.Errorf("cleanup is missing %q: %#v", want, calls)
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
			calls := []string{}
			network := &fakeNetworkManager{calls: &calls}
			dns := &fakeDNSConfigurer{calls: &calls}
			redirect := &fakePortRedirector{calls: &calls}
			test.configure(network, dns, redirect)
			provisioner, err := NewProvisioner(network, dns)
			if err != nil {
				t.Fatal(err)
			}
			manager, err := NewManager(provisioner, redirect, validManagerOptions())
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.EnterProvisioning(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := manager.LeaveProvisioning(context.Background()); err == nil {
				t.Fatal("first LeaveProvisioning() error = nil")
			}
			if err := manager.LeaveProvisioning(context.Background()); err != nil {
				t.Fatalf("second LeaveProvisioning() error = %v", err)
			}
			if got := countCalls(calls, test.wantRepeatedCall); got != 2 {
				t.Fatalf("%s calls = %d, want 2; calls = %#v", test.wantRepeatedCall, got, calls)
			}
			for _, call := range test.wantSingleCalls {
				if got := countCalls(calls, call); got != 1 {
					t.Fatalf("%s calls = %d, want 1; calls = %#v", call, got, calls)
				}
			}
		})
	}
}

func TestManagerRetriesFailedEntryCleanupBeforeReentering(t *testing.T) {
	calls := []string{}
	network := &fakeNetworkManager{calls: &calls}
	dns := &fakeDNSConfigurer{calls: &calls}
	redirect := &fakePortRedirector{
		calls:          &calls,
		fail:           "redirect",
		removeFailures: 1,
	}
	provisioner, err := NewProvisioner(network, dns)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(provisioner, redirect, validManagerOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.EnterProvisioning(context.Background()); err == nil {
		t.Fatal("first EnterProvisioning() error = nil")
	}
	redirect.fail = ""
	if err := manager.EnterProvisioning(context.Background()); err != nil {
		t.Fatalf("second EnterProvisioning() error = %v", err)
	}
	if got := countCalls(calls, "start-ap"); got != 2 {
		t.Fatalf("start-ap calls = %d, want 2; calls = %#v", got, calls)
	}
	if got := countCalls(calls, "redirect-remove"); got != 2 {
		t.Fatalf("redirect-remove calls = %d, want 2; calls = %#v", got, calls)
	}
}

func TestManagerRetriesIncompleteProvisionerStartBeforeReentering(t *testing.T) {
	calls := []string{}
	network := &fakeNetworkManager{
		calls:          &calls,
		fail:           "wait",
		deleteFailures: 1,
	}
	dns := &fakeDNSConfigurer{calls: &calls}
	redirect := &fakePortRedirector{calls: &calls}
	provisioner, err := NewProvisioner(network, dns)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(provisioner, redirect, validManagerOptions())
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.EnterProvisioning(context.Background()); err == nil {
		t.Fatal("first EnterProvisioning() error = nil")
	}
	network.fail = ""
	if err := manager.EnterProvisioning(context.Background()); err != nil {
		t.Fatalf("second EnterProvisioning() error = %v", err)
	}
	if got := countCalls(calls, "delete:provisioning-uuid"); got != 2 {
		t.Fatalf("profile delete calls = %d, want 2; calls = %#v", got, calls)
	}
	if got := countCalls(calls, "start-ap"); got != 2 {
		t.Fatalf("start-ap calls = %d, want 2; calls = %#v", got, calls)
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
