# Deploy projects manually

The normal connection is **local CLI → HTTPS → authenticated Deployer API**. An SSH key or tunnel is not needed for day-to-day deployment. Use your existing Termius session only for initial server administration.

## 1. Enable the HTTPS API once, on the server

The server must already have Deployer running on loopback and a wildcard certificate. For the current `surdic.run` installation, use your root Termius session:

```sh
curl -fsSL https://raw.githubusercontent.com/filstawski/deployer/main/scripts/setup-api.sh \
  | sh -s -- surdic.run 7420
```

The script checks the private API and nginx, adds `/etc/nginx/conf.d/deployer-api.conf`, validates it, and reloads nginx. It uses your existing wildcard certificate and DNS. Preview traffic continues through port 8081; management traffic uses the exact hostname `api.surdic.run` and the private API at 127.0.0.1:7420. Do not create a preview with subdomain `api`.

Management requests remain protected by Deployer bearer-token authentication. nginx adds a request-rate limit. Do not open firewall ports 7420 or 7421. The browser root at `https://api.surdic.run` is an informational message, not a login/dashboard page.

Test without credentials:

```sh
curl -i https://api.surdic.run/v1/server
```

Expected: HTTP **401** and a JSON `UNAUTHENTICATED` error. A preview-style 404 means the API route has not been loaded. A 502 means nginx cannot reach the private controller. A TLS error means the certificate/DNS path is not correct; do not bypass it with `-k`.

## 2. Create a laptop credential once, on the server

```sh
deployer token create mac --output /root/deployer-mac.token \
  --scopes server:read,projects:read,projects:write,deployments:read,deployments:write \
  --projects '*' --ttl 720h --json
cat /root/deployer-mac.token
```

This token can register projects and manage deployments for 30 days, but cannot create/revoke other tokens. Copy it from your own Termius window into the hidden laptop prompt below. Do not put it into a command argument, repository, screenshot, or chat. The server's bootstrap administrator token stays on the server. Keep the token ID from the first command if you need to revoke this laptop later.

An existing output file is not overwritten. If you previously issued this credential, use that file rather than issuing it again. After a successful connection, the temporary `/root/deployer-mac.token` transfer copy can be removed; keep the Mac's protected copy.

## 3. Connect the Mac over HTTPS

Install the current released client if it is not installed:

```sh
curl -fsSL https://github.com/filstawski/deployer/releases/download/v0.1.0-dev.3/install.sh \
  | sh -s -- client --version v0.1.0-dev.3
export PATH="$HOME/.local/bin:$PATH"
```

From this repository checkout:

```sh
python3 scripts/connect-client.py --url https://api.surdic.run --name pilot
```

Or download the helper on a machine without a checkout:

```sh
curl -fsSL https://raw.githubusercontent.com/filstawski/deployer/main/scripts/connect-client.py \
  -o /tmp/deployer-connect.py
python3 /tmp/deployer-connect.py --url https://api.surdic.run --name pilot
```

Paste the laptop token when prompted. Input is hidden. The helper verifies the authenticated HTTPS API, writes an owner-only credential file under `~/.config/deployer/credentials/`, and selects a saved `pilot` context. Running it again for the same HTTPS connection verifies and reuses the saved credential without prompting. It refuses to replace a context pointing elsewhere or an existing credential file. This is token enrollment; browser/device pairing is not implemented yet.

Then run locally:

```sh
deployer server status --json
deployer list
```

If your shell exports `DEPLOYER_SERVER` or `DEPLOYER_TOKEN_FILE`, unset them before using the saved context. Those variables deliberately override it.

To configure an existing credential file directly, without the helper:

```sh
deployer server add pilot --url https://api.surdic.run --credential-file /absolute/path/to/token
deployer server use pilot
```

## 4. Prepare a project

The released server fetches a **pushed Git branch or commit**. It does not upload your current laptop directory. Local uncommitted edits are not deployed.

Each project needs:

- A Dockerfile that builds and starts its HTTP application.
- A `.dockerignore` excluding `.git`, `.env*`, secrets, local caches, and dependency/build directories.
- A committed `deployer.yaml` with the Dockerfile, context, port, and readiness path.
- A process listening on `0.0.0.0` inside the container.

Example recipe:

```yaml
version: 1
source:
  dockerfile: Dockerfile
  context: .
http:
  service: web
  port: 3000
  healthcheck:
    path: /health
    timeout: 90s
```

The readiness path must return HTTP 2xx. Deployer provides `PORT` and `DEPLOYER_COMMIT`. Public HTTPS is handled on the server; do not add certificates to the application container. Static sites can build assets once and serve them with an internal HTTP server. Each deployment gets its own isolated container/network, so projects can share the same internal port.

## 5. Register and deploy

For private GitHub repositories, follow the [one-time GitHub App setup](github.md). That connection is reused across all selected repositories. You can then run `deployer deploy --ref main --ttl 4h --wait` inside a repository to register or reuse its project automatically. A Deployer API token authenticates your CLI; the server uses its GitHub App to fetch source.

For a public project with a committed recipe:

```sh
deployer project add example --repo https://github.com/OWNER/REPOSITORY.git
deployer project verify example --wait
deployer deploy example --ref main --subdomain example-main --ttl 4h --wait
```

Open the URL returned by the deployment. While building/starting, visitors see a waiting page. Omit `--subdomain` to generate a unique hostname. TTL starts when the deployment becomes ready.

For another branch, in another terminal or after the first command returns:

```sh
deployer deploy example --ref design/a --subdomain example-design-a --ttl 4h --wait
```

Both deployments can run concurrently. To submit without waiting, omit `--wait`; accepted work continues on the server after your CLI disconnects. With `--json`, output is a machine-readable envelope and operation/deployment IDs can be saved by an agent. For retries, use the same explicit `--idempotency-key` for the same request; use a new key for a new deployment.

## 6. Manage a deployment

Replace DEPLOYMENT_ID with the `dep_...` ID from `deployer list`:

```sh
deployer list
deployer inspect DEPLOYMENT_ID
deployer logs DEPLOYMENT_ID --build
deployer logs DEPLOYMENT_ID
deployer extend DEPLOYMENT_ID --by 2h --wait
deployer stop DEPLOYMENT_ID --wait
deployer wake DEPLOYMENT_ID --wait
deployer destroy DEPLOYMENT_ID --yes --wait
```

Stopping does not pause the expiry clock. Wake is supported before expiry. Idle sleep/wake-on-request and renewal of already expired deployments are not implemented. Destroy removes the deployment's owned resources. API tokens can be revoked on the server with `deployer token revoke TOKEN_ID`.

## 7. Troubleshooting

- **401:** missing, expired, or revoked API token. Issue a replacement from the server; do not disable authentication.
- **403:** the token lacks the requested scope/project grant.
- **SOURCE_FAILED:** check branch, URL, and repository visibility. Private source is unsupported in the current release.
- **Build failure:** use `deployer logs DEPLOYMENT_ID --build`.
- **Readiness failure:** verify the configured internal port, `0.0.0.0` binding, and health endpoint.
- **Preview 404:** unknown hostname, missing deployment, or a deleted deployment.
- **Expired preview:** extending the client wait timeout does not extend the deployment's TTL.

The API transport and the project source-access mechanism are separate. Neither a local SSH key nor an SSH tunnel is required for this HTTPS workflow.
