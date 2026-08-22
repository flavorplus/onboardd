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

The branding title and subtitle plus both configured SSIDs support these four fields:

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
intentionally produces a new device ID. `.Hostname` is read from the operating system.

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
product text and branding do not need command-line flags. Both `debug config` and the
production `setup` command expose these flags. The
resolver applies all layers before cross-field validation, so an intentional environment
or CLI override can repair a file-level combination such as a temporarily disabled mode.

The provisioning network is always `10.42.0.1/24`. Its public port 80 redirects only on
the provisioning interface to `portal.listener_port`, allowing a product application to
retain its own port 80 listener. Standalone mode uses its configured host address and
prefix; NetworkManager derives the shared subnet and DHCP service from that value.

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
```

## Production setup command

The normal configuration-driven entry point is:

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

This Phase 5 command intentionally enters provisioning when started. Automatic boot
reconciliation, manual recovery activation, and systemd packaging are Phase 7 and
Phase 8 work; the command is not installed as an always-on service yet.

## Compatibility

The root `schema_version` is mandatory. Breaking contract changes increment it and
require a documented migration. Additive optional fields may retain the same version.
