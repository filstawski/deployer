# HTTP API

The CLI is a client of this API, and anything it does is available over HTTP.
The base URL is the server's API address, such as
`https://api.previews.example.com`. On the server itself it is
`http://127.0.0.1:7420`.

## Conventions

**Authentication.** Every `/v1` request needs a token:
`Authorization: Bearer dpl_...`. The exceptions are `GET /healthz` and the GitHub
setup pages, which use single-use links.

**Responses** are JSON envelopes:

```json
{ "data": { ... }, "request_id": "req_k7m2q9x4abcd" }
{ "error": { "code": "NOT_FOUND", "message": "deployment \"x\" not found", "hint": "..." }, "request_id": "req_..." }
```

`request_id` is also sent as the `X-Request-ID` header, and appears in the
server log.

**Errors** carry a stable `code`; the [agent guide](agents.md#error-codes) lists
them. HTTP status codes follow from the code:

| Status | Codes |
| --- | --- |
| 400 | `INVALID_INPUT`, `UNSUPPORTED`, and operation failure codes |
| 401 | `UNAUTHENTICATED` |
| 403 | `FORBIDDEN` |
| 404 | `NOT_FOUND`, also for resources the token may not see |
| 409 | `ALREADY_EXISTS`, `NAME_TAKEN`, `IDEMPOTENCY_CONFLICT`, `INVALID_STATE`, `OPERATION_IN_PROGRESS`, `GITHUB_NOT_CONFIGURED` |
| 429 | `QUOTA_EXCEEDED` |
| 500 | `INTERNAL` |
| 502 | `GITHUB_FAILED` |
| 503 | `UNAVAILABLE` |

**Asynchronous work.** Building, starting, stopping, destroying, and verifying run
as operations. These endpoints return `202 Accepted` with an operation. Poll
`GET /v1/operations/{id}` until `status` is `succeeded`, `failed`, or `cancelled`.

**Idempotency.** Send an `Idempotency-Key` header with `POST` and `DELETE`
requests. Repeating a request with the same key returns the original result.
Using the same key with a different request returns `IDEMPOTENCY_CONFLICT`. Keys
are scoped to the token and kept for 7 days.

**Bodies** are JSON objects of at most 64 KiB. Unknown fields are rejected.
Times are RFC 3339 in UTC; durations are in seconds.

Path parameters accept a name or an ID: `{project}` is `shop` or `prj_...`, and
`{deployment}` is `design-a` or `dep_...`.

## Endpoints

| Method and path | Scope | Description |
| --- | --- | --- |
| `GET /healthz` | none | Liveness: `{"status": "ok"}` |
| `GET /v1/server` | `server:read` | Server status and the calling token |
| `GET /v1/projects` | `projects:read` | List projects |
| `POST /v1/projects` | `projects:write`, all projects | Register a project |
| `GET /v1/projects/{project}` | `projects:read` | Get a project |
| `DELETE /v1/projects/{project}` | `projects:write` | Remove a project without live deployments |
| `POST /v1/projects/{project}/verify` | `projects:write` | Check repository access (operation) |
| `GET /v1/deployments` | `deployments:read` | List deployments: `?project=shop`, `?all=true` includes destroyed |
| `POST /v1/deployments` | `deployments:write` | Create a deployment (operation) |
| `GET /v1/deployments/{deployment}` | `deployments:read` | Get a deployment |
| `POST /v1/deployments/{deployment}/stop` | `deployments:write` | Stop (operation) |
| `POST /v1/deployments/{deployment}/start` | `deployments:write` | Start (operation) |
| `POST /v1/deployments/{deployment}/extend` | `deployments:write` | Extend the lifetime |
| `POST /v1/deployments/{deployment}/update` | `deployments:write` | Rebuild from the latest commit (operation) |
| `DELETE /v1/deployments/{deployment}` | `deployments:write` | Destroy (operation) |
| `GET /v1/deployments/{deployment}/logs` | `deployments:read` | App or build output |
| `GET /v1/operations/{operation}` | read scope of its resource | Get an operation |
| `GET /v1/tokens` | `admin` | List tokens |
| `POST /v1/tokens` | `admin` | Create a token |
| `DELETE /v1/tokens/{token}` | `admin` | Revoke a token |
| `GET /v1/github` | `server:read` | GitHub connection status |
| `POST /v1/github/setup` | `admin` | Start creating the GitHub App |

Tokens see only the projects, deployments, and operations they are granted.

## Resources

### Deployment

```json
{
  "id": "dep_k7m2q9x4abcd",
  "name": "design-a",
  "project_id": "prj_9s6vn7ax49yx",
  "project": "shop",
  "ref": "design/a",
  "commit": "7d63b83b6f7e029c4be79244204d87c9ef71a460",
  "state": "running",
  "hostname": "design-a.previews.example.com",
  "url": "https://design-a.previews.example.com",
  "ttl_seconds": 259200,
  "created_by": "tok_7d63b83b6f7e",
  "created_at": "2026-10-12T09:58:12Z",
  "updated_at": "2026-10-12T10:00:03Z",
  "ready_at": "2026-10-12T10:00:03Z",
  "expires_at": "2026-10-15T10:00:03Z",
  "purge_at": "2026-10-22T10:00:03Z",
  "operation_id": "op_8cf3q2m9xkwz",
  "image": "sha256:3a3d6ed1...",
  "port": 3000,
  "health_path": "/health"
}
```

`state` is one of `queued`, `building`, `starting`, `running`, `stopped`,
`expired`, `failed`, or `destroyed`. A failed deployment has an `error` object.
`expires_at` is unset until the deployment first becomes ready. `purge_at` is
when it will be destroyed automatically.

### Operation

```json
{
  "id": "op_8cf3q2m9xkwz",
  "kind": "deployment.create",
  "status": "succeeded",
  "project_id": "prj_9s6vn7ax49yx",
  "deployment_id": "dep_k7m2q9x4abcd",
  "actor": "tok_7d63b83b6f7e",
  "attempts": 1,
  "created_at": "2026-10-12T09:58:12Z",
  "updated_at": "2026-10-12T10:00:03Z",
  "started_at": "2026-10-12T09:58:12Z",
  "finished_at": "2026-10-12T10:00:03Z",
  "deadline": "2026-10-12T11:58:12Z"
}
```

`kind` is `deployment.create`, `deployment.update`, `deployment.start`,
`deployment.stop`, `deployment.destroy`, or `project.verify`. `status` is
`queued`, `running`, `succeeded`, `failed`, or `cancelled`. Failed and cancelled
operations have an `error`. Operations that take parameters show them as
`request`. A successful `project.verify` has `"result": {"head_commit": "..."}`,
and a successful `deployment.update` has
`"result": {"previous_commit": "...", "commit": "...", "changed": true}`.

## Requests

**Create a project**: `POST /v1/projects`

```json
{ "name": "shop", "repository": "https://github.com/acme/shop" }
```

Returns `201` with the project. GitHub SSH URLs are converted to HTTPS.

**Create a deployment**: `POST /v1/deployments`

```json
{ "project": "shop", "ref": "design/a", "name": "design-a", "ttl_seconds": 259200 }
```

`name` and `ttl_seconds` are optional. Returns `202` with
`{"deployment": {...}, "operation": {...}}`. The URL is reserved immediately.

**Update**: `POST /v1/deployments/{deployment}/update`

```json
{ "ref": "design/b" }
```

Rebuilds the deployment from the latest commit of its ref, or of `ref` when
given, keeping its name, URL, and lifetime. The body may be empty. Returns `202`
with the operation. A running deployment keeps serving while the new version
builds. Its container is then replaced, and visitors see the progress page until
the new version passes its health check. If it doesn't, the previous version is
started again, and the operation fails. `changed` is `false` when the ref still
pointed at the running commit. Expired deployments must be extended first.

**Extend**: `POST /v1/deployments/{deployment}/extend`

```json
{ "seconds": 86400 }
```

Returns `200` with the deployment.

**Logs**: `GET /v1/deployments/{deployment}/logs?source=app&lines=200`

`source` is `app` (default) or `build`. Returns `{"logs": "..."}`. For build
logs, `offset=N` returns output from byte `N` onward and the next `offset`, for
following a build.

**Create a token**: `POST /v1/tokens`

```json
{ "name": "ci", "scopes": ["deployments:read", "deployments:write"], "projects": ["prj_9s6vn7ax49yx"], "ttl_seconds": 2592000 }
```

`projects` takes IDs or `"*"`. `ttl_seconds: 0` means the token never expires.
Returns `201` with `{"token": {...}, "secret": "dpl_..."}`; the secret is not
shown again.

## Example

```sh
API=https://api.previews.example.com
AUTH="Authorization: Bearer $DEPLOYER_TOKEN"

op=$(curl -fsS -X POST "$API/v1/deployments" -H "$AUTH" -H "Idempotency-Key: build-42" \
  -d '{"project": "shop", "ref": "main"}' | jq -r .data.operation.id)

until status=$(curl -fsS "$API/v1/operations/$op" -H "$AUTH" | jq -r .data.status) &&
  [ "$status" != queued ] && [ "$status" != running ]; do sleep 2; done

curl -fsS "$API/v1/deployments?project=shop" -H "$AUTH" | jq -r '.data[0].url'
```
