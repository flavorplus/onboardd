package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/flavorplus/onboardd/internal/appliance"
	"github.com/flavorplus/onboardd/internal/captive"
	"github.com/flavorplus/onboardd/internal/discovery"
	"github.com/flavorplus/onboardd/internal/networkmanager"
	"github.com/flavorplus/onboardd/internal/observability"
	"github.com/flavorplus/onboardd/internal/recovery"
	setupflow "github.com/flavorplus/onboardd/internal/setup"
	"github.com/flavorplus/onboardd/internal/webui"
)

const (
	applianceCleanupTimeout = 15 * time.Second
	runtimeMaxRestarts      = 3
	runtimeRestartDelay     = 2 * time.Second
)

func runAppliance(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	options, err := loadSetupOptions(ctx, "run", args, stderr)
	if err != nil {
		return err
	}
	serviceNotifier, err := observability.NewNotifier()
	if err != nil {
		return fmt.Errorf("configure systemd notifications: %w", err)
	}
	lifecycle := observability.NewLifecycle(stderr)
	return runManagedAppliance(ctx, options, stdout, lifecycle, serviceNotifier)
}

func runManagedAppliance(
	ctx context.Context,
	options setupOptions,
	stdout io.Writer,
	lifecycle *observability.Lifecycle,
	serviceNotifier *observability.Notifier,
) (result error) {
	if err := validateSetupOptions(options); err != nil {
		return err
	}
	if lifecycle == nil {
		return errors.New("appliance lifecycle observer is required")
	}
	lifecycle.Starting(ctx)
	failureComponent := observability.ComponentRuntime
	failure := observability.FailureStartup
	defer func() {
		finalContext := context.WithoutCancel(ctx)
		if result != nil {
			lifecycle.Failed(finalContext, failureComponent, failure)
			result = observability.RedactRuntimeError(result)
			return
		}
		lifecycle.Stopped(finalContext)
	}()

	runtimeContext, cancelRuntime := context.WithCancel(ctx)
	defer cancelRuntime()
	landingPage, err := fs.ReadFile(options.Assets, "landing.html")
	if err != nil {
		return fmt.Errorf("frontend landing page: %w", err)
	}

	dns, err := captive.NewDNSConfigFile(options.DNSConfigPath)
	if err != nil {
		return err
	}
	client, err := openClient()
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			result = errors.Join(
				result,
				fmt.Errorf("close NetworkManager D-Bus connection: %w", closeErr),
			)
		}
	}()
	components, err := buildApplianceComponents(
		runtimeContext,
		client,
		dns,
		landingPage,
		options,
		lifecycle,
	)
	if err != nil {
		return err
	}
	stopOperations := func() error {
		return shutdownSetupOperations(
			runtimeContext,
			components.setup,
			options.RestorationWait+applianceCleanupTimeout,
		)
	}

	listenConfig := &net.ListenConfig{}
	listenAddress := net.JoinHostPort("0.0.0.0", fmt.Sprint(options.ListenerHTTPPort))
	httpService, err := captive.StartHTTPService(
		runtimeContext,
		listenConfig.Listen,
		components.handler,
		captive.HTTPServiceOptions{
			Network:         "tcp4",
			Address:         listenAddress,
			MaxRestarts:     runtimeMaxRestarts,
			RestartDelay:    runtimeRestartDelay,
			ShutdownTimeout: applianceCleanupTimeout,
			OnRetry:         retryReporter(lifecycle, observability.ComponentHTTP),
			OnRecovered: func(ctx context.Context, _ int) {
				lifecycle.ComponentRecovered(ctx, observability.ComponentHTTP)
			},
		},
	)
	if err != nil {
		return fmt.Errorf("bind setup HTTP listener: %w", err)
	}
	listenerDone := make(chan error, 1)
	go func() {
		listenerDone <- httpService.Run(runtimeContext)
	}()
	stopListener := func() error {
		cancelRuntime()
		return <-listenerDone
	}
	startupRecoveryContext, cancelStartupRecovery := context.WithTimeout(
		runtimeContext,
		applianceCleanupTimeout,
	)
	startupRecoveryErr := errors.Join(
		components.captive.RecoverStartup(startupRecoveryContext),
		client.DeletePendingInfrastructureProfiles(
			startupRecoveryContext,
			options.Interface,
		),
	)
	cancelStartupRecovery()
	if startupRecoveryErr != nil {
		return errors.Join(
			fmt.Errorf("recover interrupted appliance resources: %w", startupRecoveryErr),
			stopListener(),
			stopOperations(),
		)
	}

	publisher, err := discovery.Start(runtimeContext, discovery.Options{
		ServiceName: options.ReadyLabel,
		Port:        options.ListenerHTTPPort,
	})
	if err != nil {
		listenerErr := stopListener()
		return errors.Join(err, listenerErr, stopOperations())
	}
	stopStartupResources := func() error {
		listenerErr := stopListener()
		operationErr := stopOperations()
		return errors.Join(
			listenerErr,
			operationErr,
			stopManagedResources(runtimeContext, components.captive, publisher),
		)
	}
	if !strings.EqualFold(publisher.Hostname(), options.Hostname) {
		startErr := fmt.Errorf(
			"Avahi hostname changed from %q to %q while onboardd was starting",
			options.Hostname,
			publisher.Hostname(),
		)
		return errors.Join(startErr, stopStartupResources())
	}

	supervisor, recoveryRequests, err := buildReconciler(client, components.captive, options, lifecycle)
	if err != nil {
		return errors.Join(err, stopStartupResources())
	}
	controlServer, err := recovery.StartControlServer(
		recovery.ControlSocketPath,
		recoveryRequests,
	)
	if err != nil {
		return errors.Join(
			fmt.Errorf("start manual recovery control: %w", err),
			stopStartupResources(),
		)
	}
	controlDone := make(chan error, 1)
	go func() {
		controlDone <- controlServer.Run(runtimeContext)
	}()

	fmt.Fprintf(stdout, "%s is running\n", options.ReadyLabel)
	fmt.Fprintf(stdout, "Setup: %s\n", options.Branding.Handoff.SetupURL)
	fmt.Fprintf(stdout, "Recovery SSID: %s\n", options.ProvisioningSSID)
	fmt.Fprintln(stdout, "Manual recovery: sudo onboardd recover")
	failure = observability.FailureOperational

	supervised := []supervisedComponent{
		{
			name:      "appliance controller",
			component: observability.ComponentReconciler,
			run:       func() error { return supervisor.Run(runtimeContext) },
		},
		{
			name:      "setup HTTP listener",
			component: observability.ComponentHTTP,
			run:       func() error { return <-listenerDone },
		},
		{
			name:      "manual recovery control",
			component: observability.ComponentControl,
			run:       func() error { return <-controlDone },
		},
	}
	if serviceNotifier != nil && serviceNotifier.Enabled() {
		supervised = append(supervised, supervisedComponent{
			name:      "systemd notifier",
			component: observability.ComponentSystemd,
			run: func() error {
				return serviceNotifier.Run(runtimeContext, lifecycle.Health())
			},
		})
	}
	componentErrors, failedComponent, unexpected := superviseComponents(
		runtimeContext,
		cancelRuntime,
		lifecycle,
		supervised,
	)
	if unexpected {
		failure = observability.FailureUnexpected
	}
	if failedComponent != "" {
		failureComponent = failedComponent
	}
	operationErr := stopOperations()
	cleanupErr := errors.Join(
		operationErr,
		stopManagedResources(runtimeContext, components.captive, publisher),
	)
	if len(componentErrors) > 0 || cleanupErr != nil {
		return errors.Join(errors.Join(componentErrors...), cleanupErr)
	}
	fmt.Fprintln(stdout, "Onboardd appliance stopped and temporary resources were removed")
	return nil
}

// retryReporter adapts a component identity to the OnRetry callback shape shared
// by the HTTP listener and the reconciler supervisors.
func retryReporter(
	lifecycle *observability.Lifecycle,
	component observability.Component,
) func(context.Context, int, int) {
	return func(ctx context.Context, attempt, maximum int) {
		lifecycle.ComponentRetry(ctx, component, attempt, maximum)
	}
}

// applianceComponents is everything the appliance constructs before it starts
// anything. Construction carries no cleanup obligation: every failure below is a
// plain return because nothing has been started yet.
type applianceComponents struct {
	captive *captive.Manager
	setup   *setupflow.Service
	handler http.Handler
}

// buildApplianceComponents wires the captive plumbing, the setup workflow, and
// the HTTP handler that fronts them. The D-Bus client and the DNS configuration
// are created by the caller so that a bad configuration path is still reported
// before the system bus is dialled.
func buildApplianceComponents(
	runtimeContext context.Context,
	client *networkmanager.Client,
	dns *captive.DNSConfigFile,
	landingPage []byte,
	options setupOptions,
	lifecycle *observability.Lifecycle,
) (applianceComponents, error) {
	redirect, err := captive.NewNFTRedirect("nft")
	if err != nil {
		return applianceComponents{}, err
	}
	provisioner, err := captive.NewProvisioner(client, dns)
	if err != nil {
		return applianceComponents{}, err
	}
	captiveManager, err := captive.NewManager(
		provisioner,
		redirect,
		captive.ManagerOptions{
			Provisioning: captive.ProvisioningOptions{
				Interface: options.Interface,
				SSID:      options.ProvisioningSSID,
				Password:  options.ProvisioningPSK,
				Address:   options.ProvisioningAddress,
				Band:      options.Band,
				Wait:      options.ActivationWait,
			},
			PublicHTTPPort:   options.PublicHTTPPort,
			ListenerHTTPPort: options.ListenerHTTPPort,
			CleanupTimeout:   applianceCleanupTimeout,
		},
	)
	if err != nil {
		return applianceComponents{}, err
	}

	backend, err := newNetworkBackend(client, captiveManager, options)
	if err != nil {
		return applianceComponents{}, err
	}
	service, err := setupflow.NewService(runtimeContext, backend, setupflow.Capabilities{
		Network:    options.NetworkEnabled,
		Standalone: options.StandaloneEnabled,
	})
	if err != nil {
		return applianceComponents{}, err
	}
	api, err := webui.NewAPI(
		service,
		options.PortalOrigin,
		webui.Authentication{Password: options.AdminPassword},
		options.Branding,
	)
	if err != nil {
		return applianceComponents{}, err
	}
	applicationHandler, err := webui.NewHandler(api, options.Assets, options.Branding)
	if err != nil {
		return applianceComponents{}, err
	}
	setupHandler := http.NewServeMux()
	setupHandler.Handle("/healthz", lifecycle.Health())
	setupHandler.Handle("/", applicationHandler)
	handler, err := captive.NewHTTPHandler(
		options.PortalURL,
		options.Branding.Handoff.SetupURL,
		options.ListenerHTTPPort,
		landingPage,
		setupHandler,
	)
	if err != nil {
		return applianceComponents{}, err
	}

	return applianceComponents{
		captive: captiveManager,
		setup:   service,
		handler: handler,
	}, nil
}

// supervisedComponent is one long-running part of the appliance.
type supervisedComponent struct {
	name      string
	component observability.Component
	run       func() error
}

// superviseComponents runs every component until the first one stops, then
// cancels the runtime and waits for the rest to finish. A component returning
// nil without the runtime having been cancelled is itself a failure: nothing is
// supposed to stop on its own. Failures come back in the order observed, along
// with the first one's component identity for lifecycle reporting.
func superviseComponents(
	runtimeContext context.Context,
	cancelRuntime context.CancelFunc,
	lifecycle *observability.Lifecycle,
	components []supervisedComponent,
) (failures []error, failed observability.Component, unexpected bool) {
	type componentResult struct {
		name      string
		component observability.Component
		err       error
	}
	results := make(chan componentResult, len(components))
	for _, component := range components {
		go func() {
			results <- componentResult{
				name:      component.name,
				component: component.component,
				err:       component.run(),
			}
		}()
	}

	first := <-results
	wasCanceled := runtimeContext.Err() != nil
	lifecycle.Stopping(context.WithoutCancel(runtimeContext))
	cancelRuntime()

	failures = make([]error, 0, len(components))
	if first.err == nil && !wasCanceled {
		first.err = errors.New("stopped unexpectedly")
		unexpected = true
	}
	if first.err != nil {
		failed = first.component
		failures = append(failures, fmt.Errorf("%s: %w", first.name, first.err))
	}
	for range len(components) - 1 {
		completed := <-results
		if completed.err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", completed.name, completed.err))
		}
	}
	return failures, failed, unexpected
}

// buildReconciler assembles the state engine, the controller that acts on it and
// the supervisor that restarts the controller. Every failure here is reported
// the same way by the caller, so they are grouped rather than interleaved with
// the startup steps that each need their own cleanup.
func buildReconciler(
	client *networkmanager.Client,
	captiveManager *captive.Manager,
	options setupOptions,
	lifecycle *observability.Lifecycle,
) (*appliance.Supervisor, *recovery.Requests, error) {
	observer := appliance.NewNetworkManagerObserver(client, options.Interface)
	engine, err := appliance.NewEngine(observer, appliance.EngineOptions{
		Requirement: options.Requirement,
		GracePeriod: options.ActivationWait,
	})
	if err != nil {
		return nil, nil, err
	}
	recoveryRequests := recovery.NewRequests()
	controller, err := appliance.NewController(
		engine,
		captiveManager,
		recoveryRequests,
		appliance.ControllerOptions{
			ActionTimeout: options.ActivationWait + applianceCleanupTimeout,
			Observer:      lifecycle,
		},
	)
	if err != nil {
		return nil, nil, err
	}
	supervisor, err := appliance.NewSupervisor(controller, appliance.RetryConfig{
		MaxRestarts:  runtimeMaxRestarts,
		RestartDelay: runtimeRestartDelay,
		OnRetry:      retryReporter(lifecycle, observability.ComponentReconciler),
	})
	if err != nil {
		return nil, nil, err
	}
	return supervisor, recoveryRequests, nil
}

func stopManagedResources(
	ctx context.Context,
	captiveManager *captive.Manager,
	publisher *discovery.Publisher,
) error {
	cleanupContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		applianceCleanupTimeout,
	)
	defer cancel()
	return errors.Join(
		captiveManager.LeaveProvisioning(cleanupContext),
		publisher.Close(cleanupContext),
	)
}

func shutdownSetupOperations(
	ctx context.Context,
	service *setupflow.Service,
	timeout time.Duration,
) error {
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if err := service.Shutdown(cleanupContext); err != nil {
		return fmt.Errorf("stop setup operations: %w", err)
	}
	return nil
}
