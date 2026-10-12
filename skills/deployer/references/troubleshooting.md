# Troubleshooting Deployer

Start from the exit code and `error.code` in the JSON output. `error.hint`, when
present, names the next command; prefer it over guessing. For a deployment that
failed earlier, `deployer inspect NAME --json` shows the same `error`.

Failed, stopped, and expired previews still count toward the server's
deployment limit. Destroy the failed previews you created once you have read
their logs.

## Errors from a deployment

### SOURCE_FAILED: the server couldn't fetch the code

| Message | Fix |
| --- | --- |
| `"BRANCH" was not found in the repository` | Push it: `git push -u origin BRANCH`. Check the spelling, and that the project's repository is the one you pushed to (`deployer project show NAME --json`). |
| `the repository is private or does not exist` | The server can't read the repository. Ask the user to connect GitHub with `deployer github connect`, or to make the repository readable. Don't change the repository's visibility yourself. |
| `the server could not reach the Git host` | A network problem on the server. Retry later with the same idempotency key. |

### INVALID_RECIPE: deployer.yaml is missing or invalid on that commit

Run `deployer validate --json` in the repository. It reads the local files, so
also check that the committed and pushed version matches: `git status`, then
`git log origin/BRANCH -1`. Fix the file using [recipes.md](recipes.md), commit,
push, and deploy again.

### BUILD_FAILED: the Docker build failed

Read the end of the build log:

```sh
deployer logs NAME --build --json
```

Common causes:

- a missing lockfile for `npm ci`, `pnpm install --frozen-lockfile`, and similar;
- a `COPY` of a path that `.dockerignore` excludes, or that is outside `source.context`;
- files that only exist locally: generated files, `.env`, Git submodules, or Git LFS objects;
- running out of memory (2 GiB): the log ends abruptly, or mentions `killed` or exit code 137;
- `the build took longer than 20m`: slow dependency installs, or no layer caching
  because `COPY . .` comes before installing dependencies.

When Docker is available locally, reproduce with `docker build -f DOCKERFILE CONTEXT`.
Fix, commit, push, and deploy again. Retrying unchanged fails the same way.

### HEALTHCHECK_FAILED: the app didn't answer in time

The message reads `GET /health on port 3000 did not succeed within 1m30s (...)`;
the part in parentheses is the last result. Read the app's output:

```sh
deployer logs NAME --json
```

| Last result or log line | Cause and fix |
| --- | --- |
| `connection refused` | Nothing listens on that port and address. Bind to `0.0.0.0`, not `localhost` or `127.0.0.1`, and use the port from `deployer.yaml` (or `$PORT`). |
| a status code such as 404 or 500 | The health path is wrong or erroring. Point `http.healthcheck.path` at a route that answers below 400, such as `/`. |
| `timeout`, or the log shows the app still starting | The app starts slowly. Raise `http.healthcheck.timeout`, up to `5m`. |
| errors calling an external host | There's no outbound network at runtime. Make startup and the health route work without it. |
| missing environment variables or secrets | Add plain values to `environment`, or make the app start without them. Never commit secrets. |

### CONTAINER_EXITED: the app's process ended

`the app exited during startup` or `the app stopped unexpectedly`. The cause is
in `deployer logs NAME --json`. Common causes: a crash on missing configuration,
a database that isn't reachable, an `exit 0` start command (the process must
keep running), or running out of memory (512 MiB). Fix and deploy again. If it
stopped later and the code was fine, `deployer start NAME --json` restarts it.

### Errors from `deployer update`

Updates fail with the same codes as deploys. When the message ends with
`NAME still runs COMMIT`, the previous version is still serving, and nothing
else changed: fix the cause, push, and run `deployer update NAME --json` again.
After a failed start or health check, the new version's output is at the end of
`deployer logs NAME --build --json`, because `deployer logs NAME` shows the
version that is running.

An update of a stopped or failed preview that fails leaves it `failed`, like a
deploy. `NAME has expired` means: extend it first, then update.

### START_FAILED: Docker couldn't create or start the container

Usually a server-side problem. Retry once with `deployer start NAME --json`, or
deploy again. If it repeats, report the message to the user.

### TIMEOUT, INTERRUPTED: the operation didn't finish

The operation hit its deadline, or the server restarted repeatedly while it ran.
Deploy again once. If it repeats, report it to the user.

## Errors from a request

| Code | Exit | Meaning | Fix |
| --- | --- | --- | --- |
| `INVALID_INPUT` | 2 | A bad flag, name, or value, such as a lifetime above the server's maximum. | Read the message. Check limits with `deployer status --json`. |
| `NAME_TAKEN` | 1 | A deployment that isn't destroyed has that name. | Choose another `--name`, or destroy the old preview if it's yours. |
| `ALREADY_EXISTS` | 1 | `project add` with a name that is registered. | Use the existing project: `deployer project show NAME --json`. |
| `QUOTA_EXCEEDED` | 1 | The server is at its limit of deployments, or its queue is full. | Destroy previews you created and no longer need. Otherwise ask the user. |
| `INVALID_STATE` | 1 | The action doesn't fit the state. | Follow the hint; see below. |
| `OPERATION_IN_PROGRESS` | 1 | The deployment has another operation running. | `deployer operation wait ID --json` with the ID from the hint, then retry. |
| `IDEMPOTENCY_CONFLICT` | 1 | The key was already used for a different request. | Use a new key for the new request. |
| `NOT_FOUND` | 5 | No such deployment, project, or operation. | `deployer list --json`, or `deployer project list --json`. |
| `UNAUTHENTICATED` | 3 | No token, or the token is invalid or expired. | Stop and ask the user for a valid token. |
| `FORBIDDEN` | 3 | The token lacks a scope, or access to that project. | Stop and tell the user which action needs access. |
| `GITHUB_NOT_CONFIGURED` | 1 | The server has no GitHub App. | An administrator runs `deployer github setup`. |
| `UNAVAILABLE` | 1 | The server can't reach its database or gateway. | Retry later; report it if it lasts. |
| `UNSUPPORTED` | 1 | The server is too old for the request. | Tell the user; an administrator can upgrade it. |
| `WAIT_TIMEOUT` | 4 | The CLI stopped waiting; the work continues. | `deployer operation wait ID --json`. |
| `CLIENT_ERROR` | 1 | The CLI couldn't connect, or another local problem. | Read the message: check `DEPLOYER_URL`, the network, and the login. |

### INVALID_STATE messages

| Message | Fix |
| --- | --- |
| `NAME has expired` | `deployer extend NAME --by 1d --json`, then `deployer start NAME --json`. |
| `NAME was never built successfully` | Deploy again; there's nothing to start. |
| `NAME is already running` | Nothing to do. |
| `NAME is stopped, not running`, from `stop` | It's already offline; nothing to do. Other states: wait for the current one to settle. |
| `NAME has been destroyed` | Deploy again; destroyed previews can't come back. |

## The preview is running, but the page is wrong

| Symptom | Cause and fix |
| --- | --- |
| Assets or API calls go to `localhost` | URLs were configured for local development. Use relative URLs, or read `DEPLOYER_URL` at runtime. |
| Old code is shown | A preview doesn't follow its branch by itself. Push, then run `deployer update NAME --json`. Compare `deployment.commit` with `git rev-parse origin/BRANCH`. |
| `Bad Request (400)` or `Invalid host header` | The framework rejects the preview host. Allow the preview domain (Django `ALLOWED_HOSTS`, Vite `preview.allowedHosts` or `server.allowedHosts`, Rails `config.hosts`). |
| Redirect loops, or `http://` links | The app doesn't trust the proxy. Trust `X-Forwarded-Proto`: Express `app.set("trust proxy", true)`, Django `SECURE_PROXY_SSL_HEADER`. |
| Client-side routes show 404 after a reload | The static server needs a fallback to `index.html`; see the Vite template in [recipes.md](recipes.md). |
| Features that call external services fail | There's no outbound network at runtime. Use mocks or sample data. |
| Data disappeared | Data lasts only as long as the deployment's container. A new deploy, and an update, start with the image's files. |

## What visitors see

| Status | Meaning |
| --- | --- |
| 200 | The app answered. |
| 503 with a progress page | Queued, building, or starting; it reloads when ready. |
| 503 "stopped" or "failed" | Stopped by its owner, or the build or start failed. |
| 410 | The lifetime ended. `extend`, then `start`, if it should come back. |
| 404 | No preview has that name. It was destroyed, or never existed. |
| 502 | The preview is running, but the app didn't answer the request. Check `deployer logs NAME --json`. |
