package captive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const nftTableName = "onboardd_captive"

var interfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)

// PortRedirector directs captive HTTP arriving on an interface to onboardd's private
// listener port without taking port 80 away from the appliance application.
type PortRedirector interface {
	Install(context.Context, string, uint16, uint16) error
	Remove(context.Context) error
}

type nftRunner interface {
	Run(context.Context, string, []string, string) ([]byte, error)
}

type execNFTRunner struct{}

func (execNFTRunner) Run(
	ctx context.Context,
	binary string,
	args []string,
	input string,
) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Stdin = strings.NewReader(input)
	return command.CombinedOutput()
}

// NFTRedirect owns one separately named nftables table. It never modifies tables or
// rules belonging to NetworkManager, InkyPi, or another application.
type NFTRedirect struct {
	binary string
	runner nftRunner
}

// NewNFTRedirect creates the Linux nftables adapter used by the captive lifecycle.
func NewNFTRedirect(binary string) (*NFTRedirect, error) {
	if binary == "" {
		return nil, errors.New("nft executable is required")
	}
	return &NFTRedirect{binary: binary, runner: execNFTRunner{}}, nil
}

// Install atomically creates a prerouting redirect limited to the provisioning
// interface and public HTTP port.
func (redirect *NFTRedirect) Install(
	ctx context.Context,
	interfaceName string,
	publicPort uint16,
	listenerPort uint16,
) error {
	rules, err := renderNFTRedirect(interfaceName, publicPort, listenerPort)
	if err != nil {
		return err
	}
	if err := redirect.Remove(ctx); err != nil {
		return fmt.Errorf("remove previous captive redirect: %w", err)
	}
	if output, err := redirect.runner.Run(ctx, redirect.binary, []string{"-f", "-"}, rules); err != nil {
		return commandError("install captive redirect", output, err)
	}
	return nil
}

// Remove deletes only onboardd's captive table and is safe to call repeatedly.
func (redirect *NFTRedirect) Remove(ctx context.Context) error {
	output, err := redirect.runner.Run(ctx, redirect.binary, []string{"-j", "list", "tables"}, "")
	if err != nil {
		return commandError("list nftables tables", output, err)
	}
	present, err := containsOwnedNFTTable(output)
	if err != nil {
		return err
	}
	if !present {
		return nil
	}
	output, err = redirect.runner.Run(
		ctx,
		redirect.binary,
		[]string{"delete", "table", "inet", nftTableName},
		"",
	)
	if err != nil {
		return commandError("remove captive redirect", output, err)
	}
	return nil
}

// renderNFTRedirect returns an isolated nftables ruleset for transparent captive HTTP.
func renderNFTRedirect(interfaceName string, publicPort, listenerPort uint16) (string, error) {
	if !interfaceNamePattern.MatchString(interfaceName) {
		return "", errors.New("interface name contains unsupported characters or exceeds 15 bytes")
	}
	if publicPort == 0 || listenerPort == 0 {
		return "", errors.New("redirect ports must be nonzero")
	}
	if publicPort == listenerPort {
		return "", errors.New("public and listener HTTP ports must differ")
	}
	return "table inet " + nftTableName + " {\n" +
		"  chain prerouting {\n" +
		"    type nat hook prerouting priority -100; policy accept;\n" +
		"    iifname " + strconv.Quote(interfaceName) +
		" tcp dport " + strconv.Itoa(int(publicPort)) +
		" redirect to :" + strconv.Itoa(int(listenerPort)) + "\n" +
		"  }\n" +
		"}\n", nil
}

type nftTableList struct {
	Objects []struct {
		Table *struct {
			Family string `json:"family"`
			Name   string `json:"name"`
		} `json:"table,omitempty"`
	} `json:"nftables"`
}

func containsOwnedNFTTable(output []byte) (bool, error) {
	var tables nftTableList
	if err := json.Unmarshal(output, &tables); err != nil {
		return false, fmt.Errorf("decode nftables table list: %w", err)
	}
	for _, object := range tables.Objects {
		if object.Table != nil && object.Table.Family == "inet" && object.Table.Name == nftTableName {
			return true, nil
		}
	}
	return false, nil
}

func commandError(operation string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %w: %s", operation, err, detail)
}
