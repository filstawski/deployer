# Preparing a project

Deployer builds your app from a Dockerfile and runs it as one HTTP service. A
repository needs three things:

1. a **Dockerfile** that builds the app and starts it;
2. an app that **listens on `0.0.0.0`** on a known port inside the container;
3. a **`deployer.yaml`** at the repository root.

The server builds what is pushed to the Git host, so commit and push all three.

## Create deployer.yaml

In the repository:

```sh
deployer init        # reads the port from the Dockerfile's EXPOSE line
deployer validate    # checks the file and that its paths exist
```

`init` writes a file like this:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/filstawski/deployer/main/schemas/deployer.schema.json
version: 1
source:
  dockerfile: Dockerfile
  context: .
http:
  port: 3000
  healthcheck:
    path: /health
    timeout: 90s
```

The first line enables completion and validation in editors that use the YAML
language server, such as VS Code with the Red Hat YAML extension.

## Reference

| Field | Default | Meaning |
| --- | --- | --- |
| `version` | required | Always `1`. |
| `source.dockerfile` | required | Path of the Dockerfile, relative to the repository root. |
| `source.context` | `.` | Build context directory, relative to the repository root. |
| `http.port` | required | Port the app listens on inside the container. |
| `http.healthcheck.path` | `/health` | Path checked before the preview receives traffic. |
| `http.healthcheck.timeout` | `90s` | How long the app may take to become healthy, from `1s` to `5m`. |
| `environment` | none | Plain environment variables for the app. |
| `http.service` | `web` | Reserved for multi-service support; must be `web`. |
| `project` | none | Informational; ignored by the server. |

Unknown fields are errors, so a typo cannot silently change behavior. Paths
cannot leave the repository, including through symlinks.

## What the app gets

Deployer sets these variables. You cannot override them in `environment`:

| Variable | Example |
| --- | --- |
| `PORT` | `3000`, the value of `http.port` |
| `DEPLOYER_URL` | `https://design-a.previews.example.com` |
| `DEPLOYER_REF` | `design/a` |
| `DEPLOYER_COMMIT` | `7d63b83b6f7e029c4be79244204d87c9ef71a460` |
| `DEPLOYER_DEPLOYMENT_ID` | `dep_k7m2q9x4abcd` |

Requests arrive through the gateway with the original `Host` header, plus
`X-Forwarded-Proto: https`, `X-Forwarded-Host`, and `X-Forwarded-For`. If your
framework builds absolute URLs, configure it to trust these headers. Frameworks
that check allowed hosts must allow `*.previews.example.com`.

`DEPLOYER_URL` is only available when the container runs, not during the build.
Values compiled into a frontend bundle at build time cannot depend on it; read
the URL at runtime, or use relative URLs.

## The health check

Before routing traffic to a preview, Deployer requests `http.healthcheck.path`
from inside the preview's network until it gets a status below 400. The
lifetime starts at that moment. Visitors see a progress page until then.

A dedicated endpoint that answers quickly once the app can serve requests works
best. `/` is fine for static sites. If the check doesn't pass within the
timeout, the deployment fails with `HEALTHCHECK_FAILED`. The two usual causes:

- **The app listens on `127.0.0.1` or `localhost`.** That address only exists
  inside the container. Bind to `0.0.0.0`, or pass the framework's host flag,
  such as `--host 0.0.0.0`.
- **The port is wrong.** `http.port` must match the port the app actually
  listens on. Reading `PORT` from the environment keeps the two in sync.

`deployer logs NAME` shows the app's output, and `deployer logs NAME --build`
shows the build.

## Examples

### Node.js

```dockerfile
FROM node:22-alpine
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build
ENV NODE_ENV=production HOST=0.0.0.0
EXPOSE 3000
CMD ["npm", "start"]
```

```yaml
version: 1
source:
  dockerfile: Dockerfile
http:
  port: 3000
  healthcheck:
    path: /
```

Add a `.dockerignore` so that local dependencies and secrets stay out of the build:

```text
.git
node_modules
.env*
dist
```

### Static site

Build the site, then serve the output with nginx:

```dockerfile
FROM node:22-alpine AS build
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM nginx:1.27-alpine
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
```

```yaml
version: 1
source:
  dockerfile: Dockerfile
http:
  port: 80
  healthcheck:
    path: /
```

For single-page apps that use client-side routing, add an nginx config with
`try_files $uri /index.html;`.

### Go

See [examples/hello](../examples/hello): a multi-stage build that produces a
static binary in a `scratch` image.

### Monorepos

The Dockerfile and the build context are independent, so an app in a subdirectory
can still use shared packages from the repository root:

```yaml
version: 1
source:
  dockerfile: apps/web/Dockerfile
  context: .
http:
  port: 3000
```

## Builds

- Builds run in a dedicated BuildKit builder with 2 CPUs and 2 GiB of memory,
  one at a time by default. Others wait with the state `queued`.
- Builds can download packages from the internet. Build layers are cached
  between deployments.
- A build that takes longer than 20 minutes fails. The server setting is
  `deployments.build_timeout`.
- Git submodules and Git LFS files are not fetched.
- The `.git` directory is not part of the build context.

## Runtime limits

Each preview runs with these limits:

- 1 CPU, 512 MiB of memory, and 256 processes. The server settings are
  `deployments.cpus`, `memory_mb`, and `pids`.
- **No outbound network access.** The app can't call external APIs unless the
  server sets `deployments.egress: true`.
- A private network shared only with the gateway; no published host ports.
- Docker's default capabilities, minus those apps rarely need (such as raw
  sockets), and no privilege escalation.
- Container logs rotate at 10 MB, three files.

A container's filesystem lasts as long as the deployment, across stops and
starts, but nothing is kept after it is destroyed. Previews have no persistent
volumes or databases yet.

## Secrets

`deployer.yaml` is committed to the repository, so never put secrets in
`environment`. Deployer has no secret store yet; see the [roadmap](../ROADMAP.md).
Until then, use test credentials that are safe to commit, or keep previews of apps
that need real credentials off this server.
