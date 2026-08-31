# Development

## Requirements

- Go version from `.go-version`;
- `golangci-lint` v2 (CI pins v2.13.2; a v1 binary rejects this repository's config);
- Node.js and npm for frontend work. CI uses Node 26; `npm test` runs TypeScript
  directly, so it needs a Node new enough to strip types;
- VS Code with the recommended extensions (optional);
- Debian packaging tools only when building `.deb` files.

Open the repository root in VS Code. The default build task compiles the frontend and
then the Go executable. Run/Debug configurations start the root Go package.

## Everyday checks

```bash
golangci-lint fmt ./...
go test -race -shuffle=on ./...
go vet ./...
golangci-lint run ./...
go build .
```

Use `golangci-lint fmt`, not `gofmt`. The repository is checked with `gofumpt` and
`goimports`, and `golangci-lint run` reports a formatting difference as a failure, so
`gofmt` alone can leave code that passes locally and fails CI.

Run the tests the way CI does, with `-race -shuffle=on`. Shuffling is what catches an
order-dependent test before it reaches CI.

The **Check: all** VS Code task runs format, test, vet and lint, then validates shell
scripts, tests/builds the frontend, and cross-builds Linux ARM64 and AMD64 binaries.
Note that its first step rewrites files in place.

CI additionally enforces three gates this list does not cover: `go mod tidy` leaves
`go.mod`/`go.sum` unchanged, `govulncheck ./...` finds nothing reachable, and
`npm audit --omit=dev` reports no high-severity advisory. The linter configuration, and
the reasoning behind every enabled and disabled linter, is in
[.golangci.yml](../.golangci.yml).

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
Its administrator password is printed in the Vite startup output.
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

The build writes deterministic production assets to `internal/webui/dist`, where Go
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
internal/webui/dist/         generated embedded frontend
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
