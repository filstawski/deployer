---
name: deployer
description: Deploy Git branches as temporary preview environments with the Deployer CLI (`deployer`). Use when asked to deploy a branch or pull request for review, share a preview URL, prepare a project for Deployer (Dockerfile and deployer.yaml), check, extend, stop, or remove previews, or debug a preview whose build or health check failed.
---

# Deployer

Deployer turns a pushed Git branch into an isolated preview at
`https://NAME.<domain>` on the team's own server, and removes it when its
lifetime ends. You drive it with the `deployer` CLI. The server builds what is
pushed to the Git host, never your local files.

Read the reference that matches the task:

- [references/recipes.md](references/recipes.md): writing or fixing `deployer.yaml` and the Dockerfile, with templates for common stacks.
- [references/commands.md](references/commands.md): every command, its flags, and its JSON output.
- [references/troubleshooting.md](references/troubleshooting.md): what each error code means and how to fix it.
- [references/concepts.md](references/concepts.md): lifecycle, states, limits, and how the server works.

The CLI prints the same documents: `deployer skill`, `deployer skill recipes`, and
so on. They match the installed version.

## Ground rules

- **Always pass `--json`.** Stdout then holds exactly one JSON value; progress
  goes to stderr. Never parse the human-readable output.
- **Check the exit code first**, then `error.code` in the JSON:

  | Exit | Meaning | Do |
  | --- | --- | --- |
  | 0 | Success | Use the result. |
  | 1 | The operation failed | Read `error.code` and `error.hint`; see troubleshooting. |
  | 2 | Invalid input | Fix the command; retrying the same command won't help. |
  | 3 | Not logged in or not allowed | Stop and tell the user; don't work around it. |
  | 4 | Stopped waiting; the work continues | `deployer operation wait OPERATION_ID --json` |
  | 5 | Not found | Check names with `deployer list --json`. |

- **Without a terminal the CLI never prompts.** It uses defaults or reports what
  is missing. `destroy` needs `--yes`, and `extend` needs `--by`.
- **Each deploy creates a new deployment**, even for the same branch. To show new
  code, deploy again, then destroy the old preview you created.
- **Lifetimes start when the app is ready.** Visits never extend them. The
  server caps lifetimes: see `deployments.max_ttl_seconds` in `deployer status --json`.
- **Only manage previews you created**, unless the user asks otherwise.
- **Never put secrets in `deployer.yaml`**; it is committed to the repository.
- **Leave server administration to people.** `deployer server ...`, `deployer token ...`,
  and `deployer github setup` need an administrator; don't run them unless asked.

## 1. Check access

```sh
deployer status --json
```

- Exit 0: you are connected. Note `domain`, and the lifetime limits under `deployments`.
- Exit 3: there's no login. Ask the user for the server URL and a token. Never guess
  them. Then either set `DEPLOYER_URL` and `DEPLOYER_TOKEN` in the environment, or
  run `deployer login URL --token-stdin` with the token on standard input.
- `deployer: command not found`: ask the user to install the CLI with
  `curl -fsSL https://github.com/filstawski/deployer/releases/latest/download/install.sh | sh`.

## 2. Make sure the project has a recipe

At the repository root:

```sh
deployer validate --json
```

If it reports that `deployer.yaml` is missing or invalid, create or fix the
recipe following [references/recipes.md](references/recipes.md). The essentials:

- a Dockerfile that builds the app and starts it;
- the app listens on `0.0.0.0` (not `localhost`) on the port in `deployer.yaml`,
  ideally read from the `PORT` environment variable;
- a health check path that answers with a status below 400 once the app is ready;
- a `deployer.yaml` at the repository root:

  ```yaml
  version: 1
  source:
    dockerfile: Dockerfile
    context: .
  http:
    port: 3000
    healthcheck:
      path: /health
      timeout: 90s
  ```

## 3. Push, then deploy

Commit and push the branch first: `git push -u origin BRANCH`. The server never
sees uncommitted or unpushed work; the CLI warns about it on stderr. Then:

```sh
deployer deploy --ref BRANCH --ttl 1d --idempotency-key TASK-BRANCH --json
```

- Inside the repository, the project is found from the `origin` remote, and
  registered on first use. Elsewhere, name it: `deployer deploy PROJECT --ref BRANCH`.
- `--name NAME` sets the URL to `https://NAME.<domain>`. Names are lowercase
  letters, digits, and hyphens, and must not belong to another live deployment.
  Without it a name such as `shop-feature-login-k7m2q` is generated.
- `--idempotency-key` makes retries safe: repeating the command with the same
  key returns the same deployment. Use a key that is stable for this task and
  branch, and a new key for a genuinely new deployment.
- The command waits until the app is healthy, up to 30 minutes. With `--detach`,
  it returns right away; wait later with `deployer operation wait ID --json`.

On success, report `deployment.url` and `deployment.expires_at` from:

```json
{
  "deployment": {
    "name": "shop-feature-login-k7m2q",
    "state": "running",
    "url": "https://shop-feature-login-k7m2q.previews.example.com",
    "ref": "feature/login",
    "commit": "7d63b83b6f7e029c4be79244204d87c9ef71a460",
    "expires_at": "2026-10-13T10:00:00Z"
  },
  "operation": { "id": "op_8cf3q2m9xkwz", "status": "succeeded" }
}
```

On failure, the exit code is 1, and the JSON holds `error.code`, `error.message`,
and usually `error.hint` with the next command to run. Follow
[references/troubleshooting.md](references/troubleshooting.md). Don't retry
`BUILD_FAILED` or `HEALTHCHECK_FAILED` unchanged: they need a code or recipe fix,
and a new push.

## 4. Manage previews

| Task | Command |
| --- | --- |
| List previews | `deployer list --json` (add `--project NAME`) |
| One preview, with its last error | `deployer inspect NAME --json` |
| App output | `deployer logs NAME --json` (`--lines 500` for more) |
| Build output | `deployer logs NAME --build --json` |
| Keep it longer | `deployer extend NAME --by 2d --json` |
| Take it offline, keeping it | `deployer stop NAME --json` |
| Bring it back, without rebuilding | `deployer start NAME --json` |
| Remove it now | `deployer destroy NAME --yes --json` |

An expired preview answers HTTP 410 and is stopped. Within the retention period
(7 days by default), `extend` gives it a new lifetime and `start` brings it back.
After that it is removed automatically.
