// Package cli implements onboardd's standard-library command-line interface.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/flavorplus/onboardd/internal/buildinfo"
	"github.com/flavorplus/onboardd/internal/networkmanager"
)

const defaultInterface = "wlan0"

// Run executes the onboardd command line.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printRootHelp(stdout)
		return nil
	}
	if args[0] == "debug" {
		return runDebug(ctx, args[1:], stdout, stderr)
	}
	if args[0] == "setup" {
		err := runSetup(ctx, args[1:], stdout, stderr)
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	root := flag.NewFlagSet("onboardd", flag.ContinueOnError)
	root.SetOutput(stderr)
	showVersion := root.Bool("version", false, "print version information")
	if err := root.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Fprintf(stdout, "onboardd %s\n", buildinfo.Version)
		return nil
	}
	if root.NArg() > 0 && root.Arg(0) == "help" {
		printRootHelp(stdout)
		return nil
	}
	return fmt.Errorf("unknown command %q; run 'onboardd help'", strings.Join(args, " "))
}

func openClient() (*networkmanager.Client, error) {
	client, err := networkmanager.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("NetworkManager access requires a Linux system bus: %w", err)
	}
	return client, nil
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func requireNoArgs(flags *flag.FlagSet) error {
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	return nil
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

func emptyAsDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func printRootHelp(writer io.Writer) {
	fmt.Fprintln(writer, "onboardd - headless appliance network onboarding")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Usage:")
	fmt.Fprintln(writer, "  onboardd --version")
	fmt.Fprintln(writer, "  onboardd setup [--config /etc/onboardd/config.toml] [operational overrides]")
	fmt.Fprintln(writer, "  onboardd debug <command> [options]")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "The setup command loads TOML, environment variables, and CLI overrides, then starts")
	fmt.Fprintln(writer, "the embedded setup portal. Run 'onboardd setup -h' for its options.")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Run 'onboardd debug help' for NetworkManager and reconciliation diagnostics.")
}

func printDebugHelp(writer io.Writer) {
	fmt.Fprintln(writer, "NetworkManager and reconciliation diagnostics")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "  onboardd debug config [--config FILE] [--render] [operational overrides]")
	fmt.Fprintln(writer, "  onboardd debug status [--interface wlan0] [--json]")
	fmt.Fprintln(writer, "  onboardd debug profiles [--owned] [--json]")
	fmt.Fprintln(writer, "  onboardd debug profile-delete --uuid UUID --yes")
	fmt.Fprintln(writer, "  onboardd debug scan [--interface wlan0] [--wait 5s] [--json]")
	fmt.Fprintln(writer, "  onboardd debug watch [--json]")
	fmt.Fprintln(writer, "  onboardd debug reconcile [--requirement local|internet] [--watch] [--json]")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Network changes belong to the configured 'onboardd setup' workflow.")
}
