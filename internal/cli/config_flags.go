package cli

import (
	"errors"
	"flag"

	appconfig "github.com/flavorplus/onboardd/internal/config"
	"github.com/flavorplus/onboardd/internal/connectivity"
)

// operationalConfigFlags is the deliberately small CLI override surface shared by
// setup and debug config. Product identity, branding, SSIDs, and secrets stay out of
// process arguments.
type operationalConfigFlags struct {
	interfaceName        *string
	requirementText      *string
	infrastructureEnable *bool
	standaloneEnable     *bool
	listenerPort         *uint
}

func bindOperationalConfigFlags(flags *flag.FlagSet, defaults appconfig.Config) operationalConfigFlags {
	return operationalConfigFlags{
		interfaceName: flags.String(
			"network-interface",
			defaults.Network.Interface,
			"NetworkManager Wi-Fi interface override",
		),
		requirementText: flags.String(
			"network-requirement",
			string(defaults.Network.Requirement),
			"connectivity requirement override: local or internet",
		),
		infrastructureEnable: flags.Bool(
			"infrastructure-enabled",
			defaults.Network.InfrastructureEnabled,
			"infrastructure-mode policy override",
		),
		standaloneEnable: flags.Bool(
			"standalone-enabled",
			defaults.Network.StandaloneEnabled,
			"standalone-mode policy override",
		),
		listenerPort: flags.Uint(
			"listener-port",
			uint(defaults.Portal.ListenerPort),
			"private portal listener port override",
		),
	}
}

func (values operationalConfigFlags) overrides(flags *flag.FlagSet) (appconfig.Overrides, error) {
	set := explicitlySetFlags(flags)
	overrides := appconfig.Overrides{}
	if set["network-interface"] {
		overrides.NetworkInterface = values.interfaceName
	}
	if set["network-requirement"] {
		requirement := connectivity.Requirement(*values.requirementText)
		overrides.NetworkRequirement = &requirement
	}
	if set["infrastructure-enabled"] {
		overrides.InfrastructureEnabled = values.infrastructureEnable
	}
	if set["standalone-enabled"] {
		overrides.StandaloneEnabled = values.standaloneEnable
	}
	if set["listener-port"] {
		if *values.listenerPort == 0 || *values.listenerPort > 65535 {
			return appconfig.Overrides{}, errors.New("--listener-port must be between 1 and 65535")
		}
		port := uint16(*values.listenerPort)
		overrides.ListenerPort = &port
	}
	return overrides, nil
}

func explicitlySetFlags(flags *flag.FlagSet) map[string]bool {
	set := make(map[string]bool)
	flags.Visit(func(flag *flag.Flag) {
		set[flag.Name] = true
	})
	return set
}
