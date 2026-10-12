# Architecture

Deployer is two binaries and a few Docker resources on one Linux server.

```text
                       ┌──────────────────────── server ────────────────────────┐
 deployer CLI ─HTTPS─▶ │ nginx ─▶ deployerd ─────────────▶ Docker Engine       │
 (laptop, CI, agent)   │  :443    API :7420       docker CLI   │                │
                       │          SQLite                       ├─ builder (BuildKit)
                       │          dispatcher, reconciler       │                │
                       │             │ route snapshots,        │                │
                       │             ▼ health checks           │                │
 visitor ─────HTTPS──▶ │ nginx ─▶ gateway container ──────────▶ preview containers
                       │  :443    :8080 (control :7421)   one private network each
                       └────────────────────────────────────────────────────────┘
```

| Component | Role | Privileges |
| --- | --- | --- |
| `deployer` | The CLI. Calls the API; holds no state besides saved logins. | The user's token. |
| `deployerd serve` | The controller: API, database, operations, builds, lifecycle. Runs as the `deployer` user under systemd. | Docker socket, which is root-equivalent. |
| Gateway | `deployerd gateway` in a container. Routes visitors to previews and serves status pages. | No Docker access and no database. A control token, and membership in each preview's network. |
| Builder | A dedicated BuildKit container (`docker buildx`), limited to 2 CPUs and 2 GiB. | Builds images; never runs them. |
| Previews | One container per deployment, on its own internal network. | Resource limits, reduced capabilities, no egress by default. |
| nginx | Terminates TLS for `*.domain` and `api.domain`. Already present on the host. | — |

## Code layout

```text
cmd/deployer, cmd/deployerd   Thin entry points
internal/domain               Shared types, error codes, validation: the API contract
internal/config               server.yaml and deployer.yaml
internal/store                SQLite persistence and migrations
internal/controller           Deployment lifecycle: actions, operation handlers, reconciliation
internal/operations           Durable operation dispatcher
internal/container            Runtime interface; docker/ implements it with the Docker CLI
internal/gateway              Preview routing, status pages, control API and client
internal/git, internal/github Source fetching and the GitHub App
internal/api, internal/client HTTP API server and typed client
internal/cli                  The deployer command
internal/install              Server install, upgrade, and uninstall
internal/daemon               Wires the controller and gateway processes together
internal/fakes, testenv       Test doubles and an in-process server for tests
```

Packages depend downward: `cli` uses `client` and `domain` but never `store`, so
the CLI binary contains no server code.

## A deployment's life

| From | Event | To |
| --- | --- | --- |
| (new) | `deploy` | `queued` |
| `queued` | a build slot frees up | `building` |
| `building` | image built | `starting` |
| `starting` | health check passes | `running` |
| `queued`, `building`, `starting` | any step fails | `failed` |
| `running` | `stop` | `stopped` |
| `stopped`, `failed` (with an image) | `start` | `starting` |
| `running` | the container exits on its own | `failed` |
| `running`, `stopped`, `starting` | the lifetime ends | `expired` |
| `expired` | `extend` | `stopped` |
| any | `destroy`, or the retention period ends | `destroyed` |

1. **Admission.** `POST /v1/deployments` checks capacity, reserves the name,
   inserts the deployment, and queues a `deployment.create` operation, all in
   one transaction. The controller publishes routes, so the URL shows a progress
   page immediately.
2. **Build.** A handler waits for a build slot, fetches the exact commit into a
   fresh directory with an isolated Git configuration, loads `deployer.yaml`,
   and builds the image. The commit and image ID are recorded as soon as they
   are known.
3. **Start.** The handler creates the deployment's internal network, attaches the
   gateway to it, and creates and starts the container with resource limits and
   the injected environment.
4. **Health check.** The controller asks the gateway to probe the app. Only the
   gateway can reach preview networks. On the first success, `ready_at`,
   `expires_at`, and `purge_at` are set, and the route switches to the app.
5. **Expiry.** The gateway itself refuses traffic after `expires_at`, so expiry
   holds even if the controller is down. The reconciler then stops the container.
6. **Purge.** At `purge_at`, which is the retention period after expiry or
   failure, the reconciler queues a `destroy`, which removes the container,
   network, image, and logs.

## Operations

Anything that touches Docker or Git runs as an **operation**: a row in the
`operations` table, executed by the dispatcher.

- **Durable.** Operations survive client disconnects and controller restarts.
  On startup, operations left `running` are requeued. Handlers resume from
  recorded progress: the pinned commit is fetched again, and a recorded image is
  not rebuilt. After three interruptions, an operation fails with `INTERRUPTED`.
- **Ordered per deployment.** Different deployments' operations run
  concurrently. Each deployment's run one at a time, in order, while holding
  that deployment's lock. Builds also share a semaphore (`build_concurrency`),
  and the dispatcher has enough slots that queued builds can't hold up other
  operations.
- **Cancellable.** `destroy` cancels a deployment's queued operations and the
  running one's context, so a wrong branch can be abandoned mid-build.
- **Bounded.** Every operation has a deadline. Builds also have their own
  timeout.
- **Consistent.** A failure hook updates the deployment before the operation is
  marked finished, so a client that sees the operation end also sees its
  outcome.

The reconciler runs every 5 seconds. It skips deployments with pending
operations, and handles each of the others under that deployment's lock, after
reading it again, so it never acts on a stale view. It expires and purges deployments, stops
containers that should not be running, fails deployments whose operation ended
without an outcome, and republishes the gateway's routes, which heals a
restarted gateway. Its first pass after startup restarts deployments that should
be running, which covers host reboots. Preview containers have no Docker restart
policy, because the controller decides what runs.

## Persistence

SQLite in WAL mode, at `/var/lib/deployer/state.db`, with one connection, which
serializes access. Migrations are numbered and recorded in `PRAGMA user_version`;
a binary refuses to open a database newer than it understands.

| Table | Contents |
| --- | --- |
| `projects` | Name and canonical HTTPS repository URL |
| `deployments` | State, ref, commit, image, recipe settings, lease times, last error |
| `operations` | Kind, status, attempts, deadline, result or error |
| `tokens` | Name, SHA-256 hash of the secret, scopes, project grants, expiry, revocation |
| `idempotency_keys` | Token, key, request hash, and resulting resource, kept for 7 days |
| `events` | Audit log of who created, changed, and revoked what, kept for 90 days |

Deployment names are unique among live deployments, enforced by a partial unique
index, and are released on destroy.

## The gateway

The controller pushes the complete route table (`PUT /routes`) after every state
change and on every reconciliation. Each route has a host, a state, an optional
upstream, a health path, and an expiry. The gateway keeps the table in memory,
so preview traffic never touches the database.

For each request, the gateway looks up the `Host` header:

- running and not expired: reverse proxy to the container, keeping `Host` and
  setting `X-Forwarded-For`, `-Host`, and `-Proto`, plus
  `X-Robots-Tag: noindex, nofollow`. WebSockets and streaming work; long-lived
  connections end at expiry.
- otherwise: a status page for browser navigations, and a small JSON body with
  the same status code for everything else, so API calls never receive a
  misleading HTML 200. Progress pages poll `/_deployer/status` and reload the
  original URL once the app is ready.

`/_deployer/` is reserved on every preview host. Until it receives its first
route table, the gateway answers 503 for every host.

## Security model

- **Management access** requires a bearer token. Secrets are 256-bit random
  values; only their SHA-256 hashes are stored. Scopes limit actions, and
  project grants limit which projects a token sees. Resources a token may not see
  answer 404 rather than 403.
- **The API listens on loopback** and is published through nginx with TLS and
  rate limiting, or reached through SSH. The client refuses plain HTTP to
  non-loopback hosts and never follows redirects, so tokens cannot leak to
  another host.
- **Repository content is untrusted input.** Recipe paths cannot escape the
  checkout. Git runs without hooks, credential helpers, or redirects, over HTTPS
  only. `.git` is removed before building. GitHub tokens go to Git through a
  repository-scoped HTTP header in the environment, never in arguments, URLs,
  files, or the build context.
- **Previews are isolated from each other.** Each has its own internal network
  without a host address, shared only with the gateway. Containers have no
  published ports, resource limits, reduced capabilities, and
  `no-new-privileges`.
- **Ownership labels.** Deployer modifies only Docker resources labeled with its
  installation ID, and never runs a global prune.
- **Not a sandbox.** Containers share the host kernel, and the controller has
  Docker access, which is root-equivalent. Deploy code you trust.
