# Roadmap

What Deployer does today, what comes next, and what is deliberately left out.
The [design notes](docs/design.md) describe how planned features should behave.

## Available

- Dockerfile builds of public HTTPS repositories, and of private GitHub
  repositories through a GitHub App
- An isolated container and network per preview, with resource limits
- Progress, offline, expired, failed, and not-found pages for visitors
- Lifetimes that start at readiness; stop, start, extend, and destroy
- Automatic expiry and cleanup after a retention period
- Recovery after controller restarts and host reboots
- Durable, ordered, cancellable operations; idempotent requests
- Scoped API tokens; JSON output and stable error codes for agents
- Server install, upgrade with rollback, and uninstall; nginx configuration
- CLI over HTTPS or SSH, with automatic project registration

## Next

These complete the core experience for teams.

1. **Secrets.** Store secret values on the server and reference them by name in
   `deployer.yaml`, delivered at runtime and to builds as BuildKit secrets.
2. **Protected previews.** Optional password or link-token access for previews
   of unreleased work.
3. **Multi-service apps.** A validated subset of Docker Compose, for an app with
   its own database, cache, or workers, with a private network per preview.
4. **Prebuilt images.** Deploy an image built by CI, recording its digest and
   the commit it came from.
5. **Idle sleep.** Stop previews nobody has visited for a while, and wake them
   on the next visit. The gateway already serves the waiting page this needs.
6. **Update in place.** Move an existing preview to a new commit while keeping
   its name and data.
7. **Egress policy.** Allow apps to reach specific hosts at runtime instead of
   all or nothing.

## Later

- Several branches in one command, and saved sets of branches (`--ref a --ref b`)
- A review page linking several previews, with feedback tied to commits
- Stable aliases, such as `acme.previews.example.com`, that move between previews
- Demo presets with resettable sample data
- Automatic deployments for pull requests
- Smoke tests after readiness, with screenshots
- Other private Git hosts, and Git submodules
- An MCP server for agents, as a thin layer over the API
- Automatic wildcard certificates

## Not planned

- High availability or several servers. Deployer is for previews, not production.
- Running untrusted code. Containers share the kernel; previews are for code you
  trust.
