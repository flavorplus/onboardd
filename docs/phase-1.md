# Phase 1 — NetworkManager D-Bus proof of concept

## Local implementation

Phase 1 uses [`github.com/godbus/dbus/v5`](https://github.com/godbus/dbus) as a thin
D-Bus transport. NetworkManager concepts and D-Bus types are contained within
`internal/networkmanager`; the CLI and later state engine consume product-focused Go
types.

The adapter follows NetworkManager's official API:

- [`GetDeviceByIpIface` and `ActivateConnection`](https://networkmanager.dev/docs/api/latest/gdbus-org.freedesktop.NetworkManager.html)
- [`RequestScan` and `GetAllAccessPoints`](https://networkmanager.dev/docs/api/latest/gdbus-org.freedesktop.NetworkManager.Device.Wireless.html)
- [`AddConnection2`](https://networkmanager.dev/docs/api/latest/gdbus-org.freedesktop.NetworkManager.Settings.html)
- [`GetSettings` and Delete](https://networkmanager.dev/docs/api/latest/gdbus-org.freedesktop.NetworkManager.Settings.Connection.html)
- [`user.data`](https://networkmanager.dev/docs/api/latest/settings-user.html)

`AddConnection2` is called with an explicit persistence flag:

```text
infrastructure → to-disk
standalone     → to-disk
provisioning   → in-memory
```

Each created profile includes:

```text
onboardd.owner  = onboardd
onboardd.role   = infrastructure | standalone | provisioning
onboardd.schema = 1
```

Profile deletion refuses to touch a connection that does not contain the ownership
marker.

## Safety model

Read-only commands run without confirmation. Commands that activate a connection,
delete a profile, or roll back a checkpoint require `--yes`.

Passwords are never command-line values because process arguments may be visible to
other users and diagnostic tools. Debug commands accept `--password-file` and never
include secrets in profile output.

These protections do not make Wi-Fi changes nondisruptive. Starting an AP or joining a
network on `wlan0` tears down the existing Wi-Fi communication path. Hardware tests must
therefore use a local console, serial console, or another independent management path.

## Verification

The following are verified without the Pi:

- Go compilation, formatting, vetting, and unit tests;
- exact `a{sa{sv}}` connection-settings signatures;
- UUID, SSID, password, address, role, and priority validation;
- open and WPA Personal infrastructure profile construction;
- shared IPv4 AP profile construction;
- metadata construction;
- signal decoding;
- CLI parsing and disruptive-operation confirmation;
- Linux ARM64 cross-compilation.

The following were subsequently accepted with NetworkManager and the actual radio on a
Raspberry Pi Zero 2 W running Raspberry Pi OS Trixie:

- PolicyKit authorization;
- Zero 2 W AP-mode driver/firmware behavior;
- scan completion and reported capabilities;
- activation state and reason behavior;
- DHCP/shared-mode operation;
- checkpoint rollback behavior;
- autoconnect selection across reboot;
- temporary profile disappearance across reboot.

Those checks and their safety constraints are recorded in the dedicated hardware
checklist.
