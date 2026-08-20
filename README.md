# onboardd

`onboardd` is a product-neutral onboarding daemon for headless Linux appliances.
It will provide Wi-Fi provisioning, a captive portal, permanent standalone access-point
operation, recovery, product branding, and a configurable handoff to the device's main
application.

The initial reference integrations are Anthias and InkyPi, but neither product is part
of the core daemon.

## Project status

The project is being developed one phase at a time. **Phases 0, 1, and 2 are complete.**
Phase 3 is the next implementation boundary.

| Phase | Deliverable | Status |
|---|---|---|
| 0 | Architecture, contracts, repository and editor setup | Complete |
| 1 | NetworkManager D-Bus proof of concept | Complete |
| 2 | Core reconciliation and transient state engine | Complete |
| 3 | Temporary AP and captive portal plumbing | Not started |
| 4 | Setup API and web interface | Not started |
| 5 | Branding and configuration | Not started |
| 6 | Application handoff | Not started |
| 7 | Recovery and appliance reliability | Not started |
| 8 | Packaging and installation | Not started |
| 9 | Hardware validation and v1.0 integrations | Not started |

See the [roadmap](docs/roadmap.md) for the scope and exit criteria of every phase.

## Design summary

- Go daemon and embedded web application.
- Direct NetworkManager D-Bus integration; production code does not shell out to `nmcli`.
- NetworkManager profiles are the source of persistent networking intent and current state.
- No separate `state.json` for networking mode.
- Infrastructure and standalone are persistent operating modes.
- Provisioning is a temporary, in-memory NetworkManager profile.
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
```

See [Development](docs/development.md) for the editor workflow and repository layout.

## Configuration preview

The proposed contract is demonstrated in [config/example.toml](config/example.toml) and
described by [config/schema.json](config/schema.json). It is a Phase 0 contract; loading
and validating it is scheduled for a later implementation phase.

## Initial target

The first hardware baseline is a Raspberry Pi Zero 2 W running Raspberry Pi OS Trixie.
The daemon must stay lightweight enough for the Zero 2 W's 512 MB of memory. ARM64 is
the default build/test assumption and was validated during the Phase 1 hardware run.

## Phase 1 diagnostics

The current binary contains direct NetworkManager D-Bus diagnostics:

```text
onboardd debug status
onboardd debug profiles
onboardd debug scan
onboardd debug watch
onboardd debug connect
onboardd debug provisioning-start
onboardd debug standalone-start
onboardd debug checkpoint-create
onboardd debug checkpoint-commit
onboardd debug checkpoint-rollback
onboardd debug reconcile
```

Run `onboardd debug help` for the safety flags and complete command forms. Commands that
change networking require `--yes`; credentials are accepted only through password files.
The [Phase 1 hardware checklist](docs/phase-1-hardware-checklist.md) records the accepted
Raspberry Pi validation boundary.
