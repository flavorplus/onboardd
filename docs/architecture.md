# Architecture

## Responsibilities

`onboardd` is an orchestration layer between a product-facing setup experience and
NetworkManager.

```text
Anthias / InkyPi / product application
                  ▲
                  │ configurable handoff
                  │
          onboardd HTTP API and UI
                  │
                  │ direct D-Bus
                  ▼
             NetworkManager
                  │
                  ▼
          Linux Wi-Fi interface
```

NetworkManager owns Wi-Fi devices, connection profiles, activation, IP configuration,
and persistent network settings. `onboardd` owns policy, orchestration, the captive
setup experience, product branding, and recovery behavior.

## Network roles

| Role | Profile storage | Meaning |
|---|---|---|
| `infrastructure` | Persistent | Join an existing network as the intended operating mode. |
| `standalone` | Persistent | Keep the appliance's access point active as the intended operating mode. |
| `provisioning` | In memory | Temporarily expose setup or recovery. |

Provisioning and standalone can have similar radio settings, but they are not the same
state. Provisioning exists to change configuration. Standalone is a valid final mode and
must not disappear when an old infrastructure network becomes available.

## Persistent source of truth

There is no `/var/lib/onboardd/state.json` for networking mode. Durable intent and
current reality are derived from NetworkManager:

- saved profiles;
- `connection.autoconnect` and autoconnect priority;
- active connections and device state;
- connectivity state;
- application metadata stored in `user.data`.

Profiles created by `onboardd` carry metadata similar to:

```text
onboardd.owner  = onboardd
onboardd.role   = provisioning | standalone | infrastructure
onboardd.schema = 1
```

Connection IDs remain readable for administrators, but program logic must use metadata,
UUIDs, and NetworkManager properties rather than parsing profile names.

Profiles carrying `onboardd.owner=onboardd` are managed profiles: `onboardd` may update
or remove them. Existing profiles without that marker may be observed and used, but are
not deleted or adopted automatically.

## Transient orchestration

Short-lived operations stay in process memory:

```text
BOOTING
RECONCILING
SCANNING
CONNECTING
WAITING_FOR_CONNECTIVITY
STARTING_PROVISIONING
ROLLING_BACK
HANDOFF_READY
```

These are not persisted. After a crash or reboot, `onboardd` reconciles from
NetworkManager's current profiles and active state instead of replaying an interrupted
operation.

## Startup reconciliation

```text
BOOT
  │
  ▼
Inspect NetworkManager
  │
  ├── standalone selected ─────────────► STANDALONE
  │
  ├── infrastructure candidates
  │          │
  │          ▼
  │     wait for autoconnect
  │          │
  │          ├── acceptable ───────────► INFRASTRUCTURE
  │          │
  │          └── failed after grace ───► PROVISIONING
  │
  └── nothing usable ──────────────────► PROVISIONING
```

Connectivity is product policy:

- `local`: an activated connection with usable local IP configuration is sufficient;
- `internet`: Internet connectivity must be verified after a retry/grace window.

Standalone mode never requires upstream connectivity.

## Mode selection

When both production modes are allowed, setup offers infrastructure and standalone.
When standalone is disabled, the UI skips that choice and goes directly to network
selection.

Selecting a mode updates persistent NetworkManager profile eligibility:

- infrastructure selected: managed infrastructure profiles may autoconnect; the managed
  standalone profile does not;
- standalone selected: the standalone profile autoconnects with deliberate priority;
  managed infrastructure profiles do not;
- provisioning never represents persistent intent and is created in memory only.

Unmanaged profile conflicts require hardware validation in Phase 1. The default safety
rule is that `onboardd` may activate or observe an unmanaged profile but does not rewrite
or delete it without an explicit future policy setting.

## Connection transition and recovery

Moving from provisioning or standalone to infrastructure is disruptive because a
single Wi-Fi interface cannot keep the old AP communication path while associating with
the target network.

The transition must therefore be recoverable:

```text
credentials submitted
        │
        ▼
create candidate infrastructure profile
        │
        ▼
attempt activation and validate requirement
        │
        ├── success ─► retain persistent profile
        │
        └── failure ─► restore provisioning/standalone AP
```

NetworkManager checkpoints will be evaluated during the D-Bus proof of concept. The
invariant is more important than the mechanism: a failed network change must not leave
the appliance permanently inaccessible.

## Application handoff

Handoff is configured rather than product-specific. Standalone mode can immediately
open the application because the client remains on the same AP. Infrastructure mode
requires the client to join the same network and rediscover the appliance, normally
through an mDNS hostname.

The captive browser may close automatically when Internet connectivity returns, so the
handoff flow must remain understandable even if the original portal page disappears.

## Security boundaries

- Never log Wi-Fi or AP passwords.
- Prefer protected password files to command-line secrets.
- Bind privileged capabilities as narrowly as practical.
- Manage only profiles explicitly owned by `onboardd` unless policy says otherwise.
- Treat SSIDs, NetworkManager properties, configuration, and uploaded brand assets as
  untrusted input.
- Provide CSRF and request-origin protection for state-changing HTTP endpoints.
- Document the trust model of the local setup AP before v1.0.

## Planned component boundaries

```text
cmd/onboardd                 process entry point
internal/config              load, merge, and validate configuration
internal/networkmanager      D-Bus adapter and domain translation
internal/state               reconciliation and transient event engine
internal/connectivity        local/Internet requirement evaluation
internal/captive             captive detection and portal plumbing
internal/web                 HTTP API and embedded frontend
internal/handoff             product application discovery and handoff
internal/recovery            checkpoints, retries, and recovery input
integration                  product example configurations
```

