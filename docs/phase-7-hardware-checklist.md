# Phase 7 hardware checklist

Target: Raspberry Pi Zero 2 W with Raspberry Pi OS Trixie.

Status: passed.

Accepted ARM64 binary SHA-256:
`7ea02a44a4226827229f8a07a44cd726fda36f166638196eea9bed348357c1cb`.

This is the final Phase 7 acceptance pass. It covers production boot intent, bounded
recovery, recovery inputs, Known networks, transition interruption, lifecycle logging,
Internet loss, reboot, and abrupt power loss. Phase 8 service installation is deliberately
not assumed: after a reboot, start the foreground controller again with the recorder.

The local suite now deterministically verifies that a failed `202 Accepted` write or flush
does not start a radio transition, shutdown waits for checkpoint rollback to finish, and
partial profile, DNS, or redirect cleanup retries only its unfinished steps. The final Pi
acceptance pass will interrupt a live transition while observing NetworkManager because
those failure injections cannot be reproduced safely through the browser alone.

## Prepare

Run VS Code task **Check: all**, then build with **Go: build Pi Zero 2 W**. Transfer the
binary, recorder, and Phase 7 configuration:

```bash
scp bin/onboardd-linux-arm64 admin@photodisplay:/home/admin/onboardd
scp scripts/setup-recorder.sh admin@photodisplay:/home/admin/setup-recorder.sh
scp config/phase-7.toml admin@photodisplay:/home/admin/onboardd-phase-7.toml
ssh admin@photodisplay 'chmod +x ~/onboardd ~/setup-recorder.sh'
```

Install the configuration on the Pi:

```bash
sudo install -d -o root -g root -m 0755 /etc/onboardd
sudo install -o root -g root -m 0644 \
  ~/onboardd-phase-7.toml /etc/onboardd/config.toml
```

The referenced password files must already contain WPA passwords of at least eight
characters. If they do not exist, create them with `sudoedit`, then restrict access:

```bash
sudoedit /etc/onboardd/provisioning-password
sudoedit /etc/onboardd/standalone-password
sudo chmod 0600 \
  /etc/onboardd/provisioning-password /etc/onboardd/standalone-password
sudo ~/onboardd debug config --config /etc/onboardd/config.toml --render
```

Start with GPIO disabled and a configuration that already has a working selected
infrastructure or standalone profile:

```bash
~/setup-recorder.sh run /etc/onboardd/config.toml
~/setup-recorder.sh status
~/setup-recorder.sh snapshot initial-production
```

Expected: the working production mode remains active, the private setup URL responds,
the control socket is mode `600`, and no provisioning SSID appears merely because the
process started. Snapshot commands intentionally record failed probes too: an absent
`onboardd_captive` table or dnsmasq fragment is expected in production mode.

## Clean reboot and durable mode intent

Test infrastructure first:

```bash
~/setup-recorder.sh snapshot infrastructure-before-reboot
~/setup-recorder.sh stop
sudo reboot
```

Reconnect after boot, then remember that automatic service startup belongs to Phase 8:

```bash
~/setup-recorder.sh run /etc/onboardd/config.toml
~/setup-recorder.sh snapshot infrastructure-after-reboot
```

Expected: the snapshot boot IDs differ, the same committed infrastructure intent becomes
ready, no profile says `PENDING=true`, and provisioning does not appear merely because
`onboardd` restarted.

Use the UI to select standalone, then repeat the stop, reboot, run, and snapshot sequence
with `standalone-before-reboot` and `standalone-after-reboot` labels. Expected: standalone
remains the selected autoconnect mode and infrastructure profiles are not selected behind
its back. Return to infrastructure before continuing unless a later step says otherwise.

## Lifecycle logs and health

While `onboardd run` is active, query the private listener directly:

```bash
curl -fsS http://127.0.0.1:18080/healthz
```

Expected in a stable production mode: HTTP `200`, `status` is `ready`, and both
`healthy` and `ready` are `true`. The response may contain normalized stage, mode,
reason, and sequence values; it must not contain an SSID, profile UUID, password,
configuration path, raw error, or D-Bus object path.

The recorder combines stdout and stderr, so select the JSON lifecycle records before
inspecting them:

```bash
grep '^{' ~/onboardd-setup.log
```

Expected: each selected line is a complete JSON object with `time`, `level`, `msg`, and
`event`. Trigger manual recovery and complete one transition. Confirm that the records
show normalized network-state, provisioning-action, and recovery-requested events, but
do not contain either Wi-Fi password or `/org/freedesktop/NetworkManager`.

During a deliberately injected bounded listener or observer retry, `/healthz` remains
HTTP `200` with `healthy=true`, `ready=false`, and `status=recovering`. A successful
rebind or stable reconciled state returns it to `ready`. Phase 8 will connect this same
in-process signal to systemd watchdog notifications; no service-manager behavior is
claimed by Phase 7 itself.

## Internet-loss policy

First run the ordinary `local` requirement on infrastructure and disconnect only the
router's upstream Internet connection. Leave its Wi-Fi radio and LAN running. Request a
fresh NetworkManager result if necessary:

```bash
sudo nmcli networking connectivity check
~/setup-recorder.sh snapshot local-requirement-without-internet
```

Expected: local addressing remains sufficient, the controller stays in infrastructure,
and no provisioning SSID appears. Restore the upstream connection.

Now restart the controller with the Internet requirement:

```bash
~/setup-recorder.sh stop
~/setup-recorder.sh run /etc/onboardd/config.toml --requirement internet
~/setup-recorder.sh snapshot internet-requirement-ready
```

Disconnect only the upstream Internet connection again and request a fresh connectivity
check. After the configured grace period, join the provisioning SSID and reconnect SSH to
the Pi if necessary, then capture:

```bash
~/setup-recorder.sh snapshot internet-requirement-recovery
```

Expected: health is stable and ready in `provisioning`, not stuck in `recovering`; exactly
one temporary provisioning profile, the owned nftables table, and the dnsmasq fragment
exist. Restore the upstream connection and use the portal to select the same infrastructure
network again. It must pass the Internet requirement and return to production cleanly.

## Manual recovery

Request recovery without starting a second onboardd process:

```bash
~/setup-recorder.sh recover
```

The command should report `manual recovery requested`. The Wi-Fi SSH session may then
disconnect as the radio changes. Join the configured provisioning SSID, open the portal,
and complete a transition back to infrastructure or standalone. After reconnecting SSH:

```bash
~/setup-recorder.sh status
sudo ~/onboardd debug profiles
```

Expected: the original `run` PID survived, exactly one temporary provisioning profile
was used, successful setup removed it, and the selected production profile has the
expected autoconnect state. Repeating `recover` while recovery is already pending must
not create a second temporary profile.

## Known networks

Before opening the UI, record the profile list:

```bash
sudo ~/onboardd debug profiles
```

Open the stable setup URL in a normal browser and choose **Manage known networks**.
Confirm that infrastructure profiles for `wlan0` appear, the current profile says
**In use**, and unmanaged/netplan profiles say **Read only**. Standalone and temporary
provisioning profiles must not appear.

Choose **Connect** on an inactive onboardd-owned profile and confirm the saved-password
transition. Expected: no password is requested, the ordinary applying/reconnect screen is
used, connectivity policy is checked, the selected UUID becomes active, and the previous
exact profile is restored if the attempt fails. The target saved profile must not be
deleted by a failed attempt. No Connect action is offered for the active or unmanaged
profiles.

Choose an inactive onboardd-owned infrastructure profile, press **Forget**, verify the
confirmation copy, then select **Forget network**. Run the profile command again.
Expected: only that UUID disappeared; the active, unmanaged, standalone, and provisioning
profiles are unchanged, and the existing connection remains usable. Also confirm that no
Forget action is offered for an unmanaged or active profile.

## Optional GPIO recovery

Power down before wiring. On a Pi Zero 2 W, connect a normally-open momentary button
between physical pin 11 (BCM GPIO17) and a ground pin such as physical pin 9. Never
connect the input to 5 V. Confirm the relevant character device exists:

```bash
ls -l /dev/gpiochip*
```

Enable the adapter:

```toml
[recovery.gpio]
enabled = true
chip = "/dev/gpiochip0"
line = 17
```

Restart the recorded `run` process. A short press must leave the production connection
untouched. A continuous press of at least three seconds must activate the same recovery
SSID as the manual command. Release the button before testing another hold. Complete the
portal transition, then verify that `run` remains active and the GPIO line is released
after a graceful stop:

```bash
~/setup-recorder.sh stop
test ! -e /run/onboardd/control.sock
```

Reference: the adapter follows the Linux kernel's
[GPIO v2 character-device API](https://docs.kernel.org/userspace-api/gpio/chardev.html).
Raspberry Pi's [GPIO documentation](https://www.raspberrypi.com/documentation/computers/raspberry-pi.html#gpio-and-the-40-pin-header)
describes the 3.3 V input, pull-up, permissions, and header numbering constraints.

## Graceful interruption during a protected transition

Enter manual recovery and open the full setup page. Start a connection to a known-good
infrastructure network. Immediately run the recorder's stop command from the SSH session
on the provisioning network:

```bash
~/setup-recorder.sh stop
```

The Wi-Fi SSH session may disappear after the signal is delivered. The recorder now waits
up to 75 seconds because shutdown may legitimately spend the restoration window rolling
back and confirming the exact previous profile. Reconnect on the restored network and run:

```bash
~/setup-recorder.sh snapshot after-graceful-interruption
```

Expected: `onboardd` is stopped, the exact previous profile is active, no profile is
pending, and the control socket, owned nftables table, provisioning profile, and dnsmasq
fragment are absent. Start `run` again and confirm it becomes ready without manual profile
repair.

## Abrupt process-loss recovery

This test intentionally bypasses graceful cleanup but kills only the test `onboardd`
process. Start `run`, enter manual recovery, and begin a known-good infrastructure
transition. Immediately terminate the recorded PID:

```bash
sudo kill -KILL "$(cat ~/onboardd-setup.pid)"
```

Wait at least the configured checkpoint rollback duration. Reconnect to whichever prior
network NetworkManager restored, then start the recorder again. Its private listener binds
before cold-start cleanup, preventing a second live process from winning a cleanup race:

```bash
~/setup-recorder.sh run /etc/onboardd/config.toml
~/setup-recorder.sh snapshot after-sigkill-recovery
```

Expected: startup removes the old owned redirect, dnsmasq fragment, provisioning profile,
and disk-backed `PENDING=true` candidate before reconciling. The resulting stable mode has
no pending profiles or duplicate provisioning profiles. Unmanaged and committed profiles
must be unchanged.

## Abrupt power-loss recovery

Use a backed-up test SD card and stop unrelated write-heavy applications first. Abrupt
power removal can corrupt any Raspberry Pi filesystem; this is the one acceptance step
that deliberately carries that risk. Capture and flush the precondition:

```bash
~/setup-recorder.sh snapshot before-power-loss
```

Enter manual recovery, begin a known-good infrastructure transition, and remove power
while the browser still says it is applying the configuration. Restore power. Because
Phase 8 has not installed a service yet, NetworkManager alone should activate the prior
durable production intent; reconnect there and explicitly start onboardd:

```bash
~/setup-recorder.sh run /etc/onboardd/config.toml
~/setup-recorder.sh snapshot after-power-loss
```

Expected: the boot ID changed; the appliance becomes reachable without editing a profile;
there is no pending candidate or duplicate provisioning profile; and captive DNS/redirect
artifacts match the final mode rather than the interrupted one. Unmanaged profile UUIDs,
autoconnect policy, and ownership metadata must remain unchanged.

## Acceptance evidence

After all tests, stop cleanly and retain the combined evidence log:

```bash
~/setup-recorder.sh snapshot final-ready
~/setup-recorder.sh stop
~/setup-recorder.sh snapshot final-stopped
sha256sum ~/onboardd ~/onboardd-setup.log
```

Phase 7 can close when every expected result above is confirmed, lifecycle JSON contains
no password or raw D-Bus path, and neither graceful interruption nor abrupt loss requires
manual NetworkManager profile repair.
