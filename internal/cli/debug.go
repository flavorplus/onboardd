package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/BurntSushi/toml"
	appconfig "github.com/flavorplus/onboardd/internal/config"
	"github.com/flavorplus/onboardd/internal/connectivity"
	stateengine "github.com/flavorplus/onboardd/internal/state"
)

func runDebug(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" {
		printDebugHelp(stdout)
		return nil
	}

	switch args[0] {
	case "config":
		return debugConfig(args[1:], stdout, stderr)
	case "status":
		return debugStatus(ctx, args[1:], stdout, stderr)
	case "profiles":
		return debugProfiles(ctx, args[1:], stdout, stderr)
	case "profile-delete":
		return debugProfileDelete(ctx, args[1:], stdout, stderr)
	case "scan":
		return debugScan(ctx, args[1:], stdout, stderr)
	case "watch":
		return debugWatch(ctx, args[1:], stdout, stderr)
	case "reconcile":
		return debugReconcile(ctx, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown debug command %q; run 'onboardd debug help'", args[0])
	}
}

func debugConfig(args []string, stdout, stderr io.Writer) error {
	defaults := appconfig.Defaults()
	flags := newFlagSet("debug config", stderr)
	configPath := flags.String("config", appconfig.SystemPath, "TOML configuration file")
	operational := bindOperationalConfigFlags(flags, defaults)
	render := flags.Bool("render", false, "render text and SSID templates in the output")
	deviceID := flags.String("device-id", "", "debug-only device ID used with --render")
	hostname := flags.String("hostname", "", "debug-only hostname used with --render")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}

	set := explicitlySetFlags(flags)
	overrides, err := operational.overrides(flags)
	if err != nil {
		return err
	}
	resolved, err := appconfig.Resolve(appconfig.ResolveOptions{
		ConfigPath:     *configPath,
		ConfigOptional: !set["config"],
		Environment:    os.Environ(),
		Overrides:      overrides,
	})
	if err != nil {
		return err
	}
	if !*render && (*deviceID != "" || *hostname != "") {
		return errors.New("--device-id and --hostname require --render")
	}
	if *render {
		identity := appconfig.Identity{DeviceID: *deviceID, Hostname: *hostname}
		if identity.DeviceID == "" || identity.Hostname == "" {
			detected, err := appconfig.LoadIdentity()
			if err != nil {
				return err
			}
			if identity.DeviceID == "" {
				identity.DeviceID = detected.DeviceID
			}
			if identity.Hostname == "" {
				identity.Hostname = detected.Hostname
			}
		}
		resolved, err = appconfig.RenderTemplates(resolved, identity)
		if err != nil {
			return err
		}
	}
	if err := toml.NewEncoder(stdout).Encode(resolved); err != nil {
		return fmt.Errorf("print resolved configuration: %w", err)
	}
	return nil
}

func debugProfileDelete(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug profile-delete", stderr)
	uuid := flags.String("uuid", "", "UUID of an onboardd-owned profile")
	yes := flags.Bool("yes", false, "confirm profile deletion")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if !*yes {
		return errors.New("profile deletion requires --yes")
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.DeleteOwnedProfile(ctx, *uuid); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Deleted onboardd-owned profile %s\n", *uuid)
	return nil
}

func debugStatus(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug status", stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	status, err := client.Status(ctx, *interfaceName)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, status)
	}

	fmt.Fprintf(stdout, "NetworkManager %s\n", status.Version)
	fmt.Fprintf(stdout, "State: %s\n", status.StateName)
	fmt.Fprintf(stdout, "Connectivity: %s (check available: %t)\n", status.ConnectivityName, status.ConnectivityCheck)
	fmt.Fprintf(stdout, "Wireless: enabled=%t hardware-enabled=%t\n", status.WirelessEnabled, status.WirelessHardwareEnabled)
	fmt.Fprintf(stdout, "Startup: %t\n", status.Startup)
	fmt.Fprintf(
		stdout,
		"Device: %s state=%s managed=%t active=%s\n",
		status.Device.Interface,
		status.Device.StateName,
		status.Device.Managed,
		emptyAsDash(status.Device.ActiveConnection),
	)
	return nil
}

func debugProfiles(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug profiles", stderr)
	onlyOwned := flags.Bool("owned", false, "show only profiles owned by onboardd")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	profiles, err := client.Profiles(ctx)
	if err != nil {
		return err
	}
	if *onlyOwned {
		filtered := profiles[:0]
		for _, profile := range profiles {
			if profile.Owned {
				filtered = append(filtered, profile)
			}
		}
		profiles = filtered
	}
	if *jsonOutput {
		return writeJSON(stdout, profiles)
	}

	table := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tUUID\tROLE\tOWNED\tPENDING\tAUTO\tPRIORITY\tSTORAGE\tUNSAVED\tSSID")
	for _, profile := range profiles {
		fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%t\t%t\t%t\t%d\t%s\t%t\t%s\n",
			profile.ID,
			profile.UUID,
			emptyAsDash(string(profile.Role)),
			profile.Owned,
			profile.Pending,
			profile.Autoconnect,
			profile.Priority,
			profile.Persistence,
			profile.Unsaved,
			emptyAsDash(profile.SSID),
		)
	}
	return table.Flush()
}

func debugScan(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug scan", stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	wait := flags.Duration("wait", 5*time.Second, "maximum time to wait for a fresh scan")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	accessPoints, err := client.Scan(ctx, *interfaceName, *wait)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, accessPoints)
	}

	table := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "SSID\tSIGNAL\tSECURITY\tFREQUENCY\tBSSID")
	for _, accessPoint := range accessPoints {
		ssid := accessPoint.SSID
		if accessPoint.Hidden {
			ssid = "<hidden>"
		}
		fmt.Fprintf(
			table,
			"%s\t%d%%\t%s\t%d MHz\t%s\n",
			ssid,
			accessPoint.Strength,
			accessPoint.Security,
			accessPoint.Frequency,
			accessPoint.BSSID,
		)
	}
	return table.Flush()
}

func debugWatch(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug watch", stderr)
	jsonOutput := flags.Bool("json", false, "print one JSON object per line")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	events, watchErrors, err := client.WatchProperties(ctx)
	if err != nil {
		return err
	}
	if !*jsonOutput {
		fmt.Fprintln(stdout, "Watching NetworkManager property changes; press Ctrl+C to stop.")
	}
	for events != nil || watchErrors != nil {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if *jsonOutput {
				if err := json.NewEncoder(stdout).Encode(event); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(stdout, "%s %s changed=%v invalidated=%v\n", event.Path, event.Interface, event.Changed, event.Invalidated)
			}
		case watchErr, ok := <-watchErrors:
			if !ok {
				watchErrors = nil
				continue
			}
			if watchErr != nil {
				return watchErr
			}
		}
	}
	return nil
}

func debugReconcile(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug reconcile", stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	requirementValue := flags.String("requirement", string(connectivity.RequirementLocal), "connectivity requirement: local or internet")
	gracePeriod := flags.Duration("grace-period", 30*time.Second, "connectivity and activation grace period")
	watch := flags.Bool("watch", false, "continue reconciling until interrupted")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	requirement := connectivity.Requirement(*requirementValue)
	if err := requirement.Validate(); err != nil {
		return err
	}
	if *gracePeriod <= 0 {
		return errors.New("--grace-period must be positive")
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	observer := stateengine.NewNetworkManagerObserver(client, *interfaceName)
	engine, err := stateengine.New(observer, stateengine.Config{
		Requirement: requirement,
		GracePeriod: *gracePeriod,
	})
	if err != nil {
		return err
	}
	if !*watch {
		current, inspectErr := engine.Inspect(ctx)
		if inspectErr != nil {
			return inspectErr
		}
		return writeReconciliationState(stdout, current, *jsonOutput)
	}

	transitions, engineErrors, err := engine.Run(ctx)
	if err != nil {
		return err
	}
	ctxDone := ctx.Done()
	for transitions != nil || engineErrors != nil {
		select {
		case <-ctxDone:
			// Let the engine publish its final stopped transition and close its
			// channels before the D-Bus client is closed by this function.
			ctxDone = nil
		case transition, ok := <-transitions:
			if !ok {
				transitions = nil
				continue
			}
			if *jsonOutput {
				if err := writeJSON(stdout, transition); err != nil {
					return err
				}
				continue
			}
			fmt.Fprintf(
				stdout,
				"%s\t%d\t%s\t%s\t%s\t%s\n",
				time.Now().UTC().Format(time.RFC3339),
				transition.Current.Sequence,
				transition.Current.Stage,
				transition.Current.Mode,
				transition.Current.Reason,
				emptyAsDash(transition.Current.Detail),
			)
		case engineErr, ok := <-engineErrors:
			if !ok {
				engineErrors = nil
				continue
			}
			fmt.Fprintf(stderr, "onboardd reconcile: %v\n", engineErr)
		}
	}
	return nil
}

func writeReconciliationState(stdout io.Writer, current stateengine.State, jsonOutput bool) error {
	if jsonOutput {
		return writeJSON(stdout, current)
	}
	fmt.Fprintf(stdout, "Stage: %s\n", current.Stage)
	fmt.Fprintf(stdout, "Mode: %s\n", current.Mode)
	fmt.Fprintf(stdout, "Reason: %s\n", current.Reason)
	if current.Detail != "" {
		fmt.Fprintf(stdout, "Detail: %s\n", current.Detail)
	}
	return nil
}
