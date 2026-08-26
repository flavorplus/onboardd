#!/usr/bin/env bash

set -euo pipefail

usage() {
  printf 'Usage: %s VERSION [RELEASE_DIRECTORY]\n' "${0##*/}" >&2
  printf 'Example: %s v0.1.0 dist/v0.1.0\n' "${0##*/}" >&2
}

if (($# < 1 || $# > 2)); then
  usage
  exit 2
fi

for required in dpkg-deb md5sum sha256sum tar; do
  if ! command -v "$required" >/dev/null 2>&1; then
    printf '%s is required to inspect Debian packages.\n' "$required" >&2
    exit 1
  fi
done

version=$1
version_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?(\+[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$'
if [[ ! $version =~ $version_pattern ]]; then
  printf 'Package version must be a v-prefixed semantic version.\n' >&2
  exit 2
fi

version_without_prefix=${version#v}
if [[ $version_without_prefix == *-* ]]; then
  stable_part=${version_without_prefix%%-*}
  suffix=${version_without_prefix#*-}
  debian_version="${stable_part}~${suffix}-1"
else
  debian_version="${version_without_prefix}-1"
fi

script_directory=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repository_root=$(cd -- "${script_directory}/.." && pwd)
release_directory=${2:-"${repository_root}/dist/${version}"}
if [[ $release_directory != /* ]]; then
  release_directory="$(pwd -P)/${release_directory}"
fi
temporary_root=${TMPDIR:-/tmp}
if [[ $temporary_root != /* ]]; then
  temporary_root="$(pwd -P)/${temporary_root}"
fi
inspection_root=$(mktemp -d "${temporary_root%/}/onboardd-deb-check.XXXXXX")

cleanup() {
  if [[ -n $inspection_root && -d $inspection_root ]]; then
    rm -rf -- "$inspection_root"
  fi
}
trap cleanup EXIT

export LC_ALL=C

fail() {
  printf 'Debian package check failed: %s\n' "$1" >&2
  exit 1
}

assert_equal() {
  local label=$1
  local actual=$2
  local expected=$3
  if [[ $actual != "$expected" ]]; then
    fail "${label}: got '${actual}', want '${expected}'"
  fi
}

assert_mode() {
  local manifest=$1
  local path=$2
  local expected=$3
  local line
  local actual

  line=$(awk -v path="$path" '$NF == path || $NF == path "/" { print; exit }' "$manifest")
  if [[ -z $line ]]; then
    fail "archive entry ${path} is missing"
  fi
  actual=${line%% *}
  assert_equal "mode for ${path}" "$actual" "$expected"
}

expected_contents="${inspection_root}/expected-contents"
printf '%s\n' \
  . \
  ./etc \
  ./etc/onboardd \
  ./etc/onboardd/config.toml \
  ./usr \
  ./usr/bin \
  ./usr/bin/onboardd \
  ./usr/lib \
  ./usr/lib/systemd \
  ./usr/lib/systemd/system \
  ./usr/lib/systemd/system/onboardd.service \
  ./usr/share \
  ./usr/share/doc \
  ./usr/share/doc/onboardd \
  ./usr/share/doc/onboardd/copyright \
  | sort >"$expected_contents"

expected_dependencies='network-manager, dnsmasq-base, avahi-daemon, nftables, init-system-helpers'
lifecycle_control=
lifecycle_data=

for architecture in amd64 arm64; do
  package="${release_directory}/onboardd_${debian_version}_${architecture}.deb"
  binary="${release_directory}/onboardd-linux-${architecture}"
  package_root="${inspection_root}/${architecture}"
  control_root="${package_root}/control"
  data_root="${package_root}/data"
  contents="${package_root}/contents"
  manifest="${package_root}/manifest"

  if [[ ! -f $package ]]; then
    fail "package is missing: ${package}"
  fi
  if [[ ! -f $binary ]]; then
    fail "release binary is missing: ${binary}"
  fi

  assert_equal "Package field" "$(dpkg-deb --field "$package" Package)" onboardd
  assert_equal "Version field" "$(dpkg-deb --field "$package" Version)" "$debian_version"
  assert_equal "Architecture field" \
    "$(dpkg-deb --field "$package" Architecture)" "$architecture"
  assert_equal "Depends field" \
    "$(dpkg-deb --field "$package" Depends)" "$expected_dependencies"

  install -d -m 0755 "$control_root" "$data_root"
  dpkg-deb --control "$package" "$control_root"
  dpkg-deb --extract "$package" "$data_root"

  cmp "$control_root/conffiles" "${repository_root}/packaging/debian/conffiles"
  for script in postinst prerm postrm; do
    cmp "$control_root/$script" "${repository_root}/packaging/debian/$script"
    sh -n "$control_root/$script"
    assert_equal "mode for DEBIAN/${script}" \
      "$(stat -c '%a' "$control_root/$script")" 755
  done
  if grep -E \
    'deb-systemd-invoke[[:space:]]+daemon-(reload|reexec)' \
    "$control_root/postinst" \
    "$control_root/postrm" >/dev/null; then
    fail "unsupported deb-systemd-invoke manager action found for ${architecture}"
  fi
  for script in postinst postrm; do
    if ! grep -F 'systemctl --system daemon-reload' "$control_root/$script" >/dev/null; then
      fail "${script} does not reload the system manager compatibly for ${architecture}"
    fi
  done

  cmp "$data_root/usr/bin/onboardd" "$binary"
  cmp \
    "$data_root/usr/lib/systemd/system/onboardd.service" \
    "${repository_root}/packaging/debian/onboardd.service"
  cmp "$data_root/etc/onboardd/config.toml" "${repository_root}/packaging/debian/config.toml"
  cmp "$data_root/usr/share/doc/onboardd/copyright" "${repository_root}/LICENSE"
  (
    cd -- "$data_root"
    md5sum --check "$control_root/md5sums" >/dev/null
  )

  dpkg-deb --fsys-tarfile "$package" \
    | tar --list --file=- \
    | sed 's:/$::' \
    | sort >"$contents"
  if ! cmp "$contents" "$expected_contents"; then
    fail "filesystem contents differ for ${architecture}"
  fi

  dpkg-deb --fsys-tarfile "$package" \
    | tar --list --verbose --numeric-owner --file=- >"$manifest"
  if ! awk '$2 != "0/0" { exit 1 }' "$manifest"; then
    fail "non-root archive ownership found for ${architecture}"
  fi
  assert_mode "$manifest" ./etc/onboardd drwxr-x---
  assert_mode "$manifest" ./etc/onboardd/config.toml -rw-r-----
  assert_mode "$manifest" ./usr/bin/onboardd -rwxr-xr-x
  assert_mode "$manifest" ./usr/lib/systemd/system/onboardd.service -rw-r--r--
  assert_mode "$manifest" ./usr/share/doc/onboardd/copyright -rw-r--r--

  if [[ $architecture == amd64 ]]; then
    lifecycle_control=$control_root
    lifecycle_data=$data_root
  fi
done

(
  cd -- "$release_directory"
  sha256sum --check DEBSHA256SUMS >/dev/null
)

# Exercise idempotent maintainer behavior below an isolated dpkg root. DPKG_ROOT also
# keeps deb-systemd-helper state inside this tree. Runtime service actions are skipped,
# exactly as they are when constructing an image rather than operating the live host.
lifecycle_root="${inspection_root}/lifecycle"
helper_root="${inspection_root}/helper-bin"
helper_log="${inspection_root}/deb-systemd-helper.log"
install -d -m 0755 "$lifecycle_root"
install -d -m 0755 "$helper_root"
printf '%s\n' \
  '#!/bin/sh' \
  'set -e' \
  'printf "%s\n" "$*" >>"$ONBOARDD_HELPER_LOG"' \
  >"${helper_root}/deb-systemd-helper"
chmod 0755 "${helper_root}/deb-systemd-helper"
cp -a "${lifecycle_data}/." "$lifecycle_root/"
PATH="${helper_root}:$PATH" \
  DPKG_ROOT="$lifecycle_root" \
  ONBOARDD_HELPER_LOG="$helper_log" \
  "$lifecycle_control/postinst" configure
PATH="${helper_root}:$PATH" \
  DPKG_ROOT="$lifecycle_root" \
  ONBOARDD_HELPER_LOG="$helper_log" \
  "$lifecycle_control/postinst" configure
if [[ ! -d $lifecycle_root/etc/NetworkManager/dnsmasq-shared.d ]]; then
  fail 'postinst did not create the NetworkManager dnsmasq fragment directory'
fi
assert_equal 'dnsmasq fragment directory mode' \
  "$(stat -c '%a' "$lifecycle_root/etc/NetworkManager/dnsmasq-shared.d")" 755
if [[ -e $lifecycle_root/etc/NetworkManager/system-connections ]]; then
  fail 'maintainer scripts created a NetworkManager profile directory'
fi

secret="${lifecycle_root}/etc/onboardd/provisioning-password"
install -m 0600 /dev/null "$secret"
printf 'package-check-placeholder\n' >"$secret"
DPKG_ROOT="$lifecycle_root" "$lifecycle_control/prerm" upgrade
DPKG_ROOT="$lifecycle_root" "$lifecycle_control/prerm" remove
rm -- "${lifecycle_root}/etc/onboardd/config.toml"
PATH="${helper_root}:$PATH" \
  DPKG_ROOT="$lifecycle_root" \
  ONBOARDD_HELPER_LOG="$helper_log" \
  "$lifecycle_control/postrm" purge
if [[ ! -f $secret ]]; then
  fail 'purge removed an administrator-created password file'
fi
assert_equal 'preserved password mode' "$(stat -c '%a' "$secret")" 600
assert_equal 'systemd helper update count' \
  "$(awk '$0 == "update-state onboardd.service" { count++ } END { print count + 0 }' "$helper_log")" 2
assert_equal 'systemd helper purge count' \
  "$(awk '$0 == "purge onboardd.service" { count++ } END { print count + 0 }' "$helper_log")" 1
if PATH="${helper_root}:$PATH" \
  DPKG_ROOT="$lifecycle_root" \
  ONBOARDD_HELPER_LOG="$helper_log" \
  "$lifecycle_control/postinst" unsupported \
  >/dev/null 2>&1; then
  fail 'postinst accepted an unsupported maintainer action'
fi

printf 'Debian packages passed structural and isolated lifecycle checks in %s\n' \
  "$release_directory"
