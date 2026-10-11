# Installing Deployer

The release source is **filstawski/deployer**. The development pilot is **v0.1.0-dev.2**; use the pinned installation commands in the [README](../README.md). Install directly inside your existing SSH client on the server—there is no need to transfer files from a laptop. Real Linux/systemd acceptance is still in progress.

The stable-release examples below describe the future stable distribution. `v0.1.0` is not published; use `v0.1.0-dev.2` and its release-hosted `install.sh` for the current pilot. GitHub's `latest` URL excludes prereleases.

## Install the client

The client supports macOS and Linux on Intel/AMD64 and ARM64. Go, Docker, and administrator access are not needed on a client machine. Windows users can use the Linux client in WSL; a native Windows client is not packaged yet.

After the first stable release is published:

```sh
curl -fsSL https://raw.githubusercontent.com/filstawski/deployer/main/install.sh | sh
```

The default destination is `~/.local/bin/deployer`. The installer tells you if that directory needs to be added to PATH; it does not edit shell profiles automatically. To pin a release or choose another writable destination:

```sh
curl -fsSL https://raw.githubusercontent.com/filstawski/deployer/main/install.sh \
  | sh -s -- client --version v0.1.0 --bin-dir "$HOME/.local/bin"
```

`v0.1.0` here is an example future release, not a currently published release. The installer detects OS/architecture, downloads the binary, verifies its SHA-256 checksum, stages it beside the destination, and atomically replaces a client previously installed by this script. It refuses to replace unrelated executables or symlinks. Reinstall with a newer explicit version to update the client.

The script and checksum manifest are trusted release assets delivered over HTTPS. Checksums catch incomplete or changed downloads; they are not independently signed attestations. For a pinned bootstrap, download `install.sh` from the same immutable release tag instead of the `main` branch. A Homebrew tap and native Windows installer are later distribution work.

## Install the server

The server targets a **Linux host with systemd, Docker Engine 28+, system-wide Buildx, and Git already installed**. It needs a local Docker daemon at `/var/run/docker.sock`, the conventional `docker` group, and standard account tools (`getent`, `useradd`, `runuser`, `chown`). The installer does not replace Docker, nginx, or the host's package configuration. It runs as root to create the service account, protected paths, and service.

After a release is published, run this on the server:

```sh
curl -fsSL https://raw.githubusercontent.com/filstawski/deployer/main/install.sh \
  | sudo sh -s -- server --version v0.1.0 -- --domain surdic.run
```

If already logged in as root, omit `sudo`. `--` separates download options from server setup options. This command installs the controller, gateway, and server-side CLI; creates the `deployer` service account; initializes an owner-only API credential; and starts the systemd service. The released gateway binary is packaged into a small scratch image locally, so no Go compiler or source checkout is needed. Its immutable image ID is saved in server configuration.

Without certificate arguments, **the private API is installed, but public HTTPS is pending**. The result says this explicitly. Setup does not expose an unfinished HTTP preview service on public ports or claim the domain is ready.

When a wildcard certificate already exists, one command also connects the existing nginx:

```sh
curl -fsSL https://raw.githubusercontent.com/filstawski/deployer/main/install.sh \
  | sudo sh -s -- server --version v0.1.0 -- \
      --domain surdic.run \
      --tls-cert /etc/letsencrypt/live/surdic.run/fullchain.pem \
      --tls-key /etc/letsencrypt/live/surdic.run/privkey.pem
```

Use the paths your certificate tool actually created. Setup validates that certificate and key match, that the certificate is in date, and that it covers preview hostnames. It adds `/etc/nginx/conf.d/deployer.conf`, validates nginx, verifies that nginx loads the file, and reloads it. Existing differing files are left untouched. The configuration forwards all preview subdomains to one gateway, including WebSocket upgrades. Deployer generates HTTPS URLs and sets the application's forwarded scheme from its own configuration.

Cloudflare DNS and automatic certificate issuance/renewal are still separate setup steps: see [server pilot preparation](server-pilot.md). The installer accepts existing certificates; it does not yet request or store Cloudflare credentials. A guided Cloudflare setup is the next installation feature.

## Preview or resume setup

Append `--dry-run` to see the filesystem layout and intended steps without modifying the host:

```sh
# With a versioned client binary already present:
deployer setup --domain surdic.run --dry-run --json
```

Dry-run is a plan, not a host preflight or promise that all dependencies are installed. It works on a laptop too. Applying setup requires the release bundle; the bootstrap script downloads it automatically. A development binary whose version is just `dev` must first be built as a versioned release.

After a server install, the matching release bundle remains on the host. You can add existing certificate paths without downloading again:

```sh
sudo deployer setup --bundle /opt/deployer/current \
  --domain surdic.run \
  --tls-cert /etc/letsencrypt/live/surdic.run/fullchain.pem \
  --tls-key /etc/letsencrypt/live/surdic.run/privkey.pem
```

Rerunning the same release preserves deployment state, credentials, and unchanged configuration. It can resume an interrupted install. Edited server settings or service files are never silently replaced. Changing installation ports/domain, adopting an unrelated installation, or replacing an installed server release is refused. **Server upgrades/rollback are not implemented yet**; they need coordinated database and gateway compatibility handling. Client updates are already supported.

If ports are occupied during a fresh install, choose loopback ports explicitly with `--api-port`, `--gateway-port`, and `--control-port`. Defaults are 7420, 8080, and 7421. Keep those same values when resuming setup.

## Where things live

| Path | Purpose |
| --- | --- |
| `/usr/local/bin/deployer` | Server-side CLI symlink. |
| `/usr/local/bin/deployerd` | Local controller/admin command symlink. |
| `/opt/deployer/releases/<version>/` | Immutable release binaries and gateway build context. |
| `/opt/deployer/current` | Installed release symlink. |
| `/etc/deployer/server.yaml` | Server settings. |
| `/etc/deployer/install.json` | Installation receipt, without credential values. |
| `/var/lib/deployer/` | Protected database, source, logs, builder settings, and credentials. |
| `/var/lib/deployer/operator.token` | Initial API credential, owner-only and never printed. |
| `/etc/systemd/system/deployer.service` | Controller service definition. |
| `/etc/nginx/conf.d/deployer.conf` | Optional wildcard nginx route. |

The service runs as a dedicated `deployer` user. Its access to the Docker socket is host-administrative authority. It is not a sandbox for hostile repositories. The systemd service restricts ordinary filesystem writes to its state directory and fixes Docker access to the local system socket.

## Connect and check

**Default: connect the local CLI through the authenticated HTTPS API.** Follow the [manual deployment guide](manual-deployments.md) for `api.surdic.run`, laptop token enrollment, and deploying projects. SSH contexts below are an optional transport, not required for normal use.

As root on an installed server, the CLI automatically discovers the local configuration and bootstrap credential when no explicit or saved context is selected:

```sh
deployer server status --json
deployer list --json
systemctl status deployer.service
journalctl -u deployer.service
```

The management API remains on loopback. The laptop CLI can now open and close its own SSH connection for each command. It uses your existing OpenSSH keys/configuration, requires an already verified host key, and does not forward your SSH agent. You do not need a public API port or a separately running tunnel.

First verify the server fingerprint through a trusted channel and establish normal SSH key access. Deployer uses strict host-key checking and noninteractive key authentication, so an unknown host, password prompt, or unavailable key fails with an actionable error. SSH aliases, custom SSH ports, and identity files belong in `~/.ssh/config`; `--remote-port` means the server's loopback **API** port.

Create a separate laptop credential **on the installed server**, as root:

```sh
deployer token create laptop --output /root/deployer-laptop.token \
  --scopes server:read,projects:read,projects:write,deployments:read,deployments:write \
  --projects '*' --ttl 720h --json
```

This grants preview management for all projects for 30 days, without token administration. For an agent restricted to an existing project, omit `projects:write` and replace `'*'` with its project ID. Keep the returned token ID for revocation; the secret is only written to the file.

On your laptop, copy that credential securely and save the connection:

```sh
umask 077
mkdir -p "$HOME/.config/deployer"
scp -p root@187.77.92.158:/root/deployer-laptop.token "$HOME/.config/deployer/pilot.token"
chmod 600 "$HOME/.config/deployer/pilot.token"
deployer server add pilot --ssh root@187.77.92.158 \
  --credential-file "$HOME/.config/deployer/pilot.token"
deployer server use pilot
deployer server status --json
deployer list --json
```

The contexts file stores the SSH destination, API port, and credential **path**, never the token itself. A private Unix socket carries API traffic locally; there is no temporary laptop TCP listener. SSH closes on command completion, cancellation, or failure. Accepted deployment jobs continue on the server after the command disconnects. `--wait` reuses the same SSH connection while polling.

Use `--remote-port 17420` when setup chose a different management port. Direct API contexts still accept `--url`. Overriding a saved SSH context with `--server URL` also requires an explicit `--token-file` to avoid sending its credential elsewhere. The server must permit forwarding to its loopback API.

Automatic credential pairing is still pending; the client installer never copies the server's bootstrap credential. Renew an expired laptop credential from the server and revoke its old token ID with `deployer token revoke ID`. The temporary transfer copy can be removed from `/root` after successful connection.

## Build/test unpublished releases locally

From a development checkout with Go and Python 3:

```sh
make release VERSION=v0.1.0-dev
```

This builds the macOS/Linux client variants and Linux server/gateway variants for AMD64 and ARM64 into `dist/`, plus `VERSION`, `SHA256SUMS`, `release.json`, and the bootstrap script. The output directory must be empty; release files are not silently overwritten. The builder does not publish to GitHub.

Install a local client build into a temporary/test destination:

```sh
sh install.sh client --from "$PWD/dist" --bin-dir "$PWD/.cache/client-bin"
.cache/client-bin/deployer version
.cache/client-bin/deployer setup --domain surdic.run --dry-run --json
```

To test server installation on a disposable Linux/systemd machine, copy the full release directory and run:

```sh
sudo sh install.sh server --from /path/to/dist -- --domain previews.example.com
```

Validation in this workspace covers macOS/Linux AMD64/ARM64 release cross-compilation, temporary client installation/update, a Linux release dry-run in a network-disabled container, corrupt-download rejection, server file generation/reinstall/interruption tests with injected host commands, SSH subprocess/socket lifecycle and credential-bound context tests, and the existing real Docker lifecycle test. A complete real-systemd-host install and nginx reload have not yet been verified. Do that on a disposable host before publishing a stable release. Live-server SSH access has not been retried or used for this work.

## GitHub README and release checklist

Once installation passes the Linux host acceptance test, rewrite the GitHub README around these two short installation paths, the first deployment, and the project recipe. Keep the longer concepts and architecture in docs. Before advertising a download command:

- Commit the installer and release tooling to `filstawski/deployer`.
- Build and test an explicit release version; publish all assets and checksums under that exact tag.
- Verify fresh client/server installs using the published URLs.
- Document remaining pilot limits prominently, especially private repositories, certificate automation, and SSH pairing.

The GitHub-facing README polish is intentionally deferred until those commands are genuinely available.
