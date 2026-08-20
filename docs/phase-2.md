# Phase 2 — Reconciliation and transient state engine

Status: complete.

## Boundary

Phase 2 decides what onboardd's current networking state means. It does not yet start
the captive portal or implement the provisioning AP lifecycle; those effects begin in
Phase 3.

NetworkManager remains the only source of durable intent and current network facts.
The engine does not write a state file. Every process start subscribes to changes and
then constructs a fresh normalized snapshot from:

- the Wi-Fi device state and active profile UUID;
- usable IPv4 address presence;
- NetworkManager's connectivity result;
- connection profile mode, role, ownership, and autoconnect eligibility.

Subscribing before the initial snapshot prevents a change between startup and
inspection from being missed. Notifications are coalesced because every notification
causes a fresh snapshot rather than being treated as the new source of truth.
Profiles removed between enumeration and inspection are skipped as a normal lifecycle
race; unrelated D-Bus failures remain visible.

## Connectivity policy

Connectivity evaluation is isolated in `internal/connectivity`:

```text
local    = device activated + local IPv4 address
internet = local requirement + NetworkManager connectivity FULL
```

An infrastructure candidate that has not met its requirement enters
`waiting-for-connectivity`. One in-memory grace timer begins on entry and is not reset
by every NetworkManager notification. Success stops it; expiry requests provisioning.
Standalone never depends on upstream connectivity.

Mode-changing debug commands create candidates with autoconnect disabled. Only after
NetworkManager confirms activation does onboardd remove superseded profiles and update
the durable eligibility rules: infrastructure selection enables managed infrastructure
profiles and disables standalone, while standalone selection does the inverse.
For those updates, onboardd reads the stored Wi-Fi secret with NetworkManager's
privileged API and rebuilds the small profile schema that onboardd owns. It never logs
the secret and does not retransmit NetworkManager-generated legacy properties whose
D-Bus tuple values cannot be safely round-tripped by the Go client library.

## Reconciliation states

```text
BOOTING
   ↓
RECONCILING
   ├── active/selected standalone ───────► STANDALONE
   ├── acceptable infrastructure ───────► INFRASTRUCTURE
   ├── candidate still connecting ──────► WAITING_FOR_CONNECTIVITY
   ├── active provisioning ─────────────► PROVISIONING
   ├── failed/timed-out/no candidate ───► PROVISIONING
   └── unmanaged Wi-Fi device ──────────► FAILED
```

`PROVISIONING` is a requested orchestration state in this phase. Phase 3 attaches the
actual temporary AP and captive plumbing to that state.

Transitions record their typed trigger (`boot`, `network-changed`, `grace-expired`, or
`cancelled`) and a process-local sequence number. Cancellation stops the active timer,
emits `stopped`, and persists nothing. Restart therefore inspects NetworkManager again
instead of replaying an interrupted transition.

## Deterministic tests

The fake observer and manually triggered clock cover the Phase 2 exit criteria without
sleep-based timing:

- successful infrastructure startup;
- failed activation fallback;
- Internet-connectivity grace expiry;
- standalone intent across a process restart;
- disconnection followed by grace expiry;
- cancellation during a pending transition;
- standalone operation without Internet.

## Raspberry Pi validation

Validated on a Raspberry Pi Zero 2 W running Raspberry Pi OS Trixie. A recorded
standalone-to-infrastructure transition confirmed that:

- the observer derived `standalone-active` and `infrastructure-ready` correctly;
- the selected profile became eligible for autoconnect and the other production mode
  became ineligible;
- superseded standalone and infrastructure profiles were removed;
- the temporary provisioning profile remained in memory and ineligible for autoconnect;
- unmanaged netplan profiles were observed without being modified or deleted;
- NetworkManager remained available throughout the transition.

## Diagnostic command

On Linux, inspect the current derived state without changing networking:

```bash
onboardd debug reconcile --interface wlan0 --requirement local
```

Watch transitions until interrupted:

```bash
onboardd debug reconcile \
  --interface wlan0 \
  --requirement internet \
  --grace-period 30s \
  --watch
```

Add `--json` for structured output. The command is read-only; reaching the
`provisioning` state does not start an AP in Phase 2.

## Recording disruptive remote tests

When only SSH is available, use `scripts/phase-2-recorder.sh`. It detaches both the
watcher and transition commands from the SSH session so switching `wlan0` cannot stop
them. Commands and their output are appended to one timestamped log. The recorder logs
the binary's SHA-256 before the observer and every transition command, making stale
deployments visible in the test evidence.

Copy the script to the Pi, make it executable, and start the observer while the SSH
connection is stable:

```bash
chmod +x ~/phase-2-recorder.sh
~/phase-2-recorder.sh start local 30s
```

Launch every test command through the recorder. For example:

```bash
~/phase-2-recorder.sh run debug provisioning-start \
  --interface wlan0 \
  --ssid Onboardd-Setup-Test \
  --password-file /tmp/onboardd-test-password \
  --yes
```

After reconnecting, inspect or follow the combined record:

```bash
~/phase-2-recorder.sh show
~/phase-2-recorder.sh follow
```

Stop only the reconciliation watcher when finished; launched transition commands are
short-lived independent processes:

```bash
~/phase-2-recorder.sh stop
```

## Capturing full D-Bus traffic

Use `scripts/dbus-trace-recorder.sh` when a transition fails specifically at the
D-Bus boundary. It captures complete calls to NetworkManager's connection-profile
interface plus replies and errors, allowing a failing onboardd call to be compared with
a successful reference client call.

```bash
chmod +x ~/dbus-trace-recorder.sh
~/dbus-trace-recorder.sh start
# Reproduce one onboardd failure, then perform the equivalent nmcli update.
~/dbus-trace-recorder.sh stop
~/dbus-trace-recorder.sh show
```

The trace contains full message bodies and can include Wi-Fi passwords. Treat the log
as a secret. Before reproducing anything, use `sha256sum ~/onboardd` to prove that the
Pi is executing the intended build.
