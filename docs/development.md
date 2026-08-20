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
- build, test, vet, format, and full-check tasks;
- launch configurations for the daemon and its version output;
- visible file nesting for generated companions without hiding project content.

Use **Terminal → Run Task → Check: all** before handing off a phase. Use the Run and
Debug panel with **Debug onboardd** when stepping through Go code.

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
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o bin/onboardd-linux-arm64 ./cmd/onboardd
```

Phase 1's D-Bus code is unit-tested on macOS and hardware-tested on Raspberry Pi OS
Trixie. Use the dedicated VS Code cross-build task to produce the Pi binary.

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
