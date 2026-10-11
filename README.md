# Deployer

Self-hosted, temporary Docker deployments for Git branches, designed for developers and AI agents.

The planned first installation uses `surdic.run`; the product works with a configurable base domain.

**Current status:** development pilot. Deploy public or private GitHub branches through an authenticated HTTPS API, with separate preview URLs, build/start status, logs, stop/wake/extend/destroy, and expiration. Private GitHub repositories share one server-owned GitHub App connection. Server/client installers and server upgrades are implemented. Idle sleep and automatic pull-request deployments remain pending.

## Install on your server

The current release is **v0.1.0-dev.3**, a development pilot. Upgrade an existing installation using the [GitHub and upgrade guide](docs/github.md).

Open your normal SSH client, log in to the server as root, and run:

```sh
curl -fsSL https://github.com/filstawski/deployer/releases/download/v0.1.0-dev.3/install.sh \
  | sh -s -- server --version v0.1.0-dev.3 -- \
      --domain previews.example.com --gateway-port 8081
```

Replace `previews.example.com` with your domain. This command downloads and verifies the binaries directly on the server. You do not need SCP, SSH access from a laptop terminal, Go, or a source checkout.

Requirements: Linux with systemd, Docker Engine 28+, system-wide Buildx, Git, and the standard Docker group. Ports 7420, 7421, and the selected gateway port must be free on loopback. The installer creates a service account, persistent state, a private API credential, and a systemd service. It also installs the server-side CLI:

```sh
deployer server status --json
```

Public previews additionally need wildcard DNS and HTTPS. Without certificate flags the installer leaves nginx unchanged and reports that connecting the HTTPS edge is pending. Follow the [installation guide](docs/installation.md) for certificate integration; do not add a second wildcard nginx site if one already serves your domain.

## Install the laptop CLI

Run on your Mac or Linux PC:

```sh
curl -fsSL https://github.com/filstawski/deployer/releases/download/v0.1.0-dev.3/install.sh \
  | sh -s -- client --version v0.1.0-dev.3
```

The binary is installed in `~/.local/bin`. The installer prints a reminder if that directory is missing from PATH. Connect the laptop directly to the authenticated HTTPS API using the [manual deployment guide](docs/manual-deployments.md). No local SSH key or tunnel is needed. Use your existing SSH app for the one-time server configuration.

## Connect GitHub once, deploy any branch

Follow the [GitHub connection guide](docs/github.md) to create your server's GitHub App and select repositories. Then run inside any connected project with a pushed Dockerfile and `deployer.yaml`:

```sh
deployer deploy --ref main --ttl 4h --wait
deployer deploy --ref design/a --ttl 4h --wait
```

The CLI detects the Git remote and registers or reuses the project. No per-repository GitHub token is needed. See the [project preparation guide](docs/manual-deployments.md#4-prepare-a-project) for the recipe.

## Build from source

With Go 1.27+ and Git installed:

```sh
make deps
make build
make check
```

Follow [the control-plane setup guide](docs/development.md) to bootstrap a credential, then [the Docker preview guide](docs/docker-previews.md) to run deployments. Build outputs are `bin/deployer`, `bin/deployerd`, and `bin/deployer-gateway`.

## Project documents

- [Manual deployment guide: HTTPS connection, tokens, recipes, and commands](docs/manual-deployments.md).

- [Client/server installers and release status](docs/installation.md).
- [Local setup and authentication](docs/development.md).
- [Docker previews: application recipe, commands, limits, and tests](docs/docker-previews.md).
- [Server pilot with existing nginx and Cloudflare](docs/server-pilot.md).
- [Implementation roadmap](ROADMAP.md): ordered milestones, release boundaries, acceptance criteria, and the first ten development tasks.
- [Implementation architecture](docs/ARCHITECTURE.md): repository layout, process responsibilities, persistence, operations, and verification.
- [Product concept](CONCEPT.md): complete behavior, project setup, server layout, and future review workflows.

- [Server configuration schema](schemas/server.schema.json) and [project recipe schema](schemas/project.schema.json).
- [Implemented OpenAPI contract](api/openapi.json).

Laptop and server CLI commands use the same authenticated API. This remains a development release: browser enrollment against your real GitHub account and a live server upgrade require the administrator steps in the guide.
