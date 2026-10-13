#!/bin/sh
# Builds release assets into dist/: binaries for each platform, install.sh and
# install.ps1 pinned to this version, VERSION, and SHA256SUMS. It does not
# publish.
set -eu
version=${1:-}
case "$version" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) echo "usage: scripts/release.sh v1.2.3" >&2; exit 2 ;;
esac
out=dist
if [ -d "$out" ] && [ -n "$(ls -A "$out")" ]; then
  echo "$out/ is not empty; remove it first" >&2
  exit 1
fi
mkdir -p "$out"
ldflags="-s -w -X github.com/filstawski/deployer/internal/version.Version=$version"
for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
  os=${target%/*}
  arch=${target#*/}
  for bin in deployer deployerd; do
    if [ "$bin" = deployerd ] && [ "$os" != linux ]; then continue; fi
    name="${bin}_${version}_${os}_${arch}"
    if [ "$os" = windows ]; then name="$name.exe"; fi
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$ldflags" -o "$out/$name" "./cmd/$bin"
    echo "built $name"
  done
done
sed "s/^default_version=latest$/default_version=$version/" install.sh > "$out/install.sh"
sed "s/^    \$defaultVersion = 'latest'$/    \$defaultVersion = '$version'/" install.ps1 > "$out/install.ps1"
grep -q "^default_version=$version$" "$out/install.sh" || { echo "could not pin install.sh to $version" >&2; exit 1; }
grep -q "^    \$defaultVersion = '$version'$" "$out/install.ps1" || { echo "could not pin install.ps1 to $version" >&2; exit 1; }
echo "$version" > "$out/VERSION"
cd "$out"
if command -v sha256sum >/dev/null 2>&1; then sums=$(sha256sum -- *); else sums=$(shasum -a 256 -- *); fi
printf '%s\n' "$sums" > SHA256SUMS
echo "Release $version is ready in $out/ (not published)"
