# Architecture

## Boundary

`onboardd` owns network onboarding for a single NetworkManager-managed Wi-Fi
interface. It does not own the product application, the host name, arbitrary
NetworkManager profiles, or general router/firewall policy.

The daemon is intentionally one process and one embedded web application. Linux
integration uses D-Bus for NetworkManager and Avahi, a Unix socket for manual recovery,
a small NetworkManager dnsmasq fragment for captive DNS, and an interface-scoped
nftables table for port redirection.

## Network modes

There are three roles:

- **Infrastructure**: a persistent Wi-Fi client profile. It may require a usable local
  network or confirmed Internet access.
- **Standalone**: a persistent shared-mode access-point profile. It is a valid final
  state and does not require upstream connectivity.
- **Provisioning**: a temporary, in-memory shared-mode access-point profile used only
  for setup and recovery.

NetworkManager is the source of truth. onboardd does not maintain a second persistent
mode file. Owned profiles carry application metadata in `user.data`:

```text
onboardd.owner = onboardd
onboardd.role = infrastructure | standalone | provisioning
onboardd.schema = 1
onboardd.pending = true | false
```

Profile names are for people; ownership and role decisions use metadata. Foreign
profiles are never deleted or rewritten automatically.

## Startup and reconciliation

At startup the controller:

1. removes stale onboardd-owned captive DNS, nftables, temporary provisioning, and
   pending candidate resources;
2. reads the Wi-Fi device, profiles, active connection, addresses, and connectivity;
3. honors a selected standalone profile when standalone autoconnect intent is active;
4. accepts a ready infrastructure connection according to the configured requirement;
5. waits through a bounded activation/connectivity grace period when a candidate may
   still become usable;
6. otherwise creates the temporary provisioning network.

NetworkManager property changes trigger reconciliation. Timers and operations are
cancelled through `context.Context`; no reconciliation state is written to disk.

## Protected transitions

Infrastructure and standalone changes are transactional:

1. identify the exact currently active profile;
2. create a NetworkManager checkpoint for the Wi-Fi device;
3. create or activate the candidate with autoconnect disabled;
4. wait for activation and the required connectivity;
5. finalize autoconnect intent and remove only superseded owned profiles;
6. commit the checkpoint.

Failure rolls back the checkpoint and explicitly reactivates the exact previous
profile when necessary. This prevents another eligible foreign profile from changing
the recovery target. A partially created candidate is removed on failure or next
startup.

## Captive setup

Provisioning uses NetworkManager shared IPv4 mode at the fixed address
`10.42.0.1/24`. NetworkManager supplies DHCP and its shared-mode firewall rules.
onboardd adds only:

- a wildcard dnsmasq fragment while provisioning is active;
- an nftables redirect from port 80 on the setup interface to the private configured
  listener port;
- captive-detection responses and a small landing page that opens the stable setup
  URL in a normal browser.

The private HTTP listener remains alive across all modes. The stable URL is derived
from the existing Avahi host name and advertised as `_http._tcp`; onboardd never
changes the host name.

The API allows one network operation at a time. Operation results remain queryable
after a browser disconnect, so the normal browser can reconnect through mDNS after the
radio transition.

## Recovery and service lifecycle

`onboardd recover` sends an authenticated local request over
`/run/onboardd/control.sock`. The running controller enters provisioning through its
normal state machine; there is no second standalone setup process.

The systemd service uses `Type=notify`, readiness notification, status updates, and a
watchdog heartbeat gated on the controller and HTTP listener health. Structured
lifecycle logs avoid credentials and are intended for `journalctl`.

Normal shutdown removes temporary captive resources and pending candidates. Durable
infrastructure/standalone profiles and foreign profiles remain untouched.

## Code map

```text
main.go                    process entry point
internal/cli               production command parsing and runtime assembly
internal/appliance         controller/supervisor lifecycle
internal/state             reconciliation engine and NetworkManager observer
internal/networkmanager    narrow typed D-Bus adapter
internal/captive           provisioning, DNS, redirect, and HTTP listener
internal/recovery          protected transitions and recovery control socket
internal/setup             product-facing setup operations
internal/web               API, branding/handoff, and embedded frontend
internal/config            strict TOML, templates, and device identity
internal/discovery         Avahi hostname/service publication
internal/observability     health and redacted lifecycle events
internal/systemd           readiness, status, and watchdog notification
frontend                   TypeScript source and local simulated device
config                     schema and product examples
packaging/debian           Debian metadata, service unit, and maintainer scripts
scripts                    release and package verification
```

Packages follow runtime responsibilities rather than abstract layers. Interfaces are
declared at the consumer boundary and kept as small as the tests and platform adapters
need.

## Safety rules

- Validate configuration and access-point settings before changing network state.
- Never include Wi-Fi passwords in TOML, CLI arguments, API responses without explicit
  policy, or logs.
- Restrict profile mutation to verified onboardd ownership, except explicit activation
  of a user-selected existing profile.
- Prefer idempotent cleanup; an already absent temporary resource is success.
- Keep bounded waits and cleanup contexts so shutdown and rollback cannot hang forever.
