# Deployer implementation roadmap

Status: the local Dockerfile deployment slice is implemented and exercised against real Docker through the CLI/API. Public HTTPS source fetching, commit checkpoints, isolated previews, visitor status pages, logs, stop/wake/extend/destroy, TTL, and scoped cleanup are implemented. Local release packaging, installers, saved SSH connections, and existing-certificate HTTPS setup are also implemented. Real-host acceptance, release publication, private Git enrollment, certificate automation, idle sleep, and full reboot recovery remain open. See [docs/docker-previews.md](docs/docker-previews.md) for the exact supported behavior and verification limits.

**First target:** use the CLI from a laptop or the server to deploy a Git branch to `https://<name>.surdic.run`, inspect it, and stop it—with authentication, isolated resources, and an expiration deadline already working.

**Core-complete target:** run several branches and multi-service applications simultaneously, put inactive deployments to sleep, wake them on demand, extend their lifetime, and recover correctly after a server restart.

## Delivery rules

- Work through milestones in order. Ship a working end-to-end increment at each release boundary.
- Use one Linux server, one controller, SQLite, Docker/Compose, and the existing nginx edge (Caddy remains an option for fresh hosts). Defer distributed scheduling.
- Keep CLI behavior and API behavior consistent. The server owns state and decisions; the CLI is a client.
- Build authentication, ownership checks, idempotency, resource limits, and expiration into the foundation. Add capabilities within those boundaries.
- Every task updates its relevant documentation and includes checks proportionate to the behavior changed. Use integration tests for real Docker/network behavior and deterministic clocks for lifecycle logic.
- A milestone is done only when its acceptance scenario passes and limitations are recorded. Do not equate implemented handlers with a working deployment.
- Keep this checklist current as implementation progresses. Future features must not delay the first useful installation.

## Milestones and release boundaries

| Milestone | Result | Prerequisites | Release |
| --- | --- | --- | --- |
| M0 | Repository skeleton and stable initial contracts. | None. | Internal. |
| M1 | Authenticated CLI/API with persistent operations. | M0. | Internal. |
| M2 | One branch builds, runs, routes, expires, and stops locally. | M1, a Docker-capable test host. | Local working slice. |
| M3 | Install on the real server and deploy through SSH to HTTPS. | M2, host/DNS/repository access. | **v0.1 pilot.** |
| M4 | Multi-service apps and parallel branch batches. | M3. | Pilot expansion. |
| M5 | Idle sleep/wake and complete recovery/retention behavior. | M4. | **v0.2 core complete.** |
| M6 | Demo presets, resettable data, ownership handoff. | M5. | v0.3. |
| M7 | Review pages, stable aliases, verification, feedback. | M6. | v0.4. |
| M8 | Additional build/source/integration conveniences. | Core use demonstrates demand. | Later. |

Version labels define scope, not dates. Estimate calendar time after M2 demonstrates the build/network path against an actual test environment.

## M0 — establish a small, buildable foundation

- [x] Add one Go module; select and pin the supported Go toolchain and dependencies at implementation time.
- [x] Add the three executable entry points: `deployer`, `deployerd`, and `deployer-gateway`.
- [ ] Define IDs, deployment/revision/operation records, lifecycle transitions, and typed errors.
- [ ] Define the initial `/v1` API, JSON result/error envelopes, and configuration schemas.

  Implemented: [OpenAPI for the current endpoints](api/openapi.json), result/error envelopes, and the server configuration schema. [Project recipe schema](schemas/project.schema.json) and deployment/operation contracts are now implemented for the single-service slice.
- [x] Add validated server configuration with overridable test paths and production defaults from the concept.
- [x] Add migration infrastructure and the initial SQLite schema.
- [x] Add a tiny sample HTTP application that identifies its build revision and provides `/health`.
- [ ] Add repeatable build/check commands and CI for compilation, formatting, static analysis, and unit tests.

**Done when:** a fresh checkout builds all three binaries; CLI help/version work; the controller starts with temporary state, migrates the database, and reports its version/readiness. Invalid configuration returns actionable errors. No production credentials are required.

## M1 — make the CLI and server work together

- [x] Implement private API access and an administrator-only local bootstrap flow.
- [x] Issue/revoke scoped API tokens and store only their hashes server-side.
- [x] Implement direct local API contexts and credential references without storing secrets in normal context configuration.
- [x] Add project create/list/inspect and the initial token/project permission checks.
- [x] Persist operations, normalized request hashes, idempotency keys, execution attempts, and events for repository verification. Multi-step Docker checkpoints follow in M2.
- [x] Implement bounded workers, cancellation boundaries, timeouts, and recovery of interrupted read-only verification operations.
- [x] Implement `--json`, stable errors/exit behavior, and `--wait`; keep progress off JSON stdout.
- [x] Add a read-only operation inspection endpoint and CLI command.

**Done when:** an authenticated CLI can register/list a project; unauthorized access is rejected; a repeated idempotency key returns the same operation; conflicting input returns a conflict. Restarting the controller preserves accepted work. A client timeout does not cancel server work.

## M2 — implement the first actual deployment

- [ ] Generate per-project Git keys, expose only public keys, and verify repository access and Git host identity.
- [x] Fetch a ref, record its exact commit, and create an isolated operation workspace.
- [x] Parse the first project recipe: one Dockerfile, build context, internal port, readiness route, and ordinary settings.
- [ ] Add explicit secret references without committing values to project recipes.
- [x] Reserve hostname and capacity transactionally before a build starts.
- [x] Publish a gateway status page at the reserved hostname for queued/building/starting/failed/offline/expired states; switch to application traffic only after readiness.
- [x] Build a tagged image with bounded concurrency, timeout, and resource usage; retain the resolved image identity.
- [x] Run an isolated single-service container with controlled networks, labels, resource limits, and no host-published application ports. The current adapter uses Docker CLI argument arrays; generated Compose plans move to M4.
- [x] Start the service, check readiness, and route a local test hostname through the gateway.
- [x] Add deploy/list/inspect/logs/stop/wake/extend/destroy commands and persisted TTL handling.
- [ ] Add explicit renewal of an expired deployment within retention.
- [x] Enforce access deadlines at the gateway and stop expired resources through reconciliation.
- [x] Retain a stopped/expired deployment's image and data until its recorded purge deadline; implement scoped cleanup.
- [ ] Reconcile resources after failed builds, interrupted startup, and controller restarts; prevent duplicate stacks on retries.

**Done when:** the sample repository's two different branches run concurrently with distinct URLs and correct content. Visiting during a slow build/start shows a status page that continues to the original requested path after readiness; failures end the spinner without exposing logs. Stopping one leaves the other healthy. Continuous requests cannot extend TTL. Invalid health checks yield useful diagnostics. A forced controller restart does not create duplicate resources or revive an expired preview. Cleanup leaves unrelated Docker resources untouched.

**Verification so far:** real Docker builds and two running revision fixtures through the authenticated CLI/API; network/port/resource assertions; visitor pages; logs; extension; stop/wake; route reconstruction; expiry and destruction. Separate real Git tests verify branch fetching and pinned retries. Public/private remote Git connectivity and forced process/host-crash recovery are not yet covered by the Docker test.

Use local test hostnames/Host headers here. Public DNS and certificate issuance are M3 integration work. Declare automatic idle sleep unsupported until M5; do not silently accept policy fields that are not enforced.

## M3 — install and use it on the actual server

- [ ] Package release binaries and digest-pinned infrastructure images with checksums and version metadata.
- [ ] Implement an installer preflight for OS/architecture, Docker/Compose, ports, storage, SSH, and existing installation state.
- [ ] Create the documented host directories, permissions, service identities, and systemd unit.
- [x] Include the CLI at `/usr/local/bin/deployer` in the server setup flow.
- [x] Add a checksummed bootstrap installer for macOS/Linux clients and Linux server bundles.
- [x] Add versioned cross-platform release builds for `filstawski/deployer` (publication is separate).
- [x] Add a dry-run plan, service/account/configuration setup, credential bootstrap, and same-release resume protection.
- [ ] Validate a fresh install and resume on a real Linux/systemd host before release.
- [ ] Add Cloudflare certificate issuance/renewal and guided SSH pairing.
- [ ] Polish the GitHub README after the published install commands pass acceptance.
- [x] Add SSH-backed CLI contexts, strict host-key verification, private local sockets, command-lifetime cleanup, and credential binding.
- [ ] Add guided credential enrollment and server version compatibility checks.
- [ ] Integrate existing nginx with Cloudflare DNS-01 wildcard certificates and unknown-host rejection. Add explicit external HTTPS URL/scheme settings; use Caddy only for an installation that chooses it.

  The first host already has nginx and Docker. See [server pilot preparation](docs/server-pilot.md). Installer code and existing-certificate nginx integration are now available; see [installation](docs/installation.md). Live SSH inspection, real-host validation, and installation remain pending.
- [ ] Add a protected-preview option for private customer demos.
- [ ] Add server status/preflight diagnostics, bounded logs, and a documented backup/restore procedure.
- [ ] Make installation repeatable without replacing configuration or erasing state; specify compatible upgrade behavior and migration failure handling.
- [ ] Deploy one real project from both the laptop CLI and the server CLI.

**Done when:** a fresh host can be installed with documented steps and serves two simultaneous branch previews under `surdic.run`. Both CLI locations manage the same deployments. Disconnecting SSH leaves accepted work running. Reboot restores eligible deployments, keeps expired ones offline, and retains data. The management API and application database ports are not exposed publicly. A protected preview rejects unauthenticated visits.

**v0.1 pilot ships here.** Document limitations: one public HTTP service per deployment, Dockerfile source builds, no automatic idle sleep, and one Linux host. Run the full failure/cleanup checks before using it with customer data.

## M4 — cover normal project shapes

- [ ] Support prebuilt images resolved to digests, with explicit source provenance.
- [ ] Implement a documented allowlist of supported Compose fields. Reject unsafe/unsupported options before allocation or execution.
- [ ] Add per-deployment database/cache/worker services, private backend networking, namespaced volumes, and aggregate resource admission.
- [ ] Implement dependency readiness and bounded migration jobs; distinguish first initialization from waking existing data.
- [ ] Add a database-backed sample project and a monorepo sample.
- [ ] Add `deployer init` for supported templates and `deployer validate` with machine-readable diagnostics.
- [ ] Implement repeated `--ref`, saved preview sets, subdomain templates, and independent batch outcomes.
- [ ] Implement explicit revision updates with honest maintenance/data-migration behavior.

**Done when:** `main`, `design/a`, `design/b`, and `feat/something` run together with independent databases. One branch failing does not destroy successful siblings. Compose files cannot request host networking, shared external data, Docker socket access, or arbitrary host mounts. Updating code does not silently reset the lease or demo data.

## M5 — complete sleep, retention, and recovery

- [ ] Track qualifying external activity in the gateway; exclude platform probes and account for in-flight requests/WebSockets/streams.
- [ ] Implement idle deadlines, manual sleep, explicit stop semantics, and opt-out for background-worker applications.
- [ ] Coalesce wake requests into one operation; bound wake time, retries, and request rate.
- [ ] Provide browser waiting responses and API `503`/`Retry-After` behavior without replaying unsafe requests.
- [ ] Reuse the status page for wakeups; enforce preview access before waking, preserve the original URL, and exclude platform polling from idle activity.
- [ ] Serialize sleep/wake/extend/renew/expire/purge transitions and recheck the current policy before side effects.
- [ ] Complete retention accounting across images, volumes, workspaces, logs, and artifacts.
- [ ] Exercise disk-full behavior, lost activity reports, container crashes, Docker outages, and interrupted cleanup.
- [ ] Exercise restore from a consistent database/configuration/volume backup.
- [ ] Publish CLI/API reference, schemas, and a concise agent workflow guide.

**Done when:** idle apps release compute resources and wake on a visit; simultaneous visitors produce one wake operation; data survives sleep. The wake page opens the original route automatically; wake failures stop polling; API writes are never replayed and do not receive success-status HTML. Expired/manual-stopped apps do not wake automatically. Lease changes racing with cleanup have deterministic outcomes. A host restart and backup restoration preserve deployment identity, deadlines, and data policies.

**v0.2 delivers the original requested feature set.** Use it for real demo projects before expanding product scope.

## M6 — make repeat demos quick

- [ ] Add versioned presets for branding settings, feature flags, access, lifetime, and synthetic data.
- [ ] Add explicit, authorized reset jobs with traffic draining and writer coordination.
- [ ] Record purpose, creator, current owner, and optional originating agent/task reference.
- [ ] Implement owner handoff with an explicit lease change subject to recipient/project policy.
- [ ] Expand budget reporting to build usage and retained storage.

**Done when:** a customer-demo preset produces usable sample data; reset restores the known state without affecting another deployment; handoff changes owner/deadline without changing the reviewed revision.

## M7 — add the review workflow

- [ ] Add a review record and a small page linking to each variant, exact revision, and availability.
- [ ] Add stable aliases with readiness/access checks and atomic target changes.
- [ ] Run bounded smoke/browser checks in isolated workers; produce screenshots and deployment receipts.
- [ ] Gate alias promotion on required verification checks.
- [ ] Add comments and preference selections tied to immutable revisions.
- [ ] Add expiring review shares and artifact/feedback retention rules.

**Done when:** a reviewer opens one URL, compares variants, leaves revision-specific feedback, and an agent can retrieve it as structured data. Promoting a tested revision preserves the customer link; old feedback stays on the version originally reviewed.

## M8 — extend only from demonstrated needs

- Managed framework/static-site builds without a project Dockerfile.
- Git provider app integration, pull-request hooks, and explicit redeploy policies.
- Local source upload with exclusions, size limits, and content identity.
- External customer domains and declarative frontend/API routes.
- MCP adapter backed by the same API.
- Stateless seamless updates, stronger untrusted-code isolation, or multiple workers if the workload warrants them.

## First implementation queue

These are the first commit-sized tasks, in dependency order. They map to the modules in the architecture document.

| Task | Deliverable | Verification |
| --- | --- | --- |
| DPL-001 | Go module, three binary entry points, build/check commands. | All binaries build; CLI help/version work. |
| DPL-002 | Server/project config types, schemas, validation, test path overrides. | Valid examples load; invalid ports/paths/unknown fields fail clearly. |
| DPL-003 | Core records, SQLite migrations, transactions, injectable clock. | Fresh/reopened database preserves records and timestamps. |
| DPL-004 | API version/readiness, token bootstrap, auth middleware, API client. | Allowed requests succeed; missing/revoked/wrong-scope tokens fail. |
| DPL-005 | Local CLI context, project commands, JSON/error contract. | CLI talks to a real temporary controller with stable output. |
| DPL-006 | Durable operation queue, idempotency, wait/inspect, restart recovery. | Retry/disconnect/restart preserve a single operation. |
| DPL-007 | Repository credentials, verification, fetch, exact commit workspaces. | Two refs produce distinct isolated workspaces; failed auth is actionable. |
| DPL-008 | Resource policy, Docker build adapter, single-service Compose generation. | Bounded build yields an image; prohibited settings cannot run. |
| DPL-009 | Readiness, gateway routing/status pages, hostname reservation, deploy/list/logs. | Two branches serve distinguishable content; visits during build/start progress to the original route. |
| DPL-010 | Stop/wake, persisted TTL, extension/renewal, owned-resource cleanup. | Expiry/restart/cleanup tests pass; unrelated resources remain. |

Complete DPL-001 through DPL-010 on a local Linux test host before developing the production installer. They are implementation work, not another planning phase.

## Decisions needed for the real installation

These do not block M0/M1. Gather them while implementing the local path; never store private keys or tokens in planning documents.

| Input | Needed by | Why |
| --- | --- | --- |
| Test Docker environment (Linux VM, host, or suitable local Docker setup). | M2. | Verify actual containers, isolation, and networking. |
| Server address, Linux distribution, architecture, SSH administrator. | M3. | Installer target and release packaging. |
| CPU/RAM/disk capacity and any existing proxy on 80/443. | M3. | Set quotas and avoid service conflicts. |
| DNS provider and control of `surdic.run`. | M3. | Wildcard routing and certificate automation. |
| Git provider and first pilot repository/branch. | M3. | Verify private source access with a real app. |
| Pilot build recipe, internal port, readiness path, required secrets. | M3. | Reproducible first deployment. |
| Backup destination and acceptable data loss/recovery time. | M3. | Set an operational recovery policy. |

**Immediate next work:** complete configuration/API contracts alongside DPL-007/DPL-008, implement private Git enrollment and commit workspaces, and connect the build/runtime adapters. DPL-001 and the local control-plane portions of DPL-003 through DPL-006 are in place; DPL-002 has server validation but still needs project-recipe validation. The next user-visible result should be an actual branch preview.
