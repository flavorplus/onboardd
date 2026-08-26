# Configuration

`onboardd run` resolves configuration in this order:

1. built-in product-neutral defaults;
2. one TOML file;
3. explicitly supplied operational CLI overrides.

There are no environment-variable configuration keys. Product text, colors, SSIDs,
templates, handoff URLs, and password paths belong in TOML so service behavior is
visible in one place.

The default path is `/etc/onboardd/config.toml`. An explicitly supplied missing file
is an error; the implicit default may be absent during development.

## Complete example

[config/example.toml](../config/example.toml) is the annotated source of truth and
[config/schema.json](../config/schema.json) provides VS Code validation. Product-sized
examples are available as [Anthias](../config/anthias.toml) and
[InkyPi](../config/inkypi.toml).

Unknown keys, missing `schema_version`, unsupported schema versions, and invalid final
combinations are rejected before any network change.

## Sections

### Product and branding

```toml
schema_version = 1

[product]
name = "Display Player"
device_name = "Lobby Display"

[branding]
logo = "/etc/onboardd/company.svg"
primary_color = "#cd2455"
background_color = "#f8eff3"

[branding.text]
title = "Set up {{ .DeviceName }}"
subtitle = "Choose how this display should connect."
```

The logo is optional. Supported SVG, PNG, and JPEG logos are validated and embedded
active SVG content is rejected. Colors must use six-digit hexadecimal notation.

### Network policy

```toml
[network]
interface = "wlan0"
requirement = "local"
infrastructure_enabled = true
standalone_enabled = true
```

`requirement = "local"` accepts an activated device with a usable IPv4 address.
`"internet"` additionally requires NetworkManager connectivity `FULL`. At least one
final mode must be enabled.

### Provisioning and standalone

```toml
[network.provisioning]
ssid = "{{ .ProductName }}-Setup-{{ .DeviceID }}"
password_file = "/etc/onboardd/provisioning-password"

[network.standalone]
ssid = "{{ .ProductName }}-{{ .DeviceID }}"
password_file = "/etc/onboardd/standalone-password"
address = "10.42.0.1/24"
```

Provisioning always uses the built-in `10.42.0.1/24` address. The standalone address
is configurable and must be a usable IPv4 host prefix.

Password files must be regular files with no group/other permissions (normally mode
`0600`). Their trimmed contents must satisfy NetworkManager's access-point password
rules. Passwords never belong in TOML or command arguments.

### Portal

```toml
[portal]
listener_port = 18080
```

Port 80 is reserved for captive detection and redirection. The private listener port
must be different. The setup URL uses the host's existing Avahi name, for example
`http://display.local:18080/`.

### Application handoff

```toml
[handoff]
application_label = "Open {{ .ProductName }}"
application_url = "http://{{ .Hostname }}.local/"
health_check_url = "http://127.0.0.1/health"
show_standalone_credentials = false
```

The label and application URL are optional but must be configured together. A health
check is optional and gates display of the application link. The standalone password
is returned to the browser only when `show_standalone_credentials` is true.

## Templates

These fields support Go-template placeholders:

- provisioning and standalone SSIDs;
- branding title and subtitle;
- application label, URL, and health-check URL.

Available values are `.ProductName`, `.DeviceName`, `.DeviceID`, and `.Hostname`.
`DeviceID` is derived from stable machine identity; `Hostname` is read from Avahi.
Only simple field substitution is allowed.

## Operational overrides

The production command exposes only host/runtime policy that is useful during
deployment:

```text
--network-interface
--network-requirement
--infrastructure-enabled
--standalone-enabled
--listener-port
```

Example:

```bash
sudo onboardd run \
  --config /etc/onboardd/config.toml \
  --network-interface wlan1 \
  --network-requirement internet
```

Use overrides sparingly; persistent product behavior should remain in TOML.
