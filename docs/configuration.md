# Configuration contract

TOML is the human-edited configuration format. It supports comments and typed values
without indentation-sensitive nesting. The repository's Taplo configuration connects
TOML files to `config/schema.json` so VS Code can provide completion and validation.

## Precedence

Configuration is resolved in the following order, with later sources overriding earlier
ones:

```text
built-in defaults
        ↓
/etc/onboardd/config.toml
        ↓
ONBOARDD_* environment variables
        ↓
CLI options
```

Command-line flags are intended for operational overrides. Passwords and other secrets
must not be accepted as normal CLI values because process arguments are often visible to
other users and diagnostic tools.

During development, inspect the fully resolved, secret-free configuration with:

```bash
go run ./cmd/onboardd debug config --config config/example.toml
```

Add environment or CLI overrides to that command to verify their final values without
opening D-Bus or changing the computer's network.

## Contract files

- `config/example.toml` is the annotated reference configuration.
- `config/schema.json` is the machine-readable structural contract.
- Product-specific examples will later live under `integration/<product>/`.

Configuration files overlay the built-in defaults, so only `schema_version` is required
in every file. The annotated example shows the complete contract. Unknown keys are
rejected so a typo cannot silently fall back to a default.

The schema validates structure and basic formats. Runtime validation must additionally
check operating-system facts such as whether an interface exists, a password file can be
read securely, and a configured logo can be decoded.

`branding.primary_color` drives buttons, focus rings, status accents, borders, and
supporting shades. `branding.background_color` drives the outer page background. Both
use `#RRGGBB`; onboardd derives darker and lighter supporting colors so products do not
need to maintain a larger palette.

`branding.logo` may reference a PNG, JPEG, or passive SVG file up to 512 KiB. Leaving it
out preserves the built-in wireless mark. Raster files must decode successfully. SVGs
may contain ordinary vector shapes and local fragment references, but active elements,
event attributes, external resources, directives, and style URLs are rejected before
the HTTP server starts.

The public contract is intentionally small. Network timing, captive DNS paths, radio
band, and the provisioning subnet are implementation defaults rather than product
settings. Phase 4's debug commands retain temporary overrides for troubleshooting.

## Recovery input

Manual recovery is always available to a local administrator while `onboardd run` is
active:

```bash
sudo onboardd recover
```

The command contacts the private `/run/onboardd/control.sock`; it does not start another
daemon or HTTP listener. Optional physical recovery uses a normally-open button from a
GPIO line to ground:

```toml
[recovery.gpio]
enabled = true
chip = "/dev/gpiochip0"
line = 17
```

`line` is the character-device line offset, which is the BCM GPIO number for the Pi Zero
2 W's main controller. The button must be held for three seconds. Pull-up and 50 ms
debounce are applied by onboardd through the Linux GPIO v2 API.

## Application handoff

The handoff section optionally defines the product application shown when setup
succeeds:

```toml
[handoff]
application_label = "Open {{ .ProductName }}"
application_url = "http://{{ .Hostname }}.local/"
health_check_url = "http://127.0.0.1/health"
show_standalone_credentials = false
```

At startup, onboardd reads Avahi's existing hostname over D-Bus, appends `.local` and
the private `portal.listener_port`, and uses that stable setup URL throughout the run.
The hostname belongs to the host: onboardd never changes it and there is no hostname
setting in this configuration. Configure the appliance hostname through the operating
system and Avahi before starting onboardd. The application label and URL must either
both be set or both be empty. URLs must be absolute HTTP(S) URLs without embedded
credentials or a fragment.

onboardd publishes only the private HTTP listener as an Avahi `_http._tcp` service and
checks that the hostname did not change during startup. `avahi-daemon` is therefore a
runtime dependency on Linux appliances; onboardd does not start a competing mDNS
responder.

`health_check_url` is server-only readiness policy; it requires an application URL and
is never sent to the browser. onboardd performs a bounded HTTP GET and accepts only a
2xx response. Until that succeeds, the application URL is also withheld and the
completion screen reports that the application is still starting. Without a health URL,
the application is immediately available. `show_standalone_credentials` controls
whether the standalone confirmation and completion screens show the AP password and a
Wi-Fi join QR. It defaults to false. The policy itself is never exposed by the bootstrap
API. Enabling it deliberately makes the configured password available to setup clients
before the radio changes, so the same phone can save or copy it first. The SSID remains
available when credential display is disabled.

During provisioning, public port 80 serves only a small handoff page. The complete setup
application is served on `portal.listener_port` at the stable `.local` origin. Captive
viewers that allow new browsing contexts can open that origin directly; otherwise the
landing page displays the address to enter manually in a normal browser.

## Network modes and requirement

Mode availability and connectivity requirements are separate concepts.

```toml
[network]
requirement = "local"
infrastructure_enabled = true
standalone_enabled = true
```

For a cloud-only appliance:

```toml
[network]
requirement = "internet"
infrastructure_enabled = true
standalone_enabled = false
```

At least one production mode must be enabled. If standalone is disabled, it is absent
from the setup UI rather than displayed as an unavailable option.

## Templates

The branding title and subtitle, both configured SSIDs, and the handoff text/URLs
support these four fields:

```text
{{ .ProductName }}
{{ .DeviceName }}
{{ .DeviceID }}
{{ .Hostname }}
```

Only direct field substitution is accepted. Functions, pipelines, loops, conditionals,
unknown fields, and unmatched delimiters are rejected. Configuration values are inserted
as text and are not evaluated a second time, so templates cannot access the filesystem,
environment, processes, or general code execution.

`.DeviceID` is an eight-character, application-specific value derived from the Linux
machine ID with HMAC-SHA-256. The raw machine ID is never returned, logged, placed in an
SSID, or exposed to the browser. Reinstalling the operating system with a new machine ID
intentionally produces a new device ID. During production setup, `.Hostname` is the
existing hostname read from Avahi over D-Bus. The debug configuration preview uses the
hostname supplied by its `--hostname` option, or the local operating-system hostname.

After rendering, an SSID must be valid UTF-8, contain no control characters, and fit the
Wi-Fi limit of 1–32 bytes. This check happens after product and device names have been
inserted.

On the Raspberry Pi, inspect actual rendered values with:

```bash
sudo ~/onboardd debug config --config /etc/onboardd/config.toml --render
```

The VS Code **Config: inspect example** task supplies an obvious development-only
identity so the same rendering can be checked on macOS without a Linux machine ID.

## Environment and CLI mapping

Environment variables map directly to TOML paths using uppercase underscore-separated
names. Unknown `ONBOARDD_*` variables are errors so misspellings cannot silently select
a default. For example:

```text
ONBOARDD_NETWORK_REQUIREMENT=internet
ONBOARDD_NETWORK_STANDALONE_ENABLED=false
ONBOARDD_PORTAL_LISTENER_PORT=19000

onboardd debug config --config config/example.toml \
  --network-requirement=internet \
  --standalone-enabled=false \
  --listener-port=19000
```

Every public TOML value has an environment mapping. The CLI override layer deliberately
covers only the operational interface, requirement, mode switches, and listener port;
product text and branding do not need command-line flags. `debug config`, `setup`, and
the production `run` command expose these flags. The
resolver applies all layers before cross-field validation, so an intentional environment
or CLI override can repair a file-level combination such as a temporarily disabled mode.

The provisioning network is always `10.42.0.1/24`. Its public port 80 redirects only on
the provisioning interface to the small browser-handoff page. Explicit requests to
`portal.listener_port` receive the complete setup application. This allows a product
application to retain its own port 80 listener outside provisioning. Standalone mode
uses its configured host address and prefix; NetworkManager derives the shared subnet
and DHCP service from that value.

The complete environment mapping is:

```text
ONBOARDD_PRODUCT_NAME
ONBOARDD_PRODUCT_DEVICE_NAME
ONBOARDD_BRANDING_LOGO
ONBOARDD_BRANDING_PRIMARY_COLOR
ONBOARDD_BRANDING_BACKGROUND_COLOR
ONBOARDD_BRANDING_TEXT_TITLE
ONBOARDD_BRANDING_TEXT_SUBTITLE
ONBOARDD_NETWORK_INTERFACE
ONBOARDD_NETWORK_REQUIREMENT
ONBOARDD_NETWORK_INFRASTRUCTURE_ENABLED
ONBOARDD_NETWORK_STANDALONE_ENABLED
ONBOARDD_NETWORK_PROVISIONING_SSID
ONBOARDD_NETWORK_PROVISIONING_PASSWORD_FILE
ONBOARDD_NETWORK_STANDALONE_SSID
ONBOARDD_NETWORK_STANDALONE_PASSWORD_FILE
ONBOARDD_NETWORK_STANDALONE_ADDRESS
ONBOARDD_PORTAL_LISTENER_PORT
ONBOARDD_HANDOFF_APPLICATION_LABEL
ONBOARDD_HANDOFF_APPLICATION_URL
ONBOARDD_HANDOFF_HEALTH_CHECK_URL
ONBOARDD_HANDOFF_SHOW_STANDALONE_CREDENTIALS
ONBOARDD_RECOVERY_GPIO_ENABLED
ONBOARDD_RECOVERY_GPIO_CHIP
ONBOARDD_RECOVERY_GPIO_LINE
```

## Production commands

For packaged installation, first configuration, systemd operation, upgrades, and
removal, see [Install and operate onboardd](installation.md).

The normal long-running appliance entry point is:

```bash
sudo onboardd run --config /etc/onboardd/config.toml
```

While it is active, request manual recovery with `sudo onboardd recover`. If the
long-running process is stopped, start the direct recovery workflow with `setup`.

The direct setup entry point is:

```bash
sudo onboardd setup
```

It reads `/etc/onboardd/config.toml` when present, renders device templates, checks
logos and password files, then starts the provisioning network and the setup portal
embedded in the binary. An explicitly supplied file must exist:

```bash
sudo onboardd setup --config /etc/onboardd/inkypi.toml
```

Only operational policy has CLI overrides:

```bash
sudo onboardd setup \
  --network-interface wlan0 \
  --network-requirement local \
  --standalone-enabled=true \
  --listener-port 18080
```

Password files must be regular files with no group or other permissions. Prepare them
as root with mode `0600`; the password itself is never accepted on the command line or
printed. Each WPA password must be 8–63 bytes, or exactly 64 hexadecimal characters.

`setup` intentionally enters provisioning when started. `run` performs automatic boot
reconciliation and creates the local manual-recovery control socket. When installed as
the Phase 8 systemd service, `run` also publishes readiness and watchdog notifications;
manual foreground execution remains supported.

## Compatibility

The root `schema_version` is mandatory. Breaking contract changes increment it and
require a documented migration. Additive optional fields may retain the same version.
