# How Deployer works

## The pieces

- **The server** (`deployerd`) runs on one Linux machine with Docker. It keeps
  its state in SQLite, fetches code from the Git host, builds images, runs one
  container per preview, and removes previews when they expire.
- **The gateway** routes `https://NAME.<domain>` to the right container, and
  shows a status page while a preview isn't serving. TLS is handled by the
  machine's existing reverse proxy, such as nginx.
- **The CLI** (`deployer`) is a thin client of the server's HTTP API. Every rule
  and limit lives on the server, so people, CI, and agents get the same
  behavior.

## Projects

A project is a registered Git repository with a name, such as `shop`. A
deployment always belongs to one project. `deployer deploy` inside a repository
finds the project by its `origin` remote and registers it on first use, which
needs a token with access to all projects. A project-scoped token can only use
the projects it was given.

Public repositories need nothing else. Private GitHub repositories need the
server's GitHub App, which an administrator sets up once.

## Deployments

A deployment, or preview, runs **one commit** of one project in its own
container. Pushing to the branch doesn't change it by itself:

- `deployer update NAME` moves it to the branch's latest commit, keeping its ID,
  name, URL, and lifetime. A running preview keeps serving while the new version
  builds, and keeps the previous version if the new one fails. Files the app
  wrote inside its container don't carry over.
- `deployer deploy` creates another deployment with a new name; the old one
  keeps running until it's destroyed or expires.

- **ID:** `dep_` plus 12 characters. Never reused.
- **Name:** the subdomain. Lowercase letters, digits, and hyphens, up to 63
  characters, except `api`, `www`, `admin`, `deployer`, and `status`. Unique
  among deployments that aren't destroyed, and released on destroy. Generated
  when not given: `PROJECT-REF-xxxxx`.
- **URL:** `https://NAME.<domain>`, reserved as soon as the deployment is
  accepted.

### States

```text
queued → building → starting → running ⇄ stopped
                                  ↓
                               expired
running → (update builds while serving) → starting → running
stopped or failed → (update) → building → starting → running
any of them → failed (after a failed build, start, or health check)
any of them → destroyed
```

| State | Meaning | Visitors get |
| --- | --- | --- |
| `queued` | Accepted, waiting for a build slot. Builds run one at a time by default. | 503, progress page |
| `building` | Fetching the commit and building the image. | 503, progress page |
| `starting` | The container runs; waiting for the health check. | 503, progress page |
| `running` | Healthy and serving. | The app |
| `stopped` | Taken offline by its owner. `start` brings it back without rebuilding. | 503 |
| `expired` | Its lifetime ended; the container is stopped. | 410 |
| `failed` | The build, start, or health check failed. `error` says why. | 503 |
| `destroyed` | Everything removed and the name released. Kept as history. | 404 |

## Lifetimes

- The lifetime (TTL) starts when the preview **first becomes healthy**, so slow
  builds don't use it up. Before that, `expires_at` is unset.
- The server sets the default and the maximum: `deployments.default_ttl_seconds`
  and `deployments.max_ttl_seconds` in `deployer status --json`. Out of the box
  they are 1 day and 7 days. Asking for more than the maximum is an
  `INVALID_INPUT` error.
- Visits never extend a lifetime, and stopping doesn't pause it.
- `extend --by` adds to the current expiry, up to the maximum from now. On an
  expired preview it starts a new lifetime from now and moves it to `stopped`;
  run `start` to bring it back.
- Expired and failed previews keep their resources for the retention period
  (7 days by default) after expiring or failing, then are destroyed
  automatically. `purge_at` shows when. A stopped preview follows its lifetime:
  it expires on schedule, then waits out the retention period.

## Operations

Every change (create, update, start, stop, destroy, verifying a project) is a durable
**operation** (`op_...`) that runs on the server, even if the CLI disconnects.
Commands wait for it by default; `--detach` returns it right away, and
`deployer operation wait ID --json` waits again later.

- A deployment runs one operation at a time. Another request fails with
  `OPERATION_IN_PROGRESS`, except `destroy`, which cancels what is running.
- Requests carry an **idempotency key**. Repeating a request with the same key
  returns the original result instead of doing the work twice. The CLI makes up
  a key for each run, so only `deploy --idempotency-key KEY` makes a retry of
  the whole command safe. Keys are kept for 7 days.
- An operation that fails records an `error` with a code, a message, and
  usually a hint. The deployment gets the same `error`.

## Limits

| Limit | Default | Server setting |
| --- | --- | --- |
| Deployments that aren't destroyed | 10 | `deployments.max_live` |
| Default and maximum lifetime | 1d and 7d | `deployments.default_ttl`, `max_ttl` |
| Retention after expiry or failure | 7d | `deployments.retention` |
| Builds at a time | 1 | `deployments.build_concurrency` |
| Build time | 20m | `deployments.build_timeout` |
| CPU, memory, and processes per preview | 1, 512 MiB, 256 | `deployments.cpus`, `memory_mb`, `pids` |
| Build resources | 2 CPUs, 2 GiB | fixed |
| Outbound network at runtime | none | `deployments.egress` |

Stopped, expired, and failed previews count toward `max_live`. Administrators
change these in `/etc/deployer/server.yaml`; agents can't.

## Isolation

Each preview runs in its own container on its own private network, shared only
with the gateway. It has no published ports, no privileged mode, and no
privilege escalation. It can't reach other previews, or the internet unless
egress is enabled. Its files last until it's destroyed, across stops and starts.
There are no persistent volumes, databases, or secrets yet.

## Tokens and access

Every request uses an API token (`dpl_...`). Tokens have scopes and may be
limited to certain projects:

| Scope | Allows |
| --- | --- |
| `server:read` | `deployer status` |
| `projects:read`, `projects:write` | Viewing, or registering and removing, projects |
| `deployments:read`, `deployments:write` | Viewing, or creating and changing, deployments |
| `admin` | Everything, including tokens and GitHub setup |

`deployer status --json` shows the current token under `token`. A
`FORBIDDEN` error means the token lacks a scope or a project; only a person with
an admin token can change that.
