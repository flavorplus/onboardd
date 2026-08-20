# Phase 1 hardware checklist

Target: Raspberry Pi Zero 2 W with Raspberry Pi OS Trixie.

Status: accepted on 2026-08-20.

The target run validated status inspection, scanning, profile metadata and ownership,
provisioning and standalone AP operation, infrastructure connections, persistence,
repeated profile replacement, and checkpoint commit/rollback behavior. Provisioning
remained a single in-memory profile after repeated starts.

Do not run the disruptive section through an SSH session that uses `wlan0`; activating
another profile will disconnect that session. Use a local keyboard/display, serial
console, or another independent recovery path.

## 1. Record the baseline

Capture:

```bash
uname -a
uname -m
cat /etc/os-release
NetworkManager --version
ip -brief link
```

Record the image architecture, kernel, firmware package versions, NetworkManager
version, Wi-Fi interface name, and whether the device is managed.

## 2. Build and transfer

In VS Code run **Go: build Pi Zero 2 W**, or locally:

```bash
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags=-buildid= -o bin/onboardd-linux-arm64 ./cmd/onboardd
```

Before testing, compare `shasum -a 256 bin/onboardd-linux-arm64` on the development
machine with `sha256sum ~/onboardd` on the Pi. The hashes must match.

Transfer the binary to the Pi and make it executable. If the Trixie image is 32-bit,
record that finding and produce a `GOARCH=arm` build before continuing.

## 3. Read-only checks

Run:

```bash
./onboardd debug status --interface wlan0
./onboardd debug profiles
./onboardd debug scan --interface wlan0 --wait 10s
./onboardd debug watch
```

Confirm:

- the NetworkManager version and device state are sensible;
- profiles contain no secrets;
- access points are ordered by strength;
- hidden SSIDs do not cause decoding failures;
- scanning produces property-change events;
- an unprivileged call either works through PolicyKit or fails with an actionable error.

## 4. Prepare a protected test password file

Create a temporary file without putting the password in shell history:

```bash
umask 077
read -rsp "Temporary test password: " ONBOARDD_TEST_PASSWORD
printf '%s' "$ONBOARDD_TEST_PASSWORD" > /tmp/onboardd-test-password
unset ONBOARDD_TEST_PASSWORD
```

Remove this file after testing.

## 5. Checkpoint behavior

Create a checkpoint before a disruptive transition:

```bash
./onboardd debug checkpoint-create --interface wlan0 --rollback-after 90s
```

Record its returned object path. Verify both paths separately:

- allow one test checkpoint to roll back automatically;
- create another and commit it with `checkpoint-commit --path ... --yes`;
- create another and explicitly run `checkpoint-rollback --path ... --yes`.

Record per-device rollback result codes.

## 6. Provisioning AP

From the independent console:

```bash
./onboardd debug provisioning-start \
  --interface wlan0 \
  --ssid Onboardd-Setup-Test \
  --password-file /tmp/onboardd-test-password \
  --yes
```

Confirm another device can see and join the 2.4 GHz AP, receives an address in the
configured subnet, and can reach the Pi. Confirm `debug profiles --owned` reports:

```text
role=provisioning
storage=memory
autoconnect=false
```

Run `provisioning-start` a second time and confirm there is still exactly one owned
provisioning profile for `wlan0`. The successful replacement must remove the superseded
profile without touching foreign, standalone, or other-interface profiles.

After reboot, confirm the provisioning profile no longer exists.

## 7. Standalone AP

Run the equivalent `standalone-start` command. Confirm:

```text
role=standalone
storage=disk
autoconnect=true
priority=999
```

Reboot and verify NetworkManager activates the standalone AP again. Record any conflict
with pre-existing unmanaged autoconnect profiles.

## 8. Infrastructure connection

Create a checkpoint, then run:

```bash
./onboardd debug connect \
  --interface wlan0 \
  --ssid YOUR_TEST_SSID \
  --password-file /tmp/onboardd-test-password \
  --yes
```

Confirm successful activation, IP configuration, metadata, persistence, and reconnect
after reboot. Repeat with a deliberately wrong password and verify the failure is
reported without printing the password.

Connect to the same SSID again and confirm onboardd keeps exactly one owned
infrastructure profile for that SSID and interface. Profiles for other SSIDs must remain.

## 9. Ownership guard and cleanup

Attempting to delete an unmanaged profile must be refused. Delete only test profiles
whose `debug profiles --owned` output shows `owned=true`:

```bash
./onboardd debug profile-delete --uuid TEST_PROFILE_UUID --yes
```

Remove `/tmp/onboardd-test-password` when complete.

## Acceptance

Phase 1 was accepted after the target Pi reliably completed:

```text
status → scan → provisioning AP → infrastructure → standalone AP → infrastructure
```

and the reboot, ownership, checkpoint, and failure checks above are recorded.
