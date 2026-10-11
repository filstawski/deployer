# Run the current implementation

The initial control plane works on macOS and Linux. It supports authenticated local CLI/API access, project registration, scoped API tokens, and durable public-repository verification. The opt-in Docker runtime adds branch previews and lifecycle management; follow [the Docker preview guide](docker-previews.md). Saved SSH connections and existing-certificate HTTPS setup are available through the [installer guide](installation.md); real-host acceptance and certificate automation remain pending.

## Build and check

Install Go 1.27 or newer and Git. Docker is not required for the control-only example below.

```sh
make deps
make build
make check
make smoke
```

Build/test caches stay in the ignored `.cache/` directory. `make check` runs formatting validation, static analysis, and race-enabled tests. Integration tests open temporary loopback HTTP servers. The three binaries are written to `bin/`.

`make smoke` also requires Python 3. It launches the built binaries on a temporary loopback port, verifies authentication/idempotency and durable jobs, restarts the server, and checks persistence and shutdown. It makes no external network requests and removes its temporary state afterward. The implemented API contract is in [api/openapi.json](../api/openapi.json).

## Start the API

From the repository root, initialize the first administrator credential:

```sh
./bin/deployerd token bootstrap \
  --config examples/server.local.yaml \
  --output .deployer/operator.token
```

The token is saved to a new owner-only file and never printed. Bootstrap is allowed once per state database; it will not overwrite an existing token file. The default credential expires after 720 hours (30 days). The database stores only a SHA-256 token hash.

Start the server in its own terminal:

```sh
./bin/deployerd serve --config examples/server.local.yaml
```

It listens on `127.0.0.1:7420` and persists state under `.deployer/state/`. The example's relative state path resolves against the example file, not the process working directory. Only one controller may use a given state directory. Ctrl-C/SIGTERM gracefully stops the API and worker; accepted incomplete work remains recoverable.

## Connect the CLI

Keep the development context local to this checkout:

```sh
./bin/deployer --contexts-file .deployer/contexts.json \
  server add local \
  --url http://127.0.0.1:7420 \
  --credential-file .deployer/operator.token

./bin/deployer --contexts-file .deployer/contexts.json server status
```

Alternatively pass the connection explicitly (global options work before or after a command):

```sh
export DEPLOYER_SERVER=http://127.0.0.1:7420
export DEPLOYER_TOKEN_FILE="$PWD/.deployer/operator.token"

./bin/deployer server status --json
```

These environment variables contain a URL and credential **path**, never the credential value. The contexts file also stores only credential references. Without `--contexts-file`, contexts live under the OS user-configuration directory in `deployer/contexts.json`.

Changing the server URL requires an explicit token-file override; the CLI refuses to silently reuse a saved server's credential for a different destination. Named context switching uses `server use NAME`. The client allows unencrypted HTTP only on loopback and refuses redirects, so it does not forward bearer credentials to redirect targets.

## Register and verify a project

```sh
./bin/deployer project add sample \
  --repo https://github.com/octocat/Hello-World.git \
  --idempotency-key register-sample --json

./bin/deployer project list --json
./bin/deployer project inspect sample --json

./bin/deployer project verify sample \
  --idempotency-key verify-sample-1 \
  --wait --timeout 40s --json
```

This example repository demonstrates Git access, not a deployable application. Verification runs `git ls-remote` on the server with isolated Git configuration, without the developer's credential helper. It currently supports public HTTPS repositories only, disallows Git redirects, and requires a readable default-branch HEAD. Private SSH URLs can be registered but verification returns `UNSUPPORTED_FEATURE` until server-side key enrollment is implemented.

Verification is a real persisted operation with a bounded queue and single worker. Reusing an idempotency key with the same input returns the original operation; different input produces `IDEMPOTENCY_CONFLICT`. Use a new key to explicitly verify again after a completed attempt.

Without `--wait`, the CLI returns the operation ID immediately. Inspect it with:

```sh
./bin/deployer operation inspect OPERATION_ID --wait --timeout 40s --json
```

A client wait timeout does not cancel the job. The error includes the operation ID; reconnect and inspect it. Interrupted verification jobs are requeued on controller restart, subject to their original deadline and a maximum of three execution attempts. Docker deployment operations also persist commit/image checkpoints and adopt only their owned resources on retry.

## Create restricted access

Use the project ID returned by registration/listing:

```sh
./bin/deployer token create project-reader \
  --projects PROJECT_ID \
  --scopes projects:read \
  --ttl 24h \
  --output .deployer/project-reader.token --json
```

Supported scopes: `server:read`, `projects:read`, `projects:write`, `deployments:read`, `deployments:write`, and `admin`. Project grants are explicit project IDs or `*`. Creating projects requires `projects:write` and `*`. Verification requires `projects:write` for the target; waiting/inspecting requires `projects:read`. Token issuance/listing/revocation requires `admin`. Deployment reads/logs/operation inspection require `deployments:read`; deployment creation and lifecycle commands require `deployments:write`. Per-token resource/lifetime budgets are future work; server-wide limits apply today.

```sh
./bin/deployer --token-file .deployer/project-reader.token project list --json
./bin/deployer token list --json
./bin/deployer token revoke TOKEN_ID --json
```

Revocation is checked on every API request. Token creation returns the new secret once over the authenticated API; the CLI saves it directly and prints only metadata. Existing output files are never overwritten. Filesystem access to the server's protected state directory is the local administrative trust boundary.

If all operator credentials expire, the host administrator can issue a new one locally with `deployerd token create --config FILE --output NEW_FILE`. This requires access to the protected state directory and does not expose a remote recovery endpoint.

## Machine output and limitations

`--json` emits one versioned result/error envelope to stdout. Diagnostics go to stderr. Exit codes: 0 success, 1 general/operation failure, 2 invalid or unsupported input, 3 authentication/authorization failure, 4 client wait timeout. API request IDs appear in HTTP headers/envelopes and server error logs.

With the control-only example, the API reports `deployment_runtime: disabled`. Enabling the runtime starts a Docker gateway that serves preview traffic and lifecycle pages. The gateway has a separate authenticated control listener; preview visitors cannot call management operations. It reserves `/_deployer/*` for status, waiting-page assets, and non-sensitive health.

See [Docker previews](docker-previews.md) for application setup and management commands. The sample in `examples/hello` provides an internal `/health` route and identifies the exact deployment commit.

Next: real Linux installer acceptance, credential pairing, private repository enrollment, certificate automation, and protected previews. Compose/database stacks and idle sleep follow the server pilot.
