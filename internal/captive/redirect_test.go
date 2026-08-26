package captive

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRenderNFTRedirect(t *testing.T) {
	got, err := renderNFTRedirect("wlan0", 80, 18080)
	if err != nil {
		t.Fatalf("renderNFTRedirect() error = %v", err)
	}
	want := "table inet onboardd_captive {\n" +
		"  chain prerouting {\n" +
		"    type nat hook prerouting priority -100; policy accept;\n" +
		"    iifname \"wlan0\" tcp dport 80 redirect to :18080\n" +
		"  }\n" +
		"}\n"
	if got != want {
		t.Fatalf("renderNFTRedirect() = %q, want %q", got, want)
	}
}

func TestRenderNFTRedirectValidatesInput(t *testing.T) {
	for _, test := range []struct {
		name          string
		interfaceName string
		publicPort    uint16
		listenerPort  uint16
	}{
		{name: "empty interface", publicPort: 80, listenerPort: 18080},
		{name: "unsafe interface", interfaceName: `wlan0\"; flush ruleset`, publicPort: 80, listenerPort: 18080},
		{name: "zero public port", interfaceName: "wlan0", listenerPort: 18080},
		{name: "zero listener port", interfaceName: "wlan0", publicPort: 80},
		{name: "same port", interfaceName: "wlan0", publicPort: 80, listenerPort: 80},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := renderNFTRedirect(test.interfaceName, test.publicPort, test.listenerPort); err == nil {
				t.Fatal("renderNFTRedirect() unexpectedly succeeded")
			}
		})
	}
}

func TestNFTRedirectReplacesAndRemovesOwnedTable(t *testing.T) {
	runner := &fakeNFTRunner{responses: [][]byte{
		[]byte(`{"nftables":[{"table":{"family":"inet","name":"onboardd_captive"}},{"table":{"family":"inet","name":"foreign"}}]}`),
		nil,
		nil,
		[]byte(`{"nftables":[{"table":{"family":"inet","name":"onboardd_captive"}}]}`),
		nil,
	}}
	redirect := &NFTRedirect{binary: "nft", runner: runner}

	if err := redirect.Install(context.Background(), "wlan0", 80, 18080); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if err := redirect.Remove(context.Background()); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	wantArgs := [][]string{
		{"-j", "list", "tables"},
		{"delete", "table", "inet", "onboardd_captive"},
		{"-f", "-"},
		{"-j", "list", "tables"},
		{"delete", "table", "inet", "onboardd_captive"},
	}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("command args = %#v, want %#v", runner.args, wantArgs)
	}
	if !strings.Contains(runner.inputs[2], `iifname "wlan0" tcp dport 80 redirect to :18080`) {
		t.Fatalf("install input = %q", runner.inputs[2])
	}
}

func TestNFTRedirectRemoveIsIdempotentAndPreservesForeignTables(t *testing.T) {
	runner := &fakeNFTRunner{responses: [][]byte{
		[]byte(`{"nftables":[{"table":{"family":"inet","name":"foreign"}}]}`),
	}}
	redirect := &NFTRedirect{binary: "nft", runner: runner}

	if err := redirect.Remove(context.Background()); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if len(runner.args) != 1 {
		t.Fatalf("Remove() issued unexpected commands: %#v", runner.args)
	}
}

func TestNFTRedirectReportsCommandOutput(t *testing.T) {
	runner := &fakeNFTRunner{
		responses: [][]byte{[]byte("permission denied\n")},
		errors:    []error{errors.New("exit status 1")},
	}
	redirect := &NFTRedirect{binary: "nft", runner: runner}

	err := redirect.Remove(context.Background())
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("Remove() error = %v", err)
	}
}

type fakeNFTRunner struct {
	responses [][]byte
	errors    []error
	args      [][]string
	inputs    []string
}

func (runner *fakeNFTRunner) Run(
	_ context.Context,
	_ string,
	args []string,
	input string,
) ([]byte, error) {
	index := len(runner.args)
	runner.args = append(runner.args, append([]string(nil), args...))
	runner.inputs = append(runner.inputs, input)
	var output []byte
	if index < len(runner.responses) {
		output = runner.responses[index]
	}
	var err error
	if index < len(runner.errors) {
		err = runner.errors[index]
	}
	return output, err
}
