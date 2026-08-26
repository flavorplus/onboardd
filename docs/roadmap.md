# Project roadmap

## Phase 0 — Architecture and contracts

Scope:

- establish project boundaries and terminology;
- define NetworkManager as the persistent source of truth;
- define profile ownership and metadata;
- specify infrastructure, standalone, and provisioning roles;
- define configuration precedence and schema;
- create a buildable Go repository;
- establish a VS Code workflow;
- record v1 scope and deferred features.

Exit criteria:

- [x] Architecture document exists.
- [x] Example configuration and JSON Schema exist.
- [x] Go module builds without external dependencies.
- [x] VS Code build/debug/tasks are checked in.
- [x] The repository clearly identifies Phase 1 as the next boundary.
- [x] Project owner reviews and accepts the Phase 0 contract.

## Phase 1 — NetworkManager D-Bus proof of concept

Status: complete. Accepted on Raspberry Pi Zero 2 W with Raspberry Pi OS Trixie.

Scope:

- record the Pi Zero 2 W's OS architecture, kernel, firmware, and NetworkManager version;
- choose and isolate the Go D-Bus client dependency;
- discover the configured Wi-Fi interface;
- inspect devices, active connections, and saved profiles;
- request scans and list access points;
- create and activate an infrastructure profile;
- create an in-memory provisioning AP profile;
- create a persistent standalone AP profile;
- attach and read `user.data` ownership/role metadata;
- observe D-Bus state changes;
- exercise autoconnect priorities and reboot behavior;
- evaluate NetworkManager checkpoints.

Historical Phase 1 proof commands, retired after the configured setup flow replaced
them:

```text
onboardd debug status
onboardd debug profiles
onboardd debug scan
onboardd debug connect
onboardd debug provisioning-start
onboardd debug standalone-start
```

Exit criterion: on the target Raspberry Pi, the binary can reliably demonstrate
`status → scan → provisioning AP → infrastructure → standalone AP → infrastructure`
without production code invoking `nmcli`.

Local completion:

- [x] Direct D-Bus adapter isolated in `internal/networkmanager`.
- [x] Access-point and infrastructure settings builders.
- [x] `user.data` ownership and role metadata.
- [x] Disk and in-memory `AddConnection2` support.
- [x] Profile inspection and owned-only deletion guard.
- [x] Wi-Fi scan and access-point decoding.
- [x] Property-change signal stream.
- [x] Checkpoint create, commit, and rollback calls.
- [x] Debug CLI with disruptive-operation confirmation.
- [x] Local tests and ARM64 cross-build.
- [x] Zero 2 W/Trixie hardware checklist completed.

## Phase 2 — Reconciliation and transient state engine

Status: complete.

Implement boot reconciliation, typed events, timeouts, cancellation, connectivity
requirements, and unit tests using a fake NetworkManager adapter. No persistent state
file is introduced.

Exit criterion: deterministic tests cover successful startup, failed activation,
connectivity failure, standalone persistence, disconnection, restart, and interrupted
transitions.

Implementation checklist:

- [x] Normalized NetworkManager snapshot and observer boundary.
- [x] Separate local and Internet connectivity policy.
- [x] Typed reconciliation events and observable transitions.
- [x] Grace-period timer that is cancelled when no longer needed.
- [x] Context cancellation and clean interrupted-transition handling.
- [x] Fake observer and deterministic clock.
- [x] Exit-criterion scenario tests.
- [x] Real NetworkManager observer and read-only debug command.
- [x] Raspberry Pi observer and mode-transition smoke check.

## Phase 3 — Temporary AP and captive portal plumbing

Status: complete. Accepted on 2026-08-21 on Raspberry Pi Zero 2 W with Raspberry Pi OS
Trixie.

Implement the provisioning AP lifecycle, captive-detection endpoints, DNS/HTTP behavior,
and automatic restoration following failed provisioning.

Exit criterion: iOS, Android, Windows, and macOS can reach setup reliably on supported
hardware, and a failed connection attempt restores the configuration path.

Acceptance evidence:

- NetworkManager shared-mode DHCP and wildcard captive DNS worked on the target.
- Captive probe requests reached the canonical portal through the interface-scoped
  nftables redirect while the appliance application retained port 80.
- Two rejected credential attempts restored the same provisioning profile and address.
- Correct credentials committed infrastructure mode and cleanly removed provisioning.
- Standalone mode and the return to infrastructure selected the expected persistent
  autoconnect intent without modifying foreign profiles.
- Normal shutdown removed the temporary DNS fragment, nftables table, listener, and
  provisioning profile.

## Phase 4 — Setup API and web interface

Status: complete. Accepted on 2026-08-21 on Raspberry Pi Zero 2 W with Raspberry Pi OS
Trixie.

Implement the HTTP API and a small TypeScript frontend covering mode choice, Wi-Fi
selection, hidden networks, connection progress, failure, standalone confirmation,
completion, and later mode changes.

Exit criterion: a nontechnical user can complete either allowed setup path without
seeing NetworkManager terminology.

Implementation checklist:

- [x] Versioned setup/status and Wi-Fi scan API.
- [x] Asynchronous, single-flight network operations that survive browser disconnects.
- [x] Protected infrastructure transition and recoverable user-facing errors.
- [x] Standalone confirmation and persistent mode selection.
- [x] Framework-free TypeScript interface for every setup state.
- [x] Request validation, body limits, no-store responses, and CSRF/origin protection.
- [x] Deterministic Go API/controller tests and frontend tests.
- [x] Failed changes from standalone restore the exact previous standalone profile even
      when an unmanaged infrastructure profile is also eligible for autoconnect.
- [x] Raspberry Pi end-to-end browser acceptance for both operating modes.

Acceptance evidence:

- The captive and normal-browser interfaces exposed both product modes without backend
  terminology and handled visible, open, protected, and hidden network paths.
- Wrong credentials restored setup and retained a safe retry result across browser
  reconnection; correct credentials completed infrastructure mode.
- Standalone remained reachable through the retained listener, and later mode changes
  worked in both directions.
- Exact-profile rollback restored standalone despite a competing unmanaged autoconnect
  profile without modifying that profile.
- Network-only and standalone-only policies removed the disallowed UI and API paths;
  disabling both modes was rejected before changing the network.
- Clean shutdown removed temporary HTTP, DNS, nftables, and provisioning resources while
  leaving the selected production profile durable.

## Phase 5 — Branding and configuration

Status: complete. Accepted on 2026-08-22 on Raspberry Pi Zero 2 W with Raspberry Pi OS
Trixie.

Implement TOML loading, environment/CLI overrides, schema/runtime validation, safe
templates, custom text, colors, logos, SSIDs, and product identity. Embed the compiled
frontend in the Go binary.

Implementation checklist:

- [x] Strict typed TOML loading over built-in defaults.
- [x] Environment and operational CLI overrides with documented precedence.
- [x] Safe template rendering and stable device identity.
- [x] Runtime branding data and optional logo handling.
- [x] Embedded compiled frontend and production startup command.
- [x] Local tests and two-product Raspberry Pi acceptance.

Exit criterion: Anthias and InkyPi branding can be produced from configuration without
forking or rebuilding the frontend.

## Phase 6 — Application handoff

Status: complete.

Implement configurable labels and URLs, optional health checks, and mDNS discovery.
Before a disruptive radio change, offer an explicit user-activated handoff from the
captive assistant to the stable `http://DEVICE.local:LISTENER_PORT/` setup origin in a
normal browser. Keep polling and operation history available at that origin across
provisioning, standalone, and infrastructure addresses. Automatic popup attempts are
not the primary path because clients may block them or close the captive assistant.

Add standalone handoff information before the disruptive switch, including the SSID,
copyable password, and Wi-Fi join QR when product policy permits displaying the
standalone credential. Provide a manual stable-URL fallback for captive viewers that
cannot open a normal browser and transition guidance for infrastructure mode.

Implementation checklist:

- [x] Define and validate the product handoff configuration contract.
- [x] Derive the stable setup URL and expose only browser-safe handoff data.
- [x] Offer an explicit normal-browser link before every disruptive mode transition.
- [x] Present the configured application link and connection controls after success.
- [x] Use the existing Avahi hostname and advertise the setup service on every active link.
- [x] Gate the application link on its optional health check.
- [x] Add pre-transition standalone Wi-Fi and join-QR handoff with the configured credential policy.
- [x] Verify handoff and fallback behavior on Raspberry Pi hardware.

Exit criterion: setup can reliably lead the user to a configured local application in
both production modes, and closing the captive assistant does not discard observable
operation progress.

## Phase 7 — Recovery and appliance reliability

Status: complete. Accepted on 2026-08-25 on Raspberry Pi Zero 2 W with Raspberry Pi OS
Trixie.

Add transactional transitions/checkpoints, retry policies, optional GPIO recovery,
manual setup activation, boot reconciliation hardening, watchdog behavior, log
redaction, and power-loss testing. Add a product-facing **Known networks** view that can
activate or forget onboardd-owned infrastructure profiles. Unmanaged/system profiles
remain read-only by default; deleting or adopting them requires an explicit future
policy and must never happen as an automatic recovery side effect.

Implementation checklist:

- [x] Add the production appliance lifecycle and `onboardd run` command.
- [x] Keep HTTP/mDNS available while captive resources enter and exit dynamically.
- [x] Add bounded activation/connectivity grace and controller/listener recovery.
- [x] Add explicit manual and optional GPIO recovery activation.
- [x] Add the Known networks view with protected owned-profile activation and deletion.
- [x] Harden transition shutdown, HTTP acceptance, and retryable captive cleanup.
- [x] Add redacted lifecycle logging and a watchdog-ready health signal.
- [x] Complete the Phase 7 Raspberry Pi reliability checklist.

The detailed boundary and implementation order are in [Phase 7](phase-7.md). The
accepted target procedure and evidence are in the
[Phase 7 hardware checklist](phase-7-hardware-checklist.md).

Acceptance evidence:

- Production startup preserved the selected infrastructure mode without creating
  captive resources, and reboot restored the same durable profile intent.
- Manual recovery entered one temporary provisioning profile and returned cleanly to
  production without replacing the running controller.
- Known-network activation reused the selected saved profile, retained exact-profile
  rollback protection, and did not create duplicate profiles or pending candidates.
- Health and redacted lifecycle output remained usable across production, provisioning,
  network transitions, restart, and graceful shutdown.
- Graceful and abrupt interruption recovery left no stale owned DNS, nftables,
  provisioning, control-socket, or pending-profile resources.

Exit criterion: common failures and interrupted transitions cannot leave the appliance
permanently inaccessible.

## Phase 8 — Packaging and installation

Status: complete. Accepted on Raspberry Pi OS Trixie on 2026-08-26.

Provide systemd integration, ARM64/AMD64 builds, example configuration, `.deb`
packaging, installation and upgrade documentation, and secure default permissions.

Implementation checklist:

- [x] Add native systemd readiness, status, and watchdog notifications.
- [x] Add the initial hardened systemd service unit.
- [x] Add reproducible versioned ARM64 and AMD64 release builds.
- [x] Add Debian packaging with safe conffile and secret ownership behavior.
- [x] Document installation, upgrade, rollback, removal, and purge.
- [x] Add automated package structure, lifecycle, and reproducibility checks in CI.
- [x] Verify package lifecycle and watchdog recovery on Raspberry Pi OS Trixie.

The boundary and implementation order are in [Phase 8](phase-8.md). The clean-system
acceptance procedure is in the
[Phase 8 Raspberry Pi checklist](phase-8-hardware-checklist.md).

Acceptance evidence:

- ARM64 packages installed with the documented ownership and modes and remained
  inactive and disabled until explicitly enabled.
- systemd readiness, status, watchdog recovery, automatic reboot startup, and graceful
  shutdown behaved as specified.
- Active upgrade, inactive rollback, removal, reinstall, and purge preserved
  administrator secrets and NetworkManager profiles.
- Purge removed package-owned files and transient captive resources while retaining
  administrator-created password files.
- The package and service boundary was accepted on a Raspberry Pi 4 Model B running
  Debian 13.6 (Trixie), ARM64. Raspberry Pi Zero 2 W-specific coverage remains in
  Phase 9.

Exit criterion: a clean supported system can install, enable, configure, upgrade, and
remove `onboardd` predictably.

## Phase 9 — Hardware validation and v1.0

Validate Raspberry Pi targets, multiple saved networks, WPA2/WPA3 Personal, hidden
networks, incorrect credentials, slow DHCP/DNS, local-only networks, Internet loss,
repeated mode changes, reboot/power loss, client OS captive behavior, and the Anthias
and InkyPi reference integrations.

Exit criterion: documented v1 support matrix and repeatable release checklist.

## Deferred beyond v1

- enterprise Wi-Fi;
- BLE credential provisioning;
- Android DPP;
- Apple-native provisioning;
- cloud and fleet management;
- VPN and router features;
- simultaneous multi-radio operation.
