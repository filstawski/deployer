# Guide for AI agents

Deployer gives agents a predictable way to put a branch online and hand back a
URL. This page is the contract: what to run, what comes back, and how to
recover. It is also written to be pasted into an agent's instructions.

## Setup

Give the agent a token scoped to the projects it may deploy, through the
environment:

```sh
# As an administrator:
deployer token create agent --project shop --ttl 30d

# In the agent's environment:
export DEPLOYER_URL=https://api.previews.example.com
export DEPLOYER_TOKEN=dpl_...
```

A project-scoped token can deploy and manage that project's previews, but
cannot register projects, create tokens, or see other projects.

## The workflow

1. **Check the recipe** before pushing, if you changed it:
   `deployer validate --json`.
2. **Push the branch.** The server builds what is on the Git host, not local
   files.
3. **Deploy and wait:**

   ```sh
   deployer deploy shop --ref "$BRANCH" --ttl 1d \
     --idempotency-key "$TASK_ID-$BRANCH" --json
   ```

4. **Read the result.** On success, stdout holds:

   ```json
   {
     "deployment": {
       "id": "dep_k7m2q9x4abcd",
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

   Report `deployment.url`. A `state` of `running` means the health check
   passed.

5. **Clean up** when the preview is no longer needed:
   `deployer destroy NAME --yes --json`. Unused previews expire on their own.

## Rules

- Always pass `--json`. Stdout then holds exactly one JSON value; progress goes
  to stderr.
- Use the **exit code** first, then `error.code`:

  | Exit | Meaning | What to do |
  | --- | --- | --- |
  | 0 | Success | Use the result. |
  | 1 | Operation failed | Read `error.code` and `error.hint`. |
  | 2 | Invalid input | Fix the command; retrying won't help. |
  | 3 | Authentication or permission | Stop and report; don't retry. |
  | 4 | Stopped waiting | The work continues: `deployer operation wait OPERATION_ID --json`. |
  | 5 | Not found | Check the name with `deployer list --json`. |

- **Retry with the same idempotency key.** It returns the original deployment
  instead of creating a duplicate. Use a new key only for a genuinely new
  deployment.
- **Never parse human output.** Its wording changes; JSON fields and error codes
  don't.
- **Don't loop on failed builds.** A `BUILD_FAILED` or `HEALTHCHECK_FAILED` needs
  a code or recipe change, not a retry.

## Error codes

| Code | Meaning | Typical fix |
| --- | --- | --- |
| `SOURCE_FAILED` | The branch or repository can't be fetched. | Push the branch; check the repository is public or the GitHub App is installed. |
| `INVALID_RECIPE` | `deployer.yaml` is missing or invalid. | Run `deployer validate`; fix and push. |
| `BUILD_FAILED` | The Docker build failed. | Read `deployer logs NAME --build --json`. |
| `HEALTHCHECK_FAILED` | The app didn't answer its health check in time. | The app must listen on `0.0.0.0` and the recipe's port. Read `deployer logs NAME --json`. |
| `CONTAINER_EXITED` | The app exited on startup or later. | Read the app logs. |
| `TIMEOUT` | The operation exceeded its deadline. | Check build duration; retry once. |
| `NAME_TAKEN` | A live deployment has that `--name`. | Choose another name, or omit `--name`. |
| `QUOTA_EXCEEDED` | The server is at its deployment limit. | Destroy previews you created and no longer need. |
| `INVALID_STATE` | The action doesn't fit the state, such as starting an expired preview. | Follow the hint, such as `extend` before `start`. |
| `OPERATION_IN_PROGRESS` | Another operation is running for that deployment. | `deployer operation wait` and retry. |
| `IDEMPOTENCY_CONFLICT` | The key was used for a different request. | Use a new key. |
| `UNAUTHENTICATED`, `FORBIDDEN` | Token problem. | Report it; a person must fix the token. |
| `UNAVAILABLE` | The server can't be reached. | Retry later with the same idempotency key. |

## Useful commands

```sh
deployer list --project shop --json          # previews and their states
deployer inspect NAME --json                 # one preview, including error
deployer logs NAME --build --json            # {"logs": "..."}
deployer extend NAME --by 1d --json          # keep it longer
deployer deploy ... --detach --json          # don't wait; returns operation.id
deployer operation wait OPERATION_ID --json  # wait for it later
```

The same operations are available over the [HTTP API](api.md) for agents that
can't run a CLI.
