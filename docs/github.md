# Private GitHub repositories

Available in **v0.1.0-dev.3**. Upgrade both server and client before using these commands. Your laptop keeps using the authenticated HTTPS API; no local SSH key is involved.

## 1. Upgrade your existing server

In your existing root Termius session:

```sh
curl -fsSL https://github.com/filstawski/deployer/releases/download/v0.1.0-dev.3/install.sh \
  | sh -s -- upgrade --version v0.1.0-dev.3

deployer server status
```

The installer reads the existing installation receipt. Keep your current domain, ports, certificates, nginx routes, tokens, and projects; do not pass first-install flags or run setup again. It stages the release, stops the controller briefly, backs up the database, updates the gateway image, and restarts. Preview requests may briefly fail while the gateway is replaced. Existing application containers are preserved and reattached.

Startup failure triggers an attempt to restore the previous binaries and configuration. Database backups are under `/etc/deployer/backups/` with root-only permissions. This release does not change the database schema. Automatic rollback does not restore the database or discard accepted operations. If the machine loses power during upgrade, `/etc/deployer/upgrade-pending.json` records the previous release and backup directory; stop and inspect it before retrying. Do not delete the journal without checking the active release/configuration. A future incompatible database migration will require a separate migration strategy.

## 2. Create your GitHub App once

Still in Termius, as root:

```sh
deployer github setup --public-url https://api.surdic.run --no-browser
```

Open the returned `url` on your Mac. It expires after ten minutes; rerunning setup replaces the previous pending link. Keep this link private. The browser page has a **Create GitHub App** button. Follow it, choose a unique app name (for example `deployer-filstawski`), and confirm creation. You own this GitHub App. GitHub returns you to Deployer, which saves its private key on the server and redirects you to the repository selection screen.

Choose your account and either:

- **All repositories**, to include your current and future repositories automatically.
- **Only select repositories**, to restrict this server to specific projects.

The app requests **Contents: read-only**. It is installable on other accounts so a customer or collaborator can grant access to their repositories; this does not make repository contents or the app's private key public. For an organization-owned app, add `--organization YOUR_ORG` to the setup command. GitHub may require organization-owner approval.

For `dennilz/Gasthaus-Lutz-Website`, access must be granted on **dennilz's account**. Installing the app on filstawski's account cannot grant access to that repository. Send the app installation link to its owner yourself if you cannot install it there.

The existing HTTPS API nginx route already forwards `/v1/github/`, so no new nginx site is needed. Setup and callback pages use short-lived capabilities instead of a Deployer bearer header. Their URLs should not be shared or included in analytics; consider disabling access logs on the API virtual host if retaining these short-lived URLs is undesirable.

## 3. Upgrade the laptop CLI and verify

On your Mac:

```sh
curl -fsSL https://github.com/filstawski/deployer/releases/download/v0.1.0-dev.3/install.sh \
  | sh -s -- client --version v0.1.0-dev.3

deployer github status
```

The installer preserves your saved HTTPS connection and token. Status lists the accounts where your app is installed. It never returns GitHub credentials. An installation's presence does not mean every repository was selected; verify the project to check source access.

To add another account or open the installation page later:

```sh
deployer github connect
```

On macOS this opens the browser. On Linux, or with `--no-browser` / `--json`, open the returned URL yourself. Manage existing repository selections in GitHub account settings under **Applications → Installed GitHub Apps → Configure**. No per-project credential command is needed.

## 4. Deploy from any connected repository

Commit and push its Dockerfile, `.dockerignore`, `deployer.yaml`, and application changes first. Then:

```sh
cd /path/to/project
deployer deploy --ref main --ttl 4h --wait
```

The CLI reads `origin`, converts GitHub SSH remotes to HTTPS, reuses a matching registered project, or creates one automatically. New project names include the owner, repository, and a short stable suffix. It deploys the **remote branch**, not uncommitted files on your laptop. Specify the project explicitly if multiple registered projects share the same repository.

For the already registered Gasthaus project:

```sh
deployer project verify gasthaus-lutz --wait
deployer deploy gasthaus-lutz --ref preview/github-pages \
  --subdomain gasthaus-lutz-preview --ttl 1d --wait
```

Use a fresh subdomain if an earlier failed preview still holds your preferred name. Inspect an old deployment before destroying it to reclaim that name.

## Permissions and storage

- App creation requires a Deployer **admin** token. Run it with the server's existing root CLI; do not copy its administrator token onto the laptop.
- Listing connections and opening the installation link require `projects:write` plus the `*` project grant. Your existing laptop token has these permissions.
- Project-scoped Deployer tokens retain their existing isolation. A token allowed to register arbitrary projects (`projects:write` and `*`) can register repositories accessible to this server's GitHub App. Install only repositories you want this server's project administrators to deploy. This is a shared server connection, not per-user GitHub authorization.
- `/var/lib/deployer/github-app.json` holds the app ID, slug, and RSA private key, mode `0600`, owned by the Deployer service user. Include it in protected server backups. It is not part of the public server configuration or API responses.
- Each source verification/build gets a short-lived installation token restricted to that one repository and `contents:read`. It is passed only in the Git process environment as a URL-scoped HTTP header, never in command arguments, repository URLs, build context, application containers, or logs. Privileged server administrators can still inspect process memory/environment.
- Public HTTPS repositories continue working without a GitHub installation. Other private Git providers, submodules, LFS credentials, private npm/package registry credentials, and GitHub Enterprise are not supported by this release.

To revoke repository access, remove it from the app's installation settings or uninstall the app on that account. New fetches will fail; already running previews and already checked-out code are not automatically deleted. App-key rotation currently requires replacing the protected server credential file and restarting the service; there is no rotation UI yet.

## Troubleshooting

`GITHUB_NOT_CONFIGURED`: run the one-time administrator setup on the upgraded server.

`FORBIDDEN` on setup from your Mac: expected for the normal laptop token. Run setup in your root Termius session.

Expired setup link, server restart during setup, or invalid callback: start setup again. Complete creation in the same browser where you opened the link. If GitHub created an app but Deployer failed to save it, inspect/delete the unused app in GitHub before creating another.

`SOURCE_FAILED` or verification failure: confirm the app is installed on the repository owner's account, the repository is selected, the installation is not suspended, and the requested branch exists on GitHub. Also confirm the pushed project includes the deployment files.

Sources: [GitHub App manifest setup](https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest), [repository-scoped installation tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app).
