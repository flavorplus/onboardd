# Phase 8 — Packaging and installation

Status: complete. Accepted on Raspberry Pi OS Trixie on 2026-08-26.

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
- [x] Complete Raspberry Pi package and systemd acceptance.

The target procedure and acceptance record are in the
[Phase 8 Raspberry Pi checklist](phase-8-hardware-checklist.md).

## Accepted hardware run

The package and systemd boundary was accepted on 2026-08-26 with this target and build
environment:

- Raspberry Pi 4 Model B Rev 1.1;
- Debian GNU/Linux 13.6 (Trixie), ARM64;
- Linux `6.18.39+rpt-rpi-v8`;
- NetworkManager 1.52.1;
- systemd 257.13 and dpkg 1.22.22;
- Go 1.26.7 and Node.js 26 in release CI.

The target already contained Docker interfaces and saved NetworkManager profiles but
had no installed onboardd package, binary, or systemd unit. Those existing profiles
served as preservation canaries throughout the package lifecycle. This run accepts the
ARM64 Debian package and systemd integration; Raspberry Pi Zero 2 W-specific behavior
remains part of Phase 9 hardware coverage.

The tested ARM64 packages were:

| Package | SHA-256 |
| --- | --- |
| `onboardd_0.0.0~ci.9-1_arm64.deb` | `4bed1a92903c45222200b0e28130fdab30441553913078cce1fcbf5d2a7eda7e` |
| `onboardd_0.0.2-1_arm64.deb` | `0c02877e18c6f7514706b8e1058cce723f0fb3061fc3e8f29d29f8af6334258d` |

Both package digests passed `DEBSHA256SUMS` verification. The release workflow built
and inspected the packages twice and compared the resulting binaries, packages, and
checksum manifests byte for byte.

Acceptance results:

- Fresh installation produced the documented binary, unit, directory, and conffile
  modes without creating password files or enabling or starting the service.
- Once configured and enabled, the service reported `Ready`, published a healthy and
  ready infrastructure state, maintained its 30-second watchdog, and created its runtime
  directory and control socket with modes `0700` and `0600`.
- Reboot automatically restored the same durable infrastructure profile and healthy
  service state. Only expected volatile Docker, veth, bridge, and loopback profiles
  received new identifiers.
- Suspending PID 251590 deliberately expired the watchdog; systemd restarted it as PID
  253066, incremented the restart counter once, and restored readiness and health.
- An active upgrade from `0.0.0~ci.9-1` to `0.0.2-1` restarted the service while
  preserving the configured conffile, both password files, and NetworkManager profiles.
- An inactive rollback to `0.0.0~ci.9-1` kept the service inactive and preserved the
  same administrator and network state.
- Removal stopped the service and removed the binary and unit while retaining the
  conffile, passwords, and profiles. Reinstalling `0.0.2-1` also kept the service
  inactive and preserved those files byte for byte.
- Purge removed the package, unit, binary, and dpkg conffile while retaining both
  administrator-created password files with mode `0600` and every NetworkManager
  profile.
- No onboardd control socket, dnsmasq fragment, or captive nftables table remained after
  purge.

## Exit criterion

Phase 8 is complete when a clean supported system can install, configure, enable,
operate, upgrade, and remove onboardd predictably; systemd can distinguish startup,
readiness, recovery, failure, and clean shutdown; and package operations preserve
administrator configuration and user-selected network intent.
