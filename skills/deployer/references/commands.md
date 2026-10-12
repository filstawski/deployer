# Deployer CLI commands

Pass `--json` to every command. On success stdout holds the result shown here.
On failure it holds `{"error": {"code", "message", "hint"}}`, and the exit code
is non-zero. Times are UTC in RFC 3339 format. Names and IDs (`dep_...`) are
interchangeable wherever a deployment is expected.

## Connection

The CLI uses, in order: `DEPLOYER_URL` with `DEPLOYER_TOKEN` (or
`DEPLOYER_TOKEN_FILE`); the context named by `--context` or `DEPLOYER_CONTEXT`;
and the current saved context.

| Command | Result |
| --- | --- |
| `deployer status --json` | `{"version", "domain", "preview_url", "api_url", "deployments": {"live", "max", "default_ttl_seconds", "max_ttl_seconds"}, "github_configured", "token": {...}}` |
| `deployer login URL --token-stdin --json` | `{"context", "url", "server": ServerStatus}`. Reads the token from stdin. URL is `https://api.<domain>` or `ssh://user@host`. |
| `deployer context list --json` | `{"current", "contexts": {"NAME": {"url", "token_file"}}}` |
| `deployer context use NAME --json` | `{"current"}` |
| `deployer logout [NAME] --json` | `{"removed", "current"}`. Deletes the saved token. |

## Deploying

```sh
deployer deploy [PROJECT] --ref BRANCH [--name NAME] [--ttl 1d] [--idempotency-key KEY] --json
```

| Flag | Meaning |
| --- | --- |
| `--ref`, `-r` | Branch or full commit SHA. Default: the current branch of the repository in the working directory. |
| `--name`, `-n` | Preview name and subdomain. Default: generated, such as `shop-feature-login-k7m2q`. |
| `--ttl` | Lifetime after readiness: `30m`, `4h`, `2d`, `1w`. Default: the server's `default_ttl`. |
| `--detach`, `-d` | Return once queued, without waiting. |
| `--follow`, `-f` | Stream build output to stderr while waiting. |
| `--timeout` | How long to wait (default `30m`); the deployment continues afterwards. |
| `--idempotency-key` | Retrying with the same key returns the same deployment. |
| `--yes`, `-y` | At a terminal, skip the form. Without a terminal there is never a form. |

The result is `{"deployment": Deployment, "operation": Operation}`. Without
`PROJECT`, the project comes from the repository's `origin` remote, and is
registered if needed. Outside a repository, `PROJECT` and `--ref` are required.

## Deployments

| Command | Result |
| --- | --- |
| `deployer list [--project NAME] [--all] --json` | `[Deployment, ...]`, newest first. `--all` includes destroyed ones. |
| `deployer inspect NAME --json` | `Deployment` |
| `deployer logs NAME [--lines 200] --json` | `{"logs": "..."}`, the app's recent output. |
| `deployer logs NAME --build --json` | `{"logs": "...", "offset": N}`, the end of the build log. |
| `deployer extend NAME --by 2d --json` | `Deployment` with the new `expires_at`. |
| `deployer stop NAME --json` | `Deployment` once stopped. |
| `deployer start NAME --json` | `Deployment` once healthy. |
| `deployer destroy NAME --yes --json` | `Deployment` with state `destroyed`. |
| `deployer open NAME --json` | `{"url": "..."}` |
| `deployer operation show ID --json` | `Operation` |
| `deployer operation wait ID [--timeout 30m] --json` | `Operation` once finished. Exit 1 if it failed. |

`stop`, `start`, and `destroy` wait up to `--timeout` (default `10m`). With
`--detach` they return the `Operation` right away.

### Deployment

```json
{
  "id": "dep_k7m2q9x4abcd",
  "name": "design-a",
  "project": "shop",
  "ref": "design/a",
  "commit": "7d63b83b6f7e029c4be79244204d87c9ef71a460",
  "state": "running",
  "url": "https://design-a.previews.example.com",
  "ttl_seconds": 86400,
  "created_at": "2026-10-12T09:58:12Z",
  "ready_at": "2026-10-12T10:00:03Z",
  "expires_at": "2026-10-13T10:00:03Z",
  "purge_at": "2026-10-20T10:00:03Z",
  "operation_id": "op_8cf3q2m9xkwz",
  "error": { "code": "BUILD_FAILED", "message": "...", "hint": "..." }
}
```

Some fields are left out above, such as `hostname`, `created_by`, and
`updated_at`. `state` is one of `queued`, `building`, `starting`, `running`,
`stopped`, `expired`, `failed`, or `destroyed`. `error` appears only after a
failure.
`expires_at` stays unset until the first successful health check. `purge_at` is
when it will be destroyed automatically.

### Operation

```json
{
  "id": "op_8cf3q2m9xkwz",
  "kind": "deployment.create",
  "status": "succeeded",
  "deployment_id": "dep_k7m2q9x4abcd",
  "attempts": 1,
  "created_at": "2026-10-12T09:58:12Z",
  "finished_at": "2026-10-12T10:00:03Z"
}
```

`kind` is `deployment.create`, `deployment.start`, `deployment.stop`,
`deployment.destroy`, or `project.verify`. `status` is `queued`, `running`,
`succeeded`, `failed`, or `cancelled`. A failed operation has an `error`.

## Projects

| Command | Result |
| --- | --- |
| `deployer project list --json` | `[{"id", "name", "repository", "created_at"}, ...]` |
| `deployer project add NAME --repo URL --json` | The project. GitHub SSH URLs are converted to HTTPS. |
| `deployer project show NAME --json` | The project. |
| `deployer project verify NAME --json` | The operation; exit 1 if the server can't read the repository. |
| `deployer project remove NAME --yes --json` | Only works once it has no live deployments. |
| `deployer validate [DIR] --json` | `{"valid": true, "dockerfile", "context", "port", "health_path", "health_timeout"}`, or exit 2 with the problem. Reads local files from DIR, the repository root (default: the current directory). |
| `deployer init [--port N] [--health PATH] --json` | Writes `deployer.yaml`: `{"path": "..."}`. |

## Other

| Command | Result |
| --- | --- |
| `deployer version --json` | `{"version"}` |
| `deployer upgrade --check --json` | `{"version", "latest", "update_available"}` |
| `deployer skill [TOPIC]` | Prints this skill. Topics: `recipes`, `commands`, `troubleshooting`, `concepts`. |

Administrator commands, which agents should leave alone unless asked:
`deployer token ...`, `deployer github setup`, `deployer server install|upgrade|uninstall`.

## Exit codes

| Exit | Meaning |
| --- | --- |
| 0 | Success |
| 1 | The request or operation failed |
| 2 | Invalid command line or input |
| 3 | Not logged in, invalid token, or missing permission |
| 4 | Stopped waiting; the operation continues on the server |
| 5 | Not found |
