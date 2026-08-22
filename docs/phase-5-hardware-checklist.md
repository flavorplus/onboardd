# Phase 5 hardware checklist

Target: Raspberry Pi Zero 2 W with Raspberry Pi OS Trixie.

Status: accepted on 2026-08-22.

This run proves that one unchanged binary serves its embedded frontend with two distinct
product configurations. It is intentionally shorter than the accepted Phase 4 network
matrix: the network transitions themselves have not changed.

## 1. Build and transfer

Run VS Code task **Check: all**, then copy the binary and acceptance fixtures:

```bash
shasum -a 256 bin/onboardd-linux-arm64
scp bin/onboardd-linux-arm64 admin@photodisplay:~/onboardd
scp integration/phase-5/* admin@photodisplay:~/
```

No `frontend` directory is transferred. On the Pi:

```bash
sha256sum ~/onboardd
chmod +x ~/onboardd
sudo mkdir -p /etc/onboardd
sudo cp ~/anthias.toml /etc/onboardd/anthias.toml
sudo cp ~/inkypi.toml /etc/onboardd/inkypi.toml
sudo cp ~/anthias-acceptance.svg /etc/onboardd/anthias-acceptance.svg
sudo cp ~/inkypi-acceptance.svg /etc/onboardd/inkypi-acceptance.svg
sudo chmod 0644 /etc/onboardd/*.toml /etc/onboardd/*.svg
```

Create `/etc/onboardd/provisioning-password` and
`/etc/onboardd/standalone-password` with WPA passwords of at least eight characters,
then protect them:

```bash
sudo chmod 0600 /etc/onboardd/provisioning-password /etc/onboardd/standalone-password
```

The local and Pi binary hashes must match.

## 2. Inspect rendered identities without changing Wi-Fi

```bash
sudo ~/onboardd debug config --config /etc/onboardd/anthias.toml --render
sudo ~/onboardd debug config --config /etc/onboardd/inkypi.toml --render
```

Record the rendered SSIDs. Both configurations must use the same stable eight-character
device suffix, while their product names, device names, text, colors, logos, and SSID
prefixes differ.

## 3. Anthias configuration

```bash
sudo ~/onboardd setup --config /etc/onboardd/anthias.toml
```

Join the rendered `Anthias-Setup-…` SSID and open `http://10.42.0.1/`. Confirm the
Anthias acceptance mark, magenta palette, Lobby player name, and Anthias text. Complete
either infrastructure or standalone once and confirm the result page.

Changing the only Wi-Fi interface normally disconnects the SSH terminal while onboardd
continues running. After reconnecting, locate and gracefully stop that specific process:

```bash
sudo pgrep -af '/home/admin/onboardd setup'
sudo kill -TERM PID_FROM_THE_PREVIOUS_COMMAND
sudo ss -ltnp '( sport = :18080 )'
```

The final command must show that onboardd no longer owns the private listener. Do not
use `kill -9`, because it bypasses temporary-resource cleanup.

## 4. InkyPi configuration

Run the exact same binary with only the configuration changed:

```bash
sudo ~/onboardd setup --config /etc/onboardd/inkypi.toml
```

Join the rendered `InkyPi-Setup-…` SSID. Confirm the distinct acceptance mark, blue
palette, Kitchen display name, and InkyPi text. Complete the other operating mode, then
stop the command with the same graceful procedure after reconnecting.

## 5. Evidence and acceptance

Confirm after each stop that no provisioning profile, captive nftables table, DNS
fragment, or private listener remains. Existing foreign profiles and the selected
durable production profile must remain untouched.

- [x] One local ARM64 build matches the Pi binary hash.
- [x] Neither run requires an adjacent frontend directory.
- [x] Anthias text, colors, logo, and rendered SSIDs are correct.
- [x] InkyPi text, colors, logo, and rendered SSIDs are correct.
- [x] The device suffix is stable across both configurations.
- [x] Infrastructure and standalone each complete once.
- [x] Both commands shut down cleanly and remove only temporary resources.
