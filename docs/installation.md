# Install and operate onboardd

The Debian package is the supported service installation path for Raspberry Pi OS
Trixie and other compatible Debian-family systems. It installs the binary, a hardened
systemd unit, and one dpkg-managed configuration file. A fresh package installation
does not enable or start onboardd because the product configuration and Wi-Fi passwords
must be supplied by the administrator first.

## Select and verify the package

The release directory contains one package for each supported architecture:

| `dpkg --print-architecture` | Package |
|---|---|
| `arm64` | `onboardd_VERSION_arm64.deb` |
| `amd64` | `onboardd_VERSION_amd64.deb` |

For a locally built release, create both packages on Debian, Ubuntu, or Raspberry Pi OS:

```bash
scripts/build-deb.sh v0.1.0
cd dist/v0.1.0
sha256sum --check DEBSHA256SUMS
```

Use the package matching `dpkg --print-architecture`. `DEBSHA256SUMS` verifies package
bytes only against the local checksum file; when downloading a release, obtain both
through the project's trusted release channel.

Inspect an archive without installing it:

```bash
dpkg-deb --info onboardd_0.1.0-1_arm64.deb
dpkg-deb --contents onboardd_0.1.0-1_arm64.deb
```

## Install without starting

Install the local package through APT so its declared dependencies are installed too:

```bash
sudo apt install ./onboardd_0.1.0-1_arm64.deb
```

After installation:

- `/usr/bin/onboardd` is the executable;
- `/usr/lib/systemd/system/onboardd.service` is the vendor unit;
- `/etc/onboardd/config.toml` is a dpkg conffile with mode `0640`;
- `/etc/onboardd` has mode `0750`;
- no password file has been created;
- `onboardd.service` is stopped and disabled on a clean system.

Confirm that the service has not started prematurely:

```bash
systemctl is-enabled onboardd.service
systemctl is-active onboardd.service
```

Both commands normally report `disabled` and `inactive` after a fresh installation.

## Configure the appliance

Edit the installed conffile rather than replacing it with the annotated repository
example:

```bash
sudoedit /etc/onboardd/config.toml
```

At minimum, review the product text, enabled network modes, SSID templates, Wi-Fi
interface, connectivity requirement, and optional application handoff. The complete
contract is in [Configuration](configuration.md).

Create the provisioning password as a root-only regular file. If standalone mode is
enabled, create its password too. These `install` commands are for absent files on a
fresh installation; running them again would empty an existing password file.

```bash
sudo install -o root -g root -m 0600 /dev/null /etc/onboardd/provisioning-password
sudoedit /etc/onboardd/provisioning-password

sudo install -o root -g root -m 0600 /dev/null /etc/onboardd/standalone-password
sudoedit /etc/onboardd/standalone-password
```

Each WPA password must contain 8–63 bytes, or exactly 64 hexadecimal characters. A
single trailing line ending added by the editor is ignored. Confirm ownership and
permissions without printing either secret:

```bash
sudo chown root:root \
  /etc/onboardd/provisioning-password \
  /etc/onboardd/standalone-password
sudo chmod 0600 \
  /etc/onboardd/provisioning-password \
  /etc/onboardd/standalone-password
sudo stat -c '%U %G %a %n' /etc/onboardd/*-password
```

If standalone mode is disabled, omit its file from these commands. Preview the rendered,
secret-free configuration before starting the service:

```bash
sudo onboardd debug config --config /etc/onboardd/config.toml --render
```

This validates the TOML contract and rendered templates. Operating-system resources and
password contents are checked when the service starts.

## Enable and start

Starting onboardd can change `wlan0`. If the device has no acceptable selected network,
it enters provisioning and the current Wi-Fi SSH session may disconnect. Make sure the
provisioning SSID and password are known before starting remotely.

```bash
sudo systemctl enable --now onboardd.service
```

Inspect readiness, the normalized systemd status, and structured lifecycle logs:

```bash
systemctl status --no-pager onboardd.service
systemctl show onboardd.service \
  --property=ActiveState \
  --property=SubState \
  --property=StatusText
sudo journalctl --unit=onboardd.service --boot --no-pager
curl --fail --silent --show-error http://127.0.0.1:18080/healthz
```

Substitute the configured private listener port for `18080`. A stable mode reports an
active service and health JSON with `healthy=true` and `ready=true`. JSON records named
`onboardd lifecycle` use normalized fields and contain no passwords, SSIDs, profile
UUIDs, raw D-Bus paths, or platform errors. The separate startup banner does include the
configured recovery SSID and setup URL so an administrator can find the device.

Useful service operations are:

```bash
sudo systemctl restart onboardd.service
sudo systemctl stop onboardd.service
sudo systemctl start onboardd.service
sudo systemctl disable --now onboardd.service
```

The unit waits up to 90 seconds for startup and shutdown. During shutdown, onboardd is
allowed to finish protected network rollback and remove its temporary captive resources.

## Enter recovery setup

While the service is running, ask that same process to enter temporary recovery:

```bash
sudo onboardd recover
```

`recover` uses `/run/onboardd/control.sock`; it does not start a second listener or
controller. Do not run `onboardd setup` beside the active service.

Direct setup is reserved for recovery while the service is stopped:

```bash
sudo systemctl stop onboardd.service
sudo onboardd setup --config /etc/onboardd/config.toml
```

Press Ctrl+C to stop direct setup and clean its temporary resources, then return to the
managed service with `sudo systemctl start onboardd.service`.

## Upgrade

Keep the previous `.deb` until the new release has been accepted. Back up the active
conffile before installing a version that changes configuration behavior:

```bash
sudo cp --archive \
  /etc/onboardd/config.toml \
  /etc/onboardd/config.toml.before-v0.2.0
sudo apt install ./onboardd_0.2.0-1_arm64.deb
```

dpkg preserves an administrator-modified conffile and may ask how to resolve a packaged
default that changed. Review that prompt; do not automatically replace the local file.
The package never replaces password files.

If `onboardd.service` was active, the upgrade reloads the unit and restarts it. If it was
inactive, it remains inactive. Verify the installed version and service state:

```bash
onboardd --version
dpkg-query --show --showformat='${Version}\n' onboardd
systemctl status --no-pager onboardd.service
sudo journalctl --unit=onboardd.service --boot --no-pager --lines=100
```

An upgrade does not delete, recreate, or adopt NetworkManager profiles.

## Roll back the package

Rollback is an explicit downgrade. Stop the service first so an older binary is not
started with configuration that has not yet been reviewed:

```bash
sudo systemctl stop onboardd.service
sudo cp --archive \
  /etc/onboardd/config.toml.before-v0.2.0 \
  /etc/onboardd/config.toml
sudo apt install --allow-downgrades ./onboardd_0.1.0-1_arm64.deb
sudo systemctl start onboardd.service
```

Use only a package and configuration known to be compatible. Package rollback does not
roll back administrator secrets or durable NetworkManager profiles. Confirm health and
logs using the same commands as after an upgrade.

## Remove or purge

Package operations have deliberately different ownership boundaries:

| Operation | Binary and unit | `config.toml` | Password files | NetworkManager profiles |
|---|---|---|---|---|
| Upgrade | replaced | preserved as conffile | preserved | preserved |
| `apt remove` | removed | preserved | preserved | preserved |
| `apt purge` | removed | removed by dpkg | preserved | preserved |

Ordinary removal stops onboardd cleanly and keeps the appliance configuration for a
later reinstall:

```bash
sudo apt remove onboardd
```

Purge removes the dpkg-owned conffile and systemd package state:

```bash
sudo apt purge onboardd
```

Administrator-created password files remain after purge because they were never package
files. The `/etc/onboardd` directory also remains when it contains those secrets, a logo,
backups, or other administrator files. Inspect it before deleting anything:

```bash
sudo find /etc/onboardd -maxdepth 1 -type f -printf '%M %u %g %p\n'
```

If complete credential destruction is intended, delete each known secret by its exact
configured path; do not recursively delete `/etc/onboardd` without reviewing its other
contents. Uninstall never removes NetworkManager profiles. Forget an onboardd-owned
profile through the setup UI before uninstalling if that persistent network intent is no
longer wanted.

Debian's distinction between removal and purge is documented by
[`apt(8)`](https://manpages.debian.org/trixie/apt/apt.8.en.html). Dpkg's preservation and
upgrade handling for administrator-modified conffiles is defined in the
[Debian Policy Manual](https://www.debian.org/doc/debian-policy/ap-pkg-conffiles.html).
