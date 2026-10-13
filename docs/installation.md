# Installation

This guide sets up a Deployer server and connects your laptop to it. It takes
about fifteen minutes on a server that already runs Docker and nginx.

Throughout, `previews.example.com` stands for your preview domain. Previews are
served at `https://NAME.previews.example.com`, and the management API at
`https://api.previews.example.com`.

## Requirements

On the server:

- **Linux with systemd**, on amd64 or arm64, and root access.
- **Docker Engine 28 or newer** with the **Buildx** plugin (`docker buildx version`).
  On Debian and Ubuntu, Docker's own packages include both: `docker-ce` and
  `docker-buildx-plugin`.
- **Git**.
- **nginx**, to terminate TLS. Deployer adds one site file and leaves the rest
  of your nginx configuration alone.
- **Free loopback ports** 7420 (API), 8080 (preview traffic from nginx), and
  7421 (gateway control). You can choose others with flags.

Size the server for the previews you'll run: each preview may use up to 1 CPU
and 512 MiB of memory by default, and one build runs at a time with up to 2 CPUs
and 2 GiB.

On your laptop: macOS, Linux, or Windows 10 or later.

## 1. Point DNS at the server

Create one wildcard record in your DNS provider:

| Type | Name | Value |
| --- | --- | --- |
| A | `*.previews.example.com` | your server's public IPv4 address |

The wildcard covers every preview and `api.previews.example.com`. If you use
Cloudflare, start with the record set to *DNS only*. You can enable proxying
later with SSL mode *Full (strict)*.

Check it from your laptop: `dig +short anything.previews.example.com` should
print the server's address.

## 2. Get a wildcard certificate

Previews need a certificate for `*.previews.example.com`. Let's Encrypt issues
wildcard certificates through DNS validation. With Certbot and Cloudflare, on the
server:

```sh
apt install certbot python3-certbot-dns-cloudflare

# An API token with Zone → DNS → Edit permission for your zone.
install -d -m 700 /root/.secrets
printf 'dns_cloudflare_api_token = %s\n' 'YOUR-CLOUDFLARE-TOKEN' > /root/.secrets/cloudflare.ini
chmod 600 /root/.secrets/cloudflare.ini

certbot certonly --dns-cloudflare \
  --dns-cloudflare-credentials /root/.secrets/cloudflare.ini \
  -d '*.previews.example.com' \
  --deploy-hook 'systemctl reload nginx'
```

Certbot installs a timer that renews the certificate and reloads nginx. Other DNS
providers have [their own Certbot plugins](https://eff-certbot.readthedocs.io/en/stable/using.html#dns-plugins).
Any certificate tool works as long as nginx can read the files.

## 3. Install Deployer

As root on the server:

```sh
curl -fsSL https://github.com/filstawski/deployer/releases/latest/download/install.sh | sh -s -- server \
  --domain previews.example.com \
  --tls-cert /etc/letsencrypt/live/previews.example.com/fullchain.pem \
  --tls-key  /etc/letsencrypt/live/previews.example.com/privkey.pem
```

The script downloads the release for your architecture and verifies its
checksums. It then runs `deployer server install`, which:

1. checks for systemd, Docker 28+, Buildx, and Git;
2. creates a `deployer` system account in the `docker` group;
3. installs the binaries in `/opt/deployer/releases/VERSION` and links
   `deployer` and `deployerd` into `/usr/local/bin`;
4. writes `/etc/deployer/server.yaml` and the `deployer` systemd service, and
   starts it;
5. writes `/etc/nginx/conf.d/deployer.conf` for previews and the API, tests it
   with `nginx -t`, and reloads nginx.

On its first start the service builds the gateway image and starts its
containers, which can take a minute.

Useful variations:

- Add `--dry-run` to see the plan without changing anything.
- Pin a release with `--version v0.2.0`.
- Use `--no-public-api` to keep the API off the internet. You then connect over
  SSH (see below).
- Without `--tls-cert` and `--tls-key`, nginx is left untouched. Run the same
  command again with them once you have a certificate.
- If a port is taken, choose another with `--api-port`, `--gateway-port`, or
  `--control-port`.

Running the installer again with the same options is safe. It resumes an
interrupted installation and never discards data.

### Check it

```sh
deployer status                              # as root on the server
curl -s https://api.previews.example.com/healthz
systemctl status deployer
journalctl -u deployer -f
```

As root on the server, `deployer` automatically uses the administrator token in
`/var/lib/deployer/admin.token`.

## 4. Connect your laptop

Create a token on the server. It is printed once:

```sh
deployer token create laptop
```

Install the CLI on your laptop and log in with that token:

```sh
curl -fsSL https://github.com/filstawski/deployer/releases/latest/download/install.sh | sh
deployer login https://api.previews.example.com
deployer status
```

The CLI is installed in `~/.local/bin`, and the installer tells you if that
directory is not on your `PATH`. The token is saved in
`~/.config/deployer/tokens/` with mode 600.

**On Windows**, install it from PowerShell:

```powershell
irm https://github.com/filstawski/deployer/releases/latest/download/install.ps1 | iex
deployer login https://api.previews.example.com
deployer status
```

This installs `deployer.exe` in `%LOCALAPPDATA%\Programs\deployer` and adds that
folder to your `PATH`; terminals opened afterwards find it. To install a
specific release, set `$env:DEPLOYER_VERSION = "v1.2.3"` first. The token is
saved in `%APPDATA%\deployer\tokens\`, readable only by you.

Windows Terminal, the default on Windows 11, shows deployer's colors and symbols
best. The older console window of Windows 10 works, but may show some symbols as
boxes. In Git Bash's own window, deployer can't detect the terminal, so it
prints plain output and doesn't ask questions.

The `laptop` token has the *deployer* role: it can register projects and manage
previews, but not create tokens. Tokens expire after 90 days by default. See
[tokens](cli.md#tokens) for roles, project-scoped tokens for CI, and revoking.

### Without a public API

If you installed with `--no-public-api`, log in over SSH instead. The CLI starts
`ssh` for each command, forwarding to the server's loopback API:

```sh
deployer login ssh://root@server.example.com
```

This uses your normal SSH configuration and keys. The server's host key must
already be known: run `ssh root@server.example.com` once first. On Windows, the
CLI uses the OpenSSH client that comes with Windows. ssh can't ask for a key's
passphrase here, so load such a key into Windows' SSH agent first. Its service
is off by default; start it once in PowerShell opened as administrator, then
add your key as yourself:

```powershell
Get-Service ssh-agent | Set-Service -StartupType Automatic; Start-Service ssh-agent
ssh-add
```

## 5. Deploy something

Try it with this repository's example app:

```sh
git clone https://github.com/filstawski/deployer && cd deployer
deployer deploy --ref main --name hello
```

Then [prepare your own project](projects.md). To deploy private GitHub
repositories, [connect GitHub](github.md).

## Upgrading

On the server, as root:

```sh
deployer upgrade
```

This downloads the latest release and verifies its checksums. The new release
then performs the upgrade itself: it stops the service, backs up the database to
`/var/lib/deployer/backups/`, switches over, and starts the service. If the new
release does not start, the previous release and database are restored
automatically. Preview containers keep running. Visitors may see a few seconds
of "temporarily unavailable" while the gateway container is replaced.

On your laptop, `deployer upgrade` replaces the CLI with the latest release, on
Windows too.

Useful variations:

- `deployer upgrade --check` reports whether a newer release exists.
- `deployer upgrade --version v0.3.0` installs a specific release.
- `deployer server upgrade --dry-run` shows the server upgrade plan.

`deployer status` points out when the CLI and the server run different versions.
Releases before v0.2.2 don't have `deployer upgrade` yet. Upgrade those once
with the installer: `curl -fsSL https://github.com/filstawski/deployer/releases/latest/download/install.sh | sh -s -- upgrade`
on the server, and the plain installer command on your laptop.

## Configuration

`/etc/deployer/server.yaml` holds the server's settings. Restart the service
after editing it: `systemctl restart deployer`.

```yaml
version: 1
domain: previews.example.com
state_dir: /var/lib/deployer
api:
  listen: 127.0.0.1:7420
  public_url: https://api.previews.example.com
gateway:
  listen: 127.0.0.1:8080          # nginx sends preview traffic here
  control_listen: 127.0.0.1:7421
previews:
  scheme: https
deployments:
  max_live: 10                    # deployments holding resources at once
  default_ttl: 1d                 # lifetime when --ttl is not given
  max_ttl: 7d                     # longest lifetime, including extensions
  retention: 7d                   # how long stopped and expired previews are kept
  build_concurrency: 1
  build_timeout: 20m
  cpus: 1                         # per preview
  memory_mb: 512
  pids: 256
  egress: false                   # let apps open outbound connections
```

Durations accept `m`, `h`, `d`, and `w` units, such as `90m`, `12h`, or `2w`.

For example, to allow previews that live up to 30 days:

```yaml
deployments:
  max_ttl: 30d
```

Edits to `server.yaml` survive upgrades. `deployer status` shows the lifetime
limits in effect, and the deploy form and `deployer extend` offer choices up to
the maximum.

## What is installed where

| Path | Contents |
| --- | --- |
| `/opt/deployer/releases/VERSION/` | `deployer` and `deployerd` binaries |
| `/opt/deployer/current` | Link to the active release |
| `/usr/local/bin/deployer`, `deployerd` | Links into the active release |
| `/etc/deployer/server.yaml` | Server configuration |
| `/etc/deployer/install.json` | What the installer did; no secrets |
| `/etc/systemd/system/deployer.service` | The service |
| `/etc/nginx/conf.d/deployer.conf` | nginx site for previews and the API |
| `/var/lib/deployer/state.db` | Database: projects, deployments, operations, tokens |
| `/var/lib/deployer/admin.token` | Administrator token, created on first start |
| `/var/lib/deployer/github-app.json` | GitHub App key, after [GitHub setup](github.md) |
| `/var/lib/deployer/deployments/` | Build logs |
| `/var/lib/deployer/backups/` | Database backups from upgrades |

In Docker, Deployer creates a `deployer-gateway-*` container, a
`deployer-builder-*` BuildKit builder, and one container, network, and image
per preview. They all carry an `io.deployer.installation` label. Deployer never
touches Docker resources without its label.

**Back up** `/var/lib/deployer` and `/etc/deployer`. Stop the service first, or
copy `state.db` with `sqlite3 state.db ".backup /path/backup.db"`.

## Uninstalling

```sh
deployer server uninstall --yes
```

This removes the service, the binaries, the nginx site, and every preview
container, network, and image. The database, GitHub connection, and
configuration are kept, so a later install picks up where you left off. Add
`--purge` to delete those too, along with the `deployer` account.

## Replacing a pilot installation

Releases before v0.2 (the `v0.1.0-dev` pilots) used a different layout and
cannot be upgraded. The installer stops with a message pointing here. To replace
one, run these commands as root. They keep the pilot's GitHub App, so you don't
have to create it again:

```sh
systemctl disable --now deployer.service
cp /var/lib/deployer/github-app.json /root/github-app.json 2>/dev/null

# Remove the pilot's containers, networks, images, and builder.
docker rm -f $(docker ps -aq --filter label=build.deployer.installation) 2>/dev/null
docker network rm $(docker network ls -q --filter label=build.deployer.installation) 2>/dev/null
docker image rm -f $(docker image ls -q --filter label=build.deployer.installation) 2>/dev/null
docker buildx ls --format '{{.Name}}' | grep '^dpl-builder-' | xargs -r -n1 docker buildx rm

rm -f /etc/systemd/system/deployer.service /usr/local/bin/deployer /usr/local/bin/deployerd
rm -f /etc/nginx/conf.d/deployer.conf /etc/nginx/conf.d/deployer-api.conf
rm -rf /opt/deployer /etc/deployer /var/lib/deployer
systemctl daemon-reload
nginx -t && systemctl reload nginx
```

Then [install](#3-install-deployer) as usual. The pilot used gateway port 8081,
so if something else holds port 8080, add `--gateway-port 8081`. To restore the
GitHub App afterwards:

```sh
install -o deployer -g deployer -m 600 /root/github-app.json /var/lib/deployer/github-app.json
systemctl restart deployer
deployer github status
```

Re-create your laptop token with `deployer token create laptop`, and log in
again. Projects are registered again automatically on the next `deployer deploy`.

## Troubleshooting

**The installer says Docker is too old.** Deployer needs Docker Engine 28 or
newer for its network isolation. Install Docker from
[Docker's repositories](https://docs.docker.com/engine/install/), not the
distribution's older package.

**The service doesn't start.** Read `journalctl -u deployer -e`. The usual
causes are a port already in use, or the `deployer` account lacking Docker
access. Check the latter with `runuser -u deployer -- docker info`.

**`nginx rejected the configuration`.** The installer leaves nginx as it was.
Run `nginx -t` to see why. If another site already serves `*.previews.example.com`,
remove that site, or use a different preview domain.

**A preview URL shows nginx's default page or a certificate error.** DNS or the
certificate doesn't cover that name. Check the wildcard record and that the
certificate includes `*.previews.example.com`.

**`deployer login` fails with a TLS error.** Don't work around it. Check that
`https://api.previews.example.com/healthz` loads in a browser.

**Lost every administrator token.** On the server, create a new one directly in
the database:

```sh
runuser -u deployer -- deployerd token create rescue --output /var/lib/deployer/rescue.token
```
