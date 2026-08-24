# Phase 6 hardware checklist

Target: Raspberry Pi Zero 2 W with Raspberry Pi OS Trixie.

Status: passed.

Accepted ARM64 binary SHA-256:
`6abae6aaeac1e2d93bb2fbd8a223e609bc8a13652a520dd2b008fb52a4279fed`.

This run verifies that the normal-browser setup session survives radio transitions,
the stable `.local` name follows the device, application readiness is enforced, and
standalone credentials follow product policy.

## 1. Build and transfer

Run VS Code task **Check: all**, then run these commands from the repository root:

```bash
shasum -a 256 bin/onboardd-linux-arm64
scp bin/onboardd-linux-arm64 admin@photodisplay:~/onboardd
scp scripts/setup-recorder.sh admin@photodisplay:~/setup-recorder.sh
scp integration/phase-6/*.toml admin@photodisplay:~/
```

On the Pi:

```bash
sha256sum ~/onboardd
chmod +x ~/onboardd ~/setup-recorder.sh
sudo mkdir -p /etc/onboardd
sudo cp ~/handoff-visible.toml /etc/onboardd/handoff-visible.toml
sudo cp ~/handoff-private.toml /etc/onboardd/handoff-private.toml
sudo chmod 0644 /etc/onboardd/handoff-visible.toml /etc/onboardd/handoff-private.toml
sudo chmod 0600 /etc/onboardd/provisioning-password /etc/onboardd/standalone-password
```

The local and Pi binary hashes must match.

## 2. Check prerequisites without changing Wi-Fi

```bash
systemctl is-active avahi-daemon
AVAHI_HOSTNAME=$(busctl call org.freedesktop.Avahi / org.freedesktop.Avahi.Server GetHostName | cut -d'"' -f2)
echo "$AVAHI_HOSTNAME"
curl -fsS http://127.0.0.1/ >/dev/null && echo "application health: ready"
sudo ~/onboardd debug config --config /etc/onboardd/handoff-visible.toml --render
sudo ~/onboardd debug config --config /etc/onboardd/handoff-private.toml --render
```

Avahi must report `active` and return its current hostname, the existing InkyPi HTTP
service must pass the health request, and both configuration previews must validate.
When setup starts, onboardd must print `http://$AVAHI_HOSTNAME.local:18080/` as its setup
URL. Stop here if any prerequisite fails.

## 3. Persistent handoff, infrastructure, and visible credentials

Start the first run:

```bash
ONBOARDD_LOG=~/onboardd-phase6-visible.log \
ONBOARDD_PID_FILE=~/onboardd-phase6-visible.pid \
~/setup-recorder.sh start /etc/onboardd/handoff-visible.toml
```

1. Join the rendered `Onboardd-Setup-…` Wi-Fi.
2. Open `http://10.42.0.1/`. Confirm that public port 80 shows only the compact
   **Continue in your browser** handoff page, not the complete setup UI.
3. Click **Open device setup**. If the captive viewer blocks a new browser, enter the
   displayed URL manually. Continue only in the normal browser tab at
   `http://$AVAHI_HOSTNAME.local:18080/`; it must not show another browser-handoff panel.
4. Connect to a valid infrastructure Wi-Fi. Do not reopen the page by IP address.
5. Confirm the same normal-browser tab reconnects at the same `.local` URL without a
   manual refresh, reports success, and presents **Open InkyPi**.
6. Confirm `http://$AVAHI_HOSTNAME.local/` opens the existing InkyPi application.
7. Select **Change connection** and choose standalone mode. Before applying the change,
   confirm the page shows the final SSID, password, **Copy password**, and **Join Wi-Fi**
   QR. Scan the QR with another phone if available and verify its decoded values.
8. Apply standalone mode, join the displayed Wi-Fi, and confirm the unchanged browser
   tab reconnects without a manual refresh. The completion page may repeat the Wi-Fi
   join details but must not contain an application/destination QR.

After reconnecting SSH to the Pi, inspect the run and stop it gracefully:

```bash
ONBOARDD_LOG=~/onboardd-phase6-visible.log \
ONBOARDD_PID_FILE=~/onboardd-phase6-visible.pid \
~/setup-recorder.sh status
ONBOARDD_LOG=~/onboardd-phase6-visible.log \
ONBOARDD_PID_FILE=~/onboardd-phase6-visible.pid \
~/setup-recorder.sh show
ONBOARDD_LOG=~/onboardd-phase6-visible.log \
ONBOARDD_PID_FILE=~/onboardd-phase6-visible.pid \
~/setup-recorder.sh stop
```

## 4. Unhealthy application and hidden credentials

```bash
ONBOARDD_LOG=~/onboardd-phase6-private.log \
ONBOARDD_PID_FILE=~/onboardd-phase6-private.pid \
~/setup-recorder.sh start /etc/onboardd/handoff-private.toml
```

Open setup through the captive page as before, then choose standalone mode. Confirm:

- the confirmation page shows the SSID but not the password, copy action, or Wi-Fi QR;
- the stable normal-browser page reconnects and reports standalone success;
- completion still withholds the password and Wi-Fi QR;
- the unavailable application link remains hidden and the page continues to report
  that the application is starting.

Stop this run with the same three recorder commands, changing both environment values
from `visible` to `private`.

## 5. Cleanup and evidence

```bash
sudo ~/onboardd debug profiles
sudo nft list tables
sudo ss -ltnp '( sport = :18080 )'
hostnamectl --static
AVAHI_HOSTNAME=$(busctl call org.freedesktop.Avahi / org.freedesktop.Avahi.Server GetHostName | cut -d'"' -f2)
echo "$AVAHI_HOSTNAME"
avahi-resolve-host-name "$AVAHI_HOSTNAME.local"
avahi-browse -rt _http._tcp | grep 'Onboardd setup' || true
```

After the recorder stops, onboardd must no longer own port 18080, its temporary
provisioning profile and captive nftables table must be gone, and the **Onboardd setup**
service must no longer be advertised. The Avahi hostname must be unchanged and continue
to resolve because it belongs to the host, not onboardd. The Pi's static hostname and
unrelated NetworkManager profiles must also remain unchanged.

- [x] One local ARM64 build matches the Pi binary hash.
- [x] onboardd preserves Avahi's hostname and publishes its setup service on provisioning Wi-Fi.
- [x] The normal-browser session survives infrastructure and standalone transitions.
- [x] A healthy application link appears and opens the application.
- [x] An unhealthy application remains unavailable without blocking setup.
- [x] Enabled credential policy shows correct pre-transition data, copy action, and Wi-Fi QR.
- [x] Disabled credential policy withholds the password and Wi-Fi QR.
- [x] Both runs shut down cleanly and remove only temporary resources.
