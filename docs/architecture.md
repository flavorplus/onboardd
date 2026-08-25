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

During provisioning, the product application may already own TCP port 80. A temporary
nftables rule redirects only port 80 traffic entering through the provisioning interface
to onboardd's private HTTP listener. Redirected public requests receive a minimal
browser-handoff page; the complete setup application is available only through an
explicit request to the private listener port. The rule uses a separately owned table
and is removed with the captive lifecycle; traffic arriving through production
interfaces keeps reaching the product application.

## Initial deployment baseline

The first hardware target is a Raspberry Pi Zero 2 W running Raspberry Pi OS Trixie.
This establishes several design constraints:

- the daemon and embedded assets must remain comfortable within a 512 MB device;
- expensive background polling and unnecessary runtime dependencies are avoided;
- the web interface runs in the user's phone or computer browser, not in a browser on
  the Pi;
- ARM64 is the initial build assumption, with the actual Trixie image architecture
  recorded before the first hardware acceptance run;
- hardware tests use the Zero 2 W's real Wi-Fi interface and firmware rather than
  assuming behavior from desktop Linux.

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
onboardd.pending = true  # infrastructure candidate only, removed when committed
```

Connection IDs remain readable for administrators, but program logic must use metadata,
UUIDs, and NetworkManager properties rather than parsing profile names.

Profiles carrying `onboardd.owner=onboardd` are managed profiles: `onboardd` may update
or remove them. Existing profiles without that marker may be observed and used, but are
not deleted or adopted automatically.

A disk-backed infrastructure candidate carries `onboardd.pending=true` from creation
until the validated autoconnect update commits it as durable intent. Cold-start recovery
may delete only owned pending candidates for the configured interface. This distinguishes
an interrupted transaction from a deliberately disabled, previously committed network.

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

NetworkManager checkpoints protect the single-radio transition. The candidate is not
made eligible for autoconnect until activation and the configured connectivity policy
succeed. Failure rolls the checkpoint back, removes the rejected candidate, and confirms
that the previous provisioning UUID and address are active again. The invariant remains
more important than the mechanism: a failed change must not leave the appliance
permanently inaccessible.

## Application handoff

Handoff is configured rather than product-specific. Public port 80 serves a dedicated
landing entry from the same compiled frontend as the main setup UI, with a user-activated
link and a manual address fallback. The complete setup service runs at Avahi's existing,
host-managed mDNS hostname and the private listener port. onboardd publishes only its
HTTP service and never changes that hostname. A normal browser then keeps one origin
while the hostname resolves to the provisioning, standalone, or infrastructure address
on the current link. The service retains operation history in memory, and bounded
browser requests retry after radio interruption.

Before standalone mode replaces provisioning, the UI presents the final SSID and, when
product policy permits it, the password, copy action, and Wi-Fi join QR. Application
handoff uses ordinary links rather than a second QR. Infrastructure mode requires the
client to join the same network and rediscover the appliance through the mDNS hostname.
A manual address remains available as a fallback.

The captive browser may close automatically when Internet connectivity returns, so the
handoff flow must remain understandable even if the original portal page disappears.
The design does not depend on an automatic popup: opening a new browsing context can be
blocked without immediate user activation, and captive assistants may impose additional
restrictions.

## Security boundaries

- Never log Wi-Fi or AP passwords.
- Keep structured lifecycle events limited to normalized state fields, fixed component
  names, counters, and outcomes; never pass raw errors or D-Bus details to the logger.
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
internal/appliance           long-running reconciliation and resource actions
internal/networkmanager      D-Bus adapter and domain translation
internal/state               reconciliation and transient event engine
internal/connectivity        local/Internet requirement evaluation
internal/captive             captive detection and portal plumbing
internal/web                 HTTP API and embedded frontend
internal/handoff             product application discovery and handoff
internal/recovery            checkpoints, retries, and recovery input
internal/observability       redacted lifecycle events and process health
internal/systemd             optional readiness and watchdog notification transport
integration                  product example configurations
```

The appliance controller and private HTTP listener use separate bounded supervisors.
Observer and captive-action failures restart reconciliation from a fresh NetworkManager
snapshot; listener failures rebind without disrupting that reconciliation. Credential
submissions are never replayed automatically, and an unusable selected mode converges
to temporary provisioning after its configured grace period.

Before reconciliation, a newly started controller removes only resources whose ownership
can be proven without process memory: the `inet onboardd_captive` nftables table, the
fixed onboardd dnsmasq fragment, owned provisioning profiles on the configured interface,
and owned infrastructure profiles still marked pending. The private listener is bound
first so a second live process cannot perform this cleanup after losing the port race.

Manual recovery reaches the running controller through the fixed root-only Unix socket
`/run/onboardd/control.sock`. The optional Linux GPIO adapter and the socket server both
feed one coalescing in-memory request source; neither owns networking resources. The
controller keeps a failed request pending across a supervised restart and acknowledges
it only after captive entry succeeds. GPIO uses the kernel character-device v2 API with
pull-up, debounce, and a three-second active-low hold rather than a shell command or the
deprecated sysfs GPIO interface.

The Known networks API translates saved NetworkManager Wi-Fi client profiles into a
credential-free product model. Profiles that may apply to the configured interface are
visible, including unmanaged profiles, but every mutation is authorized again on the
server: the target must be inactive, onboardd-owned, tagged as infrastructure, bound to
the configured interface, and still a Wi-Fi client profile. The browser cannot broaden
this policy by changing `can_connect`, changing `can_forget`, or submitting another UUID.
Activation uses the same checkpoint, connectivity requirement, exact-profile rollback,
durable mode selection, and captive exit as a newly entered infrastructure network;
rollback never deletes the existing target.

`onboardd run` keeps operator status text on stdout and writes JSON lifecycle events to
stderr. A concurrency-safe health tracker derives liveness and readiness from normalized
state transitions plus bounded component recovery. The private HTTP listener exposes
the same snapshot at `/healthz`, while a coalesced in-process change channel remains
independent of systemd. The optional Phase 8 notifier consumes that channel and sends
readiness, normalized status, and watchdog datagrams without putting service-manager
behavior into the controller or changing foreground execution.
