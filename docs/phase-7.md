# Phase 7 — Recovery and appliance reliability

Status: complete. Accepted on 2026-08-25 on Raspberry Pi Zero 2 W with Raspberry Pi OS
Trixie.

## Boundary

Phase 7 turns the accepted setup workflow into a long-running appliance controller.
At boot, onboardd must derive durable network intent from NetworkManager, preserve a
usable infrastructure or standalone selection, and enter temporary provisioning only
when the selected production mode cannot become usable within its grace period.

The `onboardd recover` command asks an already-running controller to enter temporary
provisioning over a private local socket. `onboardd setup` remains the standalone
recovery process for use while the controller is stopped. `onboardd run` is the normal
appliance entry point and, in Phase 8, the systemd service entry point. All three paths
use the same configuration and setup application.

Phase 7 does not install a systemd unit, assign Linux capabilities, build packages, or
define upgrade behavior. Those remain Phase 8 responsibilities.

## Startup contract

```text
BOOT
  │
  ▼
inspect NetworkManager intent and active state
  │
  ├── standalone selected
  │       ├── active/usable ───────────────► SERVE SETUP ON PRODUCTION NETWORK
  │       └── activation grace expires ───► TEMPORARY PROVISIONING
  │
  ├── infrastructure candidate selected
  │       ├── requirement satisfied ──────► SERVE SETUP ON PRODUCTION NETWORK
  │       └── activation/connectivity
  │           grace expires ──────────────► TEMPORARY PROVISIONING
  │
  └── no usable selected candidate ───────► TEMPORARY PROVISIONING
```

`onboardd run` must not replace a working production connection merely because it has
started. NetworkManager remains the source of truth for profile activation and durable
mode selection. The controller reacts to normalized state transitions and owns only the
temporary captive resources it creates.

The private HTTP setup listener and Avahi service remain available in every mode. The
wildcard DNS, provisioning profile, and nftables redirect exist only while temporary
provisioning is active.

## Recovery invariants

- Never delete, disable, or rewrite an unmanaged NetworkManager profile automatically.
- Never leave a rejected candidate selected for autoconnect.
- Restore and confirm the exact previous profile after a protected transition fails.
- Keep temporary provisioning in memory and remove only resources owned by onboardd.
- A transient cleanup failure remains retryable; idempotence must not cache an
  incomplete cleanup forever.
- Graceful shutdown waits for any active protected transition and rollback before
  removing the connection it may be restoring.
- A network mutation begins only after its `202 Accepted` response has been flushed
  successfully to the browser.
- Retry loops are bounded, cancellable, and use internal policy rather than expanding
  the public configuration surface prematurely.
- Credentials and raw D-Bus details never enter ordinary logs or browser errors.
- Cold start removes only provably owned stale captive resources and pending
  infrastructure candidates before reconciliation.

## Lifecycle observability

`onboardd run` writes its existing operator-oriented status lines to stdout and emits
machine-readable lifecycle events as one JSON object per line on stderr. The JSON event
contract contains only fixed component names, normalized state-machine fields, bounded
retry counters, and action outcomes. It deliberately has no API for passwords, SSIDs,
profile UUIDs, raw errors, `state.Detail`, or D-Bus object paths.

The always-on private listener serves `GET /healthz`. Its response separates liveness
from readiness:

- `healthy=true` means the runtime may continue receiving watchdog heartbeats. Startup
  and bounded internal recovery remain healthy.
- `ready=true` means reconciliation has reached infrastructure, standalone, or temporary
  provisioning and the private setup listener is available.
- stopping, stopped, and terminal failure return HTTP `503`; live startup and bounded
  recovery return `200` with `ready=false`.

The same concurrency-safe health tracker publishes coalesced in-process state changes.
Phase 8 can consume that signal for systemd `READY=1`, `WATCHDOG=1`, and status updates
without coupling the controller or this phase to a service-manager transport.

## Bounded recovery policy

Recovery is deliberately process-internal and does not add more product configuration.
Each long-running component receives at most three restarts, separated by a cancellable
two-second delay. Exhausting a budget terminates `onboardd run`; Phase 8 service
supervision can then start a fresh process with a fresh view of NetworkManager state.

| Failure or outcome | Behavior |
| --- | --- |
| Observer startup, snapshot, or event-stream failure | Restart the complete reconciliation session and inspect NetworkManager again. |
| Temporary captive enter/leave failure | Restart reconciliation; captive actions are idempotent and converge from their actual resource state. |
| Private HTTP listener accept or rebind failure | Rebind the same private address without stopping reconciliation. |
| Infrastructure or standalone is not usable | Allow one bounded activation/connectivity grace period, then enter provisioning. |
| Rejected user-entered Wi-Fi credentials | Use the protected transition rollback and wait for another explicit user attempt; never repeat credentials automatically. |
| Wi-Fi device is unmanaged or a controller state is unsupported | Treat the condition as terminal because repeating it cannot repair host policy or a programming error. |
| Process cancellation | Interrupt retry delays, stop both supervised components, and perform bounded cleanup without another restart. |
| Abrupt exit or power loss | On the next start, remove the owned redirect, DNS fragment, provisioning profiles, and pending candidates before deriving durable intent again. |

Every reconciliation restart receives a child context. Returning from a failed attempt
cancels that context before the next observer and state-engine goroutines are created,
preventing recovery from accumulating stale watchers.

## Commands

```text
onboardd run   --config /etc/onboardd/config.toml
onboardd recover
onboardd setup --config /etc/onboardd/config.toml
```

- `run` reconciles first and enters provisioning only when required.
- `recover` coalesces a request through `/run/onboardd/control.sock`; it never starts a
  second HTTP listener or NetworkManager controller.
- `setup` directly activates temporary provisioning when `run` is not active.
- Both remain foreground processes in Phase 7. Service supervision is added in Phase 8.

## Manual and GPIO recovery

The local control socket is created with mode `0600`, removed during graceful shutdown,
and safely replaces only a refused stale socket from a crashed process. Repeated manual
or GPIO triggers coalesce while one recovery request is pending. The controller does not
acknowledge that request until temporary provisioning has started successfully, so its
bounded supervisor can retry a failed entry without losing operator intent.

GPIO is disabled by default. When enabled, onboardd uses Linux's GPIO v2 character-device
API directly—no polling subprocess or legacy sysfs export. Connect a normally-open button
between the configured BCM GPIO line and ground. onboardd requests an input with pull-up,
50 ms kernel debounce, and both edges. Holding the button for three seconds requests
recovery; releasing it early cancels the request, and another request requires a release
before the next hold.

```toml
[recovery.gpio]
enabled = true
chip = "/dev/gpiochip0"
line = 17
```

Only the chip and line vary by hardware. Debounce and hold duration remain internal safety
policy rather than expanding the product-facing configuration contract.

## Known networks

The setup UI includes a Known networks view backed by `GET /api/v1/known-networks`.
It shows saved Wi-Fi client profiles that may apply to the configured interface without
returning passwords, file paths, or other NetworkManager internals. Unmanaged profiles
are clearly marked read-only.

`POST /api/v1/known-networks/{uuid}/connect` activates an inactive onboardd-owned profile
through the ordinary protected infrastructure transition. The server resolves the SSID,
rechecks ownership, role, interface, active state, and Wi-Fi mode, then applies the same
checkpoint, connectivity test, exact-profile rollback, mode selection, and captive cleanup.
The saved target is retained if the attempt fails.

`DELETE /api/v1/known-networks/{uuid}` uses the existing CSRF and origin checks and then
revalidates the target on the server. Only an inactive, onboardd-owned infrastructure
profile bound to the configured interface can be removed. The active profile, standalone
and provisioning profiles, profiles for another interface, and unmanaged/system profiles
are never deletable through this API. The UI also requires a separate confirmation view.

## Implementation slices

1. Define and test the appliance lifecycle contract, production `run` command, and
   normalized controller actions without changing the accepted manual setup behavior.
2. Separate the always-on HTTP/mDNS resources from temporary captive resources, then
   connect the Phase 2 state engine to provisioning entry and exit.
3. Add bounded activation/connectivity retries and recover from observer or listener
   failures without tight loops or unbounded goroutines.
4. Add explicit manual recovery activation and the optional GPIO input adapter using
   the same controller request path.
5. Add a product-facing **Known networks** view. Owned infrastructure profiles can be
   activated through protected recovery or forgotten with confirmation; unmanaged
   profiles are visible but read-only.
6. Harden transition shutdown and cleanup: wait for active rollback, honor HTTP flush
   failures, and make partial captive cleanup retryable.
7. Add structured, redacted lifecycle logging and a watchdog-ready health signal.
8. Verify boot, reboot, interrupted-transition, Internet-loss, manual recovery, GPIO,
   known-network deletion, and power-loss behavior on Raspberry Pi hardware.

## Local progress

- [x] Normalize controller actions from the Phase 2 state engine.
- [x] Separate the private HTTP listener from temporary AP and DNS ownership.
- [x] Add an idempotent captive manager shared by reconciliation and setup operations.
- [x] Add `onboardd run` with an always-on setup listener and Avahi publication.
- [x] Enter and leave temporary AP, wildcard DNS, and nftables redirect from reconciled
      production state.
- [x] Add bounded recovery when an observer, controller action, or listener fails.
- [x] Add root-local manual recovery and optional long-press GPIO recovery through one
      coalesced controller request path.
- [x] Add the Known networks API and confirmation UI with server-enforced, owned-only
      protected activation and infrastructure-profile deletion.
- [x] Wait for protected transitions during shutdown, require successful HTTP acceptance
      delivery before radio work, and retain incomplete captive cleanup for bounded retry.
- [x] Emit structured, redacted lifecycle events and expose a transport-neutral health
      signal with an HTTP liveness/readiness view.
- [x] Mark uncommitted infrastructure candidates and reconcile owned crash-surviving
      captive resources on cold start.

## Exit criterion

Phase 7 is complete when ordinary boot preserves every usable selected production mode,
recoverable failures open temporary setup without administrator intervention, explicit
recovery remains available, and interruption cannot leave the supported appliance
permanently inaccessible or with stale temporary resources.
