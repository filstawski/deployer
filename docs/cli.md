# CLI reference

`deployer` manages previews on a Deployer server. Every command has `--help`
with examples. This page describes the concepts that span commands.

- [Interactive use](#interactive-use)
- [Connecting](#connecting)
- [Deploying](#deploying)
- [Managing deployments](#managing-deployments)
- [Projects](#projects)
- [Tokens](#tokens)
- [Output, exit codes, and scripting](#output-exit-codes-and-scripting)
- [Upgrading](#upgrading)
- [Server administration](#server-administration)

## Interactive use

At a terminal, the CLI asks for what you leave out instead of failing:

| Command | Asks for |
| --- | --- |
| `deployer deploy` | A short form with the branch, name, and lifetime, filled in with suggestions: the current branch, a readable unused name, and the server's default lifetime. Press Enter to accept them. |
| `inspect`, `logs`, `open`, `stop`, `start`, `extend`, `destroy` | The deployment, picked from a list, when you give no name. `stop` only offers running ones, `start` stopped or failed ones. |
| `deployer extend` | How long, when `--by` is missing. |
| `deployer destroy` | Confirmation, unless you pass `--yes`. |
| `deployer login` | The server URL and the token. |
| `deployer init` | The port and health check path, pre-filled from the Dockerfile. |

Flags answer questions in advance, so `deployer deploy --ref main` only asks
for the name and lifetime, and `deployer deploy -y` asks nothing. Without a
terminal, in scripts, CI, and with `--json`, the CLI never prompts; it uses
defaults or reports what is missing, as described below.

Output is styled at a terminal: results appear in panels, and states, URLs,
and errors are colored. Piped or redirected output is plain text. Set
`NO_COLOR=1` to turn colors off.

## Connecting

```sh
deployer login https://api.previews.example.com     # prompts for a token
deployer status                                     # server, version, and token
```

`login` checks the token against the server and saves the connection as a
*context* named after the host. Use `--name` to choose a name. With several
servers:

```sh
deployer context list
deployer context use staging
deployer list --context production     # one command against another server
deployer logout staging
```

Contexts live in `~/.config/deployer/config.json`, or `$XDG_CONFIG_HOME/deployer`.
Tokens are stored separately in `tokens/NAME.token` with mode 600. Set
`DEPLOYER_CONFIG_DIR` to use another directory.

**Without saving anything**, for CI and agents, set both variables:

```sh
export DEPLOYER_URL=https://api.previews.example.com
export DEPLOYER_TOKEN=dpl_...            # or DEPLOYER_TOKEN_FILE=/path/to/token
```

These take precedence over saved contexts. Precedence, from highest:
`DEPLOYER_URL`, then `--context` or `DEPLOYER_CONTEXT`, then the current
context. Running as root on a Deployer server uses the local administrator token
when nothing else is set.

**Over SSH**, when the server's API is not public:

```sh
deployer login ssh://root@server.example.com
```

The CLI starts `ssh` for each command and forwards to the API's loopback port
(`--api-port`, default 7420). It never accepts unknown host keys, so connect
with `ssh` once first. Ports, keys, and aliases come from `~/.ssh/config`.

## Deploying

```sh
deployer deploy                                  # current repository and branch
deployer deploy --ref design/a --name design-a   # pick the branch and the name
deployer deploy shop --ref main --ttl 3d         # name the project explicitly
deployer deploy --ref 7d63b83b6f7e029c4be79244204d87c9ef71a460
```

Inside a Git repository, `deploy` finds the project whose repository matches the
`origin` remote and registers one if there is none. It deploys the current
branch unless you pass `--ref`. The server fetches the branch from the Git host,
so the CLI warns when you have unpushed commits or uncommitted changes.

| Flag | Meaning |
| --- | --- |
| `-r, --ref` | Branch or full commit SHA. Default: the current branch. |
| `-y, --yes` | Skip the form at a terminal and use the defaults. |
| `-n, --name` | Preview name; the URL becomes `https://NAME.previews.example.com`. Default: generated from the project and branch, such as `shop-design-a-k7m2q`. |
| `--ttl` | Lifetime after the preview becomes ready, such as `4h` or `3d`. Default and maximum are server settings; see `deployer status`. |
| `-f, --follow` | Print the build output while waiting. |
| `-d, --detach` | Return as soon as the deployment is queued. |
| `--timeout` | How long to wait, default 30m. The deployment continues on the server if the wait ends. |
| `--idempotency-key` | Repeat a request safely; see [scripting](#retries-and-idempotency). |

Names must be unique among live deployments, and are released when a deployment
is destroyed. Each deploy creates a new deployment, even for the same branch;
destroy the old one when you no longer need it.

What happens after `deploy`:

1. **queued**: the URL is reserved and shows a progress page. The deployment
   waits for a build slot.
2. **building**: the server fetches the exact commit and builds the image.
3. **starting**: the container starts, and Deployer waits for the health check.
4. **running**: visitors reach the app. The lifetime starts now.

If a step fails, the deployment ends **failed** and `deploy` prints the error
with a hint. `deployer inspect NAME` shows it later.

## Managing deployments

```sh
deployer list                     # live deployments
deployer list --project shop --all
deployer inspect design-a         # details, including the last error
deployer open design-a            # open in the browser
deployer logs design-a            # the app's output (last 200 lines; -n for more)
deployer logs design-a --build -f # follow a build in progress
```

Commands accept a deployment's name or its ID (`dep_...`). At a terminal, leave
the name out to pick from a list.

| Command | Effect |
| --- | --- |
| `deployer stop NAME` | Stops the container. Visitors see "This preview is offline". The lifetime keeps running. |
| `deployer start NAME` | Starts a stopped or failed deployment again from its built image, without rebuilding. |
| `deployer extend NAME --by 2d` | Adds time to the lifetime, up to the server's maximum measured from now. An expired deployment gets a new lifetime starting now; then `start` it. Before a deployment is ready, this lengthens the lifetime it will get. |
| `deployer destroy NAME` | Removes the container, network, image, and logs, and releases the name. Cancels a build in progress. Asks for confirmation; `--yes` skips it. |

`stop`, `start`, and `destroy` wait until they finish; `--detach` returns
immediately.

### Lifetime and cleanup

- A deployment **expires** when its lifetime ends. Its URL then answers
  "This preview has expired" (HTTP 410), and its container stops. Visits never
  extend a lifetime.
- Stopped, expired, and failed deployments keep their image and logs for the
  server's retention period (7 days by default), and are then destroyed
  automatically. `inspect` shows the time as *Removed*.
- After a server reboot, Deployer restarts deployments that were running. If an
  app's container stops on its own while the server is up, the deployment
  becomes failed with `CONTAINER_EXITED`. Check `logs`, then `start` it.

### Operations

`deploy`, `stop`, `start`, `destroy`, and `project verify` run as **operations**
on the server. An operation keeps running when the CLI exits or loses its
connection:

```sh
deployer deploy --detach --json | jq -r .operation.id
deployer operation wait op_8cf3q2m9xkwz    # wait again, from anywhere
deployer operation show op_8cf3q2m9xkwz
```

A deployment runs one operation at a time, except `destroy`, which cancels
whatever is in progress.

## Projects

A project is a registered repository. `deploy` registers projects
automatically, so these commands are mostly for inspection:

```sh
deployer project list
deployer project add shop --repo https://github.com/acme/shop
deployer project verify shop       # can the server read the repository?
deployer project remove shop --yes # only once it has no live deployments
```

Repository URLs must be HTTPS. GitHub SSH remotes (`git@github.com:acme/shop.git`)
are converted to HTTPS automatically. Private GitHub repositories need the
[GitHub connection](github.md). Other private Git hosts are not supported yet.

`deployer init` and `deployer validate` work locally on `deployer.yaml`; see
[preparing a project](projects.md).

## Tokens

Tokens authenticate the CLI and the API. Only administrators manage them:

```sh
deployer token create laptop                    # deployer role, all projects, 90 days
deployer token create ci --project shop --ttl 30d
deployer token create dashboard --role viewer --ttl never
deployer token create bot --output bot.token    # write the secret to a new file
deployer token list
deployer token revoke tok_7d63b83b6f7e
```

The secret (`dpl_...`) is shown once. The server stores only its hash. Revoking
takes effect immediately.

| Role | Scopes | Can |
| --- | --- | --- |
| `deployer` | `server:read`, `projects:read`, `projects:write`, `deployments:read`, `deployments:write` | Register projects, deploy, and manage deployments. |
| `viewer` | `server:read`, `projects:read`, `deployments:read` | Read everything it has access to, including logs. |
| `admin` | `admin` | Everything, including tokens and GitHub setup. |

`--project` limits a token to specific projects; repeat it for several. Such a
token cannot register new projects, so it fits CI jobs for one repository. You
can also pass `--scopes` with an explicit comma-separated list instead of a role.

## Output, exit codes, and scripting

Human-readable output goes to stdout. Progress, warnings, and errors go to stderr.
`--quiet` suppresses progress.

With `--json`, stdout carries exactly one JSON value:

- on success, the result: a deployment, a list, an operation, or for `deploy`
  `{"deployment": {...}, "operation": {...}}`;
- on failure, `{"error": {"code": "...", "message": "...", "hint": "..."}}`.

Times are UTC in RFC 3339 format. Field names match the [HTTP API](api.md).

| Exit code | Meaning |
| --- | --- |
| 0 | Success. |
| 1 | The request or operation failed, such as a failed build. |
| 2 | Invalid command line or input. |
| 3 | Not logged in, invalid token, or missing permission. |
| 4 | Stopped waiting; the operation continues. Use `operation wait`. |
| 5 | The deployment, project, or operation does not exist. |

### Retries and idempotency

`deploy`, `stop`, `start`, and `destroy` send an idempotency key, and the CLI
retries them automatically on network errors. To make retries across separate
invocations safe, choose the key yourself:

```sh
deployer deploy --ref main --idempotency-key "ci-$CI_PIPELINE_ID" --json
```

Repeating the command with the same key returns the original deployment instead
of creating another one. Reusing a key for a different request fails with
`IDEMPOTENCY_CONFLICT`. Keys are kept for 7 days.

### Shell completion

Completion fills in commands, flags, deployment and project names, and your
pushed branches for `--ref`. Names come from the server, so you need to be
logged in.

**zsh**, the default on macOS:

```sh
echo 'source <(deployer completion zsh)' >> ~/.zshrc
```

If zsh completion isn't enabled yet, add `autoload -Uz compinit && compinit`
above that line. Frameworks such as Oh My Zsh already do this.

**bash**, common on Linux servers, needs the `bash-completion` package
(`apt install bash-completion`):

```sh
deployer completion bash > /etc/bash_completion.d/deployer    # for all users, as root
echo 'source <(deployer completion bash)' >> ~/.bashrc          # or just for you
```

**fish**: `deployer completion fish > ~/.config/fish/completions/deployer.fish`

Open a new terminal afterwards. `echo $SHELL` shows which shell you use.

## Upgrading

```sh
deployer upgrade            # replace this CLI with the latest release
deployer upgrade --check    # only report whether a newer release exists
deployer upgrade --version v0.3.0
```

Downloads are verified against the release's checksums before anything is
replaced. On a Deployer server, `deployer upgrade` upgrades the whole server; it
runs as root and is the same as `deployer server upgrade`.

## Server administration

These commands run as root on the server; see [installation](installation.md).

| Command | Effect |
| --- | --- |
| `deployer server install --domain D [--tls-cert F --tls-key F]` | Install and start Deployer. |
| `deployer server upgrade [--version V]` | Upgrade to the latest release, with database backup and rollback. Same as `deployer upgrade` on the server. |
| `deployer server uninstall [--purge] --yes` | Remove Deployer and every preview. |

All three accept `--dry-run`. `deployerd`, the controller, has a recovery command
for lost administrator tokens:
`deployerd token create NAME --output FILE`.
