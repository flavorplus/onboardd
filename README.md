# onboardd

`onboardd` is a product-neutral onboarding daemon for headless Linux appliances.
It will provide Wi-Fi provisioning, a captive portal, permanent standalone access-point
operation, recovery, product branding, and a configurable handoff to the device's main
application.

The initial reference integrations are Anthias and InkyPi, but neither product is part
of the core daemon.

## Project status

The project is being developed one phase at a time. **Phases 0 through 7 are complete.**
Phase 8 is in progress.

| Phase | Deliverable | Status |
|---|---|---|
| 0 | Architecture, contracts, repository and editor setup | Complete |
| 1 | NetworkManager D-Bus proof of concept | Complete |
| 2 | Core reconciliation and transient state engine | Complete |
| 3 | Temporary AP and captive portal plumbing | Complete |
| 4 | Setup API and web interface | Complete |
| 5 | Branding and configuration | Complete |
| 6 | Application handoff | Complete |
| 7 | Recovery and appliance reliability | Complete |
| 8 | Packaging and installation | In progress |
| 9 | Hardware validation and v1.0 integrations | Not started |

See the [roadmap](docs/roadmap.md) for the scope and exit criteria of every phase.

## Design summary

- Go daemon and embedded web application.
- Direct NetworkManager D-Bus integration; production code does not shell out to `nmcli`.
- NetworkManager profiles are the source of persistent networking intent and current state.
- No separate `state.json` for networking mode.
- Infrastructure and standalone are persistent operating modes.
- Provisioning is a temporary, in-memory NetworkManager profile.
- Known Wi-Fi profiles are visible in the setup UI; inactive onboardd-owned
  infrastructure profiles can be activated or forgotten there.
- Configuration is loaded from defaults, TOML, environment variables, and CLI options.
- Product names, colors, logos, copy, and application handoff are configuration—not forks.

The full design is in [Architecture](docs/architecture.md) and the public configuration
contract is in [Configuration](docs/configuration.md). Items that still require an
explicit choice are tracked in [Open decisions](docs/open-decisions.md).

## Open in VS Code

Open this repository as a folder:

```bash
code ~/GitHub/onboardd
```

VS Code will recommend the Go, TOML, EditorConfig, and Markdown extensions. The Run and
Debug panel contains launch configurations, and **Terminal → Run Task** exposes build,
format, test, vet, and full-check tasks.

Useful commands outside VS Code:

```bash
go build ./cmd/onboardd
go test ./...
go vet ./...
go run ./cmd/onboardd --version
scripts/build-release.sh v0.1.0
scripts/build-deb.sh v0.1.0 # Debian, Ubuntu, or Raspberry Pi OS
```

See [Development](docs/development.md) for the editor workflow and repository layout.
The development workflow also includes a Mac-safe simulated device for iterating on the
complete portal without transferring a build or changing the Mac's network.

## Debian installation

Versioned ARM64 and AMD64 Debian packages install onboardd as a hardened systemd
service. A fresh install stays disabled until its conffile and root-only Wi-Fi password
files have been prepared. See [Install and operate onboardd](docs/installation.md) for
package verification, first configuration, service operation, upgrades, rollback,
removal, and purge behavior.

## Configuration

The implemented contract is demonstrated in [config/example.toml](config/example.toml)
and described by [config/schema.json](config/schema.json). The long-running appliance
controller is started with:

```text
sudo onboardd run --config /etc/onboardd/config.toml
```

Ask that running controller to enter temporary recovery setup with:

```text
sudo onboardd recover
```

If the controller is stopped, the standalone recovery process remains available with:

```text
sudo onboardd setup --config /etc/onboardd/config.toml
```

The compiled browser interface is embedded in the executable; no frontend directory is
needed beside the production binary.

## Initial target

The first hardware baseline is a Raspberry Pi Zero 2 W running Raspberry Pi OS Trixie.
The daemon must stay lightweight enough for the Zero 2 W's 512 MB of memory. ARM64 is
the default build/test assumption and was validated during the Phase 1 hardware run.

## Network diagnostics

The current binary contains direct NetworkManager D-Bus diagnostics:

```text
onboardd debug config
onboardd debug status
onboardd debug profiles
onboardd debug profile-delete
onboardd debug scan
onboardd debug watch
onboardd debug reconcile
```

Run `onboardd debug help` for complete command forms. Network changes use only the
configured `onboardd run` or explicit `onboardd setup` workflow; retired phase-proof
mutation commands are not a second public control surface. `profile-delete` is
restricted to onboardd-owned profiles and requires explicit confirmation.

When an SSH connection uses the Wi-Fi interface being configured, use
`scripts/setup-recorder.sh`. Its `run` command starts the appliance controller, `recover`
requests manual recovery through that running process, and `start` remains available for
the direct setup workflow. `snapshot` appends labeled health, profile, owned-resource,
and boot evidence without reading connection secrets. It retains the log and PID across
radio transitions and sends a graceful termination signal during cleanup.
