# Deployer

Self-hosted, temporary Docker deployments for Git branches, designed for developers and AI agents.

The planned first installation uses `surdic.run`; the product works with a configurable base domain.

**Current status:** the local Docker deployment path works. The authenticated CLI can deploy Dockerfile applications from public HTTPS branches, route separate preview URLs, show build/start status, read logs, stop/wake/extend/destroy previews, and enforce expiration. Real Docker tests cover two simultaneous previews. Client/server installers, saved SSH connections, and existing-certificate nginx setup are implemented locally. Real Linux host acceptance, Cloudflare certificate automation, private Git enrollment, and idle sleep remain pending.

## Install on your server

The current release is **v0.1.0-dev.2**, a development pilot. Full Linux host acceptance is still in progress; server upgrades are not implemented yet.

Open your normal SSH client, log in to the server as root, and run:

```sh
curl -fsSL https://github.com/filstawski/deployer/releases/download/v0.1.0-dev.2/install.sh \
  | sh -s -- server --version v0.1.0-dev.2 -- \
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
curl -fsSL https://github.com/filstawski/deployer/releases/download/v0.1.0-dev.2/install.sh \
  | sh -s -- client --version v0.1.0-dev.2
```

The binary is installed in `~/.local/bin`. The installer prints a reminder if that directory is missing from PATH. Connect the laptop directly to the authenticated HTTPS API using the [manual deployment guide](docs/manual-deployments.md). No local SSH key or tunnel is needed. Use your existing SSH app for the one-time server configuration.

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

The next delivery target is a Linux server pilot under `surdic.run`: private repository enrollment, installation, SSH connection, and HTTPS. Laptop and server CLI commands use the same authenticated API.
