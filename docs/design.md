# Design notes

Deployer's promise: **give it a branch, get a URL, keep it only as long as you
need it.** These notes record the principles behind the current design, and how
planned features are meant to behave, so that they fit what exists. The
[roadmap](../ROADMAP.md) lists what is built.

## Principles

- **One command, honest results.** A deploy returns a URL that works, or a clear
  reason it doesn't. The URL is reserved up front and explains itself while the
  app isn't ready.
- **Exact and repeatable.** A deployment is one commit, resolved when it is
  requested. Later pushes don't change it. Deploying again creates a new
  deployment; changing an existing one will be an explicit update.
- **Temporary by default.** Everything has a lifetime and is removed afterwards.
  Lifetimes start at readiness, so slow builds don't use them up. Traffic never
  extends them.
- **Safe to retry.** Every change is a durable operation with an idempotency
  key. Clients can disconnect, time out, and retry without creating duplicates.
- **Contracts for machines.** JSON output, stable error codes with hints, and
  exit codes are public API. Human wording may change; they don't.
- **The server decides.** The CLI is a thin client. Policy, limits, and state
  live on the server, which is why a laptop, CI, and an agent all behave alike.
- **Small and boring infrastructure.** One server, SQLite, Docker, and the nginx
  already there. Nothing distributed until it is needed.
- **Own only what you created.** Deployer touches only resources carrying its
  labels, and never prunes globally.

## Lifecycle vocabulary

These terms should keep their meaning as features are added:

| Term | Meaning |
| --- | --- |
| Lifetime (TTL) | Time from first readiness until expiry. |
| Expire | The lifetime ended. Traffic is refused with 410 and the container stops. |
| Stop / start | The owner takes a preview offline, or back online. It doesn't wake on visits. |
| Sleep / wake *(planned)* | Automatic idle stop, and starting again on the next visit. |
| Extend | Add to the lifetime, up to the server's maximum. Revives an expired preview. |
| Retention | How long stopped, expired, and failed previews keep their resources. |
| Destroy | Remove everything and release the name. Not reversible. |

## Planned features

### Idle sleep and wake on request

A preview with `idle_timeout` stops after that long without visitor requests,
and starts again on the next visit. The gateway counts only external requests,
and treats WebSockets and in-flight requests as activity. Its own status polling
doesn't count. While waking, browsers get the existing progress page, which
reloads the original URL. API calls and non-GET requests get 503 with
`Retry-After`, and are never replayed. Simultaneous visits share one wake
operation. Expired previews never wake. Lifetimes keep running while asleep.

### Secrets

Secret values live on the server, set with something like
`deployer secret set shop STRIPE_KEY`. `deployer.yaml` lists the names it needs:
`secrets: [STRIPE_KEY]`. They reach builds as BuildKit secret mounts, never as
build arguments, and reach containers as environment variables. Values never
appear in API responses, logs, or operation records.

### Multi-service apps

`source.compose: compose.preview.yaml` replaces `source.dockerfile`. Deployer
reads a documented subset of Compose and generates its own plan. It rejects host
networking, published ports, privileged mode, devices, bind mounts, and external
volumes or networks. Each preview gets private networks and its own named
volumes; only `http.service` joins the gateway's network. Readiness waits for
dependencies' health checks. Sleeping stops the whole stack, keeping volumes.

### Updating a preview

`deployer update NAME --ref design/a` builds the new commit while the old one
keeps serving, then switches. Stateful stacks may need a short maintenance
window. A failed build leaves the old version running. Updates never change the
lifetime, and reverting the image doesn't undo database migrations.

### Several previews at once

`deploy --ref a --ref b` creates independent deployments in one request, and
reports each one's outcome; one failure doesn't affect the others. Named sets in
`deployer.yaml` (`preview_sets: {review: {refs: [a, b, main]}}`) make a common
comparison one command.

### Reviews and aliases

A review page lists several previews with their commits and states, and
collects feedback tied to the exact revision reviewed, so feedback never moves
to newer code. An alias such as `acme.previews.example.com` points at one healthy
preview, and can move atomically. It doesn't extend lifetimes or prevent sleep.

### Protected previews

Per-preview access with a password, or a share link whose token is exchanged for
a host-only cookie and removed from the URL. Access checks run before status
pages, so a protected preview reveals nothing, and can't be woken, without
access.

### Presets and resettable data

A preset bundles the settings, lifetime, and seed data for a kind of demo.
`deployer data reset NAME` restores a preview's data to its seeded state, using
the project's own seed and reset commands, run as jobs inside the preview's
network. It never copies live database files.

### Agents

An MCP server will expose the same operations as tools, implemented as a thin
layer over the HTTP API, with no separate logic. The [agent guide](agents.md)
is the contract it will follow.
