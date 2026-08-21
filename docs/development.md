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
- Go and frontend build, test, vet, format, and full-check tasks;
- launch configurations for the daemon and its version output;
- a dedicated Phase 2 state-test debugger;
- a dedicated Phase 3 captive-test debugger;
- dedicated Phase 4 setup-controller and API debuggers;
- visible file nesting for generated companions without hiding project content.

Use **Terminal → Run Task → Check: all** before handing off a phase. Use the Run and
Debug panel with **Debug onboardd** when stepping through Go code.

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

The simulator is a Vite development-server plugin. It is not bundled into
`frontend/dist` and cannot affect the Pi or production networking paths.

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
or changing the development machine's network. The target-device run is documented in
`docs/phase-3-hardware-checklist.md`.

Phase 4 adds deterministic controller and HTTP tests in `internal/setup` and
`internal/web`, plus small TypeScript model tests. The complete browser and radio test
is documented in `docs/phase-4-hardware-checklist.md`; transfer the built
`frontend/dist` directory alongside the Pi binary for this phase.

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
