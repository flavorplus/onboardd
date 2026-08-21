# Phase 3 hardware checklist

Target: Raspberry Pi Zero 2 W with Raspberry Pi OS Trixie.

Status: accepted on 2026-08-21.

This run validates the complete temporary provisioning path: NetworkManager shared-mode
DHCP, wildcard DNS, interface-scoped HTTP redirection, captive probes, clean shutdown,
and checkpoint-backed recovery. The appliance's existing port 80 application must remain
running throughout the test.

Use `scripts/phase-3-recorder.sh` when SSH depends on `wlan0`; it keeps the portal alive
after that SSH session disconnects and records the binary hash, commands, and output.

## 1. Build identity and test setup

Run VS Code task **Go: build Pi Zero 2 W**, transfer the binary and recorder, then compare:

```bash
# Development machine
shasum -a 256 bin/onboardd-linux-arm64

# Raspberry Pi
sha256sum ~/onboardd
chmod +x ~/onboardd ~/phase-3-recorder.sh
```

The hashes must match. Prepare two password files: a valid AP password and deliberately
incorrect infrastructure credentials. Do not overwrite the known-good Wi-Fi password
file used to recover the Pi.

Confirm nftables is available before starting:

```bash
nft --version
```

## 2. Start the complete captive lifecycle

While SSH is still available:

```bash
~/phase-3-recorder.sh start \
  --ssid Onboardd-Setup-Test \
  --password-file /tmp/onboardd-test-password \
  --yes
```

Wait for the SSH session to disconnect, join `Onboardd-Setup-Test` from a client, and
open `http://10.42.0.1/`. The page must say that the Phase 3 portal is reachable.

Confirm the log contains `captive provisioning is ready`, one in-memory provisioning
UUID, and `Portal: http://10.42.0.1/`.

On the Pi, confirm onboardd created only its own redirect table while the product app
continues listening on port 80:

```bash
sudo nft list table inet onboardd_captive
sudo ss -H -ltnp '( sport = :80 or sport = :18080 )'
```

The rule must match only `iifname "wlan0"`, public port 80 must still belong to the
product application, and onboardd must listen on `0.0.0.0:18080`.

## 3. DHCP and wildcard DNS

On a joined laptop or phone, confirm it receives an address in `10.42.0.0/24` with the
Pi as gateway and DNS server. From a laptop, record equivalent checks for:

```bash
nslookup example.com 10.42.0.1
nslookup www.msftconnecttest.com 10.42.0.1
```

Both A queries must resolve to `10.42.0.1`. An AAAA query must finish locally without
being forwarded to an upstream resolver; an empty/NODATA answer is acceptable.

## 4. Captive probe behavior

From a joined laptop, run:

```bash
curl -i --max-redirs 0 http://connectivitycheck.gstatic.com/generate_204
curl -i --max-redirs 0 http://captive.apple.com/hotspot-detect.html
curl -i --max-redirs 0 http://www.msftconnecttest.com/connecttest.txt
curl -i --max-redirs 0 http://detectportal.firefox.com/canonical.html
```

Every request must return `302 Found`, `Location: http://10.42.0.1/`, and no-cache
headers. Direct `http://10.42.0.1/` must return the portal page. HTTPS certificate
warnings are outside the design: onboardd deliberately does not intercept HTTPS.

## 5. Client captive-window checks

Join the AP normally on each available platform and record whether its captive setup
window opens and reaches the portal:

- iOS or iPadOS;
- Android;
- Windows;
- macOS.

A platform that does not open a mini-browser automatically must still reach the portal
when the user opens `http://10.42.0.1/` manually. Record OS versions with the result.

## 6. Failed infrastructure attempt and restoration

Read the provisioning UUID from the recorder log. After reconnecting to the Pi through
an independent interface if necessary, launch a deliberately bad candidate through the
same recorder:

```bash
~/phase-3-recorder.sh run debug connect-protected \
  --interface wlan0 \
  --ssid YOUR_REAL_TEST_SSID \
  --password-file /tmp/onboardd-wrong-wifi-password \
  --requirement local \
  --provisioning-uuid PROVISIONING_UUID_FROM_LOG \
  --provisioning-address 10.42.0.1 \
  --wait 30s \
  --rollback-after 90s \
  --restoration-wait 30s \
  --yes
```

The AP will disappear temporarily. It must return with the same SSID, provisioning UUID,
and `10.42.0.1` address. Rejoin it and confirm the existing captive portal process is
reachable again. The rejected infrastructure profile must no longer appear in
`debug profiles --owned`.

## 7. Clean shutdown

Reconnect to the Pi and run:

```bash
~/phase-3-recorder.sh stop
sudo ~/onboardd debug profiles --owned
test ! -e /etc/NetworkManager/dnsmasq-shared.d/onboardd.conf
sudo nft list table inet onboardd_captive
```

The stop message must report that temporary resources were removed. The provisioning
profile, DNS fragment, and nftables table must be gone; the final `nft list` command is
expected to report that the table does not exist. A persistent standalone or
infrastructure profile and the application port 80 listener must remain untouched.

## Acceptance

Phase 3 acceptance results:

- [x] DHCP and wildcard DNS work on the Zero 2 W.
- [x] All cleartext probes consistently reach the portal.
- [x] Available iOS, Android, Windows, and macOS clients were checked.
- [x] Two rejected infrastructure attempts restored the same usable AP and portal.
- [x] A valid infrastructure attempt committed and removed provisioning cleanly.
- [x] Normal shutdown removed only Phase 3 temporary resources.
- [x] Standalone and return-to-infrastructure mode transitions preserved the expected
  autoconnect intent and left foreign profiles untouched.
