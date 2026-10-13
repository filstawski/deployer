#!/bin/sh
# Deployer installer. https://github.com/filstawski/deployer
#
#   Install the CLI (macOS, Linux):
#     curl -fsSL https://github.com/filstawski/deployer/releases/latest/download/install.sh | sh
#
#   On Windows, install the CLI with install.ps1 instead, in PowerShell:
#     irm https://github.com/filstawski/deployer/releases/latest/download/install.ps1 | iex
#
#   Install the server (Linux, as root); flags go to `deployer server install`:
#     curl -fsSL .../install.sh | sh -s -- server --domain previews.example.com
#
#   Upgrade the server (as root):
#     curl -fsSL .../install.sh | sh -s -- upgrade
set -eu

default_version=latest
repository=${DEPLOYER_REPOSITORY:-filstawski/deployer}
version=${DEPLOYER_VERSION:-$default_version}
bin_dir=${HOME:-}/.local/bin
from=
mode=client

usage() {
  cat <<'HELP'
Usage: install.sh [client|server|upgrade] [options] [server install flags]

Modes:
  client    Install the deployer CLI (default)
  server    Install Deployer on this Linux server (root); other flags are
            passed to `deployer server install`, e.g. --domain
  upgrade   Upgrade the installed server (root)

Options:
  --version vX.Y.Z   Release to install (default: this script's release, or latest)
  --bin-dir DIR      Where the client is installed (default: ~/.local/bin)
  --from DIR         Use release files from DIR instead of downloading them
  -h, --help         Show this help
HELP
}

fail() {
  printf 'install.sh: %s\n' "$*" >&2
  exit 1
}

case "${1:-}" in
  client | server | upgrade) mode=$1; shift ;;
esac
# Consume this script's options. Everything else is rotated to the end of
# "$@", in order, and later passed to `deployer server install`.
remaining=$#
while [ "$remaining" -gt 0 ]; do
  arg=$1
  shift
  remaining=$((remaining - 1))
  case "$arg" in
    --version | --bin-dir | --from)
      [ "$remaining" -gt 0 ] || fail "$arg needs a value"
      value=$1
      shift
      remaining=$((remaining - 1))
      case "$arg" in
        --version) version=$value ;;
        --bin-dir) bin_dir=$value ;;
        --from) from=$value ;;
      esac ;;
    -h | --help) usage; exit 0 ;;
    *)
      [ "$mode" = server ] || fail "unknown option: $arg (see --help)"
      set -- "$@" "$arg" ;;
  esac
done

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  MINGW* | MSYS* | CYGWIN*)
    fail "on Windows, install the CLI in PowerShell: irm https://github.com/$repository/releases/latest/download/install.ps1 | iex" ;;
  *) fail "unsupported operating system $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac
if [ "$mode" != client ]; then
  [ "$os" = linux ] || fail "the server runs on Linux"
  [ "$(id -u)" = 0 ] || fail "run the server installer as root (for example with sudo)"
fi

tmp=$(mktemp -d "${TMPDIR:-/tmp}/deployer-install.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT TERM

fetch() { # fetch NAME DEST
  if [ -n "$from" ]; then
    [ -f "$from/$1" ] || fail "$from/$1 not found"
    cp "$from/$1" "$2"
  else
    command -v curl >/dev/null 2>&1 || fail "curl is required"
    curl -fsSL --proto '=https' --tlsv1.2 --retry 3 -o "$2" "$base/$1" ||
      fail "could not download $base/$1"
  fi
}

if [ "$version" = latest ] && [ -z "$from" ]; then
  base="https://github.com/$repository/releases/latest/download"
  fetch VERSION "$tmp/VERSION"
  version=$(cat "$tmp/VERSION")
fi
case "$version" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) [ -n "$from" ] && [ "$version" = latest ] || fail "invalid version: $version" ;;
esac
if [ -n "$from" ] && [ "$version" = latest ]; then
  version=$(cat "$from/VERSION")
fi
base="https://github.com/$repository/releases/download/$version"
fetch SHA256SUMS "$tmp/SHA256SUMS"

verify() { # verify ASSET FILE
  expected=$(awk -v f="$1" '$2 == f || $2 == "*" f { print $1 }' "$tmp/SHA256SUMS")
  [ -n "$expected" ] || fail "no checksum for $1"
  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$2" | awk '{ print $1 }')
  else
    actual=$(shasum -a 256 "$2" | awk '{ print $1 }')
  fi
  [ "$actual" = "$expected" ] || fail "checksum mismatch for $1; nothing was installed"
}

binaries=deployer
[ "$mode" = client ] || binaries="deployer deployerd"
for bin in $binaries; do
  asset="${bin}_${version}_${os}_${arch}"
  fetch "$asset" "$tmp/$bin"
  verify "$asset" "$tmp/$bin"
  chmod 755 "$tmp/$bin"
done

case "$mode" in
  server)
    "$tmp/deployer" server install --bundle "$tmp" "$@"
    ;;
  upgrade)
    "$tmp/deployer" server upgrade --bundle "$tmp"
    ;;
  client)
    case "$bin_dir" in /*) ;; *) fail "--bin-dir must be an absolute path" ;; esac
    dest="$bin_dir/deployer"
    [ ! -L "$dest" ] || fail "$dest is a symlink; choose another --bin-dir"
    if [ -e "$dest" ] && ! "$dest" version 2>/dev/null | grep -q 'deployer\|"version"'; then
      fail "$dest exists and is not the Deployer CLI; choose another --bin-dir"
    fi
    mkdir -p "$bin_dir"
    cp "$tmp/deployer" "$bin_dir/.deployer.new"
    mv -f "$bin_dir/.deployer.new" "$dest"
    printf 'Installed deployer %s to %s\n' "$version" "$dest"
    case ":$PATH:" in
      *":$bin_dir:"*) ;;
      *) printf '\n%s is not on your PATH. Add it, for example:\n  echo '\''export PATH="%s:$PATH"'\'' >> ~/.profile\n' "$bin_dir" "$bin_dir" ;;
    esac
    printf '\nNext: deployer login https://api.YOUR-DOMAIN\n'
    ;;
esac
