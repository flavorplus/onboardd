# Phase 4 — Setup API and web interface

Status: complete. Accepted on 2026-08-21 on Raspberry Pi Zero 2 W with Raspberry Pi OS
Trixie.

## Boundary

Phase 4 turns the Phase 3 captive network into a complete product-neutral setup
experience. It owns the versioned HTTP API, in-memory setup operations, and browser
interface. It does not yet own product branding, TOML loading, embedded assets, or
application handoff; those remain Phase 5 and Phase 6 work.

The primary design constraint is the single Wi-Fi radio. Starting infrastructure or a
new standalone profile interrupts the browser's current connection. A setup request
therefore starts a background operation and returns immediately. The browser may lose
contact, reconnect to restored provisioning after failure, and retrieve the same
operation result.

## User-visible states

The interface uses plain product language and never exposes NetworkManager terms:

```text
WELCOME
   │
   ├── connect to a network ─► NETWORK LIST ─► CREDENTIALS
   │                                                │
   │                                                ▼
   │                                           CONNECTING
   │                                         ┌──────┴──────┐
   │                                         ▼             ▼
   │                                      COMPLETE       TRY AGAIN
   │
   └── use standalone ─────► CONFIRM STANDALONE ─► SWITCHING ─► COMPLETE
```

If infrastructure is the only allowed mode, the welcome choice is skipped. Hidden
networks use the same credentials step with a manually entered SSID. Later mode changes
reuse the same routes and screens rather than introducing a second administration UI.

## HTTP API

All API routes live below `/api/v1`. Successful and error responses are JSON and carry
`Cache-Control: no-store`. Unknown JSON fields are rejected.

### `GET /api/v1/setup`

Returns the setup bootstrap document:

- allowed infrastructure and standalone modes;
- the currently selected network role in product language;
- whether a network operation is active;
- a per-process CSRF token used by mutation requests.

### `GET /api/v1/networks`

Requests a fresh Wi-Fi scan and returns one safe entry per visible network. Duplicate
access points with the same SSID and security class are collapsed to the strongest
entry. BSSIDs are not shown to the user. Hidden networks are entered manually.

### `POST /api/v1/connections`

Accepts an SSID, an optional password for explicitly open networks, and a hidden-network
flag. It creates one protected infrastructure operation and returns `202 Accepted` with
an operation ID. The response is flushed before the radio transition begins.

### `POST /api/v1/standalone`

Accepts explicit confirmation and starts a standalone-mode operation. The standalone
SSID, password, and address are appliance policy supplied by the server, never by the
browser.

### `GET /api/v1/operations/{id}`

Returns one of:

- `pending`: accepted but not yet changing the radio;
- `running`: transition in progress;
- `succeeded`: the selected production mode is durable;
- `failed`: provisioning was restored and the user may retry.

Only one mutation operation may run at a time. A concurrent request receives
`409 Conflict` and the current operation ID. Completed results remain in memory for the
life of the process and are bounded to a small recent history.

## Failure behavior

Backend errors are mapped to stable product-facing codes and copy. Raw D-Bus paths,
profile UUIDs, implementation names, and credentials never enter API responses. Initial
codes cover invalid input, rejected connections, connectivity not acceptable,
restoration failure, operation conflict, and internal failure.

Polling is deliberately preferred over WebSockets or server-sent events. A broken radio
link is normal during setup, and ordinary retrying `GET` requests recover more reliably
across captive mini-browsers.

## Request security

- Mutation bodies have a small fixed size limit and require `application/json`.
- Mutation requests require the bootstrap CSRF token in a custom header.
- `Origin`, when supplied, must match either the canonical portal or the same direct
  listener address serving the page after a mode change.
- No CORS permission is emitted.
- Wi-Fi credentials are passed directly to the transition and are never logged or
  included in operation state.
- UI text is rendered through DOM text properties rather than HTML injection.

The provisioning AP remains a local trust boundary rather than an authenticated public
service. Packaging and service hardening remain Phase 8 work.

## Frontend structure

The browser application uses framework-free TypeScript, semantic HTML, and responsive
CSS. Vite provides local development, type-aware builds, and static output. The UI must
support keyboard navigation, visible focus, screen-reader labels, touch targets, reduced
motion, narrow captive windows, and browsers that reopen after the AP is restored.

During Phase 4, a debug command serves the built frontend directory alongside the Go
API. Phase 5 replaces that filesystem dependency with embedded compiled assets while
preserving the API and UI behavior.

## Acceptance

Phase 4 is complete when a nontechnical tester can:

- choose either allowed mode without seeing networking implementation terminology;
- scan, select, and join a protected or open Wi-Fi network;
- enter a hidden network manually;
- understand progress while the connection is temporarily unavailable;
- recover from wrong credentials and retry through the restored portal;
- confirm standalone mode and remain able to reach the interface;
- revisit setup later and change the selected mode;
- complete these flows on the supported captive-browser clients from Phase 3.

The repeatable Raspberry Pi procedure and required evidence are in the
[Phase 4 hardware checklist](phase-4-hardware-checklist.md).
