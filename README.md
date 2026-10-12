# Deployer

Self-hosted preview environments for Git branches. Give it a branch, get a URL,
and it cleans up after itself.

```console
$ deployer deploy --ref design/a --name design-a --ttl 3d
Deploying design/a as design-a → https://design-a.previews.example.com
✓ design-a is live at https://design-a.previews.example.com
  Ref      design/a @ 7d63b83b6f7e
  Expires  2026-10-15 14:05 (in 3d)
```

Deployer runs on one Linux server with Docker. Each deployment is built from an
exact commit, runs in its own container and private network, gets its own HTTPS
hostname, and expires after a lifetime you choose. It is built for developers
sharing feature previews and design variants, and for AI agents that need a
predictable, scriptable way to put a branch online.

- **One command.** `deployer deploy` in a repository deploys its current branch.
  The project is registered on first use.
- **Isolated.** Every preview has its own container, network, and resource limits.
  Previews cannot reach each other.
- **Temporary by default.** Lifetimes start when the app is ready. Expired
  previews stop, and are removed a week later.
- **Friendly for visitors.** While a preview builds, its URL shows a progress page
  that opens the app once it is ready. Stopped, expired, and failed previews say
  so.
- **Friendly for agents.** Every command has `--json` output and stable exit codes
  and error codes, and accepts idempotency keys for safe retries.
- **Private repositories.** A GitHub App reads only the repositories you
  select, using short-lived, read-only tokens.

## How it works

```text
 deployer CLI ──HTTPS──▶ nginx ──▶ deployerd (API, builds, lifecycle) ──▶ Docker
                                        │ routes
 Visitor ────────HTTPS──▶ nginx ──▶ gateway ──▶ preview container (private network)
```

The `deployerd` controller fetches the commit, builds the image with BuildKit,
starts the container, and waits for its health check. A small gateway
container routes `<name>.<your-domain>` to the right preview and serves the
status pages. Your existing nginx terminates TLS for both. See
[architecture](docs/architecture.md) for details.

## Quick start

You need a Linux server with Docker Engine 28+, Buildx, Git, and nginx; a domain
whose wildcard DNS record points at the server; and a wildcard TLS certificate.
[Installation](docs/installation.md) covers each step.

**1. Install the server.** As root on the server:

```sh
curl -fsSL https://github.com/filstawski/deployer/releases/latest/download/install.sh | sh -s -- server \
  --domain previews.example.com \
  --tls-cert /etc/letsencrypt/live/previews.example.com/fullchain.pem \
  --tls-key  /etc/letsencrypt/live/previews.example.com/privkey.pem
```

Then create a token for your laptop, still on the server:

```sh
deployer token create laptop
```

**2. Install the CLI and log in.** On your Mac or Linux machine:

```sh
curl -fsSL https://github.com/filstawski/deployer/releases/latest/download/install.sh | sh
deployer login https://api.previews.example.com    # paste the token
```

**3. Deploy.** In a repository that has a Dockerfile:

```sh
deployer init        # writes deployer.yaml; commit and push it
deployer deploy
```

Private GitHub repositories need a one-time [GitHub connection](docs/github.md).

## Everyday commands

| Command | What it does |
| --- | --- |
| `deployer deploy [--ref BRANCH] [--name NAME] [--ttl 3d]` | Deploy a branch as a new preview |
| `deployer list` | List previews with their state, expiry, and URL |
| `deployer inspect NAME` | Show one preview, including why it failed |
| `deployer logs NAME [--build]` | Show the app's output, or the build log |
| `deployer stop NAME` / `start NAME` | Take a preview offline and bring it back |
| `deployer extend NAME --by 2d` | Keep a preview longer |
| `deployer destroy NAME` | Remove a preview now |
| `deployer upgrade` | Upgrade the CLI, or the whole server when run on it |

Every command accepts `--json`. Run `deployer --help` for the full list, or see
the [CLI reference](docs/cli.md).

## The project recipe

A project needs a Dockerfile and a `deployer.yaml` at the repository root:

```yaml
version: 1
source:
  dockerfile: Dockerfile
  context: .
http:
  port: 3000              # the app listens on 0.0.0.0:3000 in the container
  healthcheck:
    path: /health         # a status below 400 means ready
    timeout: 90s
environment:
  NODE_ENV: production
```

The app receives `PORT`, `DEPLOYER_URL`, `DEPLOYER_REF`, `DEPLOYER_COMMIT`, and
`DEPLOYER_DEPLOYMENT_ID`. [Preparing a project](docs/projects.md) has examples
for Node.js, static sites, and monorepos.

## Documentation

- [Installation](docs/installation.md): server setup, DNS and TLS, upgrades, and removal
- [Preparing a project](docs/projects.md): `deployer.yaml`, Dockerfiles, and health checks
- [CLI reference](docs/cli.md): every command, output formats, and exit codes
- [Private GitHub repositories](docs/github.md)
- [Guide for AI agents](docs/agents.md)
- [HTTP API](docs/api.md)
- [Architecture](docs/architecture.md): components, lifecycle, and security model
- [Development](docs/development.md): building, testing, and releasing Deployer
- [Roadmap](ROADMAP.md) and [design notes](docs/design.md)

## Limitations

Deployer is young. It currently supports:

- one public HTTP service per preview, built from a Dockerfile (no Compose, no
  prebuilt images yet);
- public HTTPS Git repositories, and private ones on GitHub;
- plain environment variables (no secret store yet), and no outbound network
  access from apps at runtime unless the server enables it;
- one server.

Previews are isolated with containers, which suits your own team's code. It is
not a sandbox for untrusted code.
