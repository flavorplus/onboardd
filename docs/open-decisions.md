# Open decisions

This file keeps provisional assumptions visible. A choice is removed from this list
when it has been accepted and recorded in the relevant contract or an architecture
decision record.

## Confirm during Phase 0 review

### Go module path

The scaffold currently uses:

```text
github.com/jvermeulen/onboardd
```

This matches the local owner name and repository folder but should be changed before
published packages depend on it if the eventual GitHub organization or account differs.

### License

No license has been added. The intended open-source license must be selected explicitly.
MIT and Apache-2.0 are likely candidates, but the project owner should choose based on
the desired patent and attribution terms.

### Initial hardware/software baseline

The plan targets current Raspberry Pi OS with NetworkManager on an ARM64 Raspberry Pi.
Before Phase 1 hardware acceptance, record:

- Raspberry Pi model and Wi-Fi chipset;
- Raspberry Pi OS release;
- NetworkManager version;
- whether Anthias or a clean Raspberry Pi OS image is the first test environment.

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

## Resolve before the relevant later phase

- Captive DNS/HTTP implementation and OS-specific probe routes: Phase 3.
- TypeScript frontend framework or framework-free approach: Phase 4.
- Exact environment-variable and CLI flag surface: Phase 5.
- `.deb` ownership, capabilities, and service hardening: Phase 8.

