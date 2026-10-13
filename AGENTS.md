# Working on Deployer

Guidance for people and coding agents changing this repository. Product
documentation is in [README.md](README.md) and [docs/](docs/).

## Commands

```sh
make check    # gofmt, go vet, race-enabled tests; must pass before a change is done
make e2e      # real Docker end-to-end test; run after touching docker/, controller/, gateway/, daemon/
make build    # bin/deployer, bin/deployerd
```

## Map

| Change | Start in |
| --- | --- |
| A CLI command or its output | `internal/cli` |
| An API endpoint | `internal/api/routes.go`, the client in `internal/client`, types in `internal/domain` |
| Deployment behavior (build, start, stop, expiry) | `internal/controller` |
| How operations are queued or retried | `internal/operations` |
| Docker commands, containers, networks | `internal/container/docker` |
| Visitor pages and routing | `internal/gateway`, page markup in `internal/webui` |
| `deployer.yaml` or `server.yaml` fields | `internal/config`, `schemas/`, `docs/projects.md` or `docs/installation.md` |
| Database schema | `internal/store/migrations.go`: append a migration, never edit a released one |
| Server installation | `internal/install`, `install.sh` |
| CLI installation | `install.sh`, `install.ps1` (Windows), `internal/release` |

[docs/architecture.md](docs/architecture.md) explains how the pieces fit together.

## Rules

- `internal/domain` is the wire contract. Don't rename JSON fields, error codes,
  or exit codes; add new ones.
- User-facing errors are `*domain.Error` with a code, a plain message, and, when
  there is an obvious next step, a hint with the exact command.
- Lifecycle rules belong in `internal/controller`. The store persists, and
  multi-step changes go through `store.Tx`.
- Operation handlers run again after crashes. Record progress immediately, and
  adopt resources an earlier attempt created.
- Only touch Docker resources labeled with this installation's ID.
- Run subprocesses through `internal/execx`, never a shell.
- The CLI must not import server packages (`store`, `controller`, `api`).
- The CLI also runs on Windows. Its packages must build there (`make check`
  vets them); keep platform differences in `_windows.go` and `_unix.go` files.
- Every behavior change ships with tests and updated docs. Prefer the real thing
  in tests (SQLite, HTTP, Git); `internal/testenv` runs a full server
  in-process.
- Write docs for the reader's task: commands they can paste, behavior stated
  plainly, limitations stated once.
