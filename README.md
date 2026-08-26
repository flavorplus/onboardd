# onboardd

`onboardd` is a product-neutral Wi-Fi onboarding daemon for headless Linux
appliances. It provides a captive setup network, persistent infrastructure and
standalone modes, protected transitions, recovery, branding, and an optional link to
the device's main application.

Anthias and InkyPi are reference configurations; neither product is coupled to the
daemon.

## What it does

- Talks directly to NetworkManager over D-Bus; production code never invokes `nmcli`.
- Stores durable network intent in NetworkManager profiles, not a second state file.
- Creates provisioning as a temporary in-memory access-point profile.
- Keeps infrastructure and standalone access-point profiles as durable modes.
- Protects network changes with NetworkManager checkpoints and exact-profile rollback.
- Serves a framework-free setup UI embedded in the Go binary.
- Lets users connect to, activate, or forget onboardd-owned Wi-Fi profiles.
- Leaves foreign/system-managed profiles visible but read-only.

See [Architecture](docs/architecture.md) for the runtime design and
[Configuration](docs/configuration.md) for the TOML contract.

## Commands

The production CLI deliberately has only two actions:

```text
onboardd run --config /etc/onboardd/config.toml
onboardd recover
```

`run` starts the long-lived controller. `recover` asks that controller to enter its
temporary setup network. Use `onboardd --version` to identify a build.

For diagnostics, use the service journal and NetworkManager's standard tools:

```bash
systemctl status onboardd.service
journalctl -u onboardd.service
nmcli connection show
```

## Develop in VS Code

Open the repository as a folder:

```bash
code ~/GitHub/onboardd
```

The checked-in VS Code configuration provides build, test, vet, frontend, and release
tasks. Equivalent shell commands are:

```bash
go build .
go test ./...
go vet ./...
go run . --version

cd frontend
npm test
npm run build
```

The frontend development server includes a simulated device, so browser work can be
done safely on macOS without changing the Mac's network. See
[Development](docs/development.md).

## Configure and install

The complete annotated contract is [config/example.toml](config/example.toml), with
editor validation from [config/schema.json](config/schema.json). Smaller reference
configurations are available for [Anthias](config/anthias.toml) and
[InkyPi](config/inkypi.toml).

Versioned ARM64 and AMD64 Debian packages install a hardened, initially disabled
systemd service. See [Installation](docs/installation.md) for package verification,
password-file setup, service operation, upgrades, rollback, removal, and purge.

## Status and target

The implementation and Debian lifecycle have been accepted on Raspberry Pi OS/Debian
Trixie. Phase 9 covers the final Raspberry Pi Zero 2 W support matrix and v1.0 release
validation. See the concise [roadmap](docs/roadmap.md).

The project is licensed under the [MIT License](LICENSE).
