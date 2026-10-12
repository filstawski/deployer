# Private GitHub repositories

Public repositories work without any setup. For private ones, the server uses a
**GitHub App** that you create once. You then install it on the accounts whose
repositories you want to deploy, choosing which repositories it may read.

- The App asks only for **read access to repository contents**.
- Its private key stays on your server, in `/var/lib/deployer/github-app.json`.
- Each fetch uses a token that is valid for one hour and limited to the one
  repository being built. Tokens are never stored, logged, or passed to builds.

## Before you start

The API must be reachable at `https://api.previews.example.com`, because GitHub
sends your browser back to it during setup. The default installation sets this
up. If your API has another public address, pass it with `--public-url`.

## 1. Create the App (once, as an administrator)

On the server as root, or anywhere with an admin token:

```sh
deployer github setup
```

This prints a link that works once, for ten minutes. Open it in your browser:

1. Click **Create GitHub App**. GitHub shows the App's settings, pre-filled with
   a name such as `Deployer previews.example.com` and read-only *Contents*
   permission.
2. App names are unique across GitHub. If the name is taken, change it, then
   confirm.
3. GitHub returns you to your server, which saves the App's key and forwards
   you to the App's installation page.

To create the App under an organization instead of your personal account, use
`deployer github setup --org acme`. GitHub may require an organization owner to
approve it.

## 2. Install it on your repositories

On the installation page, pick the account that **owns** the repositories.
Choose **All repositories** or **Only select repositories**, and confirm.

You can return to that page anytime:

```sh
deployer github connect     # opens the installation page
deployer github status      # lists the accounts where the App is installed
```

A repository owned by someone else, such as a client's organization, needs the
App installed on *their* account. Send them the link from `deployer github connect`;
the App is created as installable by other accounts for this reason. Installing
it grants your server read access to the repositories they select, nothing else.

## 3. Deploy

Nothing else changes. Inside a clone of the private repository:

```sh
deployer deploy
```

Check access without deploying with `deployer project verify NAME`.

## Managing access

- **Change the repository list:** GitHub → Settings → Applications → Installed
  GitHub Apps → your App → Configure. Changes apply to the next fetch.
- **Revoke access:** uninstall the App on that page. New builds of those
  repositories fail; running previews are not affected.
- **Remove the App entirely:** delete it in the App's settings on GitHub, then
  delete `/var/lib/deployer/github-app.json` on the server and restart the service.
  You can run `deployer github setup` again afterwards.

Anyone with a token that has the *deployer* role on all projects can register
any repository the App can read. Install the App only on repositories that this
server's users may deploy.

## Troubleshooting

**`GITHUB_NOT_CONFIGURED`.** Run `deployer github setup` as an administrator.

**`the repository is private or does not exist`.** The App is not installed on
the repository's owner account, or that repository is not selected. Check with
`deployer github status`, then open `deployer github connect`.

**`This link has expired`.** Links work once and for ten minutes. Run
`deployer github setup` again.

**`Open the setup link in this browser`.** GitHub must return you to the same
browser that opened the setup link. Start again in one browser.

**`GitHub setup didn't finish`.** GitHub may have created the App anyway. Look
under GitHub → Settings → Developer settings → GitHub Apps, delete the unused
App, and run setup again.
