# Phase 5 — Branding and configuration

Status: complete. Accepted on 2026-08-22 on Raspberry Pi Zero 2 W with Raspberry Pi OS
Trixie.

## Boundary

Phase 5 replaces Phase 4's debug-only flags and external frontend directory with a
stable product configuration and frontend assets embedded in the Go binary. One binary
must be able to present Anthias, InkyPi, or product-neutral setup by changing TOML and
optional assets only.

Application discovery, handoff health checks, QR codes, and mDNS remain Phase 6 work.
GPIO recovery and production service packaging remain later phases.

## Implementation slices

1. Load TOML into typed defaults, reject unknown keys, and validate cross-field rules.
2. Apply documented `ONBOARDD_*` environment and operational CLI overrides with clear
   source precedence.
3. Resolve the small template allowlist using a stable device identity and validate
   rendered Wi-Fi names.
4. Expose product text, colors, and an optional logo to the existing setup UI without
   allowing markup or CSS injection.
5. Embed the compiled frontend in the Go binary and add the production startup command.
6. Run product-neutral local checks, then verify two distinct product configurations on
   the Raspberry Pi without rebuilding the frontend.

Each slice keeps the existing Phase 4 debug command operational until the production
startup path has replaced it.

The production entry point is `onboardd setup`. It resolves the public configuration,
loads secure password files and optional branding, renders device-specific values, and
serves the embedded bundle. Low-level Phase 4 flags stay under `debug setup-start`.
This phase starts provisioning explicitly; boot-time mode reconciliation and service
installation remain Phase 7 and Phase 8 responsibilities.

## Configuration rules

- `schema_version` is required whenever a TOML file exists.
- A file overlays built-in defaults, so product files may contain only the settings they
  intentionally change.
- Unknown keys are errors; misspelled product settings must never be silently ignored.
- Structural and cross-field checks happen before NetworkManager or the filesystem is
  changed.
- Runtime checks separately verify interface availability, secret-file permissions, and
  logo readability.
- Secrets remain in referenced files and are not accepted as direct environment or CLI
  values.

The public contract intentionally exposes only product identity and branding, mode
policy, Wi-Fi interface, SSID/password-file references, the standalone gateway subnet,
and the private portal listener port. Provisioning always uses `10.42.0.1/24` and public
port 80. Low-level timing, DNS, and radio settings remain internal defaults with debug
overrides rather than permanent product API.

Template rendering is limited to `.ProductName`, `.DeviceName`, `.DeviceID`, and
`.Hostname` substitutions in branding text and SSIDs. The device ID is a stable,
application-specific eight-character derivative of the Linux machine ID; the raw value
is never exposed. Rendered SSIDs are validated against the 32-byte Wi-Fi limit.

The setup bootstrap returns the rendered product/device names, title, subtitle, and two
validated base colors. The frontend derives its supporting shades from the primary
color rather than expanding the public configuration contract. An optional PNG, JPEG,
or passive SVG logo is loaded before server startup and served from a same-origin API
route. Logos are limited to 512 KiB; malformed raster files and SVG active content,
event handlers, external references, directives, scripts, and embedded objects are
rejected.

## Exit criterion

Phase 5 is complete when one unchanged binary can run the full setup flow with both an
Anthias configuration and an InkyPi configuration, including their text, color, logo,
and rendered Wi-Fi names, while the frontend requires no adjacent asset directory.

## Acceptance evidence

- One unchanged ARM64 binary served both Anthias and InkyPi configurations.
- Product text, colors, acceptance logos, device names, and rendered SSIDs changed from
  TOML without rebuilding or transferring frontend files.
- The stable device suffix remained identical across both product configurations.
- Infrastructure and standalone transitions each completed successfully.
- Secure password-file checks and the embedded production frontend worked on the target.
- Changing `wlan0` disconnected the SSH terminal while leaving onboardd alive to finish
  the browser operation. After reconnecting, a separate graceful termination stopped
  the process and cleaned up its temporary resources.
