VERSION ?= dev
LDFLAGS := -s -w -X github.com/filstawski/deployer/internal/version.Version=$(VERSION)
export CGO_ENABLED := 0
# The packages the CLI is built from. They also build, and pass their tests, on
# Windows; the server's packages only run on Linux.
CLIENT_PACKAGES = $(shell go list -deps ./cmd/deployer | grep '^github.com/filstawski/deployer')

.DEFAULT_GOAL := help
.PHONY: help build test check fmt e2e gateway-image dev release clean

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build bin/deployer and bin/deployerd
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/ ./cmd/...

test: ## Run the unit and integration tests
	CGO_ENABLED=1 go test -race ./...

check: ## Check formatting, vet, and run tests (what CI runs)
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "Run make fmt"; exit 1; }
	go vet ./...
	GOOS=windows go vet $(CLIENT_PACKAGES)
	CGO_ENABLED=1 go test -race ./...

fmt: ## Format the code
	gofmt -w .

e2e: ## Run the end-to-end test against the local Docker daemon
	DEPLOYER_DOCKER_TEST=1 go test -count=1 -timeout 15m -run TestEndToEnd -v ./internal/daemon/

gateway-image: ## Build deployer-gateway:dev for the local Docker daemon
	./scripts/gateway-image.sh

dev: build gateway-image ## Run a local controller (see docs/development.md)
	./bin/deployerd serve --config examples/dev-server.yaml

release: ## Build release assets into dist/ (make release VERSION=v1.2.3)
	./scripts/release.sh $(VERSION)

clean: ## Remove build output
	rm -rf bin dist
