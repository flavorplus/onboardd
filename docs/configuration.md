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

## Contract files

- `config/example.toml` is the annotated reference configuration.
- `config/schema.json` is the machine-readable structural contract.
- Product-specific examples will later live under `integration/<product>/`.

The schema validates structure and basic formats. Runtime validation must additionally
check operating-system facts such as whether an interface exists, a password file can be
read securely, an address is usable, and the configured application health endpoint is
reachable.

## Network modes and requirement

Mode availability and connectivity requirements are separate concepts.

```toml
[network]
requirement = "local"

[network.modes.infrastructure]
enabled = true

[network.modes.standalone]
enabled = true
```

For a cloud-only appliance:

```toml
[network]
requirement = "internet"

[network.modes.infrastructure]
enabled = true

[network.modes.standalone]
enabled = false
```

At least one production mode must be enabled. If standalone is disabled, it is absent
from the setup UI rather than displayed as an unavailable option.

## Templates

Selected text values can contain templates such as:

```text
{{ .ProductName }}
{{ .DeviceName }}
{{ .DeviceID }}
{{ .Hostname }}
```

The exact template allowlist will be implemented with strict missing-value errors. The
template engine must not expose filesystem, process, or general code-execution helpers.

## Environment and CLI mapping

The stable mapping is finalized when the configuration loader is implemented. The
intended style is:

```text
ONBOARDD_NETWORK_REQUIREMENT=internet
ONBOARDD_NETWORK_MODES_STANDALONE_ENABLED=false

onboardd --network-requirement=internet --standalone-enabled=false
```

Nested environment names use uppercase underscore-separated paths. CLI names use
lowercase kebab case and should cover common operational overrides, not every branding
string.

The captive HTTP public port and onboardd listener port are separate. The default
`public_port = 80` is redirected only on the provisioning interface to
`listener_port = 18080`, allowing a product application to retain its own port 80
listener.

## Compatibility

The root `schema_version` is mandatory. Breaking contract changes increment it and
require a documented migration. Additive optional fields may retain the same version.
