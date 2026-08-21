# Decision log and open questions

This file records important Phase 0 choices and keeps remaining evidence-driven
questions visible. Larger decisions can move into individual architecture decision
records as the project grows.

## Resolved during Phase 0

### Go module path

The repository and Go module use:

```text
github.com/flavorplus/onboardd
```

The configured `origin` remote is `https://github.com/flavorplus/onboardd.git`.

### License

The project uses Apache License 2.0. It permits commercial and private use,
modification, and redistribution while providing explicit patent terms and preserving
license/notice obligations.

### Initial hardware/software baseline

The first hardware target is Raspberry Pi Zero 2 W with Raspberry Pi OS Trixie and its
NetworkManager-based networking stack. ARM64 is the initial build assumption. The exact
Trixie image architecture and NetworkManager version will be captured from the device at
the beginning of Phase 1.

## Resolve through Phase 1 evidence

### Go D-Bus library

Choose the smallest maintained Go D-Bus stack that exposes the NetworkManager APIs we
need. Keep it behind `internal/networkmanager` so the rest of the application does not
depend on generated D-Bus types or library-specific concepts.

### Existing unmanaged profiles

The accepted safety default is “observe/use, but do not delete or rewrite.” Hardware
tests must determine the precise precedence behavior when an unmanaged autoconnect
profile competes with the selected managed standalone profile.

### NetworkManager checkpoint behavior

Test whether checkpoints fully protect the profile and device transitions needed by our
single-radio provisioning flow. Regardless of the API selected, failed transitions must
restore an accessible AP.

## Resolved during Phase 3

### Captive DNS and HTTP plumbing

Keep NetworkManager IPv4 shared mode for the Trixie baseline instead of launching a
competing DHCP server. Isolate its current `dnsmasq-shared.d` wildcard-DNS hook behind
`internal/captive`, redirect all non-portal cleartext HTTP requests consistently, and do
not intercept HTTPS. Keep an appliance's existing port 80 service intact by listening on
a private onboardd port and temporarily redirecting only provisioning-interface HTTP in
the separately owned `inet onboardd_captive` nftables table.

## Resolve before the relevant later phase

- TypeScript frontend framework or framework-free approach: Phase 4.
- Exact environment-variable and CLI flag surface: Phase 5.
- `.deb` ownership, capabilities, and service hardening: Phase 8.
