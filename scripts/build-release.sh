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

version=$1
version_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?(\+[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$'
if [[ ! $version =~ $version_pattern ]]; then
  printf 'Release version must be a v-prefixed semantic version, for example v0.1.0 or v1.0.0-rc.1.\n' >&2
  exit 2
fi
version_without_build=${version%%+*}
if [[ $version_without_build == *-* ]]; then
  prerelease=${version_without_build#*-}
  IFS=. read -r -a prerelease_identifiers <<<"$prerelease"
  for identifier in "${prerelease_identifiers[@]}"; do
    if [[ $identifier =~ ^[0-9]+$ && ${#identifier} -gt 1 && $identifier == 0* ]]; then
      printf 'Numeric prerelease identifiers must not contain leading zeroes: %s\n' \
        "$identifier" >&2
      exit 2
    fi
  done
fi

script_directory=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repository_root=$(cd -- "${script_directory}/.." && pwd)
toolchain_version=$(tr -d '[:space:]' <"${repository_root}/.go-version")
if [[ ! $toolchain_version =~ ^1\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  printf 'Invalid Go toolchain version in .go-version: %s\n' "$toolchain_version" >&2
  exit 2
fi
go_toolchain="go${toolchain_version}"
output_root=${2:-"${repository_root}/dist"}
if [[ $output_root != /* ]]; then
  output_root="$(pwd -P)/${output_root}"
fi
release_directory="${output_root}/${version}"
temporary_root=${TMPDIR:-/tmp}
if [[ $temporary_root != /* ]]; then
  temporary_root="$(pwd -P)/${temporary_root}"
fi
staging_directory=$(mktemp -d "${temporary_root%/}/onboardd-release.XXXXXX")

cleanup() {
  if [[ -n $staging_directory && -d $staging_directory ]]; then
    rm -rf -- "$staging_directory"
  fi
}
trap cleanup EXIT

export LC_ALL=C
umask 022

ldflags="-s -w -buildid= -X github.com/flavorplus/onboardd/internal/cli.Version=${version}"

build_linux() {
  local architecture=$1
  local output=$2
  local architecture_level_name
  local architecture_level_value

  case "$architecture" in
    amd64)
      architecture_level_name=GOAMD64
      architecture_level_value=v1
      ;;
    arm64)
      architecture_level_name=GOARM64
      architecture_level_value=v8.0
      ;;
    *)
      printf 'Unsupported release architecture: %s\n' "$architecture" >&2
      return 2
      ;;
  esac

  (
    cd -- "$repository_root"
    env \
      GOENV=off \
      GOFLAGS= \
      GOEXPERIMENT= \
      GOTOOLCHAIN="$go_toolchain" \
      GOOS=linux \
      GOARCH="$architecture" \
      CGO_ENABLED=0 \
      "$architecture_level_name=$architecture_level_value" \
      go build \
        -mod=readonly \
        -buildmode=exe \
        -trimpath \
        -buildvcs=false \
        -ldflags="$ldflags" \
        -o "$output" \
        .
  )
}

verify_embedded_version() {
  local host_binary="${staging_directory}/onboardd-host"
  local host_os
  local host_architecture
  local actual

  host_os=$(env GOENV=off GOFLAGS= GOEXPERIMENT= GOTOOLCHAIN="$go_toolchain" go env GOHOSTOS)
  host_architecture=$(env GOENV=off GOFLAGS= GOEXPERIMENT= GOTOOLCHAIN="$go_toolchain" go env GOHOSTARCH)

  (
    cd -- "$repository_root"
    env \
      GOENV=off \
      GOFLAGS= \
      GOEXPERIMENT= \
      GOTOOLCHAIN="$go_toolchain" \
      GOOS="$host_os" \
      GOARCH="$host_architecture" \
      GOAMD64=v1 \
      GOARM64=v8.0 \
      CGO_ENABLED=0 \
      go build \
        -mod=readonly \
        -buildmode=exe \
        -trimpath \
        -buildvcs=false \
        -ldflags="$ldflags" \
        -o "$host_binary" \
        .
  )
  actual=$("$host_binary" --version)
  if [[ $actual != "onboardd ${version}" ]]; then
    printf 'Embedded version check failed: got %q, want %q.\n' \
      "$actual" "onboardd ${version}" >&2
    return 1
  fi
}

write_checksums() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum onboardd-linux-amd64 onboardd-linux-arm64
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 onboardd-linux-amd64 onboardd-linux-arm64
    return
  fi
  printf 'Neither sha256sum nor shasum is available.\n' >&2
  return 1
}

build_linux amd64 "${staging_directory}/onboardd-linux-amd64"
build_linux arm64 "${staging_directory}/onboardd-linux-arm64"
verify_embedded_version

(
  cd -- "$staging_directory"
  write_checksums >SHA256SUMS
)

install -d -m 0755 "$release_directory"
install -m 0755 \
  "${staging_directory}/onboardd-linux-amd64" \
  "${staging_directory}/onboardd-linux-arm64" \
  "$release_directory/"
install -m 0644 "${staging_directory}/SHA256SUMS" "$release_directory/SHA256SUMS"

printf 'Release binaries created in %s\n' "$release_directory"
while IFS= read -r checksum; do
  printf '  %s\n' "$checksum"
done <"${release_directory}/SHA256SUMS"
