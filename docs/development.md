# Development

## Requirements

- Go 1.27 or newer
- Git
- Docker with Buildx, for the end-to-end test and local servers. Docker Desktop
  works.

## Build and test

```sh
make build    # bin/deployer and bin/deployerd
make check    # gofmt, go vet, and all tests with the race detector (what CI runs)
make e2e      # the end-to-end test against your Docker daemon
make help     # every target
```

`make check` needs no Docker. The tests run a complete server in-process: the
real store, controller, dispatcher, and API, with fake Docker and Git
(`internal/testenv`, `internal/fakes`). The `git` package tests against real
local repositories, and the installer tests run against a temporary root
directory with a fake host.

`make e2e` (`internal/daemon/e2e_test.go`) is the real thing. It cross-compiles
`deployerd` for your Docker host, builds the gateway image, runs the controller,
and uses the CLI to deploy two branches into real containers. It then checks
progress pages, routing, isolation and limits, logs, stop and start, extend,
destroy, and a controller restart. Only Git fetching is replaced by a fixture.
It removes everything it created, and takes about a minute on the first run.

## Run a local server

```sh
make dev
```

This builds the binaries and the `deployer-gateway:dev` image, then runs a
controller with [examples/dev-server.yaml](../examples/dev-server.yaml): API on
`127.0.0.1:7420`, previews at `http://NAME.localhost:8080`, and state in
`.deployer/`. On macOS the controller can't build its gateway image from its own
binary, so the config names the image `make gateway-image` builds.

In another terminal:

```sh
./bin/deployer login http://127.0.0.1:7420 --name local --token-stdin < .deployer/admin.token
./bin/deployer deploy --ref main --name hello      # inside this repository
curl http://hello.localhost:8080/
```

The repository's root `deployer.yaml` builds [examples/hello](../examples/hello),
so this checkout deploys itself. The server fetches from GitHub, so the branch
must be pushed. Stop the server with Ctrl-C. Remove its containers and state with
`./bin/deployerd cleanup --config examples/dev-server.yaml && rm -rf .deployer`.

## Conventions

- **The API contract lives in `internal/domain`.** JSON field names, error codes,
  and exit codes are compatibility promises: add, don't rename. Errors that
  reach users are `*domain.Error` values with a stable code and, where useful, a
  hint naming the next command to run.
- **The controller owns lifecycle rules**; the store only persists. Changes that
  must be atomic go through `store.Tx`.
- **Operation handlers must be safe to repeat**, because interrupted operations
  run again. Record progress as soon as it exists, and adopt resources an
  earlier attempt created.
- **Never touch Docker resources without this installation's labels.** No global
  prunes.
- **Subprocesses run without a shell**, through `internal/execx`.
- **Keep the CLI free of server code.** It must not import `store`, `controller`,
  or `api`.
- **Tests use the real thing where it's cheap**: real SQLite, real HTTP, real Git.
  Fakes stand in for Docker only.

## Releasing

Releases are built by GitHub Actions when a tag is pushed:

```sh
git tag v0.2.0
git push origin v0.2.0
```

The [release workflow](../.github/workflows/release.yml) runs `make check`, then
`scripts/release.sh`. That script builds `deployer` for macOS, Linux, and Windows
(`.exe`), and `deployerd` for Linux, on amd64 and arm64. It also writes an
`install.sh` and an `install.ps1` that default to that version, plus `VERSION`
and `SHA256SUMS`, and the workflow publishes everything as a GitHub release. Tags with a suffix, such as
`v0.2.0-rc.1`, become prereleases. GitHub's `latest` URL skips those.

To build release files locally without publishing: `make release VERSION=v0.2.0`.
Test them with the installer's `--from` option:

```sh
sh dist/install.sh --from "$PWD/dist" --bin-dir /tmp/deployer-test
```

## Windows

The CLI runs on Windows; the server doesn't. The packages the CLI is built from
must build there, which `make check` verifies with `GOOS=windows go vet`. Keep
platform differences in `_windows.go` and `_unix.go` files, as `secretfile`,
`execx`, and `sshtunnel` do. CI also runs those packages' tests on Windows, then
[installs and upgrades the CLI](../scripts/test-windows-install.ps1) with
`install.ps1` the way a user would.
