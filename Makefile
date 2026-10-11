GO ?= go
VERSION ?= dev
export GOCACHE := $(CURDIR)/.cache/go-build
export GOMODCACHE := $(CURDIR)/.cache/go-mod

.PHONY: deps build test check smoke fmt clean
deps:
	$(GO) mod tidy
build:
	$(GO) build -trimpath -ldflags '-X deployer/internal/version.Version=$(VERSION)' -o bin/ ./cmd/...
test:
	$(GO) test -race ./...
smoke: build
	python3 scripts/smoke.py
check:
	@test -z "$$($(GO) fmt ./...)" || (echo 'Formatting changes were required'; exit 1)
	$(GO) vet ./...
	$(GO) test -race ./...
fmt:
	$(GO) fmt ./...
clean:
	$(GO) clean ./...

.PHONY: gateway-image docker-test
gateway-image:
	python3 scripts/gateway-image.py
docker-test: gateway-image
	DEPLOYER_DOCKER_TEST=1 $(GO) test -race -count=1 -timeout 10m ./internal/deployments -run TestDockerLifecycle -v

.PHONY: release install-test
release:
	python3 scripts/release.py --version $(VERSION)
install-test:
	python3 scripts/test-install.py
