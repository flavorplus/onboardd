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
	"github.com/flavorplus/onboardd/internal/observability"
	"github.com/flavorplus/onboardd/internal/recovery"
	setupflow "github.com/flavorplus/onboardd/internal/setup"
	stateengine "github.com/flavorplus/onboardd/internal/state"
	"github.com/flavorplus/onboardd/internal/systemd"
	webui "github.com/flavorplus/onboardd/internal/web"
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
	serviceNotifier, err := systemd.NewNotifier()
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
	serviceNotifier *systemd.Notifier,
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
	redirect, err := captive.NewNFTRedirect("nft")
	if err != nil {
		return err
	}
	provisioner, err := captive.NewProvisioner(client, dns)
	if err != nil {
		return err
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
		return err
	}

	backend, err := newNetworkBackend(client, captiveManager, options)
	if err != nil {
		return err
	}
	service, err := setupflow.NewService(runtimeContext, backend, setupflow.Capabilities{
		Network:    options.NetworkEnabled,
		Standalone: options.StandaloneEnabled,
	})
	if err != nil {
		return err
	}
	stopOperations := func() error {
		return shutdownSetupOperations(
			runtimeContext,
			service,
			options.RestorationWait+applianceCleanupTimeout,
		)
	}
	api, err := webui.NewAPI(
		service,
		options.PortalOrigin,
		webui.Authentication{Password: options.AdminPassword},
		options.Branding,
	)
	if err != nil {
		return err
	}
	applicationHandler, err := webui.NewHandler(api, options.Assets)
	if err != nil {
		return err
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
		return err
	}

	listenConfig := &net.ListenConfig{}
	listenAddress := net.JoinHostPort("0.0.0.0", fmt.Sprint(options.ListenerHTTPPort))
	httpService, err := captive.StartHTTPService(
		runtimeContext,
		listenConfig.Listen,
		handler,
		captive.HTTPServiceOptions{
			Network:         "tcp4",
			Address:         listenAddress,
			MaxRestarts:     runtimeMaxRestarts,
			RestartDelay:    runtimeRestartDelay,
			ShutdownTimeout: applianceCleanupTimeout,
			OnRetry: func(ctx context.Context, attempt, maximum int) {
				lifecycle.ComponentRetry(
					ctx,
					observability.ComponentHTTP,
					attempt,
					maximum,
				)
			},
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
		captiveManager.RecoverStartup(startupRecoveryContext),
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
			stopManagedResources(runtimeContext, captiveManager, publisher),
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

	observer := stateengine.NewNetworkManagerObserver(client, options.Interface)
	engine, err := stateengine.New(observer, stateengine.Config{
		Requirement: options.Requirement,
		GracePeriod: options.ActivationWait,
	})
	if err != nil {
		return errors.Join(err, stopStartupResources())
	}
	recoveryRequests := recovery.NewRequests()
	controller, err := appliance.NewController(
		engine,
		captiveManager,
		recoveryRequests,
		appliance.Config{
			ActionTimeout: options.ActivationWait + applianceCleanupTimeout,
			Observer:      lifecycle,
		},
	)
	if err != nil {
		return errors.Join(err, stopStartupResources())
	}
	supervisor, err := appliance.NewSupervisor(controller, appliance.RetryConfig{
		MaxRestarts:  runtimeMaxRestarts,
		RestartDelay: runtimeRestartDelay,
		OnRetry: func(ctx context.Context, attempt, maximum int) {
			lifecycle.ComponentRetry(
				ctx,
				observability.ComponentReconciler,
				attempt,
				maximum,
			)
		},
	})
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

	type componentResult struct {
		name      string
		component observability.Component
		err       error
	}
	componentCount := 3
	componentResults := make(chan componentResult, 4)
	go func() {
		componentResults <- componentResult{
			name:      "appliance controller",
			component: observability.ComponentReconciler,
			err:       supervisor.Run(runtimeContext),
		}
	}()
	go func() {
		componentResults <- componentResult{
			name:      "setup HTTP listener",
			component: observability.ComponentHTTP,
			err:       <-listenerDone,
		}
	}()
	go func() {
		componentResults <- componentResult{
			name:      "manual recovery control",
			component: observability.ComponentControl,
			err:       <-controlDone,
		}
	}()
	if serviceNotifier != nil && serviceNotifier.Enabled() {
		componentCount++
		go func() {
			componentResults <- componentResult{
				name:      "systemd notifier",
				component: observability.ComponentSystemd,
				err:       serviceNotifier.Run(runtimeContext, lifecycle.Health()),
			}
		}()
	}

	first := <-componentResults
	wasCanceled := runtimeContext.Err() != nil
	lifecycle.Stopping(context.WithoutCancel(runtimeContext))
	cancelRuntime()
	componentErrors := make([]error, 0, componentCount)
	if first.err == nil && !wasCanceled {
		first.err = errors.New("stopped unexpectedly")
		failure = observability.FailureUnexpected
	}
	if first.err != nil {
		failureComponent = first.component
		componentErrors = append(componentErrors, fmt.Errorf("%s: %w", first.name, first.err))
	}
	for range componentCount - 1 {
		completed := <-componentResults
		if completed.err != nil {
			componentErrors = append(
				componentErrors,
				fmt.Errorf("%s: %w", completed.name, completed.err),
			)
		}
	}
	operationErr := stopOperations()
	cleanupErr := errors.Join(
		operationErr,
		stopManagedResources(runtimeContext, captiveManager, publisher),
	)
	if len(componentErrors) > 0 || cleanupErr != nil {
		return errors.Join(errors.Join(componentErrors...), cleanupErr)
	}
	fmt.Fprintln(stdout, "Onboardd appliance stopped and temporary resources were removed")
	return nil
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
