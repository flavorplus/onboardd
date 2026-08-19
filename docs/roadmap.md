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

Developer-facing commands:

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

## Phase 2 — Reconciliation and transient state engine

Implement boot reconciliation, typed events, timeouts, cancellation, connectivity
requirements, and unit tests using a fake NetworkManager adapter. No persistent state
file is introduced.

Exit criterion: deterministic tests cover successful startup, failed activation,
connectivity failure, standalone persistence, disconnection, restart, and interrupted
transitions.

## Phase 3 — Temporary AP and captive portal plumbing

Implement the provisioning AP lifecycle, captive-detection endpoints, DNS/HTTP behavior,
and automatic restoration following failed provisioning.

Exit criterion: iOS, Android, Windows, and macOS can reach setup reliably on supported
hardware, and a failed connection attempt restores the configuration path.

## Phase 4 — Setup API and web interface

Implement the HTTP API and a small TypeScript frontend covering mode choice, Wi-Fi
selection, hidden networks, connection progress, failure, standalone confirmation,
completion, and later mode changes.

Exit criterion: a nontechnical user can complete either allowed setup path without
seeing NetworkManager terminology.

## Phase 5 — Branding and configuration

Implement TOML loading, environment/CLI overrides, schema/runtime validation, safe
templates, custom text, colors, logos, SSIDs, and product identity. Embed the compiled
frontend in the Go binary.

Exit criterion: Anthias and InkyPi branding can be produced from configuration without
forking or rebuilding the frontend.

## Phase 6 — Application handoff

Implement configurable labels and URLs, optional health checks, mDNS discovery,
standalone immediate handoff, and infrastructure-network transition guidance.

Exit criterion: setup can reliably lead the user to a configured local application in
both production modes.

## Phase 7 — Recovery and appliance reliability

Add transactional transitions/checkpoints, retry policies, optional GPIO recovery,
manual setup activation, boot reconciliation hardening, watchdog behavior, log
redaction, and power-loss testing.

Exit criterion: common failures and interrupted transitions cannot leave the appliance
permanently inaccessible.

## Phase 8 — Packaging and installation

Provide systemd integration, ARM64/AMD64 builds, example configuration, `.deb`
packaging, installation and upgrade documentation, and secure default permissions.

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
