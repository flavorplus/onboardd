# Phase 3 — Temporary AP and captive portal plumbing

Status: complete. Accepted on 2026-08-21 on Raspberry Pi Zero 2 W with Raspberry Pi OS
Trixie.

## Boundary

Phase 3 makes the provisioning state reachable from a client device. It owns the
temporary AP lifecycle, captive DNS behavior, cleartext HTTP probe handling, and
restoration of the provisioning AP after a failed infrastructure attempt.

Phase 4 supplies the setup API and product-facing web interface. Phase 3 therefore
accepts an HTTP handler as its portal content rather than embedding product UI.

## Platform plumbing

The Raspberry Pi OS Trixie baseline keeps NetworkManager's tested IPv4 `shared` mode:

- NetworkManager assigns the configured AP address;
- NetworkManager starts DHCP and forwarding DNS for clients;
- NetworkManager owns forwarding and NAT behavior;
- onboardd does not launch a competing DHCP or DNS daemon on the same interface.

On the current baseline, NetworkManager implements shared-mode DHCP/DNS with dnsmasq.
Wildcard captive DNS is supplied through a small generated
`dnsmasq-shared.d/onboardd.conf` fragment before the provisioning profile activates.
This detail stays behind the captive DNS interface because NetworkManager documents
that the shared-mode implementation may change.

The generated DNS policy resolves every hostname to the provisioning AP's IPv4 address.
It is active only for the temporary provisioning lifecycle and is removed when that
lifecycle ends. Standalone mode is not treated as captive by default.

## HTTP behavior

The captive HTTP listener uses one configured canonical URL, initially the AP address
`http://10.42.0.1/`:

- requests whose `Host` matches the canonical portal host are passed to the portal
  handler;
- every other cleartext HTTP request receives a non-cacheable `302` redirect to the
  canonical URL;
- Android, Apple, Windows, ChromeOS, and Firefox probe paths use the same behavior as
  ordinary unknown paths;
- HTTPS traffic is never intercepted with an invalid certificate;
- no request-controlled value is used as a redirect destination.

This keeps probe behavior consistent, which is important for Windows NCSI. An IP address
is deliberately used for the initial redirect target: `.local` belongs to multicast DNS
and must not be assumed to use NetworkManager's unicast DNS server. A friendly product
hostname can be added later only if it has equally deterministic client behavior.

The appliance application may already own TCP port 80 on all interfaces. onboardd
therefore listens on a private port (`18080` by default) and installs a temporary,
interface-scoped nftables prerouting redirect from `wlan0:80` to that listener. The rule
lives in the separately owned `inet onboardd_captive` table. It does not redirect traffic
arriving on another interface and is removed before the portal listener stops, so InkyPi,
Anthias, or another application retains its normal port 80 service.

Leaving captive mode and stopping HTTP are separate operations. After an accepted
infrastructure transition, onboardd can remove the redirect, temporary AP, and wildcard
DNS while keeping its `0.0.0.0:18080` service alive for progress and handoff. InkyPi then
immediately receives normal port 80 traffic on the infrastructure network. Phase 4 owns
the user-visible continuation flow between those two endpoints.

## Lifecycle ordering

```text
enter provisioning
        │
        ├── prepare wildcard DNS configuration
        ├── activate in-memory provisioning AP
        ├── confirm AP address
        ├── start private HTTP listener
        └── install interface-scoped HTTP redirect
                    │
                    ▼
              PORTAL REACHABLE
```

If HTTP or redirect startup fails, onboardd tears down the incomplete provisioning
lifecycle rather than reporting it ready.

An infrastructure attempt made from provisioning is protected by a NetworkManager
checkpoint:

```text
create checkpoint
        │
        ├── activate and validate candidate
        │          ├── success ─► finalize mode, commit checkpoint
        │          └── failure ─► rollback checkpoint
        │                              │
        └──────────────────────────────┴──► provisioning AP restored
```

The checkpoint mechanism remains behind a recovery interface so deterministic tests can
verify ordering and failure handling without changing the development machine's network.

## Initial implementation checklist

- [x] Canonical-host HTTP redirect handler and probe-path tests.
- [x] Wildcard dnsmasq fragment renderer and file lifecycle.
- [x] HTTP listener lifecycle with deterministic shutdown tests.
- [x] Provisioning AP start/stop coordinator.
- [x] Checkpoint-backed infrastructure attempt and AP restoration.
- [x] Readable debug command for the complete lifecycle.
- [x] Pi validation of DHCP, wildcard DNS, HTTP probes, and rollback.
- [x] Client checks on iOS, Android, Windows, and macOS.

The repeatable target procedure is in [Phase 3 hardware checklist](phase-3-hardware-checklist.md).

## Hardware acceptance result

The accepted target run used ARM64 binary SHA-256
`de10843c685d4a1c3d1ca59cfd50d8d59aec4dfab751e11c8b4090760d52783e`.
The captive AP, DHCP, wildcard DNS, probe redirects, private listener, and
interface-scoped nftables table all behaved as designed. Two wrong-password
infrastructure attempts restored the same provisioning UUID and `10.42.0.1` address;
a subsequent correct-password attempt committed infrastructure mode. A final
infrastructure-to-standalone-to-infrastructure round trip confirmed persistent mode
selection, superseded-profile cleanup, and foreign-profile isolation.

NetworkManager may remove a failed activation object before its stable device property
retains a specific failure reason. The current diagnostic can consequently report
`reason none (0)` after a rejected password. This does not affect checkpoint rollback or
Phase 3 acceptance; capturing the transient `StateChanged` reason remains diagnostic
housekeeping for a later reliability pass.
