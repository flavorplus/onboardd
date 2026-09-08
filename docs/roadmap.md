# Roadmap

## Completed foundation

Phases 0–8 established and hardware-tested the complete appliance path:

- architecture, configuration contracts, repository, and VS Code workflow;
- direct NetworkManager D-Bus integration and profile metadata;
- deterministic reconciliation without a separate persistent state file;
- temporary access point, captive DNS/HTTP, and scoped nftables redirect;
- setup API and embedded TypeScript UI;
- administrator-authenticated setup API sessions;
- product branding, templates, and application handoff;
- checkpoint-protected transitions, exact-profile rollback, manual recovery, known
  network management, watchdog integration, and interrupted-start cleanup;
- reproducible ARM64/AMD64 releases, Debian packaging, hardened systemd service, and
  verified install/upgrade/rollback/remove/purge behavior.

Development runs on a Raspberry Pi Zero 2 W and Raspberry Pi 4 with Raspberry Pi
OS/Debian Trixie exercised the whole path. That is not the same as the formal candidate
validation Phase 9 covers, where every row of the support matrix is still untested. The detailed historical phase checklists were removed
after acceptance; durable behavior is documented in Architecture, Configuration,
Development, and Installation.

## Phase 9 — Hardware validation and v1.0

Validate the release candidate across:

- Raspberry Pi Zero 2 W and Raspberry Pi 4;
- WPA2/WPA3 Personal, open and hidden networks, and multiple saved profiles;
- incorrect credentials, slow DHCP/DNS, local-only networks, and Internet loss;
- repeated infrastructure/standalone/recovery transitions;
- reboot, power interruption, listener restart, and stale-resource cleanup;
- iOS, Android, macOS, and Windows captive-portal behavior;
- Anthias and InkyPi reference configurations;
- fresh install, upgrade, rollback, removal, purge, and reproducible release artifacts.

Exit criteria:

- a documented support matrix;
- no unresolved data-loss or permanent-inaccessibility failure;
- a repeatable release checklist;
- signed-off v1.0 packages and checksums.

The support matrices, scenarios, evidence requirements, and sign-off record live in
the durable [release-validation checklist](release.md).

## Captive release to a normal browser

Setup must continue in a normal browser, because the captive viewer dies with the
provisioning access point while a real browser tab survives the radio transition and
can reach the stable mDNS URL again. Today the `/landing` view can only ask the user to
carry the URL across by hand; the viewer itself is never released.

Commercial venue portals do not open the real browser. They stop being captive: the
portal authorizes the client, the next platform probe succeeds, and the operating
system dismisses the viewer on its own. The equivalent for onboardd is to sequence the
probe answers described under [Captive viewers](architecture.md#captive-viewers):

1. redirect the probes while the `/landing` view still needs to be found;
2. answer each probe with its exact expected response once the user has chosen to
   continue in a browser;
3. keep answering it for as long as provisioning is active, so the platform does not
   later decide the setup network is unusable and roam back to a known network
   mid-setup.

Both the dnsmasq fragment and the nftables table are already owned by
`internal/captive`, so this stays inside one package and is testable with `httptest`.

Two constraints are settled and should not be rediscovered:

- The historical iOS behavior where a plain link inside the captive viewer opened
  Safari is deprecated. Handing off by link is not a supported mechanism on current
  iOS, so releasing the viewer is the only route to the real browser.
- RFC 8910 (DHCP option 114) with an RFC 8908 `application/captive+json` endpoint is
  the standards-based form of the same release, and its `venue-info-url` is the one
  field platforms open in the real browser. RFC 8908 requires the API endpoint to be
  reachable over HTTPS with a validated certificate, and requires TLS for
  `user-portal-url`. The provisioning network is deliberately plain HTTP with no
  trusted certificate for `10.42.0.1`, so option 114 can only ever be a best-effort
  addition for lenient clients, never the primary path.

## Deferred beyond v1

- enterprise Wi-Fi;
- BLE, DPP, or platform-native credential provisioning;
- cloud and fleet management;
- VPN/router features;
- simultaneous multi-radio operation;
- adoption or deletion of foreign NetworkManager profiles.
