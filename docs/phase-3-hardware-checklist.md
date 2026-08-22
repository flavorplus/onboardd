# Phase 3 hardware acceptance record

Target: Raspberry Pi Zero 2 W with Raspberry Pi OS Trixie.

Status: accepted on 2026-08-21.

Phase 3 proved the temporary captive lifecycle and recovery path on the target. The
accepted run covered:

- NetworkManager shared-mode DHCP and wildcard captive DNS;
- interface-scoped nftables HTTP redirection while the appliance retained port 80;
- iOS/macOS-style captive probes reaching the canonical portal;
- repeated wrong-password attempts restoring the exact provisioning profile and address;
- a correct connection committing infrastructure and removing temporary resources;
- standalone selection and a later return to infrastructure;
- clean shutdown of the listener, DNS fragment, nftables table, and temporary profile.

The phase-specific captive and transition commands and recorder were removed after the
complete configured setup flow passed acceptance. `scripts/setup-recorder.sh` is now the
single SSH-safe runner for hardware setup tests.

The implementation contract remains documented in [Phase 3](phase-3.md) and exercised
by deterministic tests in `internal/captive` and `internal/recovery`.
