# Phase 4 hardware acceptance record

Target: Raspberry Pi Zero 2 W with Raspberry Pi OS Trixie.

Status: accepted on 2026-08-21.

Phase 4 proved the complete product-facing API and browser flow on the target. The
accepted run covered:

- visible, open, protected, and hidden Wi-Fi paths;
- safe wrong-password restoration and retry after browser reconnection;
- successful infrastructure and standalone completion;
- later changes in both directions from the retained private listener;
- exact standalone-profile rollback despite a competing unmanaged autoconnect profile;
- network-only and standalone-only product policies;
- clean shutdown without disturbing the selected durable or unmanaged profiles.

Phase 5 embedded the frontend and replaced the external-directory debug runner. The
temporary Phase 4 command, copied frontend directory, and phase-specific recorder were
therefore removed. Current hardware runs use the production binary and
`scripts/setup-recorder.sh`.

The API and UI contract remains documented in [Phase 4](phase-4.md) and covered by tests
in `internal/setup`, `internal/web`, and `frontend`.
