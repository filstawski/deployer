# Deployer — disposable branch deployments

**Naming:** The product is named **Deployer**. Surdic is the primary user and its installation will serve previews under `surdic.run`. The CLI is `deployer`, project configuration is `deployer.yaml`, and platform variables use the `DEPLOYER_` prefix. Product identity remains independent of the organization and hosting domain.

**Proposal:** a self-hosted deployment service that turns a Git branch into an isolated, temporary HTTPS environment with one command. Built for AI agents and developers shipping customer demos, design comparisons, and feature previews on one Linux server.

The product promise: **“Give it a branch. Get a URL. Keep it only as long as you need it.”**

This document describes the target interface. The first authenticated local control-plane slice is implemented; [the development guide](docs/development.md) lists commands that work today. Deployment execution and preview routing are not implemented yet.

Implementation order and release acceptance criteria live in [ROADMAP.md](ROADMAP.md); code structure and runtime contracts live in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md). The roadmap distinguishes the first v0.1 pilot from the v0.2 release that completes the core feature set below.

## 1. The everyday experience

Register a repository once:

```sh
deployer project add customer-portal \
  --repo git@github.com:your-team/customer-portal.git
```

Deploy one branch with a chosen subdomain:

```sh
deployer deploy customer-portal \
  --ref design/a \
  --subdomain customer-a \
  --ttl 3d \
  --idle-timeout 30m \
  --wake-on-request \
  --wait
```

Result: `https://customer-a.surdic.run`.

Deploy several branches together:

```sh
deployer deploy customer-portal \
  --ref design/a \
  --ref design/b \
  --ref main \
  --ref feat/something \
  --ttl 2d \
  --idle-timeout 30m \
  --wait --json
```

Each gets its own deployment ID, URL, containers, private networks, and data volumes. They run concurrently; builds use a bounded queue so four builds cannot exhaust the server. Each branch has an independent result if another fails.

Without `--subdomain`, generate a readable, collision-checked name such as `portal-design-a-k7m2q.surdic.run`. Sanitize and truncate branch names, then add randomness; never use the branch name as the unique identity. For multiple refs, reject a single `--subdomain` and allow `--subdomain-template 'portal-{ref}-{id}'` instead.

## 2. Core concepts

| Object | Meaning |
| --- | --- |
| Project | Repository, credentials reference, deployment recipe, and defaults. |
| Deployment | One independently managed environment with a stable ID, URL, lease, and data. |
| Revision | An immutable resolved commit and built image digest for a deployment. |
| Operation | A durable asynchronous job: build, deploy, wake, stop, update, or destroy. |
| Lease | Permission to remain available until a persisted expiration time. |
| Preset | A versioned demo configuration: non-secret settings, data recipe, checks, access, and lifetime. |
| Review | A shareable collection of deployments, with revision-specific feedback. |
| Alias | A stable hostname explicitly assigned to a healthy deployment. |

A branch is resolved to a commit when deployment is requested. Later pushes do not silently change the running demo. Repeating `deploy` creates another environment unless the caller reuses an idempotency key. Updating an existing environment requires `update`.

Allow several deployments of the same branch. This is useful for different customer data, environment variables, or side-by-side revisions.

## 3. Expiration, sleep, and deletion

These need distinct, predictable meanings:

| Policy or action | Behavior | Data and URL |
| --- | --- | --- |
| `--ttl 3d` | Expires three days after first successful readiness. Traffic never extends this deadline. | Retained during a recovery period. |
| `--idle-timeout 30m` | Stops the deployment's services after 30 minutes without qualifying activity. | Retained; can wake. |
| `sleep` | Stops services immediately and permits wake-on-request if enabled. | Retained. |
| `stop` | Stops services and disables automatic waking. | Retained until expiration/cleanup. |
| `extend --by 2d` | Adds two days to the existing expiration. | Does not wake stopped or expired deployments. |
| `renew --ttl 3d` | Sets a new deadline three days from now, including for an expired deployment. | Explicit `wake` resumes it. |
| `destroy` | Removes the deployment and its owned resources. Volume deletion requires an explicit flag. | No recovery promise for deleted data. |

**Recommended defaults:** 72-hour TTL, 30-minute idle timeout, wake-on-request enabled, seven-day data recovery after expiration. These are product defaults, configurable by the server operator. Permanent deployments require explicit `--ttl none` and a token allowed to create them.

TTL starts at first readiness so build time does not consume demo time. Separate queue, build, and startup timeouts prevent a deployment that never becomes ready from occupying resources forever. Once started, TTL keeps running during sleep and server downtime.

At expiration, block new traffic and gracefully stop services with a bounded drain deadline. Expired environments never wake just because somebody visits. Return a clear expired response, with HTTP 410 for machine clients. Cleanup removes retained deployment data after the configured recovery period; show the exact `purge_at` time in CLI/API output. Extending or renewing before purge cancels/reschedules that cleanup atomically.

Track activity in the request gateway, rather than application logs. Ignore internal health checks; count external requests admitted by the gateway, even if the app returns an error. Keep in-flight requests, WebSockets, and streaming responses active, with configurable maximum connection lifetimes. Bots can keep public previews awake, so TTL remains the firm limit. Optional preview authentication reduces unwanted traffic.

For sleeping environments, coalesce concurrent visits into one wake operation. Browser GETs receive a small waiting page that retries after readiness; API clients and requests with bodies receive HTTP 503 plus `Retry-After` until ready. Do not silently replay POST requests. Wake has a timeout and rate limits. Applications with background jobs can disable idle sleep; HTTP activity cannot describe their workload.

### What visitors see before the application is ready

The shared gateway serves a lightweight status page independently of the application containers. A reserved hostname becomes available in **status-page mode immediately after allocation**, assuming the installation's DNS and HTTPS are working. It switches to application traffic only after readiness passes. The accepted-operation response can therefore include a URL with `ready: false`; `--wait` reports success only when the app itself is ready.

| Situation | Visitor-facing message | What happens next |
| --- | --- | --- |
| Waiting for build capacity | “Your preview is being prepared.” | Show a waiting indicator; update when work starts. |
| Initial image build | “We're preparing your preview.” | Show the current friendly stage and elapsed time; automatically continue when ready. |
| Starting the application/dependencies | “Your preview is starting.” | Wait for readiness checks before admitting application traffic. |
| First visit after idle sleep | “This preview was resting. We're waking it up.” | Start the retained image/services once; automatically open the requested page when ready. |
| Manual stop or sleep with automatic waking disabled | “This preview is currently offline.” | Explain that the owner needs to start it; visitors cannot override the policy. |
| Expiration | “This preview has expired.” | No automatic wake or endless polling; owner can renew and start it during retention. |
| Failed build/start/wake or timeout | “We couldn't open this preview.” | End the spinner, show a support reference and an optional owner-configured contact link. |
| Unknown or fully removed hostname | “Preview not found.” | Return a generic 404 without exposing project details. |

These pages are provided by Deployer; applications do not implement them. Use a clean, responsive template with optional installation/customer name and logo, accessible status text, and an explicit manual “Check again” action when JavaScript is unavailable. No product-name or Surdic branding is hardcoded into customer copy. A preview access challenge runs before disclosing status or triggering a wake for a protected environment.

For an existing deployment update, keep serving the old healthy revision during the new image build. Show the startup/maintenance page only during the actual replacement window for stateful stacks. A failed candidate build leaves the old version online. A failed replacement shows an actionable failure state; database migrations can prevent safe automatic rollback. Expiration still applies during updates.

The waiting document keeps the original browser URL, including its path/query/fragment. A small script polls a gateway-owned status endpoint with bounded backoff, initially around two seconds, then reloads that same URL once ready. Reserve `/_deployer/` for status-page assets/status requests and document that namespace in project validation. The status response contains only sanitized state, retry guidance, and a support reference—never raw build logs, repository URLs, credentials, or stack traces. Owners inspect details through CLI logs/events.

Only HTML document navigation gets the waiting page. API requests, assets, WebSocket handshakes, and non-GET requests receive protocol-appropriate unavailable responses rather than a success-status HTML document. Temporarily unavailable application routes use HTTP 503; `Retry-After` indicates when checking again is useful, not a guaranteed readiness time. Never buffer/replay a submitted form or API write while waking. A browser already running the application must handle failed API requests itself; the gateway cannot replace an already loaded page. [HTTP 503 semantics](https://www.rfc-editor.org/rfc/rfc9110.html#section-15.6.4)

Status pages and polling responses use `Cache-Control: no-store` and noindex headers so a waiting/expired page does not stay cached after recovery. The platform's own status polling does not refresh application idleness or extend TTL. On readiness, grant a short bounded handoff grace period so unusually short idle policies cannot stop the application before waiting visitors reload.

Show stages and elapsed time, not an invented percentage or promised countdown. Initial builds and normal wakes are different: waking reuses the retained image and data without rebuilding, reinstalling dependencies, or reseeding. Actual durations depend on queue load, image/dependency downloads, application initialization, and database recovery. Queue/build/start/wake timeouts are separately configurable; reaching one produces a final visible failure rather than an infinite spinner. Merely refreshing a failure page must not start repeated rebuilds or unlimited wake attempts.

The status page covers application unavailability while the shared edge/gateway is reachable. It cannot hide a server outage or broken DNS/TLS. During a gateway-only outage, the HTTPS edge may serve a generic temporary-unavailability page; whole-host availability remains outside the single-server guarantee.

## 4. Recommended architecture

Start with **Go + Docker Engine/Compose + SQLite + Caddy**, on one Linux server.

```text
Developer or AI agent
        │
     deployer CLI ── authenticated API ── deployerd
                                         ├── SQLite: desired state, leases, operations
                                         ├── bounded build queue / BuildKit
                                         └── Docker Compose: deployment stacks

Customer browser
        │
    Caddy: HTTPS
        │
    Deployer gateway: hostname lookup, access checks, activity, wakeups
        │
    deployment web service ── private database / supporting services
```

**`deployer` CLI:** a single binary with human-readable and machine-readable output. It calls the same versioned API used by future integrations.

**`deployerd`:** the control service. Owns repository access, builds, resource allocation, lifecycle decisions, and reconciliation. SQLite holds authoritative desired state and durable jobs; no Redis or external database is required initially. Persist the database and take backups.

**Gateway:** a separate, unprivileged process that remains available while applications sleep. It routes only registered hosts to server-generated upstreams and exposes no Docker API. It reports activity and requests wakeups through a narrow authenticated internal interface. The daemon serializes wake/sleep/expiry decisions per deployment, rechecking activity before stopping services.

**Caddy:** handles HTTPS and forwards preview traffic to the gateway. A wildcard setup avoids per-deployment proxy changes entirely. Unknown hostnames are rejected; they never fall through to another customer's app. Caddy supports automatic HTTPS and wildcard certificates via DNS validation; the chosen DNS provider needs a compatible module. [Caddy documentation](https://caddyserver.com/docs/caddyfile/patterns)

**Docker Compose:** handles multi-service applications. Use a unique project name derived from the deployment ID, never the branch. Compose project names support separate instances of the same application. [Docker documentation](https://docs.docker.com/compose/how-tos/project-name/)

The deployer itself can ship as an operator-owned Compose stack, with persistent state and a host Docker connection for `deployerd`. Application recipes cannot modify this stack. The daemon is trusted host infrastructure.

I recommend Caddy plus this gateway because sleep requires a route that survives stopped application containers. Traefik is also viable, but Docker label discovery alone does not implement activity accounting or wake-on-request.

## 5. Isolation and application support

Support Dockerfiles, prebuilt images, and a **validated subset of Compose**. Generate a Compose definition for a single Dockerfile or image; normalize a project's Compose file for multi-service apps. Later, add managed builders for selected frameworks so supported projects can omit a Dockerfile.

Each deployment gets:

- A separate checkout of its resolved commit and immutable image references.
- Private service networking and separately scoped persistent volumes.
- A separate ingress network connecting the gateway to only the exposed service.
- CPU, memory, process, log, and storage budgets, with host admission checks.
- Its own runtime configuration and narrowly scoped secret references.

Avoid one shared network containing all preview applications. Supporting services such as databases stay on the deployment's private backend network. Enforce network/firewall policy against access to other deployments, the control plane, host services, and infrastructure metadata; allow required outbound access by policy.

Reject or explicitly normalize `container_name`, host-published ports, external/shared volumes and networks, explicit global resource names, host networking, privileged containers, device access, and arbitrary host bind mounts. Do not let repository files bypass isolation. No application receives the Docker socket.

Container isolation is suitable for trusted team projects and demos. It is not a strong boundary for arbitrary hostile code: Docker documents risks from daemon access, configuration, and kernel vulnerabilities. Public execution of untrusted repositories would require a stronger boundary such as separate VM workers. [Docker security](https://docs.docker.com/engine/security/)

Set runtime and build limits separately. Serialize expensive builds by default and allow configurable concurrency. Runtime limits alone do not prevent a build from exhausting the server. Treat disk budgets as enforceable quotas where supported; otherwise reject admissions and prune owned caches before thresholds are crossed, clearly reporting the limitation.

## 6. Project configuration

Keep a small, versioned `deployer.yaml` in the repository:

```yaml
version: 1
project: customer-portal

source:
  compose: compose.yaml

http:
  service: web
  port: 3000
  healthcheck:
    path: /health
    timeout: 90s

lifecycle:
  ttl: 72h
  idle_timeout: 30m
  wake_on_request: true
  retention_after_expiry: 7d

resources:
  defaults_per_service:
    cpus: 1
    memory: 512MiB

environment:
  NODE_ENV: production

secrets:
  - DEMO_API_KEY

preview_sets:
  design-review:
    refs: [design/a, design/b, main, feat/something]
```

Run a saved selection with `deployer deploy customer-portal --set design-review`.

For a single service, replace `source.compose` with a Dockerfile build specification. Require explicit service/port configuration when detection is ambiguous; an agent should receive a useful validation error instead of guessing. Precedence is CLI override → project configuration → server defaults, always bounded by server-enforced policy.

Inject the final preview URL before the build when frontend frameworks need it, and at runtime otherwise. Include the URL and non-secret build settings in the cache key. Supply credentials through BuildKit secret mounts rather than build arguments. [Docker build secrets](https://docs.docker.com/build/building/secrets/)

Use secret references in configuration, never committed secret values. Private repository access uses a read-only deploy key or scoped integration credential. When a project declares a database service, each deployment gets an isolated instance and storage; Deployer does not invent a database requirement for applications that do not need one. Sharing an external database is an explicit exception.

### 6.1. What does a project actually need?

The project supplies **a repeatable way to build/start its application and the internal HTTP port it listens on**. Deployer supplies the deployment infrastructure. There is no application SDK, Deployer API endpoint, or agent process to install in the application.

| Project situation | What the repository needs | Availability in the plan |
| --- | --- | --- |
| One web application | Dockerfile, `.dockerignore`, and a small `deployer.yaml`. | Initial version; recommended default. |
| Application plus database, cache, or workers | A deployment-oriented Compose file and `deployer.yaml`; Dockerfiles only for services built from source. | Initial version. |
| An image already built by CI | Image reference and `deployer.yaml`; no Dockerfile needed by Deployer. | Initial version. |
| Supported framework with no Docker setup | Runtime/build/start settings in `deployer.yaml`; Deployer creates the image through a managed builder. | Later convenience feature. |
| Static frontend | Build recipe and output directory; Deployer packages the output with a managed static HTTP server. | Later convenience feature; Dockerfile works initially. |

Every path ends in a Docker image. **A handwritten Dockerfile is optional, but a known build recipe is essential.** An existing Dockerfile remains the most explicit and portable first-version approach. Compose describes how services run together; it does not replace a build recipe for application source.

The same project recipe works on all branches. Each resolved branch must contain its own valid recipe, including newly introduced services or configuration. Prefer checking configuration into Git so agents can inspect it and reproduce a deployment. Server-side defaults remain available for repositories that cannot be modified.

Proposed onboarding:

```sh
# Inspect locally, then generate missing configuration for a supported stack.
deployer init
deployer validate

# Commit the generated recipe before deploying the remote branch.
deployer deploy customer-portal --ref design/a --wait
```

`init` detects existing files and suggests a recipe, without overwriting existing files silently. In noninteractive agent use, require an explicit template if detection is ambiguous. Framework templates generate reviewable Dockerfiles initially; fully managed builders can come later. A deployment from Git uses the remote commit, so uncommitted local files are not included. A future local-source upload must be a separate, explicit mode.

### 6.2. Minimal single-application setup

```text
customer-portal/
  src/
  package.json          # Or the equivalent for the project's language
  package-lock.json
  Dockerfile
  .dockerignore
  deployer.yaml
```

Example `deployer.yaml` for an existing Dockerfile:

```yaml
version: 1
project: customer-portal

source:
  dockerfile: Dockerfile
  context: .

http:
  service: web
  port: 3000
  healthcheck:
    path: /health
    timeout: 90s

environment:
  HOST: 0.0.0.0
  PORT: "3000"

lifecycle:
  ttl: 72h
  idle_timeout: 30m
  wake_on_request: true
```

For Dockerfile/image mode, Deployer generates the service named `web`. The Dockerfile installs dependencies from the project's lockfile, builds the application, and provides its production start command. `.dockerignore` excludes `.git`, local dependencies, credentials, and unnecessary files from the build context. A monorepo can set `context: .` and `dockerfile: apps/portal/Dockerfile` to retain access to shared packages.

The application must bind to **`0.0.0.0:3000` inside its container**, or an equivalent reachable interface. Binding only to `127.0.0.1` prevents the gateway from reaching it. `HOST` and `PORT` are conventions, not universal framework settings: the start command/application must actually honor them, or use the framework's own binding flags. Deployer does not change source code to make the port work.

`GET /health` should return HTTP 200 when the application is ready. It need not disclose diagnostic details. A configured existing route such as `/` is acceptable if its response reliably indicates readiness; a dedicated health endpoint is recommended. Use separate dependency checks for databases, and do not route traffic until necessary dependencies and the application are ready.

Only the server's shared edge publishes ports 80/443. A preview service uses an internal container port; it needs no host `ports: ["3000:3000"]` mapping, DNS integration, TLS certificate, nginx configuration, or Docker socket. All branches can use internal port 3000 simultaneously. Docker's `EXPOSE 3000` documents the port and does not publish it; it is optional for Deployer when the configured port is explicit. [Dockerfile reference](https://docs.docker.com/reference/dockerfile/)

The request path is `https://customer-a.surdic.run` → shared HTTPS edge → gateway → that deployment's `web:3000`.

### 6.3. Applications with databases or multiple services

Keep a deployment-specific file, for example `compose.preview.yaml`, if local Compose uses development servers, source bind mounts, or host-published ports. Point `source.compose` at that file.

Its responsibilities are to declare:

- `web`: built from the application Dockerfile, listening on an internal port.
- `db`: a pinned database image, a private named volume, credentials from secret references, and a readiness check.
- Optional cache/worker services and their dependencies.
- Application connection configuration using service names, for example `db:5432`, never `localhost:5432` for a separate database container.

Deployer scopes the networks and volumes to the deployment. Only `web` joins its ingress network; the database stays private. Database readiness must be checked explicitly; merely starting its container is insufficient. Compose supports waiting for dependencies with `condition: service_healthy`. [Compose startup order](https://docs.docker.com/compose/how-tos/startup-order/)

The first version exposes one public HTTP service per deployment. A separate frontend and API can use an existing application server that handles `/api`, or later use declarative gateway routes such as `/` → `frontend:3000` and `/api` → `api:8080`. Deployer should generate these routes and make path preservation/stripping explicit. Browser code cannot resolve Docker names like `api`; it must use a public URL or a relative path such as `/api`.

Store durable database files/uploads in declared volumes. Container-local scratch files are disposable. Sleeping stops the entire stack while retaining its volumes. Waking starts dependencies before the web service; it does not recreate or seed the database. Worker-heavy applications can disable idle sleep.

### 6.4. Build, runtime, and application responsibilities

| Concern | Project provides | Deployer provides |
| --- | --- | --- |
| Build | Dockerfile/image or a supported builder recipe; pinned dependencies. | Checkout, bounded build execution, cache, immutable image identity. |
| Start | Production command, correct bind address and port. | Containers, resource limits, isolated networks. |
| Health | A suitable readiness endpoint and dependency checks. | Polling, startup timeout, diagnostics, routing gate. |
| Configuration | Names of needed settings and secrets. | Scoped values, secret delivery, deployment URL and ID. |
| Data | Schema migrations, synthetic seed/reset scripts, volume declarations. | Separate storage and controlled execution of declared data operations. |
| Sharing | Optional application-specific URL/allowed-host configuration. | HTTPS, hostname, access policy, share link, expiration. |

Expose `DEPLOYER_URL` and `DEPLOYER_DEPLOYMENT_ID` as platform variables, with explicit mappings to framework-specific names such as an application's public base URL. Public frontend build variables are visible to visitors and must never contain secrets. Runtime environment changes cannot rewrite values already compiled into JavaScript.

Applications using OAuth, CORS, allowed-host lists, or secure cookies may need their preview URL configured. Configure trusted proxy handling narrowly for Deployer's gateway. External identity providers may require callback URLs registered separately; automated routing alone cannot solve that. A stable demo alias can help with providers that require a fixed callback.

Migration, seed, and reset commands run as bounded jobs inside the deployment's container/network context, never as arbitrary host scripts. Run migrations for a new revision before readiness, record completion, and require scripts to tolerate retry after a crash. Seed only fresh demo data unless explicitly requested. Destructive reset is a separate operation, not an ordinary container startup hook.

### 6.5. Without a Dockerfile

For prebuilt artifacts, `source.image` replaces `source.dockerfile`/`source.compose`. Resolve tags to immutable digests, verify registry access and server architecture, and record the digest in the deployment receipt. In this mode, an image identifies the executable artifact; `--ref` alone cannot prove which branch produced it. Require an explicit CI-provided commit-to-image mapping for branch deployment, or clearly report source provenance as unverified.

Later, a managed-builder recipe could look like this:

```yaml
version: 1
project: customer-portal
source:
  builder: node
  runtime: "24"
  install: [npm, ci]
  build: [npm, run, build]
  start: [npm, run, start]
http:
  service: web
  port: 3000
  healthcheck:
    path: /health
```

These are proposed Deployer fields. The supported builder pins its underlying image/toolchain and records that resolved version, including on retries. Detection proposes settings; the committed recipe decides them. Unknown stacks return actionable errors and can use a Dockerfile. Cloud Native Buildpacks are one possible implementation for converting source into images; decide between buildpacks and maintained templates after validating the initial framework targets. [Buildpacks documentation](https://buildpacks.io/docs/)

For static sites, a managed recipe also declares the output directory, such as `dist`, and whether client-side routing needs an index fallback. Deployer builds and serves those files from a container. The project does not need to write its own HTTP server.

## 7. CLI management

```sh
# Inspect and diagnose
deployer list --project customer-portal
deployer inspect dep_k7m2q --json
deployer logs dep_k7m2q --service web --tail 100
deployer logs dep_k7m2q --build
deployer events dep_k7m2q --follow

# Manage lifetime and availability
deployer extend dep_k7m2q --by 2d
deployer renew dep_k7m2q --ttl 3d
deployer sleep dep_k7m2q
deployer wake dep_k7m2q --wait
deployer stop dep_k7m2q
deployer policy set dep_k7m2q --idle-timeout 1h

# Update source, preserving the environment's identity
deployer update dep_k7m2q --ref design/a --wait

# Explicit destructive cleanup
deployer destroy dep_k7m2q --delete-volumes --yes
```

`list` should show ID, project, ref, short commit, state, URL, last activity, expiration, and resource usage. Show both desired and observed state in `inspect`, plus a reason such as `idle_timeout`, `lease_expired`, `manual_stop`, or `healthcheck_failed`.

Updating builds the new revision before stopping the old one. Initially allow a brief maintenance window for stateful stacks: stop, start the candidate against the retained data, health-check, then reopen traffic. On failure, keep diagnostics and report recovery options. Reverting the image does not undo database migrations; never promise automatic data rollback. Add seamless replacement for stateless apps later.

## 8. Designed for AI agents

The key is a predictable contract with useful errors and safe retries.

- **Structured output:** `--json` emits one versioned result object on stdout; progress goes to stderr. Event streaming has an explicit NDJSON mode.
- **Durable asynchronous operations:** every mutation returns an operation ID. `--wait --timeout 120s` waits for the result; a client timeout leaves the operation running and queryable.
- **Idempotency:** accept `--idempotency-key` on mutations. Persist the normalized request and resolved commit with the result. A retry returns the same operation; reusing the key with different input returns a conflict.
- **Stable errors:** return a code, message, details, retryability, and suggested next action. Examples: `SUBDOMAIN_TAKEN`, `BUILD_FAILED`, `HEALTHCHECK_FAILED`, `QUOTA_EXCEEDED`, `POLICY_DENIED`.
- **Explicit intent:** `deploy` creates; `update` changes an existing environment. Ambiguous matches never select an arbitrary deployment.
- **Discovery:** publish JSON Schema and OpenAPI; provide `deployer schema` and `deployer validate` so agents can inspect supported inputs.
- **Planning:** `deployer deploy ... --dry-run --json` reports normalized configuration and validation without allocating a deployment. It cannot guarantee future capacity or hostname availability.
- **Noninteractive behavior:** no prompts when stdin is not a terminal. Destructive commands require explicit flags; errors explain what is missing.
- **Scoped access:** tokens restrict projects, actions, resource usage, and maximum TTL. Record actor, operation, and affected deployment in an audit log.

Example successful result after `--wait --json`:

```json
{
  "schema_version": 1,
  "operation_id": "op_8cf3",
  "deployment_id": "dep_k7m2q",
  "state": "running",
  "project": "customer-portal",
  "ref": "design/a",
  "commit": "7d63b83b6f7e029c4be79244204d87c9ef71a460",
  "url": "https://portal-design-a-k7m2q.surdic.run",
  "expires_at": "2026-10-14T10:00:00Z",
  "idle_timeout_seconds": 1800,
  "wake_on_request": true
}
```

Use absolute UTC timestamps in machine output and local, readable times for people. Multi-ref deployment returns a batch operation with per-deployment outcomes. A failed member makes the completed batch exit nonzero while preserving successful environments.

Ship a concise agent guide alongside the CLI: validate → deploy with idempotency → wait → inspect health → return URL. Add an MCP adapter later as a thin wrapper around this API; it should not contain separate deployment logic.

## 9. Domains and customer access

The planned Surdic installation uses `surdic.run` as its base domain. Use one wildcard DNS record pointing `*.surdic.run` at the server, then a wildcard certificate obtained through DNS validation. Keep DNS credentials confined to certificate management. Use flat names such as `portal-design-a-k7m2q.surdic.run`; deeper names require a different certificate scope. [Caddy wildcard setup](https://caddyserver.com/docs/caddyfile/patterns)

Reserve infrastructure names and allocate custom subdomains transactionally. A collision returns `SUBDOMAIN_TAKEN`; it must never replace another environment. Retain the hostname during the recovery period, and quarantine released names for a configured period before reuse to reduce stale-link surprises.

`--subdomain customer-a` means a label under the configured base domain. A later `--domain preview.customer.com` feature means an external custom hostname and needs explicit ownership/DNS validation and certificate provisioning. Keep these separate in the API.

For customer demos, offer public or password-protected access and send `X-Robots-Tag: noindex, nofollow`. A random hostname and noindex are not authentication. Use host-only preview cookies; keep administrative sessions off the preview domain. Expose management through an authenticated private endpoint, reachable via VPN or SSH tunnel initially.

Product naming is independent of installation branding. Configure the base domain, installation display name, and optional logo on the server. Project recipes remain portable between installations without renaming configuration files or changing CLI commands. Deployer is the product name; Surdic branding belongs to this installation and its customer projects.

## 10. Recovery and operation

Use a reconciliation loop, not one in-memory timer per deployment. Persist desired state, expiration, last activity checkpoints, resource ownership, and operations. On restart, compare the database with Docker and repair incomplete work. Stop expired environments before making them routable; conservatively handle gaps in activity tracking so a monitoring interruption does not falsely imply idleness.

Reserve hostname and capacity before building. Record intent before external actions, label all created resources with ownership and deployment IDs, and make each step repeatable. Per-deployment locks prevent extend/expire/purge and wake/sleep races. Renewing data that is already being purged must fail clearly rather than claim success.

Startup, dependency readiness, app readiness, and gateway routing are separate checks. Return a usable URL only after readiness succeeds. A ready deployment can later become unhealthy; expose that fact without treating it as idle.

Use restart policies that cannot silently resurrect sleeping or expired stacks after a host reboot. The controller owns recovery ordering. Unknown/orphaned resources are surfaced and quarantined; cleanup only touches resources positively identified as Deployer-owned. Never run indiscriminate host-wide pruning.

Keep logs bounded and rotate them. Report available capacity, build queue length, failed operations, expiring leases, and storage pressure. Retained images, volumes, and build caches still consume disk while apps sleep. Back up control state; persistent customer data needs its own backup policy.

## 11. Shareable review environments

Extend the core deployer into a review workflow while keeping plain branch deployment useful on its own. All commands in this section are planned extensions.

### Preview sets and comparison pages

```sh
deployer review create customer-portal \
  --refs main,design/a,design/b \
  --ttl 5d
```

Return one review URL containing labeled links, exact commits, readiness/check results, and optional screenshots for all variants. Reviewers can mark a preferred variant. Open previews in their own tabs by default; embedded applications may forbid framing. A review groups deployments but does not merge their databases or silently prolong their leases. Display each member's availability and partial failures.

### Customer demo presets

```sh
deployer deploy customer-portal \
  --ref main \
  --preset customer-demo \
  --data synthetic \
  --ttl 7d
```

A versioned preset combines application-supported branding settings, feature flags, a synthetic data recipe, access policy, checks, and lifetime. It references secrets rather than embedding them. The project implements branding/data behavior; Deployer supplies configuration and runs the declared jobs. Store presets in project configuration and record the resolved preset with each deployment. Precedence becomes CLI → selected preset → project defaults → server defaults, constrained by server policy.

### Resettable demo data

```sh
deployer data reset dep_k7m2q --to initial --yes
```

Restore a known demo state before a meeting or after agent experimentation. The project supplies a deterministic seed/reset recipe; optional database-specific adapters can support consistent snapshots later. Drain traffic and pause writers during reset, then rerun health checks before reopening the preview. Never copy live database files as a generic backup strategy. Reset must cover the declared database/uploads consistently, require a dedicated token permission, and affect only that deployment. Synthetic data is the default; importing real customer data requires a separately authorized workflow. External emails, payments, and other side effects cannot be undone by resetting local data; presets should use sandbox integrations.

### Feedback tied to the reviewed revision

Reviewers can submit comments, preferred-variant selections, and screenshots through the review page. Record deployment ID, immutable revision ID, page URL, author, and time. Updating the deployment must not retarget old feedback to the new code. Agents retrieve feedback through structured CLI/API output and can publish a new revision for review. A feedback overlay inside applications is optional later and may require opt-in integration; a standalone review page needs no application SDK. Apply access and retention rules to screenshots and feedback as well as the running demo.

### Stable customer links

```sh
deployer alias set acme --deployment dep_k7m2q
```

Keep `acme.surdic.run` while explicitly moving the alias to a verified, ready deployment in the same project. Reserve the alias, enforce compatible access policy, validate application host/callback configuration, and update the route atomically. Preserve the old deployment until its own lease ends or an operator removes it. An alias does not extend TTL or prevent sleep; expired targets show an expired response until explicitly renewed or replaced. Automatic alias promotion requires a separate policy.

### Automatic verification and deployment receipts

After startup readiness, run optional bounded smoke tests against the preview, such as opening the homepage and completing a demo login. Project-defined browser tests supply selectors and test credentials. Collect screenshots, results, and bounded/redacted diagnostic logs. Run browsers in isolated workers with scoped access, not inside the privileged daemon.

Return a receipt containing source commit, image digests, resolved recipe/preset, URL, readiness and verification status, expiration/purge times, owner, and operation IDs. Report `ready` and `verified` separately: a passing health endpoint does not prove the user journey works. With `--require-checks`, failed checks prevent customer alias promotion and produce a failed operation while retaining an access-restricted environment for diagnosis.

### Access, budgets, ownership, and handoff

Build on the base access controls with expiring share links and invited reviewers. Share grants have their own expiry, bounded by deployment availability; granting access never silently renews a deployment. Tokens in share URLs should be redeemed for a session and stripped from the URL to reduce leakage.

Track project/agent quotas for concurrent environments, build minutes, CPU/memory, retained storage, and maximum TTL. Check aggregate quotas before admitting work. Return the quota exceeded and actionable options; agents must not work around it by creating another alias or project.

Record who/which agent created the deployment, an optional originating task reference, its purpose, and its current owner. Support an explicit handoff:

```sh
deployer handoff dep_k7m2q --owner filip --ttl 7d
```

This transfers responsibility and sets an explicit new lease, subject to permission and quota checks. It does not change the running revision. Temporary agent experiments remain short-lived; customer-facing demos gain a named owner. A handoff receipt states the owner and new deadline so cleanup is understandable.

## 12. Installation, CLI connection, and source delivery

There are two separate delivery paths: **install Deployer itself on the server**, then **give it access to the repositories it should deploy**. The CLI sends requests; it does not need to stay running or upload every repository on every deployment.

### Server prerequisites and release packaging

The initial installation targets one Linux host with SSH access, Docker Engine and the Compose plugin, persistent disk, and outbound access to the Git host and required image/package registries. Choose capacity based on the application and build requirements; check available CPU, RAM, and disk during setup. Reserve public ports 80/443 for the shared edge, and configure `*.surdic.run` to resolve to the host. Wildcard certificate issuance also needs DNS-provider credentials and a compatible Caddy DNS module. If another proxy already owns those ports, require an explicit integration/migration plan before installation.

Keep Deployer's own source in its own repository. A release pipeline builds:

- CLI binaries for the developer/agent machines and the server architecture.
- Versioned server images for `deployerd`, the gateway, and the Caddy build with the selected DNS module.
- The operator Compose definition, configuration schema, checksums, and installation assets.

The server installs these release artifacts; it does not need a live checkout of Deployer's source. Pin images by digest and upgrade explicitly. During initial development, an administrator can build from the Deployer repository and run the same Compose stack manually. Building application images on this host does not require a private application registry; Docker can use the locally built images. A registry becomes useful when CI supplies images or workers move to multiple hosts.

Proposed installation command, once the installer exists:

```sh
deployer server install \
  --ssh admin@server.example.com \
  --domain surdic.run \
  --version 0.1.0
```

This is a one-time administrator operation: inspect prerequisites and conflicts, install the requested release, configure persistent directories, start services, initialize operator access, and verify the API/edge. It must explicitly report DNS or credential steps the operator still needs to complete. It must not silently replace an existing proxy or reset existing Deployer state.

Keep configuration under `/etc/deployer`, persistent control state under `/var/lib/deployer`, and rebuildable Git caches/build workspaces under `/var/cache/deployer`. Docker manages deployment volumes separately. These are proposed server paths, not files created by this concept. When the daemon is containerized, mount only its required state paths and explicitly handle host-versus-container paths for any generated mounts. Back up the database, configuration, and required secrets before upgrades; preserve existing deployments and reject incompatible migrations unless a supported upgrade path exists.

### Concrete host layout

Install Deployer as a system service. An optional `/home/deployer` belongs only to the restricted SSH connection account; application checkouts and service state do not depend on a person's home directory. The initial installation assumes a system Docker Engine on a Linux host with systemd. Rootless Docker would be a separate supported installation profile later, with different storage and permission rules.

```text
/opt/deployer/
  releases/
    0.1.0/
      compose.yaml           # Deployer infrastructure only
      release.json           # Version, image digests, compatibility
  current -> releases/0.1.0  # Selected release

/etc/deployer/
  server.yaml                # Domain, limits, storage paths, API settings
  credentials/
    repositories/            # Private deploy keys, Git host verification
    runtime/                 # Project runtime/build secret values
    dns/                     # DNS-provider credential

/var/lib/deployer/
  state.db                   # Projects, deployments, leases, operations, audit
  deployments/
    dep_k7m2q/
      revisions/
        rev_01/
          compose.yaml       # Normalized execution plan; secret references only
          receipt.json       # Non-secret source/build identity and settings
  artifacts/                 # Bounded build logs, checks, screenshots
  caddy/                     # Persistent certificate/ACME state

/var/cache/deployer/
  repositories/
    prj_123.git/              # Rebuildable Git object cache
  workspaces/
    op_8cf3/
      source/                # One operation's isolated commit checkout

/run/deployer/               # Temporary sockets/locks/secret staging if needed
/etc/systemd/system/deployer.service
/usr/local/bin/deployer     # Host CLI, installed by default

/home/deployer/
  .ssh/authorized_keys       # Only if using the dedicated SSH account
```

Use generated IDs in filesystem paths, never raw repository names or branch names. `design/a` is metadata, not a path that creates nested directories. Validate paths and reject escaping symlinks when processing repository-supplied files. `state.db` is authoritative; generated plans and receipts support execution/diagnostics and are not a second mutable configuration system. Repeated builds/updates have distinct operation workspaces and revision records.

These paths follow the conventional Linux split between configuration, persistent state, cache, and temporary runtime files. [systemd directory conventions](https://github.com/systemd/systemd/blob/main/man/systemd.exec.xml)

Docker keeps image layers, writable container layers, named volumes, and its own build cache in its configured storage. Common defaults are `/var/lib/docker` and, with the containerd image store, `/var/lib/containerd`. Inspect the installed backend rather than assuming all Docker data is under one directory. Deployer accesses this storage through Docker APIs and never edits its internal files. Moving Deployer's workspace directory alone does not move Docker's large image/volume storage. [Docker storage configuration](https://docs.docker.com/engine/daemon/)

### Processes, ownership, and boot behavior

Use one operator Compose project, `deployer-system`, for the controller, gateway, and HTTPS edge. Give deployment stacks separate projects such as `deployer_dep_k7m2q`. The infrastructure Compose file never lists customer applications; the controller creates and manages them dynamically.

An administrator installs a `deployer.service` systemd unit that starts the infrastructure stack after Docker, networking, and required storage mounts are available. Keep one documented service-management path: systemd starts/stops the stack; infrastructure containers have restart policies for process failures. Do not enable competing boot managers. The unit retains state and volumes when stopped.

The controller reconciles desired application state on boot. Customer containers do not use policies that automatically bring expired/sleeping stacks back online before that reconciliation. The gateway initially denies application routing until it receives validated state and lease deadlines; it enforces expiry even during a temporary controller outage. A control-plane restart can cause a short preview interruption in the initial single-server design; do not promise high availability.

Separate three identities: the installing administrator, the restricted SSH connection account, and the server processes. The SSH account cannot read deployment secrets/state or control Docker. The controller is trusted host infrastructure because it has Docker API access; its privileges must not be described as ordinary user-level isolation. Run the gateway and edge with only their necessary permissions and mounts. Only the certificate service receives DNS credentials, and only the controller's repository-fetch path receives deploy keys.

The installer creates directories with explicit ownership for each service. Keep credentials owner-readable only (normally mode 0600 inside restricted directories); service state is not world-readable. Mount release assets/configuration read-only where possible and expose only the appropriate state subdirectory to each container. Application containers receive their declared data volumes and required secrets, not the control-state directories or repository credentials. Secret staging under `/run/deployer` is transient and cleaned after use.

### What happens on disk during a deployment?

1. Save the accepted operation and resource reservation in SQLite.
2. Fetch Git objects into the project's cache, then materialize the exact commit into that operation's `source/` directory.
3. Build from that context. Application code becomes part of the image; running apps do not depend on a source bind mount.
4. Save the validated revision plan and receipt, then create labeled containers, networks, and named volumes through Docker.
5. After successful build/startup, remove the source workspace when no operation references it. Keep failed workspaces only for a short, configurable diagnostic period.
6. Sleeping stops containers while retaining images and data. Expiration stops access; final cleanup removes owned resources according to retention policy.

Protect active workspaces/cache objects with operation references during cleanup. Git caches are bounded by size/age and can be fetched again; retain exact built image digests for recoverable deployments so waking does not depend on rebuilding a deleted upstream commit. Do not prune shared image layers or volumes still referenced by another deployment. Log/artifact retention is separate from database-volume retention, and both count toward storage budgets.

### Storage expansion, backups, and removal

For a small installation, use these defaults. For a larger data disk, allow configured Deployer state/cache paths or dedicated mount points, require the mounts before boot, and plan Docker/containerd storage independently. A missing mount must stop startup rather than silently write new state onto the root filesystem. An existing installation's storage move is an explicit maintenance operation, not an automatic installer side effect.

Back up server configuration, encrypted credential backups with a separate recovery key, a consistent SQLite backup, certificate state, and any customer volumes that need recovery. Use database-native dumps or coordinated snapshots for database volumes. The Git cache, workspaces, and downloadable release images are normally rebuildable and excluded. Preserve otherwise unavailable application images in a registry/archive when required for recovery; an image digest in a receipt alone is not a backup. Store backups off the host and exercise restoration.

The proposed `deployer server status` should report service health, versions, configured paths, filesystem free space, Docker storage use, and counts of running/sleeping/expired deployments. Daily operation uses the CLI; administrators should not need to edit generated deployment files. Uninstall should stop/remove Deployer's infrastructure but retain state and customer data by default; full data deletion requires explicit scope and confirmation, and never removes unrelated Docker resources.

### CLI connection: API over SSH first

For the initial version, use an authenticated HTTP API bound only to the server's loopback address, for example `127.0.0.1:7420`. The CLI automatically opens an SSH tunnel when using an SSH-backed server context. OpenSSH supports forwarding local connections over its encrypted connection. [OpenSSH documentation](https://man.openbsd.org/ssh)

```text
Local CLI → SSH tunnel → private Deployer API → durable deployment operation
```

The proposed everyday setup is:

```sh
deployer server add surdic --ssh deployer@server.example.com
deployer server use surdic
deployer server status
```

Here `surdic` is a saved installation/context name, not product branding. The context stores the SSH destination and a credential reference. Read the user's SSH configuration and verify host keys normally. Bind any local forwarding endpoint to loopback, allocate a free local port, and close it when the command exits. The CLI negotiates API compatibility and gives a useful error for mismatched versions.

Use separate credentials for separate boundaries:

| Credential | Purpose | Where it belongs |
| --- | --- | --- |
| User/agent SSH key | Reach the server's private API. | Local SSH agent or protected local key file. |
| Scoped Deployer API token | Authorize project operations, quotas, and lifetime. | Local credential store or agent secret injection. |
| Repository deploy key | Let the server fetch one project's code. | Protected server credential store. |
| DNS-provider credential | Issue/renew the installation's certificates. | Certificate-management service only. |

Installation creates an operator credential through an administrator-only bootstrap path. `server add` securely enrolls an existing token; it does not mint administrator access just because SSH succeeded. Interactive use can store it in the OS credential store; headless agents can use a protected file or secret input. Never put tokens in URLs, source control, normal logs, or command-line arguments. Store token hashes server-side and support revocation. A dedicated connection account can be restricted to the API tunnel; routine CLI access needs neither a root shell nor Docker group membership. Administrator installation and user deployment are distinct permission levels.

A lost SSH connection does not cancel an accepted deployment. Jobs run on the server, and the CLI reconnects to inspect their operation IDs. Later, support the same API over authenticated HTTPS or a private VPN endpoint for agents without SSH; transport changes must not bypass project authorization.

### How application source reaches the server

Recommend **server-side Git fetch** for the first version. Register each project once:

```sh
deployer project add customer-portal \
  --repo git@github.com:your-team/customer-portal.git
```

For a private repository, the server creates a dedicated key pair and returns only the public key for registration at the Git provider. Keep the project in an `awaiting_repository_access` state until verification succeeds. With GitHub, a deploy key grants access to one repository and is read-only by default; use a separate key for each repository. [GitHub deploy keys](https://docs.github.com/en/authentication/connecting-to-github-with-ssh/managing-deploy-keys)

```sh
# After adding the public key to the repository's settings:
deployer project verify customer-portal

# After pushing the desired branch to the Git host:
deployer deploy customer-portal --ref design/a --ttl 3d --wait
```

The server then:

1. Fetches the registered repository using its own credential and verified Git-host identity.
2. Resolves the requested branch to an immutable commit and records it with the operation.
3. Creates a separate checkout for this deployment; it never switches branches in a workspace used by another build.
4. Reads and validates `deployer.yaml` and the build/Compose recipe from that commit.
5. Builds the application image, starts the isolated stack, runs readiness checks, and activates its hostname.
6. Returns the deployment ID, exact commit, URL, and expiration.

Keep a bounded, credential-isolated Git cache per project to avoid full repeated clones; serialize updates to shared caches and build from separate workspaces. Handle submodules and Git LFS only when explicitly enabled and their additional repository/object access is configured. Repository credentials remain outside application build contexts and runtime containers; private dependency downloads use separately scoped build secrets.

Only registered/authorized projects are fetched, not every repository in the organization. A later GitHub App integration can simplify enrollment across selected repositories. Local uncommitted or unpushed work is absent from this Git flow; optional `--source .` upload would be a separate future mode with explicit exclusions, size limits, and a recorded source digest. CI-built images are the alternative already described in section 6.5.

### What is needed before the first deployment?

The local Docker slice now supports authenticated project registration, public HTTPS source, immutable commit builds, isolated previews, visitor status pages, and TTL/lifecycle commands. The next server pilot completes the end-to-end path: connect CLI → enroll one repository → fetch a commit → build one Dockerfile → expose a healthy HTTPS preview → list/log/stop it. Include API authentication, isolation/resource limits, and a persisted TTL from the first server used for real projects. Add constrained Compose, recovery hardening, and the complete sleep/review workflow incrementally.

Before provisioning an actual host, choose its address/OS/architecture, administrator SSH access, DNS provider for `surdic.run`, Git provider and first repository, and the project's build/port/health configuration. These choices configure the installation; they do not require changes to the overall product design.

## 13. Delivery scope

**First usable version:** one Linux server; CLI/API; repository registration; Dockerfile, prebuilt-image, and constrained Compose support; project onboarding/validation; multiple branch deployments; generated/chosen subdomains; wildcard HTTPS; health checks; logs/list/inspect; TTL; explicit sleep/wake/stop; idle tracking and wake-on-request; scoped tokens; JSON output; idempotency; resource quotas; basic ownership; retained-data cleanup; restart recovery.

Follow the implementation milestones in [ROADMAP.md](ROADMAP.md). Establish authentication, persistence, agent output contracts, isolation, and capacity admission first; ship a Dockerfile-based v0.1 pilot with HTTPS and enforced TTL; then add multi-service/batch support and complete idle sleep/recovery for v0.2. Advanced review features follow the core release. The initial pilot is deliberately narrower than the core-complete feature set listed above.

After the reliable deployment lifecycle, prioritize **presets and resettable data → preview sets and stable aliases → automatic verification and receipts → revision-specific feedback**. Add explicit handoff alongside presets, and richer sharing/budget controls as review workflows grow. Introduce managed framework/static-site builders once the Dockerfile path establishes the build contract.

**Further extensions:** pull-request hooks, automatic redeploy policies, a small read-only dashboard, external customer domains, MCP integration, declarative routing to multiple public services, stateless seamless updates, and multiple servers. Keep production hosting, high availability, and arbitrary untrusted-code execution outside the initial promise. Extending a lease and updating application code remain separate operations in every workflow.

Acceptance scenarios should include four branches running simultaneously; independent databases and networks; TTL expiration despite continuous traffic; sleep followed by a successful wake; one startup for many simultaneous wake requests; repeated agent requests creating only one deployment; expired stacks staying offline after reboot; failed builds leaving sibling demos healthy; subdomain collisions leaving the original route intact; and renewal racing safely with cleanup.

Project-integration checks should cover a single Dockerfile app, an image-only deployment, a database-backed Compose app, a monorepo build context, and an app accidentally bound to localhost receiving a useful readiness error. Extension checks should verify that reset affects only the selected environment, old feedback stays tied to the old revision, a failing check cannot promote an alias, and handoff cannot exceed the recipient/project policy.

The finished workflow is a single command that returns a working preview URL, with an explicit lifetime and enough structured information for an agent to manage the entire deployment without editing proxy configuration or logging into the server.
