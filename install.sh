#!/bin/sh
# Deployer release bootstrap. Run with sh; supports macOS/Linux amd64/arm64.
set -eu
umask 077
repo=filstawski/deployer
release=latest
source_dir=
bin_dir=${HOME:-}/.local/bin
mode=client
usage() {
  cat <<'HELP'
Usage: sh install.sh [client|server] [options]
  --version vX.Y.Z     Install a pinned release (default: latest stable)
  --repository O/R    Release repository (default: filstawski/deployer)
  --from DIRECTORY    Install checksummed local release files, without downloads
  --bin-dir DIRECTORY Client destination (default: ~/.local/bin)
  --                  Pass remaining arguments to server setup

Server example: sh install.sh server --version v0.1.0 -- --domain example.run
Server setup supports --dry-run; a real installation requires Linux and root.
HELP
}
fail() { printf '%s\n' "deployer installer: $*" >&2; exit 1; }
case "${1:-}" in client|server) mode=$1; shift ;; esac
while [ "$#" -gt 0 ]; do
  case "$1" in
    --version|--repository|--from|--bin-dir)
      [ "$#" -ge 2 ] || fail "$1 requires a value"
      option=$1; value=$2; shift 2
      case "$option" in --version) release=$value;; --repository) repo=$value;; --from) source_dir=$value;; --bin-dir) bin_dir=$value;; esac ;;
    --) shift; break ;;
    --help|-h) usage; exit 0 ;;
    *) fail "unknown option $1 (put server setup options after --)" ;;
  esac
done
[ "$mode" = server ] || [ "$#" = 0 ] || fail "client installation takes no setup options"
case "$repo" in *[!A-Za-z0-9_./-]*) fail 'invalid GitHub repository';; esac
printf '%s\n' "$repo" | LC_ALL=C grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' || fail 'invalid GitHub repository'
case "$(uname -s)" in Darwin) target_os=darwin;; Linux) target_os=linux;; *) fail 'supported clients: macOS and Linux (use WSL on Windows)' ;; esac
case "$(uname -m)" in arm64|aarch64) target_arch=arm64;; x86_64|amd64) target_arch=amd64;; *) fail 'supported architectures: amd64 and arm64' ;; esac
[ "$mode" != server ] || [ "$target_os" = linux ] || fail 'server installation requires Linux'
task_tmp=$(mktemp -d "${TMPDIR:-/tmp}/deployer-install.XXXXXXXX")
staged=
cleanup() { rm -rf "$task_tmp"; if [ -n "$staged" ]; then rm -f "$staged"; fi; }
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
fetch() {
  if [ -n "$source_dir" ]; then
    [ -f "$source_dir/$1" ] && [ ! -L "$source_dir/$1" ] || fail "missing regular release file: $1"
    cp "$source_dir/$1" "$2"
  else
    command -v curl >/dev/null 2>&1 || fail 'curl is required'
    curl --fail --show-error --silent --location --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 300 "$base/$1" -o "$2" || fail "download failed: $1 (has this release been published?)"
  fi
}
if [ "$release" = latest ]; then
  base="https://github.com/$repo/releases/latest/download"
  fetch VERSION "$task_tmp/VERSION"
  release=$(cat "$task_tmp/VERSION")
fi
case "$release" in *[!A-Za-z0-9.-]*) fail 'invalid release version';; esac
printf '%s\n' "$release" | LC_ALL=C grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9][A-Za-z0-9.-]*)?$' || fail 'version must look like v0.1.0 or v0.1.0-rc.1'
base="https://github.com/$repo/releases/download/$release"
fetch SHA256SUMS "$task_tmp/SHA256SUMS"
verify() {
  expected=$(awk -v file="$1" '$2==file {print $1}' "$task_tmp/SHA256SUMS")
  [ "${#expected}" = 64 ] || fail "missing or duplicate checksum for $1"
  case "$expected" in *[!0-9a-f]*) fail 'invalid checksum';; esac
  if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$2"); else actual=$(shasum -a 256 "$2"); fi
  actual=${actual%% *}
  [ "$actual" = "$expected" ] || fail "checksum mismatch: $1; nothing installed"
}
binaries=deployer
[ "$mode" != server ] || binaries='deployer deployerd deployer-gateway'
for binary in $binaries; do
  asset="${binary}_${release}_${target_os}_${target_arch}"
  fetch "$asset" "$task_tmp/$binary"
  verify "$asset" "$task_tmp/$binary"
  chmod 755 "$task_tmp/$binary"
done
if [ "$mode" = server ]; then
  # Keep this shell alive so its trap removes downloads after setup returns.
  "$task_tmp/deployer" setup --bundle "$task_tmp" "$@"
else
  case "$bin_dir" in /*) ;; *) fail '--bin-dir must be an absolute path';; esac
  [ ! -L "$bin_dir/deployer" ] || fail 'destination is a symlink; choose a different --bin-dir'
  if [ -e "$bin_dir/deployer" ] && [ ! -f "$bin_dir/.deployer-install" ]; then fail 'destination already exists and was not installed here by this installer'; fi
  mkdir -p "$bin_dir"
  staged=$(mktemp "$bin_dir/.deployer.XXXXXXXX")
  if ! cp "$task_tmp/deployer" "$staged" || ! chmod 755 "$staged" || ! mv -f "$staged" "$bin_dir/deployer"; then rm -f "$staged"; fail 'cannot install client'; fi
  staged=
  printf '%s\n' "$release" > "$bin_dir/.deployer-install"
  printf 'Installed Deployer %s at %s/deployer\n' "$release" "$bin_dir"
  case ":${PATH:-}:" in *":$bin_dir:"*) ;; *) printf 'Add this directory to your PATH: %s\n' "$bin_dir" ;; esac
  printf 'Next: deployer server add NAME --url URL --credential-file FILE\n'
fi
