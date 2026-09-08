# CLAUDE.md

`onboardd` is a product-neutral Wi-Fi onboarding daemon for headless Linux appliances.
Read [README](README.md) for what it does, and [Architecture](docs/architecture.md) for
the runtime design and code map. This file covers only what those leave implicit.

## Verification

Go 1.26.7 (pinned in `.go-version`) and Node 26. The local sequence matches the VS Code
`Check: all` task:

```bash
golangci-lint fmt ./...
golangci-lint run ./...
go vet ./...
go test ./...
npm test --prefix frontend
npm run build --prefix frontend
```

- Formatting is `golangci-lint fmt` — gofumpt plus goimports — not plain `gofmt`.
- Linting requires golangci-lint **v2**, which CI pins to v2.13.2. `.golangci.yml`
  records why each disabled linter is off; read that comment before re-enabling one.
- CI runs `go test -race -shuffle=on ./...`. Use those flags to reproduce a failure
  that only appears in CI.
- `lll` is deliberately disabled. Prose in `docs/` wraps near 88 columns; wrapping Go
  is the formatter's business, not yours.

## The frontend bundle is committed

`internal/webui/dist` is checked into the repository and embedded with `go:embed`. **Any
change under `frontend/` needs `npm run build --prefix frontend`**, or the Go binary
keeps serving the previous assets. CI enforces it with
`git diff --exit-code -- internal/webui/dist`.

`npm test` runs `node --test` over `src/*.test.ts` and `dev/*.test.ts`. The project has
no vitest, jest, or watch mode — invoking one reports every file as an empty suite,
which looks like a broken test run and is not one.

`npm run dev --prefix frontend` serves a simulated device on port 5173. The
`dev:branded`, `dev:network-only`, and `dev:standalone-only` modes cover the other
product configurations; `.claude/launch.json` has entries for the first two, so the
setup views can be checked against a custom palette and logo rather than the defaults
compiled into `styles.css`.

## Deliberate constraints

These read like defects and are not. Changing any of them is a design decision first.

- **Production code never invokes `nmcli`.** NetworkManager is reached over D-Bus
  through `internal/networkmanager`. `nmcli` is for a human diagnosing a device.
- **NetworkManager profiles are the only durable state.** There is no second state
  file, and reconciliation persists nothing to disk.
- **The captive workflow is plain HTTP on purpose.** An untrusted certificate for
  intercepted traffic would be both unreliable and misleading. Do not add TLS to it.
- **Port 80 is never bound.** A dedicated nftables table redirects it to the private
  listener, scoped to the setup interface, because the appliance application owns
  port 80.
- **onboardd never changes the host name.** It reads the name Avahi already publishes.
- **Foreign NetworkManager profiles stay read-only.** onboardd deletes only profiles
  it owns, and adopting or removing foreign ones is deferred beyond v1.
- **The production CLI has exactly two actions**, `run` and `recover`. Diagnostics
  belong to the service journal and NetworkManager's own tools.

## Conventions

Packages follow runtime responsibility rather than abstract layers, and interfaces are
declared at the consumer boundary, kept as small as the tests and platform adapters
need.

`docs/` describes durable behaviour and is expected to match the code — a previous
commit exists purely to correct drift. When behaviour changes, update the affected doc
in the same change.

Commit subjects are conventional (`fix:`, `refactor:`, `docs:`, `chore:`). Work lands
on `type/short-name` branches through pull requests into `main`.
