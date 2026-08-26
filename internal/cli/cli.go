// Package cli implements onboardd's standard-library command-line interface.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/flavorplus/onboardd/internal/networkmanager"
	"github.com/flavorplus/onboardd/internal/recovery"
)

// Version is replaced through -ldflags for release builds.
var Version = "development"

// Run executes the onboardd command line.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printRootHelp(stdout)
		return nil
	}
	if args[0] == "run" {
		err := runAppliance(ctx, args[1:], stdout, stderr)
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if args[0] == "recover" {
		err := runRecover(ctx, args[1:], stdout, stderr)
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
		fmt.Fprintf(stdout, "onboardd %s\n", Version)
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

func printRootHelp(writer io.Writer) {
	fmt.Fprintln(writer, "onboardd - headless appliance network onboarding")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Usage:")
	fmt.Fprintln(writer, "  onboardd --version")
	fmt.Fprintln(writer, "  onboardd run [--config /etc/onboardd/config.toml] [operational overrides]")
	fmt.Fprintln(writer, "  onboardd recover")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "The run command reconciles network state and opens recovery setup when needed.")
	fmt.Fprintln(writer, "The recover command asks a running appliance to enter manual recovery.")
	fmt.Fprintln(writer, "The run command loads TOML and operational CLI overrides.")
}

func runRecover(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("recover", stderr)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if err := recovery.RequestControl(ctx, recovery.ControlSocketPath); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "manual recovery requested")
	return nil
}
