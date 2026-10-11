# Deployer implementation structure

This is the implementation companion to [the roadmap](../ROADMAP.md). It describes the target structure; [the development guide](development.md) and [Docker preview guide](docker-previews.md) distinguish implemented features from planned modules. Keep a single repository and Go module so the CLI, server, and gateway share versioned contracts without separate release coordination.

Current runtime: `internal/deployments` orchestrates durable jobs and lifecycle reconciliation; `internal/runtime/docker` executes shell-free Docker CLI argument arrays; `internal/gateway` provides routing and visitor pages. For the single-service slice the controller creates containers directly, without generated Compose or a Docker SDK. Each deployment has its own internal isolated bridge, with only the application and shared gateway attached. The gateway lives in Docker so it reaches application networks on both Docker Desktop and Linux. The controller runs on the host and publishes authenticated route snapshots over a loopback control port. Compose, private source credentials, idle tracking, and system installation remain planned. See the preview guide for current retention and recovery limits.

## Repository layout

```text
deployer/
  README.md
  CONCEPT.md
  ROADMAP.md
  go.mod
  go.sum
  Makefile

  cmd/
    deployer/main.go             # CLI, on laptops and the server
    deployerd/main.go            # API, workers, reconciliation
    deployer-gateway/main.go     # Routing, activity, access, wake requests

  internal/
    domain/                     # IDs, records, states, errors, policy types
    config/                     # Server/project config validation
    store/sqlite/               # Persistence and migrations
    auth/                       # Tokens, scopes, project authorization
    api/                        # Versioned HTTP handlers and DTOs
    client/                     # Shared API client used by the CLI
    cli/                        # Commands, output, local server contexts
    transport/                  # Direct API and SSH tunnel transports
    operations/                 # Durable jobs, step execution, idempotency
    projects/                   # Registration, configuration, credentials
    source/git/                 # Fetch, ref resolution, isolated workspaces
    build/                      # Build execution, timeouts, image identity
    runtime/docker/             # Docker/Compose adapter and resource ownership
    deployments/                # Deploy/update/stop and revision coordination
    lifecycle/                  # TTL, idle policy, desired-state decisions
    gateway/                    # Data-plane routing and activity tracking
    secrets/                    # Credential storage and scoped delivery
    artifacts/                  # Bounded logs and verification outputs
    maintenance/                # Reconciliation, retention, admission accounting

  api/openapi.json              # Implemented public /v1 contract
  schemas/
    project.schema.json
    server.schema.json

  deploy/
    server/                     # Operator Compose/Caddy assets
    systemd/                    # Service definition
    docker/                     # Image build recipes for Deployer itself
  templates/                    # Project init templates, introduced in M4
  examples/
    hello/                      # Minimal revision-identifying HTTP app
    database/                   # Added with Compose support
  tests/
    integration/                # API, database, Git, Docker integration
    e2e/                        # CLI → server → HTTPS/lifecycle scenarios
    fixtures/                   # Invalid recipes, Git fixtures, sample secrets
  docs/
    ARCHITECTURE.md
    installation.md             # Written with the installer
    operations.md               # Written as operational behavior is verified
    agent-guide.md              # Written against the implemented CLI
  .github/workflows/            # If GitHub hosts the repository
```

Create modules as they acquire behavior; the tree is an ownership map, not a requirement to populate empty packages. Keep entry points thin. Use interfaces at real external boundaries (storage, clock, Git/build/runtime, gateway publisher), avoiding a generic plugin system in the initial version.

## Process boundaries

| Process | Responsibilities | Privileged access |
| --- | --- | --- |
| `deployer` | Parse user intent, connect, submit operations, render results. | User's API credential and optional SSH identity. |
| `deployerd` | Authorize, persist intent, execute jobs, reconcile Docker, manage lifecycle. | Docker API and required project credentials. Trusted host infrastructure. |
| `deployer-gateway` | Match registered hosts, enforce preview access/expiry, route, report activity, request wakeups. | Narrow internal API identity; no Docker socket or SQLite file. |
| Caddy | HTTPS termination and forwarding preview traffic. | DNS credential and certificate storage only. |

The laptop CLI and server CLI use the same API. Direct loopback access replaces SSH tunneling on the server; authorization remains identical. Server CLI installation is mandatory in the default installer.

Keep application traffic independent of database reads on every request. Publish versioned route snapshots/deltas from the controller, with deployment IDs, allowed upstreams, access policy, state, and expiration deadlines. The gateway rejects unknown hosts and locally enforces deadlines. On a fresh gateway start, routing is closed until a valid snapshot arrives. A brief controller outage can retain known eligible routes until their deadline, but cannot authorize new routes or wake expired apps.

A registered route can have no application upstream yet: publish it as a status-page route during queue/build/start. Include the applicable operation timeout while first-readiness TTL is not yet set. The gateway serves embedded status-page assets and a sanitized `/_deployer/status` response; neither requires the application to be running. Apply preview access controls to both. Admit application traffic only after readiness, without changing the hostname. Polling uses no-store responses and does not count as application activity. Ready handoff has a short bounded grace period before idle shutdown becomes eligible.

Select HTML waiting responses only for GET document navigations that accept HTML, using browser fetch metadata when available. API calls/assets/upgrade requests must retain appropriate failure semantics; never return a successful HTML placeholder for an unavailable API or replay non-idempotent requests. Expose temporary unavailability as 503, known expired leases as 410, and unknown hosts as 404. Terminal failures stop automatic readiness polling and require authorized recovery, not an unlimited public rebuild/restart loop. Existing loaded pages must handle their own API errors. Controller failures preserve valid known-route deadlines; DNS/TLS/whole-host outages cannot be solved by the waiting page.

Preview containers communicate only over deployment-scoped networks. The gateway joins each exposed service's ingress network; it is shared trusted routing infrastructure. The Docker adapter must verify network membership and host/control-plane access restrictions on the target host. Do not claim network isolation merely because Compose project names differ.

## Persistence model

Use SQLite migrations and transactions from the first implementation. Store UTC timestamps; inject time into lifecycle tests. Use one controller instance initially and bound writes/worker concurrency.

| Record | Essential fields |
| --- | --- |
| Project | ID, name, repository URL, credential reference, defaults, allowed actions. |
| Deployment | ID, project, owner, desired state, observed state/reason, current revision, policy, expiry/purge timestamps. |
| Revision | ID, deployment, requested ref, resolved commit, image digests, resolved config hash, readiness outcome. |
| Operation | ID, actor, kind, target, status, current step, attempts, deadline, result/error, normalized input. |
| Idempotency record | Actor/project scope, key, request hash, operation ID. |
| Hostname reservation | Normalized hostname, deployment/alias target, reservation/quarantine state. |
| Resource record | Deployment/revision owner, Docker resource ID/type, retention/refcount information. |
| Capacity reservation | Operation/deployment, requested budgets, active reservation status. |
| Event | Actor, operation/deployment, event kind, timestamp, bounded non-secret payload. |
| Token | ID, hash, owner, scopes/project grants, expiration, revocation. |

Token values, private Git keys, and project secret values stay out of event payloads, receipts, generated Compose files, and CLI output. Configuration records hold secret references. Retain idempotency records at least as long as clients may retry recoverable operations; document the eventual retention bound.

## State and operation design

Separate desired availability from observed execution state. Observed states include `queued`, `building`, `starting`, `running`, `sleeping`, `stopped`, `expired`, `failed`, `destroying`, and `destroyed`; operations additionally describe transitional work such as wake or update. An update failure should not mark a still-running old revision as offline: expose operation failure and deployment availability separately.

An accepted operation persists its intent before interacting with Docker. Each external step has a durable checkpoint and a way to inspect whether it already happened. Database transactions cannot atomically commit Docker side effects; reconciliation closes that gap using deterministic ownership labels and stored operation IDs. Re-running a step must adopt the correct owned resource or fail clearly, never allocate duplicate stacks blindly.

Initial deployment sequence:

1. Authorize project/action; validate request; establish idempotency record.
2. Reserve hostname and capacity; create deployment, operation, and initial revision records; publish a status-page route without an application upstream.
3. Fetch/resolve commit; validate the recipe and enforce server policy.
4. Build image; record immutable image identity and the normalized execution plan.
5. Create isolated resources and start dependencies/application.
6. Check readiness; set first-readiness TTL deadline transactionally.
7. Switch the status-page route to the ready upstream with that deadline; finish the operation with a receipt. During an update, retain the healthy old upstream until replacement is actually necessary.

Every failure path defines reservation release, owned-resource cleanup, and retained diagnostics. Queue/build/start timeouts exist independently of TTL. Failed operations keep stable IDs and useful errors. Cleanup failures remain visible and retryable.

Serialize mutations affecting a deployment; protect global hostname uniqueness and capacity allocation transactionally. Recheck generation/version and policy before acting after a long build or health wait. Stopping during a build, renewing during expiration, and deleting during wake cannot be handled by an unguarded sequence of API calls.

The reconciler runs at startup and periodically, with event-triggered work for prompt responses. It compares desired state, deadlines, observed resources, and outstanding operations. A controller timer alone is not an expiry enforcement mechanism; route deadlines and post-restart reconciliation are also required.

## Runtime and source adapters

Use the Docker API for resource inspection, events, logs, and lifecycle actions. Use the installed Compose CLI to apply server-generated Compose plans for single- and multi-service deployments. Treat both as one runtime adapter with pinned/tested compatibility. Build through BuildKit using Docker's supported interface; evaluate SDK/library details during DPL-008 against the test host.

Never execute a repository's Compose file directly before validation. Normalize supported fields into owned resources; reject fields that can escape the policy. Use argument arrays for subprocesses rather than interpolated shell commands. Supply sensitive input through protected files/streams, with redacted diagnostics. Repository scripts run only inside their designated build/job containers.

Git fetch uses registered project credentials and verified host identities. Serialize mutation of shared repository caches. Workspaces are immutable snapshots per operation; build input and generated plans cannot change underneath an executing operation. The server fetches its own source; forwarding the developer's SSH agent is unnecessary.

## Initial API and CLI contracts

Publish only the implemented subset of OpenAPI, keeping the following resource shape as the target:

| API family | CLI family | Purpose |
| --- | --- | --- |
| `GET /v1/server` | `server status` | Version, capabilities, health, authorized diagnostics. |
| `/v1/projects` | `project add/list/inspect` | Repository registration and project settings. |
| `/v1/projects/{id}/verify` | `project verify` | Check configured repository access. |
| `/v1/deployments` | `deploy`, `list`, `inspect` | Create or inspect environments. |
| `/v1/deployments/{id}/actions/{action}` | `stop/wake/extend/renew/...` | Validated lifecycle mutations. |
| `/v1/deployments/{id}/logs` | `logs` | Bounded build/runtime logs. |
| `/v1/operations/{id}` | `operation inspect`, `--wait` | Durable operation result/status. |
| `/v1/deployments/{id}/events` | `events` | Ordered bounded events/optional stream. |

Mutations return operation IDs except simple transactional resource creation where the contract explicitly returns the created resource. Use stable machine error codes and meaningful HTTP status codes. Human text can improve without changing the machine contract. A not-yet-implemented policy/flag returns `UNSUPPORTED_FEATURE`, rather than silently ignoring it.

Client waits have their own deadline and do not cancel jobs. Operation cancellation must be explicit and constrained to safe boundaries. JSON mode emits a single result envelope on stdout; streams use an explicitly selected NDJSON mode. Persist request IDs in diagnostics for troubleshooting without exposing credentials.

## Verification boundaries

- Unit tests cover policy precedence, state transitions, URL/name validation, idempotency conflict rules, and deterministic deadline behavior.
- Storage/API integration tests use temporary SQLite state and real HTTP handlers; exercise authorization, transactions, migration/restart behavior, and partial failures.
- Git/build/runtime integration tests use controlled repositories and Docker resources tagged with a unique test-run identity. Cleanup is restricted to those resources.
- End-to-end tests execute the CLI against the controller/gateway, then make HTTP requests to previews. Include multiple branches and a database-backed stack when available.
- A Linux host is the reference for production networking/reboot behavior. Local Docker tests are useful but do not replace the real-host installation/recovery checks.
- Installer and release checks verify fresh install, repeated install, compatible upgrade, credential handling, and preservation of existing customer state.

The roadmap defines each release's required scenarios. Add broader tests when failures or new behaviors warrant them; documentation-only changes do not require running the application test suite.
