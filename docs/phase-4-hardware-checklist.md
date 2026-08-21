# Phase 4 hardware checklist

Target: Raspberry Pi Zero 2 W with Raspberry Pi OS Trixie.

Status: accepted on 2026-08-21.

This run validates the product-facing setup API and browser interface on top of the
accepted Phase 3 captive network. Use `scripts/phase-4-recorder.sh` when SSH depends on
`wlan0`; the setup process and operation history remain alive while the radio changes.

## 1. Build and transfer

Run VS Code task **Check: all**. Transfer these three artifacts to the Pi:

- `bin/onboardd-linux-arm64` as `~/onboardd`;
- the contents of `frontend/dist/` as `~/onboardd-frontend/`;
- `scripts/phase-4-recorder.sh` as `~/phase-4-recorder.sh`.

Copy the built directory contents into the Pi target directory without adding an
extra `dist/` level:

```bash
ssh admin@photodisplay 'mkdir -p ~/onboardd-frontend'
scp -r frontend/dist/. admin@photodisplay:~/onboardd-frontend/
```

Then verify the binary and prepare the files:

```bash
# Development machine
shasum -a 256 bin/onboardd-linux-arm64

# Raspberry Pi
sha256sum ~/onboardd
chmod +x ~/onboardd ~/phase-4-recorder.sh
test -f ~/onboardd-frontend/index.html
! grep -Fq '/src/main.ts' ~/onboardd-frontend/index.html
```

The last command must produce no output and succeed; `/src/main.ts` identifies the
development entry page, which cannot run on the Pi without Vite. The binary hashes must
match. Prepare provisioning, standalone, correct Wi-Fi, and deliberately incorrect
Wi-Fi password files without putting their values on the command line.

## 2. Start interactive setup

While SSH is still available:

```bash
~/phase-4-recorder.sh start \
  --ssid Onboardd-Setup-Test \
  --password-file /tmp/onboardd-test-password \
  --standalone-ssid Onboardd-Standalone-Test \
  --standalone-password-file /tmp/onboardd-test-password \
  --requirement local \
  --yes
```

Join `Onboardd-Setup-Test` and open `http://10.42.0.1/`. Confirm that:

- the page offers **Wi-Fi network** and **Standalone** in ordinary product language;
- it works in a narrow captive window and a normal browser;
- keyboard focus is visible and all controls can be reached without a pointer;
- no NetworkManager, D-Bus, profile UUID, or password appears in the page.

## 3. Network list and input paths

Choose **Wi-Fi network**. Confirm visible networks are strongest-first, duplicate APs
are collapsed, and open versus protected networks are clear. Use **Scan again**.

Open **Enter a hidden network** and confirm the exact SSID and security can be entered,
then go back without starting a transition. Select a protected visible network and
confirm the browser requires an 8–63 character password before submitting.

## 4. Failed connection and browser recovery

Submit the real test SSID with the deliberately incorrect password. Expected behavior:

1. the page immediately shows connection progress;
2. the provisioning AP disappears while NetworkManager tries the candidate;
3. the same provisioning AP and address return after rejection;
4. after rejoining, the open page resumes polling and shows a safe **Try again** message;
5. the rejected owned profile is removed and no password or backend detail is shown.

Refresh the page after restoration. The failed operation must still be displayed, and
**Choose a network** must return to the scan flow.

## 5. Successful infrastructure mode

Retry with the correct password. The Pi must join the selected network, persist the new
owned infrastructure profile with autoconnect enabled, disable standalone intent, and
remove provisioning DNS, redirect, and profile resources.

Find the Pi's address on the selected network and open its retained Phase 4 listener:

```text
http://PI_ADDRESS:18080/
```

The page must show **The device is connected**. Select **Change connection** and confirm
that scanning and mutation requests still work from this direct listener address. Phase
6 will replace this manual address step with the configured application handoff.

## 6. Successful standalone mode and later change

From **Change connection**, choose **Standalone** and confirm the warning before the
switch. Join `Onboardd-Standalone-Test` and open:

```text
http://10.42.0.1:18080/
```

The operating system may close its captive mini-browser when the provisioning AP
disappears; this is expected and is not an operation failure. Open the retained listener
in a normal browser after joining the standalone network. The page must show
**Standalone mode is ready**. Confirm the standalone profile is the only onboardd mode
with autoconnect enabled:

```bash
sudo ~/onboardd debug profiles --owned
```

Use **Change connection** once more and connect to the known-good infrastructure
network. Confirm infrastructure becomes durable again and standalone autoconnect is
disabled. Foreign profiles must remain untouched throughout.

Repeat that change once with an incorrect password while a valid foreign infrastructure
profile is still eligible for autoconnect. The operation must fail and explicitly return
to the same standalone UUID and `10.42.0.1` address; the foreign profile must not replace
the recovery target and must not be modified or deleted.

## 7. Capability switches

Stop and restart setup twice to verify product policy. With
`--standalone-enabled=false`, the page must go directly to the Wi-Fi list and never
offer standalone. With `--network-enabled=false`, it must go directly to standalone
confirmation and never expose scanning or network credentials.

The command must reject a start where both switches are false before changing the
network.

## 8. Clean shutdown and evidence

Reconnect to the Pi and run:

```bash
~/phase-4-recorder.sh stop
~/phase-4-recorder.sh show
sudo ~/onboardd debug profiles --owned
```

Keep `~/onboardd-phase4.log` as the acceptance record. Shutdown must stop the private
listener and remove only remaining temporary resources; the selected production profile
must stay durable.

## Acceptance

- [x] Both product modes can be selected without networking implementation language.
- [x] Visible, open, protected, and hidden network paths behave correctly.
- [x] Wrong credentials restore setup and a refreshed browser shows the safe failure.
- [x] Correct credentials complete infrastructure mode and remove captive resources.
- [x] Standalone completes and remains locally reachable.
- [x] Later changes work from infrastructure and standalone modes.
- [x] A rejected change from standalone restores its exact UUID despite competing foreign autoconnect.
- [x] Network-only and standalone-only policy switches remove the disallowed choice.
- [x] Existing port 80 application and foreign NetworkManager profiles remain untouched.
