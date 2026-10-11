# Local Docker previews

The local deployment slice runs one HTTP service per deployment. Each preview gets an immutable Git commit, a built image, a private network, a hostname, a readiness check, and a lease. The CLI and API manage the same persistent state.

## Start the runtime

Requirements: Go 1.27+, Git, Python 3 for the build helper, and a local Linux Docker engine with Buildx. The Docker network isolation option requires a recent engine; this slice was validated with Docker Engine 29 on Docker Desktop (ARM64). Production Linux-host validation is still part of the installer milestone. Docker Desktop must share the state directory with its VM. A remote Docker context is not supported by the token-file mount.

```sh
make deps
make build
make gateway-image
```

If this installation has not been bootstrapped yet:

```sh
./bin/deployerd token bootstrap \
  --config examples/server.docker.yaml \
  --output .deployer/operator.token
```

The Docker and control-only examples share the same state directory; keep your existing token if you already bootstrapped it. Stop an existing controller before starting:

```sh
./bin/deployerd serve --config examples/server.docker.yaml
```

The first start creates a dedicated BuildKit builder and may download its image. Startup fails with a useful error if Docker or the gateway image is missing. The management API binds to `127.0.0.1:7420`, preview ingress to `127.0.0.1:8080`, and gateway control to `127.0.0.1:7421`. The gateway has a separate credential and no Docker socket.

In another terminal:

```sh
export DEPLOYER_SERVER=http://127.0.0.1:7420
export DEPLOYER_TOKEN_FILE="$PWD/.deployer/operator.token"
./bin/deployer server status --json
```

## Prepare an application repository

Commit a `Dockerfile` and `deployer.yaml`. This is the complete supported recipe:

```yaml
version: 1
project: demo                    # Optional informational name.
source:
  dockerfile: Dockerfile         # Relative to repository root.
  context: .                    # Relative to repository root; default: .
http:
  service: web                   # Only web is supported in this slice.
  port: 3000                     # The container's internal listening port.
  healthcheck:
    path: /health                # Default: /health. Must return HTTP 2xx.
    timeout: 90s                 # Default: 90s. Allowed: 1s–5m.
environment:
  NODE_ENV: production           # Ordinary settings; do not commit secrets.
```

The process must listen on **`0.0.0.0`**, using the configured port. Deployer provides `PORT` and `DEPLOYER_COMMIT`; the latter identifies the exact fetched revision. Define the start command in the Dockerfile's `CMD` or `ENTRYPOINT`. The app does not implement HTTPS, routing, or a Deployer API. Readiness runs from the gateway against the real application port, so listening on container-local `127.0.0.1` fails readiness.

The recipe is deliberately strict: unknown fields fail, and paths/symlinks cannot escape the checkout. Build context and Dockerfile paths are independent, which supports monorepos. See this repository's root [deployer.yaml](../deployer.yaml) and [hello app](../examples/hello). Once this repository is hosted at a public HTTPS Git URL, its root recipe builds that sample.

Only public HTTPS repositories are supported today. Branch names such as `design/a` and full commit SHAs work. The server fetches source into its own checkout, records the commit before building, and reuses that commit on retries even if the branch moves. It does not use laptop source files, host credential helpers, Git submodules, or private SSH credentials.

## Deploy and manage

Replace the repository URL and branch names with a real repository containing the recipe:

```sh
./bin/deployer project add demo --repo https://YOUR_GIT_HOST/YOUR_TEAM/YOUR_REPO.git

./bin/deployer deploy demo --ref design/a --subdomain design-a \
  --ttl 1d --idempotency-key demo-a-001 --wait --json
./bin/deployer deploy demo --ref design/b \
  --ttl 4h --idempotency-key demo-b-001 --wait --json

./bin/deployer list --json
./bin/deployer inspect DEPLOYMENT_ID --json
./bin/deployer logs DEPLOYMENT_ID --build --json
./bin/deployer logs DEPLOYMENT_ID --json
./bin/deployer extend DEPLOYMENT_ID --by 1d --wait --json
./bin/deployer stop DEPLOYMENT_ID --wait --json
./bin/deployer wake DEPLOYMENT_ID --wait --json
./bin/deployer destroy DEPLOYMENT_ID --yes --wait --json
```

Deploy and lifecycle commands return durable operation records. Without `--wait`, creation returns an operation immediately with a `deployment_id`; inspect that deployment for its URL. With `--wait`, the completed operation's `result` contains the deployment. The default wait limit is 20 minutes for deployment and 5 minutes for lifecycle actions. A client timeout leaves the job running. Continue with `operation inspect OPERATION_ID --wait`.

Provide a stable `--idempotency-key` when an agent may retry a request after an ambiguous network failure. The CLI generates a fresh key when omitted. The same key and input returns the original operation; a changed request conflicts. Only one lifecycle operation per preview may be pending. Build jobs are serialized while already-running previews remain available.

URLs look like `http://design-a.localhost:8080`. Omit `--subdomain` for a generated name. If your local resolver does not support wildcard `.localhost`, test using the Host header:

```sh
curl -H 'Host: design-a.localhost' http://127.0.0.1:8080/
```

Names remain reserved after destruction in this first slice. Reusable aliases and hostname release policy are future work.

## Visitor behavior and lifetime

Browser page visits show a neutral status page while queued, building, or starting. It polls readiness, then reloads the original URL including path and query. API requests and POSTs receive JSON and HTTP 503; the gateway never automatically replays them. Failure ends polling without exposing logs. A manually stopped deployment stays offline until its owner runs `wake`.

The TTL starts at the **first successful readiness check**, not at submission or build start. It may be 1 second to 7 days. `extend` adds time to an active lease, with a maximum resulting deadline of 7 days from now. `stop` does not pause that deadline, and `wake` reuses the built image without resetting it. Expired deployments cannot be extended or woken yet; create a fresh deployment.

At expiration the gateway returns HTTP 410 independently of the controller, and reconciliation stops the container. Requests proxied before an extension retain the deadline they started with. Stopped/expired images, containers, build logs, and source remain until the recorded purge deadline, normally seven days after expiry. Failed builds are retained for seven days. `destroy --yes` removes them earlier. Resource deletion checks installation/deployment ownership labels and never runs a global Docker prune.

## Limits and recovery

- Default admission limit: 8 retained deployments (configurable from 1 to 100). Destroy unused previews to release capacity.
- Application budget: 1 CPU, 512 MiB RAM, 256 processes; logs rotate at 5 MiB × 2. CLI logs return the latest 500 lines, bounded to 1 MiB. Build logs are bounded to the first 1 MiB.
- One build at a time in a dedicated 2 CPU / 2 GiB BuildKit worker, with a 20-minute operation deadline including queue time. Its cache persists; disk quota/admission and builder cache maintenance remain installer work.
- Each application has a distinct internal bridge network with no published host ports or outbound runtime access. The shared gateway joins that network. Builds can access dependency registries. Applications that require external APIs at runtime need a future explicit egress policy.
- The gateway is public to visitors who can reach ingress; protected previews are not implemented. All management routes require scoped bearer tokens. Use trusted application source: this is container isolation on one host, not a hostile-code sandbox.
- Controller restarts recover interrupted jobs using commit/image checkpoints and deterministic, labelled resource names. Existing healthy containers keep serving; snapshots restore gateway routing. If an application exits or the Docker host reboots, this slice marks it failed and requires explicit `wake` while its lease is active. Automatic host-reboot recovery is still pending.
- No idle sleep/wake-on-request, private Git key enrollment, Compose/database stacks, secret references, prebuilt-image recipes, or automatic certificate issuance yet. Installer, existing-certificate HTTPS, and automatic SSH connections are documented in the [installation guide](installation.md). Unsupported recipe fields fail explicitly.
- The local gateway image and configuration are development assets. Automatic infrastructure upgrades and changed-port migration are not supported yet; retain the existing configuration between starts.

## Verification

```sh
make check
make smoke
make docker-test
```

`make check` covers authentication/project boundaries, transactional admission/idempotency, Git commit pinning, recipe boundaries, startup failure, interrupted-build recovery, expiration, and gateway behavior. `make smoke` tests the built CLI/controller processes and persistence without Docker.

`make docker-test` builds the gateway and a small application, then exercises authenticated CLI/API calls through real Docker builds and containers: two revision-specific previews, status pages, isolation/limits, logs, extension, stop/wake, route reconstruction, expiration, and destruction. It uses deterministic source fixtures; separate Git tests fetch real local Git branches and verify pinned retries. It does not prove private or public remote repository connectivity. Test containers, networks, per-preview images, and the dedicated test builder are removed afterward. The reusable gateway image, BuildKit download, and local build caches remain.
