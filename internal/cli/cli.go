// Package cli implements onboardd's small standard-library command-line interface.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/flavorplus/onboardd/internal/buildinfo"
	"github.com/flavorplus/onboardd/internal/captive"
	"github.com/flavorplus/onboardd/internal/connectivity"
	"github.com/flavorplus/onboardd/internal/networkmanager"
	"github.com/flavorplus/onboardd/internal/recovery"
	stateengine "github.com/flavorplus/onboardd/internal/state"
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

func runDebug(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" {
		printDebugHelp(stdout)
		return nil
	}

	switch args[0] {
	case "status":
		return debugStatus(ctx, args[1:], stdout, stderr)
	case "profiles":
		return debugProfiles(ctx, args[1:], stdout, stderr)
	case "profile-delete":
		return debugProfileDelete(ctx, args[1:], stdout, stderr)
	case "scan":
		return debugScan(ctx, args[1:], stdout, stderr)
	case "connect":
		return debugConnect(ctx, args[1:], stdout, stderr)
	case "provisioning-start":
		return debugAccessPoint(ctx, args[1:], stdout, stderr, networkmanager.RoleProvisioning)
	case "standalone-start":
		return debugAccessPoint(ctx, args[1:], stdout, stderr, networkmanager.RoleStandalone)
	case "captive-start":
		return debugCaptiveStart(ctx, args[1:], stdout, stderr)
	case "connect-protected":
		return debugConnectProtected(ctx, args[1:], stdout, stderr)
	case "watch":
		return debugWatch(ctx, args[1:], stdout, stderr)
	case "reconcile":
		return debugReconcile(ctx, args[1:], stdout, stderr)
	case "checkpoint-create":
		return debugCheckpointCreate(ctx, args[1:], stdout, stderr)
	case "checkpoint-commit":
		return debugCheckpointCommit(ctx, args[1:], stdout, stderr)
	case "checkpoint-rollback":
		return debugCheckpointRollback(ctx, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown debug command %q; run 'onboardd debug help'", args[0])
	}
}

func debugConnectProtected(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug connect-protected", stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	ssid := flags.String("ssid", "", "target Wi-Fi SSID")
	passwordFile := flags.String("password-file", "", "file containing the Wi-Fi password")
	open := flags.Bool("open", false, "connect to an explicitly open network")
	hidden := flags.Bool("hidden", false, "target network hides its SSID")
	id := flags.String("id", "", "optional human-readable NetworkManager profile ID")
	priority := flags.Int("priority", 0, "autoconnect priority from -999 to 999")
	requirementText := flags.String("requirement", "local", "connectivity requirement: local or internet")
	activationWait := flags.Duration("wait", 30*time.Second, "maximum time to confirm candidate activation")
	rollbackAfter := flags.Duration("rollback-after", 90*time.Second, "automatic checkpoint rollback duration")
	restorationWait := flags.Duration("restoration-wait", 30*time.Second, "maximum time to confirm AP restoration")
	provisioningUUID := flags.String("provisioning-uuid", "", "active provisioning profile UUID")
	provisioningAddressText := flags.String("provisioning-address", "10.42.0.1", "provisioning AP IPv4 address")
	yes := flags.Bool("yes", false, "confirm the disruptive checkpoint-backed Wi-Fi change")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if !*yes {
		return errors.New("protected connect changes the active Wi-Fi interface; repeat with --yes while captive-start provides the recovery path")
	}
	if *priority < -999 || *priority > 999 {
		return errors.New("--priority must be between -999 and 999")
	}
	requirement := connectivity.Requirement(*requirementText)
	if err := requirement.Validate(); err != nil {
		return err
	}
	provisioningAddress, err := netip.ParseAddr(*provisioningAddressText)
	if err != nil {
		return fmt.Errorf("parse --provisioning-address: %w", err)
	}
	password, err := readPassword(*passwordFile, *open)
	if err != nil {
		return err
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	transition, err := recovery.NewInfrastructure(client)
	if err != nil {
		return err
	}
	activation, err := transition.Attempt(ctx, recovery.InfrastructureOptions{
		Interface: *interfaceName,
		Candidate: networkmanager.InfrastructureOptions{
			ID:       *id,
			SSID:     *ssid,
			Password: password,
			Open:     *open,
			Hidden:   *hidden,
			Priority: int32(*priority),
		},
		Requirement:             requirement,
		ActivationWait:          *activationWait,
		RollbackAfter:           *rollbackAfter,
		RestorationWait:         *restorationWait,
		ProvisioningUUID:        *provisioningUUID,
		ProvisioningIPv4Address: provisioningAddress,
	})
	if err != nil {
		return err
	}
	return writeActivation(stdout, activation, *jsonOutput)
}

func debugCaptiveStart(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug captive-start", stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	ssid := flags.String("ssid", "", "provisioning access-point SSID")
	passwordFile := flags.String("password-file", "", "file containing the access-point password")
	addressText := flags.String("address", "10.42.0.1/24", "access-point IPv4 address and prefix")
	band := flags.String("band", "bg", "Wi-Fi band: bg, a, or 6GHz")
	wait := flags.Duration("wait", 30*time.Second, "maximum time to confirm AP activation")
	httpPort := flags.Uint("http-port", 80, "captive HTTP port")
	listenerHTTPPort := flags.Uint("listener-http-port", 18080, "private onboardd HTTP listener port")
	portalURL := flags.String("portal-url", "", "canonical cleartext portal URL (defaults to the AP address)")
	dnsConfigPath := flags.String(
		"dns-config",
		"/etc/NetworkManager/dnsmasq-shared.d/onboardd.conf",
		"NetworkManager dnsmasq-shared fragment path",
	)
	yes := flags.Bool("yes", false, "confirm the disruptive Wi-Fi change")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if !*yes {
		return errors.New("starting captive provisioning changes the active Wi-Fi interface; repeat with --yes after ensuring a recovery path")
	}
	if *wait <= 0 {
		return errors.New("--wait must be positive")
	}
	if *httpPort == 0 || *httpPort > 65535 {
		return errors.New("--http-port must be between 1 and 65535")
	}
	if *listenerHTTPPort == 0 || *listenerHTTPPort > 65535 {
		return errors.New("--listener-http-port must be between 1 and 65535")
	}
	if *httpPort == *listenerHTTPPort {
		return errors.New("--http-port and --listener-http-port must differ")
	}
	address, err := netip.ParsePrefix(*addressText)
	if err != nil {
		return fmt.Errorf("parse --address: %w", err)
	}
	password, err := readRequiredPassword(*passwordFile)
	if err != nil {
		return err
	}
	canonicalURL := *portalURL
	if canonicalURL == "" {
		host := address.Addr().String()
		if *httpPort != 80 {
			host = net.JoinHostPort(host, fmt.Sprint(*httpPort))
		}
		canonicalURL = "http://" + host + "/"
	}

	dns, err := captive.NewDNSConfigFile(*dnsConfigPath)
	if err != nil {
		return err
	}
	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	redirect, err := captive.NewNFTRedirect("nft")
	if err != nil {
		return err
	}
	listenConfig := &net.ListenConfig{}
	lifecycle, err := captive.NewLifecycle(client, dns, redirect, listenConfig.Listen)
	if err != nil {
		return err
	}
	portal := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(
			response,
			"<!doctype html><html><head><meta name=\"viewport\" content=\"width=device-width\"></head>"+
				"<body><main><h1>onboardd setup</h1><p>The Phase 3 captive portal is reachable.</p>"+
				"</main></body></html>",
		)
	})
	session, err := lifecycle.Start(ctx, captive.StartOptions{
		Interface:        *interfaceName,
		SSID:             *ssid,
		Password:         password,
		Address:          address,
		Band:             *band,
		Wait:             *wait,
		PublicHTTPPort:   uint16(*httpPort),
		ListenerHTTPPort: uint16(*listenerHTTPPort),
		PortalURL:        canonicalURL,
	}, portal)
	if err != nil {
		return err
	}

	activation := session.Activation()
	fmt.Fprintln(stdout, "captive provisioning is ready")
	fmt.Fprintf(stdout, "SSID: %s\n", *ssid)
	fmt.Fprintf(stdout, "Portal: %s\n", session.PortalURL())
	fmt.Fprintf(stdout, "UUID: %s\n", activation.UUID)
	fmt.Fprintln(stdout, "Press Ctrl+C to stop and remove the temporary provisioning AP.")

	var serveErr error
	select {
	case <-ctx.Done():
	case <-session.Done():
		serveErr = session.Wait()
		if serveErr == nil {
			serveErr = errors.New("captive HTTP listener stopped unexpectedly")
		}
	}
	cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelCleanup()
	stopErr := session.Stop(cleanupContext)
	if serveErr != nil || stopErr != nil {
		return errors.Join(serveErr, stopErr)
	}
	fmt.Fprintln(stdout, "captive provisioning stopped and temporary resources were removed")
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
	fmt.Fprintln(table, "ID\tUUID\tROLE\tOWNED\tAUTO\tPRIORITY\tSTORAGE\tUNSAVED\tSSID")
	for _, profile := range profiles {
		fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%t\t%t\t%d\t%s\t%t\t%s\n",
			profile.ID,
			profile.UUID,
			emptyAsDash(string(profile.Role)),
			profile.Owned,
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

func debugConnect(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug connect", stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	ssid := flags.String("ssid", "", "target Wi-Fi SSID")
	passwordFile := flags.String("password-file", "", "file containing the Wi-Fi password")
	open := flags.Bool("open", false, "connect to an explicitly open network")
	hidden := flags.Bool("hidden", false, "target network hides its SSID")
	id := flags.String("id", "", "optional human-readable NetworkManager profile ID")
	priority := flags.Int("priority", 0, "autoconnect priority from -999 to 999")
	wait := flags.Duration("wait", 30*time.Second, "maximum time to confirm activation")
	yes := flags.Bool("yes", false, "confirm the disruptive Wi-Fi change")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if !*yes {
		return errors.New("connect changes the active Wi-Fi interface; repeat with --yes after ensuring a recovery path")
	}
	if *priority < -999 || *priority > 999 {
		return errors.New("--priority must be between -999 and 999")
	}
	if *wait <= 0 {
		return errors.New("--wait must be positive so activation can be confirmed before selecting infrastructure mode")
	}
	password, err := readPassword(*passwordFile, *open)
	if err != nil {
		return err
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	activation, err := client.ConnectInfrastructure(ctx, networkmanager.InfrastructureOptions{
		ID:          *id,
		Interface:   *interfaceName,
		SSID:        *ssid,
		Password:    password,
		Open:        *open,
		Hidden:      *hidden,
		Autoconnect: false,
		Priority:    int32(*priority),
	})
	if err != nil {
		return err
	}
	if err := client.WaitForActivation(ctx, activation.ActivePath, *interfaceName, *wait); err != nil {
		return fmt.Errorf("profile %s was created but activation failed: %w", activation.UUID, err)
	}
	if err := client.FinalizeTransition(
		ctx,
		*interfaceName,
		networkmanager.RoleInfrastructure,
		*ssid,
		activation.UUID,
	); err != nil {
		return fmt.Errorf("profile %s activated but mode selection failed: %w", activation.UUID, err)
	}
	return writeActivation(stdout, activation, *jsonOutput)
}

func debugAccessPoint(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	role networkmanager.Role,
) error {
	commandName := "debug " + string(role) + "-start"
	flags := newFlagSet(commandName, stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	ssid := flags.String("ssid", "", "access-point SSID")
	passwordFile := flags.String("password-file", "", "file containing the access-point password")
	address := flags.String("address", "10.42.0.1/24", "access-point IPv4 address and prefix")
	band := flags.String("band", "bg", "Wi-Fi band: bg, a, or 6GHz")
	id := flags.String("id", "", "optional human-readable NetworkManager profile ID")
	wait := flags.Duration("wait", 30*time.Second, "maximum time to confirm activation")
	yes := flags.Bool("yes", false, "confirm the disruptive Wi-Fi change")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if !*yes {
		return errors.New("starting an access point changes the active Wi-Fi interface; repeat with --yes after ensuring a recovery path")
	}
	if *wait <= 0 {
		return errors.New("--wait must be positive so activation can be confirmed before selecting access-point mode")
	}
	password, err := readRequiredPassword(*passwordFile)
	if err != nil {
		return err
	}

	priority := int32(0)
	if role == networkmanager.RoleStandalone {
		priority = 999
	}
	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	activation, err := client.StartAccessPoint(ctx, networkmanager.AccessPointOptions{
		ID:          *id,
		Interface:   *interfaceName,
		SSID:        *ssid,
		Password:    password,
		Address:     *address,
		Role:        role,
		Autoconnect: false,
		Priority:    priority,
		Band:        *band,
	})
	if err != nil {
		return err
	}
	if err := client.WaitForActivation(ctx, activation.ActivePath, *interfaceName, *wait); err != nil {
		return fmt.Errorf("profile %s was created but activation failed: %w", activation.UUID, err)
	}
	if err := client.FinalizeTransition(ctx, *interfaceName, role, *ssid, activation.UUID); err != nil {
		return fmt.Errorf("profile %s activated but mode selection failed: %w", activation.UUID, err)
	}
	return writeActivation(stdout, activation, *jsonOutput)
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

func debugCheckpointCreate(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug checkpoint-create", stderr)
	interfaceName := flags.String("interface", defaultInterface, "NetworkManager Wi-Fi interface")
	rollbackAfter := flags.Duration("rollback-after", 90*time.Second, "automatic rollback duration")
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
	checkpoint, err := client.CreateCheckpoint(ctx, *interfaceName, *rollbackAfter)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, checkpoint)
	}
	fmt.Fprintf(stdout, "Checkpoint: %s\n", checkpoint.Path)
	fmt.Fprintf(stdout, "Interface: %s\n", checkpoint.Interface)
	fmt.Fprintf(stdout, "Automatic rollback: %d seconds\n", checkpoint.RollbackSeconds)
	return nil
}

func debugCheckpointCommit(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug checkpoint-commit", stderr)
	path := flags.String("path", "", "NetworkManager checkpoint object path")
	yes := flags.Bool("yes", false, "confirm removal of rollback protection")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if !*yes {
		return errors.New("checkpoint commit removes rollback protection; repeat with --yes")
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.CommitCheckpoint(ctx, *path); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Committed checkpoint %s\n", *path)
	return nil
}

func debugCheckpointRollback(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := newFlagSet("debug checkpoint-rollback", stderr)
	path := flags.String("path", "", "NetworkManager checkpoint object path")
	yes := flags.Bool("yes", false, "confirm disruptive network rollback")
	jsonOutput := flags.Bool("json", false, "print JSON output")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireNoArgs(flags); err != nil {
		return err
	}
	if !*yes {
		return errors.New("checkpoint rollback changes the active network configuration; repeat with --yes")
	}

	client, err := openClient()
	if err != nil {
		return err
	}
	defer client.Close()
	result, err := client.RollbackCheckpoint(ctx, *path)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "Rolled back checkpoint %s\n", result.Checkpoint)
	for device, code := range result.Devices {
		fmt.Fprintf(stdout, "Device %s: result %d\n", device, code)
	}
	return nil
}

func openClient() (*networkmanager.Client, error) {
	client, err := networkmanager.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("NetworkManager debug commands require a Linux system bus: %w", err)
	}
	return client, nil
}

func readPassword(path string, open bool) (string, error) {
	if open {
		if path != "" {
			return "", errors.New("--open and --password-file cannot be used together")
		}
		return "", nil
	}
	return readRequiredPassword(path)
}

func readRequiredPassword(path string) (string, error) {
	if path == "" {
		return "", errors.New("--password-file is required; passwords are intentionally not accepted as command-line values")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read password file: %w", err)
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(contents), "\n"), "\r"), nil
}

func writeActivation(stdout io.Writer, activation networkmanager.Activation, jsonOutput bool) error {
	if jsonOutput {
		return writeJSON(stdout, activation)
	}
	fmt.Fprintf(stdout, "%s profile activated\n", activation.Role)
	fmt.Fprintf(stdout, "UUID: %s\n", activation.UUID)
	fmt.Fprintf(stdout, "Persistence: %s\n", activation.Persistence)
	fmt.Fprintf(stdout, "Profile: %s\n", activation.ProfilePath)
	fmt.Fprintf(stdout, "Active connection: %s\n", activation.ActivePath)
	return nil
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
	fmt.Fprintln(writer, "  onboardd debug <command> [options]")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Run 'onboardd debug help' for NetworkManager and reconciliation tools.")
}

func printDebugHelp(writer io.Writer) {
	fmt.Fprintln(writer, "NetworkManager D-Bus and reconciliation diagnostics")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Read-only commands:")
	fmt.Fprintln(writer, "  onboardd debug status [--interface wlan0] [--json]")
	fmt.Fprintln(writer, "  onboardd debug profiles [--owned] [--json]")
	fmt.Fprintln(writer, "  onboardd debug scan [--interface wlan0] [--wait 5s] [--json]")
	fmt.Fprintln(writer, "  onboardd debug watch [--json]")
	fmt.Fprintln(writer, "  onboardd debug reconcile [--requirement local|internet] [--watch] [--json]")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Checkpoint commands:")
	fmt.Fprintln(writer, "  onboardd debug checkpoint-create [--interface wlan0] [--rollback-after 90s]")
	fmt.Fprintln(writer, "  onboardd debug checkpoint-commit --path OBJECT_PATH --yes")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Disruptive commands (require --yes and a recovery path):")
	fmt.Fprintln(writer, "  onboardd debug connect --ssid NAME (--password-file FILE | --open) --yes")
	fmt.Fprintln(writer, "  onboardd debug connect-protected --ssid NAME --provisioning-uuid UUID --yes")
	fmt.Fprintln(writer, "  onboardd debug provisioning-start --ssid NAME --password-file FILE --yes")
	fmt.Fprintln(writer, "  onboardd debug standalone-start --ssid NAME --password-file FILE --yes")
	fmt.Fprintln(writer, "  onboardd debug captive-start --ssid NAME --password-file FILE --yes")
	fmt.Fprintln(writer, "  onboardd debug profile-delete --uuid UUID --yes")
	fmt.Fprintln(writer, "  onboardd debug checkpoint-rollback --path OBJECT_PATH --yes")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Passwords are accepted only through files and are never printed.")
}
