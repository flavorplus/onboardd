# Release validation

This is the durable release gate for onboardd. Run it against the exact package built
from the proposed release commit; do not substitute a locally rebuilt binary during
hardware validation.

## Release decision

A candidate may be released when:

- CI quality and reproducible-package jobs pass for the release commit;
- every required row in the support matrix has a recorded result;
- no open issue can delete foreign profiles, lose a previously working owned profile,
  or leave the device without automatic or manual recovery;
- ARM64 and AMD64 binaries and Debian packages match their published SHA-256 files;
- installation, upgrade, downgrade, removal, and purge behavior pass on the candidate;
- user-facing limitations are documented.

Use `PASS`, `FAIL`, `LIMITED`, or `NOT TESTED`. `LIMITED` is acceptable only when the
limitation is understood, documented, and does not violate the recovery guarantees.

## Candidate identity

Record these values before testing:

| Field | Value |
| --- | --- |
| Version | Pending |
| Git commit | Pending |
| GitHub Actions run | Pending |
| ARM64 Debian SHA-256 | Pending |
| AMD64 Debian SHA-256 | Pending |
| Test started | Pending |
| Test completed | Pending |

Run the GitHub Actions workflow manually from the release commit and enter a candidate
version such as `v1.0.0-rc.1`. The package builder validates the version before doing
any release work. Download its single release archive and verify both manifests before
use:

```bash
tar -xf onboardd-v1.0.0-rc.1.tar.gz
cd v1.0.0-rc.1
sha256sum --check SHA256SUMS
sha256sum --check DEBSHA256SUMS
```

## Platform support matrix

| Platform | OS | Architecture | Result | Evidence or limitation |
| --- | --- | --- | --- | --- |
| Raspberry Pi Zero 2 W | Raspberry Pi OS Trixie | arm64 | NOT TESTED | |
| Raspberry Pi 4 | Raspberry Pi OS or Debian Trixie | arm64 | NOT TESTED | |
| Debian Trixie VM or host | Debian Trixie | amd64 | NOT TESTED | D-Bus/package lifecycle; no Wi-Fi claim without hardware |

For every host, record the exact model, kernel, NetworkManager, systemd, Avahi, and
installed onboardd version:

```bash
if test -r /proc/device-tree/model; then
  printf 'Model: '
  tr -d '\0' </proc/device-tree/model
  printf '\n'
else
  echo 'Model: not exposed by this host'
fi
uname -a
dpkg --print-architecture
NetworkManager --version
systemctl --version | head -n 1
avahi-daemon --version
onboardd --version
dpkg-query --show --showformat='${Status} ${Version}\n' onboardd
```

## Network compatibility matrix

Run the rows supported by the available access point. Record the access-point model
and security configuration with the result.

| Network | Zero 2 W | Pi 4 | Required for v1 | Notes |
| --- | --- | --- | --- | --- |
| WPA2 Personal, visible SSID | NOT TESTED | NOT TESTED | Yes | New connection and saved-profile activation |
| WPA2 Personal, hidden SSID | NOT TESTED | NOT TESTED | Yes | Manual SSID entry |
| Open network | NOT TESTED | NOT TESTED | Yes | No password sent or stored |
| WPA2/WPA3 transition mode | NOT TESTED | NOT TESTED | Yes | Must work with the current protected-network flow |
| WPA3 Personal only | NOT TESTED | NOT TESTED | To determine | Current code does not explicitly select SAE |
| Local network without Internet | NOT TESTED | NOT TESTED | Yes | Passes `requirement = "local"` |
| Internet loss | NOT TESTED | NOT TESTED | Yes | Behavior follows configured requirement |
| Multiple saved profiles | NOT TESTED | NOT TESTED | Yes | Exact-profile rollback remains deterministic |

Enterprise Wi-Fi, router/VPN use, simultaneous radios, and management of foreign
profiles are outside the v1 scope.

## Core appliance scenarios

Run these on both Raspberry Pi models unless a row explicitly says otherwise. Perform
user choices through the setup UI; use `nmcli` only to prepare a failure or independently
inspect NetworkManager state.

| ID | Scenario | Expected result |
| --- | --- | --- |
| C01 | Boot with a selected, usable infrastructure profile | Service becomes ready without creating provisioning |
| C02 | Request `sudo onboardd recover` | Provisioning appears and the browser setup remains reachable |
| C03 | Enter an incorrect infrastructure password | Exact previous profile returns; candidate is removed |
| C04 | Connect to a new valid infrastructure network | Candidate becomes durable; provisioning resources disappear |
| C05 | Select standalone mode | Standalone AP persists and is selected for autoconnect |
| C06 | Activate a known owned infrastructure profile | Protected transition succeeds without asking for its password |
| C07 | Forget an inactive owned profile | Only that profile is deleted |
| C08 | Inspect a foreign profile in the UI | It remains read-only and is never rewritten or deleted |
| C09 | Delete the active infrastructure profile externally | Reconciliation enters provisioning when no usable selected mode remains |
| C10 | Reboot in infrastructure, standalone, and provisioning recovery | Each state reconciles to its intended result |
| C11 | Repeat infrastructure → standalone → infrastructure five times | No duplicate owned profiles or stale captive resources accumulate |
| C12 | Open setup without a session, then log in | Every API route first returns `401`; the correct admin password unlocks the UI |
| C13 | Try an incorrect admin password | Login fails without creating a session; the correct password still works afterward |
| C14 | Restart onboardd while setup is open | The old session is rejected and the UI asks for the admin password again |

After each scenario, retain these credential-free observations:

```bash
systemctl status --no-pager onboardd.service
journalctl -u onboardd.service --since '-10 minutes' --no-pager
nmcli --fields NAME,UUID,TYPE,AUTOCONNECT connection show
sudo nft list tables
test -e /etc/NetworkManager/dnsmasq-shared.d/onboardd.conf \
  && echo 'captive DNS present' || echo 'captive DNS absent'
```

The `onboardd_captive` nftables table and dnsmasq fragment must exist only during
provisioning. Evidence must not contain passwords.

## Failure and lifecycle scenarios

| ID | Scenario | Expected result |
| --- | --- | --- |
| F01 | Wrong password twice, then correct password | Both failures recover; final attempt succeeds |
| F02 | Slow or failed DHCP/DNS | Grace window is bounded; recovery remains possible |
| F03 | Kill the daemon during provisioning, then let systemd restart it | Startup removes stale owned resources and recreates one clean session |
| F04 | Interrupt power during a protected transition | Next boot has a usable prior mode or provisioning recovery |
| F05 | Stop and restart NetworkManager | Controller recovers or systemd restarts it without profile loss |
| F06 | Make the private HTTP listener unavailable | Bounded listener restart policy is visible in the journal |
| F07 | Stop, start, and reboot the service | Control socket, readiness, watchdog, and cleanup remain correct |

Run the package lifecycle on at least one ARM64 Pi and the AMD64 environment:

1. fresh install remains disabled and inactive;
2. enabling starts the configured service;
3. upgrade restarts only an already-active service;
4. downgrade restores the older binary and preserves configuration, admin/access-point secrets, and
   profiles;
5. removal preserves configuration, secrets, and profiles;
6. reinstall restores the service without changing retained files;
7. purge removes the package conffile and retains administrator-created passwords and
   NetworkManager profiles.

## Captive-client matrix

| Client | Captive landing | Normal browser setup | Reconnect and completion | Result |
| --- | --- | --- | --- | --- |
| iOS / iPhone | NOT TESTED | NOT TESTED | NOT TESTED | NOT TESTED |
| Android | NOT TESTED | NOT TESTED | NOT TESTED | NOT TESTED |
| macOS | NOT TESTED | NOT TESTED | NOT TESTED | NOT TESTED |
| Windows | NOT TESTED | NOT TESTED | NOT TESTED | NOT TESTED |

For each client, verify the captive viewer offers the browser handoff, the stable mDNS
setup page survives the Wi-Fi transition, operation polling recovers after a stalled
request, and the optional application link is shown only when its health check passes.

## Product configurations

Validate both [Anthias](../config/anthias.toml) and [InkyPi](../config/inkypi.toml):

- rendered product/device text and colors;
- deterministic device-specific provisioning and standalone SSIDs;
- application handoff and health gating;
- standalone credential policy;
- infrastructure and standalone capability switches.

## Sign-off

Record every failure as an issue linked to the exact candidate and evidence. After all
release blockers are resolved, build a new candidate and repeat affected scenarios;
never sign off a package that differs from the tested artifact.

| Role | Name | Candidate | Date | Decision |
| --- | --- | --- | --- | --- |
| Maintainer | Pending | Pending | Pending | Pending |
| Hardware validation | Pending | Pending | Pending | Pending |
