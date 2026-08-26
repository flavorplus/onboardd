# Roadmap

## Completed foundation

Phases 0–8 established and hardware-tested the complete appliance path:

- architecture, configuration contracts, repository, and VS Code workflow;
- direct NetworkManager D-Bus integration and profile metadata;
- deterministic reconciliation without a separate persistent state file;
- temporary access point, captive DNS/HTTP, and scoped nftables redirect;
- setup API and embedded TypeScript UI;
- product branding, templates, and application handoff;
- checkpoint-protected transitions, exact-profile rollback, manual recovery, known
  network management, watchdog integration, and interrupted-start cleanup;
- reproducible ARM64/AMD64 releases, Debian packaging, hardened systemd service, and
  verified install/upgrade/rollback/remove/purge behavior.

Accepted hardware runs include a Raspberry Pi Zero 2 W and Raspberry Pi 4 on
Raspberry Pi OS/Debian Trixie. The detailed historical phase checklists were removed
after acceptance; durable behavior is documented in Architecture, Configuration,
Development, and Installation.

## Phase 9 — Hardware validation and v1.0

Validate the release candidate across:

- Raspberry Pi Zero 2 W and Raspberry Pi 4;
- WPA2/WPA3 Personal, open and hidden networks, and multiple saved profiles;
- incorrect credentials, slow DHCP/DNS, local-only networks, and Internet loss;
- repeated infrastructure/standalone/recovery transitions;
- reboot, power interruption, listener restart, and stale-resource cleanup;
- iOS, Android, macOS, and Windows captive-portal behavior;
- Anthias and InkyPi reference configurations;
- fresh install, upgrade, rollback, removal, purge, and reproducible release artifacts.

Exit criteria:

- a documented support matrix;
- no unresolved data-loss or permanent-inaccessibility failure;
- a repeatable release checklist;
- signed-off v1.0 packages and checksums.

The support matrices, scenarios, evidence requirements, and sign-off record live in
the durable [release-validation checklist](release.md).

## Deferred beyond v1

- enterprise Wi-Fi;
- BLE, DPP, or platform-native credential provisioning;
- cloud and fleet management;
- VPN/router features;
- simultaneous multi-radio operation;
- adoption or deletion of foreign NetworkManager profiles.
