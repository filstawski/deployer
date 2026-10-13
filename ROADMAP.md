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
- CLI for macOS, Linux, and Windows, over HTTPS or SSH, with automatic project
  registration
- Updating a preview in place from newer commits, keeping its name, URL, and
  lifetime, with the previous version kept if the new one fails

## Next

These complete the core experience for teams, in order of priority.

1. **Secrets.** Store secret values on the server and reference them by name in
   `deployer.yaml`, delivered at runtime and to builds as BuildKit secrets.
2. **Protected previews.** Optional password or link-token access for previews
   of unreleased work, checked before any page is shown.
3. **Pull request previews.** Deploy a preview when a pull request opens, update
   it on every push, comment the URL on the pull request, and destroy it when
   the pull request closes. Builds on the GitHub App and on `deployer update`.
4. **Multi-service apps.** A validated subset of Docker Compose, for an app with
   its own database, cache, or workers, with a private network per preview.
5. **Idle sleep.** Stop previews nobody has visited for a while, and wake them
   on the next visit. The gateway already serves the waiting page this needs.
6. **Prebuilt images.** Deploy an image built by CI, recording its digest and
   the commit it came from.
7. **Egress policy.** Allow apps to reach specific hosts at runtime instead of
   all or nothing.

## Small improvements

- Show who created each preview in `deployer list`, and filter with `--mine`.
  The server already records the creating token.
- Notifications to Slack or Discord when a preview is ready or fails, with its URL.
- A web page listing running previews, for people who don't use the CLI.
- A live, full-screen terminal dashboard when `deployer` runs without arguments.

## Later

- Several branches in one command, and saved sets of branches (`--ref a --ref b`)
- A review page linking several previews, with feedback tied to commits
- Stable aliases, such as `acme.previews.example.com`, that move between previews
- Demo presets with resettable sample data
- Smoke tests after readiness, with screenshots
- Other private Git hosts, and Git submodules
- An MCP server for agents, as a thin layer over the API. The agent skill
  (`deployer skill`) covers most of what it would offer.
- Automatic wildcard certificates

## Not planned

- High availability or several servers. Deployer is for previews, not production.
- Running untrusted code. Containers share the kernel; previews are for code you
  trust.
