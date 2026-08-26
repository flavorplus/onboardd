#!/usr/bin/env bash

set -euo pipefail

usage() {
  printf 'Usage: %s VERSION [OUTPUT_DIRECTORY]\n' "${0##*/}" >&2
  printf 'Example: %s v0.1.0\n' "${0##*/}" >&2
}

if (($# < 1 || $# > 2)); then
  usage
  exit 2
fi

if ! command -v dpkg-deb >/dev/null 2>&1; then
  printf 'dpkg-deb is required; build Debian packages on Debian, Ubuntu, or Raspberry Pi OS.\n' >&2
  exit 1
fi
if ! command -v md5sum >/dev/null 2>&1; then
  printf 'md5sum is required to create the Debian package file manifest.\n' >&2
  exit 1
fi

version=$1
version_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?(\+[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$'
if [[ ! $version =~ $version_pattern ]]; then
  printf 'Package version must be a v-prefixed semantic version, for example v0.1.0 or v1.0.0-rc.1.\n' >&2
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
output_root=${2:-"${repository_root}/dist"}
if [[ $output_root != /* ]]; then
  output_root="$(pwd -P)/${output_root}"
fi
release_directory="${output_root}/${version}"
packaging_directory="${repository_root}/packaging/debian"
temporary_root=${TMPDIR:-/tmp}
if [[ $temporary_root != /* ]]; then
  temporary_root="$(pwd -P)/${temporary_root}"
fi
staging_root=$(mktemp -d "${temporary_root%/}/onboardd-deb.XXXXXX")

cleanup() {
  if [[ -n $staging_root && -d $staging_root ]]; then
    rm -rf -- "$staging_root"
  fi
}
trap cleanup EXIT

export LC_ALL=C
export SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-$(git -C "$repository_root" log -1 --format=%ct)}
if [[ ! $SOURCE_DATE_EPOCH =~ ^[0-9]+$ ]]; then
  printf 'SOURCE_DATE_EPOCH must be an integer Unix timestamp.\n' >&2
  exit 2
fi
umask 022

"${script_directory}/build-release.sh" "$version" "$output_root"

build_package() {
  local architecture=$1
  local binary="${release_directory}/onboardd-linux-${architecture}"
  local package_root="${staging_root}/${architecture}"
  local output="${release_directory}/onboardd_${debian_version}_${architecture}.deb"

  install -d -m 0755 \
    "${package_root}/DEBIAN" \
    "${package_root}/usr/bin" \
    "${package_root}/usr/lib/systemd/system" \
    "${package_root}/usr/share/doc/onboardd"
  install -d -m 0750 "${package_root}/etc/onboardd"

  install -m 0755 "$binary" "${package_root}/usr/bin/onboardd"
  install -m 0644 \
    "${packaging_directory}/onboardd.service" \
    "${package_root}/usr/lib/systemd/system/onboardd.service"
  install -m 0640 \
    "${packaging_directory}/config.toml" \
    "${package_root}/etc/onboardd/config.toml"
  install -m 0644 \
    "${repository_root}/LICENSE" \
    "${package_root}/usr/share/doc/onboardd/copyright"

  sed \
    -e "s/@VERSION@/${debian_version}/g" \
    -e "s/@ARCHITECTURE@/${architecture}/g" \
    "${packaging_directory}/control.in" >"${package_root}/DEBIAN/control"
  install -m 0644 "${packaging_directory}/conffiles" "${package_root}/DEBIAN/conffiles"
  install -m 0755 \
    "${packaging_directory}/postinst" \
    "${packaging_directory}/prerm" \
    "${packaging_directory}/postrm" \
    "${package_root}/DEBIAN/"

  (
    cd -- "$package_root"
    find etc usr -type f -print0 | sort -z | xargs -0 md5sum >DEBIAN/md5sums
  )

  find "$package_root" -exec touch -d "@${SOURCE_DATE_EPOCH}" {} +
  dpkg-deb \
    --root-owner-group \
    --uniform-compression \
    --threads-max=1 \
    -Zxz \
    -z9 \
    --build \
    "$package_root" \
    "$output" >/dev/null

  printf '  %s\n' "$output"
}

printf 'Debian packages:\n'
build_package amd64
build_package arm64

(
  cd -- "$release_directory"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum onboardd_*.deb >DEBSHA256SUMS
  else
    shasum -a 256 onboardd_*.deb >DEBSHA256SUMS
  fi
)
printf 'Debian checksums: %s\n' "${release_directory}/DEBSHA256SUMS"
"${script_directory}/check-deb.sh" "$version" "$release_directory"
