#!/bin/sh
# Builds release assets into dist/: binaries for each platform, install.sh
# pinned to this version, VERSION, and SHA256SUMS. It does not publish.
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
for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
  os=${target%/*}
  arch=${target#*/}
  for bin in deployer deployerd; do
    if [ "$bin" = deployerd ] && [ "$os" != linux ]; then continue; fi
    CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "$ldflags" \
      -o "$out/${bin}_${version}_${os}_${arch}" "./cmd/$bin"
    echo "built ${bin}_${version}_${os}_${arch}"
  done
done
sed "s/^default_version=latest$/default_version=$version/" install.sh > "$out/install.sh"
echo "$version" > "$out/VERSION"
cd "$out"
if command -v sha256sum >/dev/null 2>&1; then sums=$(sha256sum -- *); else sums=$(shasum -a 256 -- *); fi
printf '%s\n' "$sums" > SHA256SUMS
echo "Release $version is ready in $out/ (not published)"
