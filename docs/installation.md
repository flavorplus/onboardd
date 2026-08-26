# Installation and operation

## Host requirements

- Debian 13 / Raspberry Pi OS Trixie;
- ARM64 or AMD64;
- NetworkManager managing the selected Wi-Fi interface;
- Avahi daemon;
- systemd;
- nftables and NetworkManager shared-mode dnsmasq support.

Confirm the architecture with `dpkg --print-architecture` and choose the matching
package.

## Verify and install

Extract the release archive and verify the package checksum:

```bash
tar -xf onboardd-v0.1.0.tar
cd v0.1.0
grep '_arm64.deb$' DEBSHA256SUMS | sha256sum --check -
sudo apt install ./onboardd_0.1.0-1_arm64.deb
```

A fresh install places:

```text
/usr/bin/onboardd
/usr/lib/systemd/system/onboardd.service
/etc/onboardd/config.toml
```

The service is installed disabled and inactive. It is never started before the
administrator supplies configuration and password files.

## Configure

Edit `/etc/onboardd/config.toml`; see [Configuration](configuration.md). Create the
referenced access-point password files without exposing their contents in shell
arguments:

```bash
sudo install -d -m 0750 -o root -g root /etc/onboardd
sudo install -m 0600 -o root -g root /dev/null /etc/onboardd/admin-password
sudo install -m 0600 -o root -g root /dev/null /etc/onboardd/provisioning-password
sudo install -m 0600 -o root -g root /dev/null /etc/onboardd/standalone-password
sudoedit /etc/onboardd/admin-password
sudoedit /etc/onboardd/provisioning-password
sudoedit /etc/onboardd/standalone-password
```

Use a unique 12–256 byte administrator password and 8–63 character WPA passphrases.
Keep one trailing newline at most. Validate file ownership and mode:

```bash
stat -c '%U %G %a %n' /etc/onboardd /etc/onboardd/*
```

The directory should be `root:root 750`, the TOML conffile `root:root 640`, and password
files `root:root 600`.

The setup frontend is public static content, but every API endpoint other than login
requires the administrator session. Login uses a styled in-app form rather than the
browser's native Basic Authentication prompt. The session ends when the browser closes
or onboardd restarts. Because captive setup uses HTTP, this protects against casual
unauthorized changes on a shared LAN but does not encrypt credentials in transit.

## Start and recover

```bash
sudo systemctl enable --now onboardd.service
systemctl status onboardd.service
journalctl -u onboardd.service -f
```

The service reconciles the selected durable profile. If none is usable it creates the
temporary setup network automatically. To request setup manually while the controller
is running:

```bash
sudo onboardd recover
```

The request goes through `/run/onboardd/control.sock`; do not start a second onboardd
process. Stop the appliance with `sudo systemctl stop onboardd.service`.

Useful independent network diagnostics are:

```bash
nmcli device status
nmcli connection show
sudo nft list ruleset
```

## Upgrade and rollback

Before upgrading an already running pre-authentication installation, create
`/etc/onboardd/admin-password` with mode `0600`. The default applies even when an older
TOML conffile does not yet contain `portal.password_file`; without the file, the
restarted service deliberately fails closed.

Install a newer package with `apt install ./PACKAGE.deb`. If the service was active,
the package reloads systemd and restarts it; an intentionally inactive service remains
inactive. `/etc/onboardd/config.toml` follows normal Debian conffile rules and
administrator-created password files are never replaced.

Rollback uses the same command with the older package:

```bash
sudo apt install --allow-downgrades ./onboardd_OLD_arm64.deb
```

NetworkManager profiles are host state and are not package-owned, so upgrades and
rollbacks preserve them.

## Remove or purge

```bash
sudo apt remove onboardd
```

Removal stops an active service, removes the executable and unit, and preserves the
configuration conffile, password files, and NetworkManager profiles.

```bash
sudo apt purge onboardd
```

Purge additionally removes the package-owned TOML conffile and systemd enablement
state. Administrator-created password files deliberately remain; remove them manually
only when their secrets are no longer needed. The package never deletes durable or
foreign NetworkManager profiles.

Normal stop/removal cleans the control socket, onboardd dnsmasq fragment, captive
nftables table, and temporary provisioning profile. Startup cleanup handles the same
resources after an interrupted process.

## Service security boundary

The packaged unit runs as root because NetworkManager profile changes and captive
nftables rules require host authority. It applies a narrow capability bounding set,
read-only system protection, private devices/tmp, restricted address families and
namespaces, and a single writable NetworkManager dnsmasq-fragment directory.
