# Phase 1 hardware acceptance record

Target: Raspberry Pi Zero 2 W with Raspberry Pi OS Trixie.

Status: accepted on 2026-08-20.

Phase 1 proved the direct NetworkManager D-Bus boundary on the target hardware. The
accepted run covered:

- device, connectivity, active-connection, saved-profile, and access-point inspection;
- infrastructure, temporary provisioning, and persistent standalone profiles;
- onboardd ownership and role metadata in `user.data`;
- repeated profile replacement without accumulating provisioning profiles;
- durable autoconnect selection between infrastructure and standalone modes;
- property-change observation and NetworkManager checkpoint behavior;
- preservation of unmanaged profiles.

The temporary mutation and checkpoint CLI commands used to establish this evidence were
removed after the production setup flow superseded them. Current verification uses
`onboardd setup`; retained read-only diagnostics are listed by `onboardd debug help`.

Detailed transition behavior and the resulting architecture decisions remain in
[Phase 1](phase-1.md), the [roadmap](roadmap.md), and the NetworkManager unit tests.
