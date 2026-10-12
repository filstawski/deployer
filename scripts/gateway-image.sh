#!/bin/sh
# Builds deployer-gateway:dev for the local Docker daemon. Development servers
# reference it with gateway.image (see examples/dev-server.yaml); installed
# servers build their gateway image themselves. Keep the Dockerfile in sync
# with GatewayDockerfile in internal/container/docker/infra.go.
set -eu
arch=$(docker version --format '{{.Server.Arch}}')
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go build -trimpath -o "$dir/deployerd" ./cmd/deployerd
cat > "$dir/Dockerfile" <<'DOCKERFILE'
FROM scratch
COPY deployerd /deployerd
USER 65532:65532
ENTRYPOINT ["/deployerd", "gateway"]
DOCKERFILE
docker build --quiet --tag deployer-gateway:dev "$dir" >/dev/null
echo "Built deployer-gateway:dev for linux/$arch"
