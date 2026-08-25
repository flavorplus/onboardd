# Development workflow

## VS Code

Open the repository folder rather than an individual source file:

```bash
code ~/GitHub/onboardd
```

The checked-in `.vscode` configuration provides:

- extension recommendations for Go, TOML, EditorConfig, and Markdown;
- automatic Go formatting and import organization on save;
- TOML formatting, navigation, completion, and validation against `config/schema.json`;
- Go and frontend build, test, vet, format, release, and full-check tasks;
- launch configurations for the daemon and its version output;
- a dedicated Phase 2 state-test debugger;
- a dedicated Phase 3 captive-test debugger;
- dedicated Phase 4 setup-controller and API debuggers;
- Phase 5 configuration and embedded-frontend test debuggers plus resolved-example inspection;
- visible file nesting for generated companions without hiding project content.

Use **Terminal → Run Task → Check: all** before handing off a phase. Use the Run and
Debug panel with **Debug onboardd** when stepping through Go code.

Use **Terminal → Run Task → Config: inspect example** to print the final configuration
after built-in defaults and `config/example.toml` are combined and templates are
rendered with a clearly synthetic development identity. This is read-only and does not
contact NetworkManager.

Run **Frontend: install** after cloning, **Frontend: dev** while working on the browser
interface, and **Frontend: build** before running the real setup command. VS Code uses
the repository's TypeScript version from `frontend/node_modules`.

### Local portal simulation

The frontend development server includes a Mac-safe simulated device. It runs the real
portal without opening an access point, changing NetworkManager profiles, or requiring
the Go daemon:

1. In VS Code, run **Terminal → Run Task → Frontend: dev**.
2. Open `http://127.0.0.1:5173/`.
3. Select `Studio Wi-Fi`. Enter `wrong-password` to exercise restoration and retry;
   enter any other password of at least eight characters to complete successfully.
4. Exercise standalone and **Change connection** from the completion page.

Every transition deliberately returns one temporary unavailable response so the same
browser reconnect state used on hardware is visible locally. Use **Frontend: dev
(Wi-Fi only)** or **Frontend: dev (standalone only)** to verify that a product policy
removes the disallowed setup choice. Stop the task before starting another variant,
because all variants use port 5173.

Use **Frontend: dev (branded)** to preview alternate product text, colors, and a logo.
The ordinary **Frontend: dev** task retains the default wireless mark so both logo paths
can be checked locally.

The simulator is a Vite development-server plugin. It is not bundled into either
compiled output directory and cannot affect the Pi or production networking paths.

### Embedded frontend build

`npm run build` writes production assets directly to `internal/frontend/dist` for Go
embedding. The directory is committed so a clean checkout always compiles; rebuilding
the frontend refreshes it before the binary is built. VS Code's **Go: build onboardd**
and Linux cross-build tasks automatically run **Frontend: build** first.

### Versioned release binaries

Run VS Code task **Release: build Linux binaries** and enter a v-prefixed version such
as `v0.1.0`, or invoke the same repository script directly:

```bash
scripts/build-release.sh v0.1.0
```

The release is written to `dist/v0.1.0/` with `onboardd-linux-arm64`,
`onboardd-linux-amd64`, and `SHA256SUMS`. The version is embedded through Go linker
flags and is printed by `onboardd --version`. The script rejects unsafe or ambiguous
version strings before using one in a linker flag or output path.

Release builds use the local Go toolchain with the user Go environment file, `GOFLAGS`,
and optional Go experiments disabled; read-only module metadata; no CGO, VCS stamping,
host paths, or Go build ID; and fixed `GOAMD64=v1` and `GOARM64=v8.0` baselines. Given
the same source tree, embedded frontend, version, dependencies, and Go toolchain,
repeated builds are byte-for-byte identical. To verify that contract without overwriting
the normal `dist` directory:

```bash
first=$(mktemp -d)
second=$(mktemp -d)
scripts/build-release.sh v0.1.0 "$first"
scripts/build-release.sh v0.1.0 "$second"
diff -u "$first/v0.1.0/SHA256SUMS" "$second/v0.1.0/SHA256SUMS"
cmp "$first/v0.1.0/onboardd-linux-arm64" "$second/v0.1.0/onboardd-linux-arm64"
cmp "$first/v0.1.0/onboardd-linux-amd64" "$second/v0.1.0/onboardd-linux-amd64"
```

The script builds a temporary host binary and verifies its exact `--version` output
before publishing either Linux binary. Run **Check: all** first so the committed embedded
frontend and tests have already been validated.

### Debian packages

Run VS Code task **Release: build Debian packages** from a Debian, Ubuntu, or Raspberry
Pi OS development host, or invoke:

```bash
scripts/build-deb.sh v0.1.0
```

The script first builds the matching release binaries, then writes AMD64 and ARM64
packages plus `DEBSHA256SUMS` below `dist/v0.1.0/`. It finishes by running
`scripts/check-deb.sh`, which verifies package fields, exact contents, modes, ownership,
conffile and maintainer-script metadata, checksums, binary identity, and isolated
install/purge behavior. It requires the standard Debian `dpkg-deb`, GNU core utilities,
and tar; macOS can validate and edit the packaging sources but does not build or inspect
a `.deb` with those host tools.

The package build uses the latest Git commit timestamp as `SOURCE_DATE_EPOCH` unless the
caller provides one. It normalizes the staging tree timestamps, uses root ownership in
the archive without requiring a root build, and fixes compression to one xz thread.
Given identical release inputs and `dpkg-deb` versions, repeated package builds are
byte-for-byte identical.

GitHub Actions runs tests and static checks, then builds and inspects the complete release
twice and compares both binaries and both packages byte-for-byte. Tag and manually
dispatched runs retain the first build as a short-lived inspectable workflow artifact;
the workflow does not publish a GitHub Release.

The normal Pi command no longer needs a copied frontend directory:

```bash
sudo ~/onboardd setup --config /etc/onboardd/config.toml
```

For an SSH-safe hardware run, copy `scripts/setup-recorder.sh` beside the binary and use:

```bash
~/setup-recorder.sh run /etc/onboardd/config.toml
~/setup-recorder.sh status
~/setup-recorder.sh snapshot before-transition
~/setup-recorder.sh show
~/setup-recorder.sh recover
~/setup-recorder.sh stop
```

The recorder logs the binary hash and command, survives a `wlan0` transition, and can
append labeled, secret-free evidence snapshots containing health, NetworkManager profile,
owned nftables, DNS-fragment, process, and boot information. Its stop command sends
`SIGTERM` and waits up to 75 seconds for rollback and cleanup; it deliberately never falls
back to a forced kill. Use `start` instead of `run` only when testing the direct setup
workflow without the production controller.

## Phase discipline

Work happens one phase at a time:

1. Confirm the current phase's scope and exit criteria.
2. Implement only what is needed to satisfy them.
3. Run the phase's automated and hardware checks.
4. Update the roadmap and architecture documentation.
5. Review the result before starting the next phase.

Cross-phase scaffolding is allowed when it keeps the repository buildable or makes the
current work visible, but it must not silently implement later behavior.

## Local commands

```bash
go fmt ./...
go test ./...
go vet ./...
go build -o bin/onboardd ./cmd/onboardd
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags=-buildid= -o bin/onboardd-linux-arm64 ./cmd/onboardd
scripts/build-release.sh v0.1.0
cd frontend && npm install && npm test && npm run build
```

Phase 1's D-Bus code is unit-tested on macOS and hardware-tested on Raspberry Pi OS
Trixie. Use the dedicated VS Code cross-build task to produce the Pi binary. Its
`-trimpath`, disabled VCS stamping, and an empty Go build ID make repeated builds from
the same source and Go toolchain byte-for-byte identical regardless of output path or
Git dirty state. Compare
`shasum -a 256 bin/onboardd-linux-arm64` locally with `sha256sum ~/onboardd` on the Pi
before every hardware test.

Phase 2 reconciliation tests use a fake NetworkManager observer and manually controlled
clock. Run or debug `internal/state` without waiting for real timeouts or changing the
development machine's network.

Phase 3 tests use fake NetworkManager/checkpoint adapters and in-memory HTTP connections,
so `internal/captive` and `internal/recovery` can be debugged without opening local ports
or changing the development machine's network. Its accepted hardware evidence is
summarized in `docs/phase-3-hardware-checklist.md`.

Phase 4 added deterministic controller and HTTP tests in `internal/setup` and
`internal/web`, plus small TypeScript model tests. Its accepted browser and radio
evidence is summarized in `docs/phase-4-hardware-checklist.md`. Current hardware tests
transfer only the binary, recorder, configuration, optional logo, and secret files.

Phase 7 adds deterministic appliance-supervisor and recovery-input tests. The recovery
package's Unix-socket tests use only a temporary local path, while GPIO button timing is
driven by a manual test clock and fake edge source on macOS. Use **Debug Phase 7 recovery
tests** in VS Code. The Linux ARM64 build compiles the real GPIO v2 ioctl adapter without
requiring GPIO hardware on the development Mac.

## Repository layout

The repository grows into the following shape as phases are completed:

```text
onboardd/
├── .vscode/
├── cmd/onboardd/
├── internal/
│   ├── buildinfo/
│   ├── captive/
│   ├── config/
│   ├── connectivity/
│   ├── handoff/
│   ├── networkmanager/
│   ├── recovery/
│   ├── state/
│   └── web/
├── config/
├── docs/
├── frontend/
├── integration/
└── packaging/
```

Directories are added when their phase begins; empty placeholder directories are not
kept solely to make the future tree appear complete.
