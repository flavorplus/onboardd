# Development

## Requirements

- Go version from `.go-version`;
- Node.js and npm for frontend work;
- VS Code with the recommended extensions (optional);
- Debian packaging tools only when building `.deb` files.

Open the repository root in VS Code. The default build task compiles the frontend and
then the Go executable. Run/Debug configurations start the root Go package.

## Everyday checks

```bash
gofmt -w .
go test ./...
go test -race ./...
go vet ./...
go build .
```

The **Check: all** VS Code task also validates shell scripts, tests/builds the frontend,
and cross-builds Linux ARM64 and AMD64 binaries.

Tests use fake consumer-side interfaces and in-memory listeners. NetworkManager, Avahi,
nftables, dnsmasq, systemd, and Unix-socket integration require Linux hardware or a
suitable VM; production Go code does not shell out to `nmcli`.

## Frontend on macOS

```bash
cd frontend
npm ci
npm run dev
```

Open `http://127.0.0.1:5173`. The development server supplies a simulated setup API.
Additional modes are available:

```bash
npm run dev:network-only
npm run dev:standalone-only
npm run dev:branded
```

Run frontend checks with:

```bash
npm test
npm run build
```

The build writes deterministic production assets to `internal/web/dist`, where Go
embeds them. Commit source and rebuilt assets together. CI rebuilds the frontend and
fails if the committed output differs.

## Local Go execution

macOS can build and test the platform-neutral code, but the appliance runtime requires
the Linux system bus and services. Safe local commands include:

```bash
go run . --version
go test ./...
```

On a configured Pi, run the packaged service and inspect it with:

```bash
sudo systemctl restart onboardd.service
systemctl status onboardd.service
journalctl -u onboardd.service -f
sudo onboardd recover
```

NetworkManager profiles can be inspected independently with `nmcli connection show`.

## Build and release

```bash
go build -o bin/onboardd .

GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -trimpath -buildvcs=false -ldflags=-buildid= \
  -o bin/onboardd-linux-arm64 .

scripts/build-release.sh v0.1.0
scripts/build-deb.sh v0.1.0
```

`build-release.sh` builds stripped, reproducible ARM64 and AMD64 executables and
checksums. `build-deb.sh` wraps them in Debian packages. `check-deb.sh` validates
package contents, maintainer scripts, permissions, checksums, and reproducibility.

Release versions are injected into `internal/cli.Version` with `-ldflags`; development
builds report `development`.

## Repository layout

```text
main.go                    executable entry point
internal/                  Go runtime packages grouped by responsibility
frontend/                  TypeScript UI and simulated device
internal/web/dist/         generated embedded frontend
config/                    JSON Schema and example TOML files
docs/                      durable architecture and operating documentation
packaging/debian/          service unit and Debian package metadata
scripts/                   release/package build and verification
.vscode/                   shared editor tasks and launch settings
```

Keep packages tied to a runtime responsibility. Do not create a package for a single
constant or one helper that naturally belongs to its caller. Prefer small interfaces
declared by consumers, explicit error wrapping, bounded contexts, and table-driven
tests around behavior rather than implementation details.
