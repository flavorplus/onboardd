# Phase 8 — Packaging and installation

Status: in progress.

## Boundary

Phase 8 turns the accepted foreground appliance into an installable Linux service. It
adds systemd readiness and watchdog integration, reproducible release binaries, a Debian
package, secure file ownership, and documented install, upgrade, and removal behavior.
It does not broaden the supported Wi-Fi or client-platform matrix; final product and
hardware coverage remains Phase 9.

The package must preserve these boundaries:

- NetworkManager remains the source of durable network intent.
- `/etc/onboardd/config.toml` and referenced password files are administrator-owned
  configuration and are never silently replaced during upgrade.
- Runtime state belongs below `/run/onboardd` and disappears with the service.
- Uninstalling the package does not delete user-selected NetworkManager profiles.
- Temporary captive resources are still cleaned by the daemon's owned-resource rules.

## Installed layout

```text
/usr/bin/onboardd                         executable
/usr/lib/systemd/system/onboardd.service vendor service unit
/etc/onboardd/                           root-owned configuration directory, mode 0750
/etc/onboardd/config.toml                dpkg conffile, mode 0640
/etc/onboardd/*-password                 administrator-created secrets, mode 0600
/run/onboardd/control.sock               transient recovery control socket
```

The package does not create password files or start or enable the service on a fresh
install. The administrator must customize the conffile, create every referenced secret,
and then explicitly enable the service. Upgrades restart onboardd only when it was
already active. Removal stops the daemon cleanly and preserves configuration; purge
allows dpkg to remove its conffile but preserves administrator-created password files.
No package lifecycle script deletes NetworkManager profiles.

The package runs `onboardd` as root because NetworkManager's system-bus policy,
nftables, the owned dnsmasq fragment, and optional GPIO access cross privileged host
boundaries. The service unit reduces that authority with a capability bounding set,
read-only system and home mounts, a private temporary directory, a restrictive umask,
and kernel, namespace, and address-family restrictions. `PrivateDevices` is deliberately
not enabled because it would hide the optional configured GPIO character device.

## systemd contract

The `run` process reads `NOTIFY_SOCKET`, `WATCHDOG_USEC`, and `WATCHDOG_PID` once before
starting concurrent runtime components. Without `NOTIFY_SOCKET`, foreground behavior is
unchanged.

Under `Type=notify`:

- the first stable infrastructure, standalone, or provisioning state sends `READY=1`;
- normalized lifecycle changes update `STATUS=` without exposing SSIDs, UUIDs, paths,
  passwords, or raw platform errors;
- a healthy startup, stable state, or bounded recovery sends `WATCHDOG=1` every half of
  the configured watchdog interval;
- stopping or terminal failure does not keep the watchdog alive;
- notification delivery failure stops the runtime cleanly so systemd can apply
  `Restart=on-failure`.

The initial unit uses a 30-second watchdog, a 90-second startup limit, and a 90-second
shutdown limit. The shutdown window accommodates protected-transition rollback and
owned-resource cleanup already accepted in Phase 7.

## Implementation slices

1. Attach the Phase 7 health signal to systemd readiness, status, and watchdog
   notifications; add a hardened service unit and validate it on the target.
2. Define version metadata and reproducible Linux ARM64/AMD64 release builds without
   requiring CGO on the target.
3. Build the Debian package layout, maintainer scripts, dependency metadata, conffile
   policy, and secure default permissions.
4. Add install, first-boot configuration, service operation, upgrade, rollback,
   uninstall, and purge documentation.
5. Add CI release checks and verify installation, watchdog restart, upgrade, and removal
   on a clean Raspberry Pi OS Trixie image.

## Progress

- [x] Implement optional native systemd readiness, status, and watchdog notifications.
- [x] Add a hardened vendor service unit with bounded restart and shutdown behavior.
- [x] Add reproducible versioned ARM64 and AMD64 release builds.
- [x] Add Debian packaging and maintainer behavior.
- [x] Add operator installation, upgrade, rollback, removal, and purge documentation.
- [x] Add automated package structure, lifecycle, and reproducibility checks in CI.
- [ ] Complete clean-image Raspberry Pi package and systemd acceptance.

The target procedure and acceptance record are in the
[Phase 8 Raspberry Pi checklist](phase-8-hardware-checklist.md).

## Exit criterion

Phase 8 is complete when a clean supported system can install, configure, enable,
operate, upgrade, and remove onboardd predictably; systemd can distinguish startup,
readiness, recovery, failure, and clean shutdown; and package operations preserve
administrator configuration and user-selected network intent.
