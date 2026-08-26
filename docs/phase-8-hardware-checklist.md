# Phase 8 Raspberry Pi package and systemd checklist

Run this checklist on a clean Raspberry Pi Zero 2 W with 64-bit Raspberry Pi OS Trixie.
It accepts the Debian package lifecycle and service-manager boundary; Phase 7 already
accepted the underlying network transitions. Use a wired, serial, or local console when
possible because provisioning and watchdog recovery can interrupt Wi-Fi SSH.

Do not run the removal steps on an appliance that contains NetworkManager profiles or
configuration you are not prepared to preserve and inspect. The checklist deliberately
never deletes a NetworkManager profile or an administrator-created password.

## Build two inspected packages

On a Debian, Ubuntu, or Raspberry Pi OS build host with the checked-out source, build two
versions from the same commit. The second version exercises upgrade while the first is
retained for rollback:

```bash
scripts/build-deb.sh v0.1.0-phase8.1
scripts/build-deb.sh v0.1.0-phase8.2
```

Each command automatically runs `scripts/check-deb.sh`. It must report that the AMD64
and ARM64 packages passed structural and isolated lifecycle checks. Confirm that a
second build to another directory is byte-identical:

```bash
phase8_first=$(mktemp -d)
phase8_second=$(mktemp -d)
scripts/build-deb.sh v0.1.0-phase8.2 "$phase8_first"
scripts/build-deb.sh v0.1.0-phase8.2 "$phase8_second"
diff -u \
  "$phase8_first/v0.1.0-phase8.2/SHA256SUMS" \
  "$phase8_second/v0.1.0-phase8.2/SHA256SUMS"
diff -u \
  "$phase8_first/v0.1.0-phase8.2/DEBSHA256SUMS" \
  "$phase8_second/v0.1.0-phase8.2/DEBSHA256SUMS"
cmp \
  "$phase8_first/v0.1.0-phase8.2/onboardd_0.1.0~phase8.2-1_arm64.deb" \
  "$phase8_second/v0.1.0-phase8.2/onboardd_0.1.0~phase8.2-1_arm64.deb"
```

GitHub Actions performs the same two-build comparison for both architectures. A tag or
manual workflow run also produces a 14-day inspectable artifact; it does not publish a
GitHub Release.

Copy the ARM64 packages, checksum files, and acceptance configuration to the clean Pi:

```bash
scp \
  dist/v0.1.0-phase8.1/onboardd_0.1.0~phase8.1-1_arm64.deb \
  admin@photodisplay.local:/home/admin/
scp \
  dist/v0.1.0-phase8.1/DEBSHA256SUMS \
  admin@photodisplay.local:/home/admin/DEBSHA256SUMS.phase8.1
scp \
  dist/v0.1.0-phase8.2/onboardd_0.1.0~phase8.2-1_arm64.deb \
  admin@photodisplay.local:/home/admin/
scp \
  dist/v0.1.0-phase8.2/DEBSHA256SUMS \
  admin@photodisplay.local:/home/admin/DEBSHA256SUMS.phase8.2
scp config/phase-8.toml admin@photodisplay.local:/home/admin/
```

On the Pi, verify the copied ARM64 files against the separately named manifests:

```bash
grep 'phase8.1-1_arm64.deb$' DEBSHA256SUMS.phase8.1 | sha256sum --check -
grep 'phase8.2-1_arm64.deb$' DEBSHA256SUMS.phase8.2 | sha256sum --check -
```

## Capture the clean host baseline

On the Pi, record the operating-system and service-manager facts:

```bash
cat /etc/os-release
uname -a
dpkg --print-architecture
NetworkManager --version
systemctl --version
dpkg-deb --version
systemctl is-active NetworkManager.service
systemctl is-active avahi-daemon.service
```

Expected: Trixie, `arm64`, and active NetworkManager and Avahi services. Confirm that no
onboardd package or vendor unit is already installed:

```bash
test ! -e /usr/bin/onboardd
test ! -e /usr/lib/systemd/system/onboardd.service
test ! -e /etc/onboardd/config.toml
! dpkg-query --show onboardd
```

Record the durable profile names and UUIDs before package operations:

```bash
nmcli --terse --fields UUID,NAME connection show \
  | LC_ALL=C sort >~/phase8-profiles-clean.txt
```

## Fresh install stays inactive

Install the first package through APT:

```bash
sudo apt install ./onboardd_0.1.0~phase8.1-1_arm64.deb
```

Confirm its version, layout, permissions, and inactive first-install policy:

```bash
onboardd --version
dpkg-query --show --showformat='${Version}\n' onboardd
stat -c '%U %G %a %n' \
  /usr/bin/onboardd \
  /usr/lib/systemd/system/onboardd.service \
  /etc/onboardd \
  /etc/onboardd/config.toml
systemctl is-enabled onboardd.service
systemctl is-active onboardd.service
test ! -e /etc/onboardd/provisioning-password
test ! -e /etc/onboardd/standalone-password
sudo systemd-analyze verify /usr/lib/systemd/system/onboardd.service
```

Expected:

- `onboardd v0.1.0-phase8.1` and Debian version `0.1.0~phase8.1-1`;
- binary mode `755`, unit mode `644`, config directory mode `750`, conffile mode `640`;
- `disabled` and `inactive` on this clean installation;
- no package-created password files;
- no unit verification errors.

The package may create `/etc/NetworkManager/dnsmasq-shared.d`, but it must not create a
system-connection profile or activate a provisioning SSID merely by being installed.

## Configure and start the service

Install the acceptance conffile as an administrator modification:

```bash
sudo install -o root -g root -m 0640 \
  ~/phase-8.toml /etc/onboardd/config.toml
```

Create fresh test passwords. These commands truncate existing files, which is acceptable
only on this clean test image:

```bash
sudo install -o root -g root -m 0600 \
  /dev/null /etc/onboardd/provisioning-password
sudo install -o root -g root -m 0600 \
  /dev/null /etc/onboardd/standalone-password
sudoedit /etc/onboardd/provisioning-password
sudoedit /etc/onboardd/standalone-password
sudo onboardd debug config --config /etc/onboardd/config.toml --render
```

Both passwords must have 8–63 characters. Start the managed service:

```bash
sudo systemctl enable --now onboardd.service
```

Because this is a `Type=notify` unit, the command should return only after onboardd has
reached a stable infrastructure, standalone, or provisioning state. Inspect the service:

```bash
systemctl status --no-pager onboardd.service
systemctl show onboardd.service \
  --property=MainPID \
  --property=ActiveState \
  --property=SubState \
  --property=StatusText \
  --property=WatchdogUSec \
  --property=WatchdogTimestampMonotonic
curl --fail --silent --show-error http://127.0.0.1:18080/healthz
sudo journalctl --unit=onboardd.service --boot --no-pager --lines=100
sudo stat -c '%a %U %G %n' /run/onboardd /run/onboardd/control.sock
```

Expected: active/running, a nonzero main PID, `WatchdogUSec=30s`, a normalized status,
health with `healthy=true` and `ready=true`, runtime directory mode `700`, and socket mode
`600`. The JSON lifecycle records contain no password, SSID, UUID, raw D-Bus path, or
platform error. The separate startup banner may show the recovery SSID and setup URL.

If the clean device entered provisioning, complete one connection through the setup UI
before continuing. Select a known-good infrastructure network or standalone mode and
confirm that `/healthz` is ready in that persistent non-provisioning mode. The remaining
restart, upgrade, rollback, and package-removal comparisons assume no temporary
provisioning profile is active.

Reboot once and repeat the status, health, journal, and runtime-permission checks. The
service must start automatically and preserve the selected durable network mode.

## Watchdog restart

Use a wired, serial, or local session for this test. Record the current PID and restart
count, then suspend the daemon so it cannot send its watchdog heartbeat:

```bash
phase8_old_pid=$(systemctl show --property=MainPID --value onboardd.service)
phase8_old_restarts=$(systemctl show --property=NRestarts --value onboardd.service)
sudo kill -STOP "$phase8_old_pid"
```

Wait for the 30-second watchdog and five-second restart delay. Poll until the service has
a different nonzero PID:

```bash
for phase8_attempt in $(seq 1 18); do
  sleep 5
  phase8_new_pid=$(systemctl show --property=MainPID --value onboardd.service)
  if [ "$phase8_new_pid" -gt 0 ] && [ "$phase8_new_pid" != "$phase8_old_pid" ]; then
    break
  fi
done
printf 'old PID=%s new PID=%s old restarts=%s new restarts=%s\n' \
  "$phase8_old_pid" \
  "$phase8_new_pid" \
  "$phase8_old_restarts" \
  "$(systemctl show --property=NRestarts --value onboardd.service)"
```

Expected: systemd reports a watchdog failure in the journal, kills the suspended process,
and starts a new one. The new process becomes ready, `/healthz` recovers, the selected
network mode remains usable, and cold-start cleanup leaves no stale owned captive table,
DNS fragment, provisioning profile, or pending candidate.

```bash
systemctl status --no-pager onboardd.service
curl --fail --silent --show-error http://127.0.0.1:18080/healthz
sudo journalctl --unit=onboardd.service --boot --no-pager --lines=150
sudo nft list table inet onboardd_captive
sudo test ! -e /etc/NetworkManager/dnsmasq-shared.d/onboardd.conf
```

The nft command is expected to report that the table does not exist in a stable
non-provisioning mode.

## Active upgrade preserves administrator state

Create root-only comparison copies and capture the profile list before upgrade:

```bash
sudo install -d -o root -g root -m 0700 /root/onboardd-phase8-backup
sudo cp --archive \
  /etc/onboardd/config.toml \
  /etc/onboardd/provisioning-password \
  /etc/onboardd/standalone-password \
  /root/onboardd-phase8-backup/
nmcli --terse --fields UUID,NAME connection show \
  | LC_ALL=C sort >~/phase8-profiles-before-upgrade.txt
phase8_upgrade_old_pid=$(systemctl show --property=MainPID --value onboardd.service)
```

Install the second package while the service is active:

```bash
sudo apt install ./onboardd_0.1.0~phase8.2-1_arm64.deb
```

Confirm that the active service restarted, the conffile and passwords are byte-identical,
and no profile changed:

```bash
test "$(systemctl is-active onboardd.service)" = active
test "$(systemctl show --property=MainPID --value onboardd.service)" \
  != "$phase8_upgrade_old_pid"
test "$(onboardd --version)" = 'onboardd v0.1.0-phase8.2'
sudo cmp /etc/onboardd/config.toml /root/onboardd-phase8-backup/config.toml
sudo cmp \
  /etc/onboardd/provisioning-password \
  /root/onboardd-phase8-backup/provisioning-password
sudo cmp \
  /etc/onboardd/standalone-password \
  /root/onboardd-phase8-backup/standalone-password
nmcli --terse --fields UUID,NAME connection show \
  | LC_ALL=C sort >~/phase8-profiles-after-upgrade.txt
diff -u ~/phase8-profiles-before-upgrade.txt ~/phase8-profiles-after-upgrade.txt
curl --fail --silent --show-error http://127.0.0.1:18080/healthz
```

## Inactive rollback stays inactive

Stop the service and explicitly downgrade to the retained first package:

```bash
sudo systemctl stop onboardd.service
sudo apt install --allow-downgrades ./onboardd_0.1.0~phase8.1-1_arm64.deb
```

Expected: package version `0.1.0~phase8.1-1`, service inactive, and unchanged conffile,
passwords, and profiles. The downgrade must not start a deliberately stopped service.

```bash
test "$(systemctl is-active onboardd.service)" = inactive
test "$(onboardd --version)" = 'onboardd v0.1.0-phase8.1'
sudo cmp /etc/onboardd/config.toml /root/onboardd-phase8-backup/config.toml
sudo cmp \
  /etc/onboardd/provisioning-password \
  /root/onboardd-phase8-backup/provisioning-password
sudo cmp \
  /etc/onboardd/standalone-password \
  /root/onboardd-phase8-backup/standalone-password
nmcli --terse --fields UUID,NAME connection show \
  | LC_ALL=C sort >~/phase8-profiles-after-rollback.txt
diff -u ~/phase8-profiles-before-upgrade.txt ~/phase8-profiles-after-rollback.txt
sudo systemctl start onboardd.service
```

## Remove, reinstall, and purge

Capture profiles once more, then remove the running package:

```bash
nmcli --terse --fields UUID,NAME connection show \
  | LC_ALL=C sort >~/phase8-profiles-before-remove.txt
sudo apt remove onboardd
```

Expected: graceful service stop, binary and vendor unit removed, and the modified
conffile, both passwords, and every NetworkManager profile preserved.

```bash
test ! -e /usr/bin/onboardd
test ! -e /usr/lib/systemd/system/onboardd.service
test -f /etc/onboardd/config.toml
test -f /etc/onboardd/provisioning-password
test -f /etc/onboardd/standalone-password
nmcli --terse --fields UUID,NAME connection show \
  | LC_ALL=C sort >~/phase8-profiles-after-remove.txt
diff -u ~/phase8-profiles-before-remove.txt ~/phase8-profiles-after-remove.txt
```

Reinstall the second package. The prior conffile and secrets must remain, and postinst
must not start an inactive service during this install:

```bash
sudo apt install ./onboardd_0.1.0~phase8.2-1_arm64.deb
test "$(systemctl is-active onboardd.service)" = inactive
sudo cmp /etc/onboardd/config.toml /root/onboardd-phase8-backup/config.toml
sudo cmp \
  /etc/onboardd/provisioning-password \
  /root/onboardd-phase8-backup/provisioning-password
sudo cmp \
  /etc/onboardd/standalone-password \
  /root/onboardd-phase8-backup/standalone-password
```

Disable any preserved boot enablement and purge:

```bash
sudo systemctl disable --now onboardd.service
sudo apt purge onboardd
```

Expected: package files and dpkg conffile removed, administrator-created passwords and
all NetworkManager profiles preserved:

```bash
test ! -e /usr/bin/onboardd
test ! -e /usr/lib/systemd/system/onboardd.service
test ! -e /etc/onboardd/config.toml
test -f /etc/onboardd/provisioning-password
test -f /etc/onboardd/standalone-password
nmcli --terse --fields UUID,NAME connection show \
  | LC_ALL=C sort >~/phase8-profiles-after-purge.txt
diff -u ~/phase8-profiles-before-remove.txt ~/phase8-profiles-after-purge.txt
sudo test ! -e /etc/NetworkManager/dnsmasq-shared.d/onboardd.conf
sudo nft list table inet onboardd_captive
```

Again, the absent nftables table is the expected stable result. `/etc/onboardd` remains
because it contains administrator secrets. Do not delete it as part of acceptance.

## Acceptance record

Phase 8 can close after recording:

- the Pi model, Trixie version, architecture, kernel, NetworkManager, systemd, Go, Node,
  and dpkg versions used to build and test;
- the two package SHA-256 values and successful automated inspection output;
- fresh-install inactive/disabled evidence and installed modes;
- initial and post-reboot readiness, status, health, and journal evidence;
- watchdog old/new PIDs, restart count, and recovered health;
- active-upgrade and inactive-rollback package versions and PIDs;
- byte-identical conffile/password comparisons across upgrade and rollback;
- unchanged profile-list diffs across upgrade, rollback, remove, and purge;
- final absence of package-owned captive resources after purge.

Do not include password contents. Once these checks pass, update `docs/phase-8.md` and
`docs/roadmap.md` with the accepted date, target facts, package hashes, and concise
evidence summary.
