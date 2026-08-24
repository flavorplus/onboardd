# Phase 6 — Application handoff

Status: complete.

## Boundary

Phase 6 keeps the setup experience observable when changing the only Wi-Fi radio tears
down the captive assistant. The user explicitly opens a stable normal-browser setup URL
before the change. That browser keeps the same origin while mDNS maps the host name
to whichever provisioning, standalone, or infrastructure address is currently active.

The phase also leads the user from network setup to the configured product application.
It does not make the product application part of onboardd, proxy arbitrary application
traffic, or depend on an automatically opened browser window.

## Configuration and browser contract

The first slice adds one compact `[handoff]` section. The setup hostname is deliberately
absent because it belongs to the host and is read from Avahi over D-Bus:

- `application_label` and `application_url` are an optional pair.
- `health_check_url` is optional server-side readiness policy and requires an
  application URL.
- `show_standalone_credentials` defaults to false and controls whether the standalone
  password and Wi-Fi join QR are presented before and after the mode switch.

Public port 80 serves a dedicated entry from the same compiled frontend rather than the
setup application.
The complete browser UI lives on the stable `.local` origin and private listener port.
The handoff page makes a user-activated best-effort attempt to open that origin in a
normal browser and always presents the address as a manual fallback. Captive assistants
remain free to block new windows; the design does not pretend they can be forced to
launch a system browser. Once opened normally, the UI hides the redundant handoff panel.

## Implementation slices

1. Define, render, validate, and document handoff configuration.
2. Expose the browser-safe handoff model and add the pre-transition setup link and
   completion application link to the UI.
3. Read the host-managed Avahi hostname and register the setup service through mDNS on
   every active network.
4. Run the optional bounded health check before presenting the application as ready.
5. Add pre-transition standalone Wi-Fi details and a join QR when product policy
   permits it, with plain-text and copy fallbacks.
6. Verify captive-to-browser continuity, infrastructure discovery, standalone handoff,
   health failures, and credential policy on Raspberry Pi hardware.

All six slices are complete. The local simulator includes the stable setup and
application links so UI work remains testable on macOS. Production setup uses Avahi's
D-Bus API rather than running a second mDNS stack. It reads the existing Avahi hostname,
uses that value for templates and the stable setup URL, and publishes the private
listener as `_http._tcp`. It never calls `SetHostName`. Avahi then follows address
changes across provisioning, standalone, and infrastructure links without onboardd
publishing stale address records itself.

`avahi-daemon` must be installed, enabled, permitted on the selected interfaces, and new
enough to receive the distribution's security updates. A missing daemon, a D-Bus error,
an invalid host-managed name, or a service collision aborts startup and removes the
temporary provisioning resources rather than showing an unusable stable URL.

When `health_check_url` is configured, the server performs a two-second bounded HTTP
GET. Only a 2xx response marks the application ready. The private health endpoint and
the application destination remain absent from the browser response while the check is
unhealthy, unreachable, or timed out. The completion screen keeps checking in the
background and adds the configured application link when readiness succeeds. Without a
health URL, the configured application is ready immediately.

The captive landing markup and styles live in the frontend build beside the main setup
application. Go serves the built page and injects only the already-validated setup URL,
so the public landing and full UI share one visual source of truth.

The standalone SSID is available before the transition so the user knows which network
will replace provisioning. When `show_standalone_credentials` is true, the server also
returns the password and the UI presents a copy action plus a Wi-Fi join QR before and
after the switch. No application QR is generated; completion uses ordinary application
links. The completion page omits a redundant setup link because it is already running at
the stable setup origin. The password remains absent from the browser API when policy is
false.

All frontend requests have a five-second deadline. A request caught on the old radio
path is aborted so the operation loop can retry after mDNS resolves the same stable
origin on the new link.

The repeatable target procedure is in the
[Phase 6 hardware checklist](phase-6-hardware-checklist.md).

Hardware acceptance passed on the Raspberry Pi Zero 2 W using ARM64 binary SHA-256
`6abae6aaeac1e2d93bb2fbd8a223e609bc8a13652a520dd2b008fb52a4279fed`.
