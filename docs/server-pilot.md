# Server pilot: existing nginx and Cloudflare

The deployment runtime works locally. The server pilot is **not installed**. [Installer and release tooling](installation.md) now exist locally, including nginx integration for an existing wildcard certificate. GitHub release publication, real Linux/systemd validation, Cloudflare certificate automation, and SSH pairing remain pending. SSH inspection was not approved, so the server's OS, architecture, nginx configuration, certificate tooling, and available ports have not been verified. The domain owner uses Cloudflare and already has nginx and Docker on the target host. Keep that existing nginx as the HTTPS edge.

## Cloudflare preparation

In the `surdic.run` zone, confirm that Cloudflare considers the zone active and that its assigned nameservers are installed at the domain registrar. Add this record if there is no conflicting wildcard record:

| Type | Name | Content | Proxy | TTL |
| --- | --- | --- | --- | --- |
| A | `*` | The server's public IPv4 address supplied by the operator | DNS only initially | Auto |

A wildcard supplies DNS for generated preview names; explicit records can override wildcard behavior. The apex `surdic.run` does not need an A record just to serve previews. Only add an AAAA record after verifying the host's IPv6 routing and nginx listener. See [Cloudflare wildcard DNS](https://developers.cloudflare.com/dns/manage-dns-records/reference/wildcard-dns-records/).

Use a publicly trusted wildcard certificate for `*.surdic.run` on nginx. Let's Encrypt supports wildcard issuance through [DNS-01 validation](https://letsencrypt.org/docs/challenge-types/#dns-01-challenge). Automate renewal with the existing certificate tool if possible; otherwise use Certbot's Cloudflare plugin after checking the operating system and current renewal jobs.

The [Certbot Cloudflare plugin](https://certbot-dns-cloudflare.readthedocs.io/en/stable/) requires a Cloudflare API token with `Zone:DNS:Edit`, restricted to `surdic.run`. Save it in an owner-only credential file on the server; do not paste it into project recipes, CLI flags, logs, or chat. It is certificate infrastructure, not an application secret.

Keep DNS-only mode while verifying direct HTTPS. Cloudflare proxying can be enabled later if desired; use [Full (strict)](https://developers.cloudflare.com/ssl/origin-configuration/ssl-modes/full-strict/) with a valid origin certificate. Certificate issuance and renewal must be verified before public previews are considered ready.

## Request path

```text
Browser → nginx :443 → Deployer gateway on loopback → isolated application
Laptop CLI → SSH tunnel → authenticated deployerd API on loopback
Server CLI → authenticated deployerd API on loopback
```

There is one nginx wildcard virtual host for `*.surdic.run`. Its upstream is the shared gateway, never an individual application. Deployer changes routes internally as previews are created or stopped. nginx retains its existing sites and controls ports 80/443.

The nginx location must preserve Host, support [WebSocket upgrades](https://nginx.org/en/docs/http/websocket.html), and forward status responses without replacing Deployer's waiting pages. Use [proxy buffering/timeouts](https://nginx.org/en/docs/http/ngx_http_proxy_module.html) appropriate for preview streaming. The gateway must know the configured external HTTPS scheme without trusting arbitrary visitor-supplied forwarding headers. No nginx template should be applied before reading the actual configuration and checking available loopback ports.

## Installation work, in order

1. Inspect the host without changes: OS/architecture, Docker and Buildx, nginx sites/listeners, current certificates/renewal, disk/RAM, and existing uses of ports 7420/7421/8080. Verify the SSH host key through the operator's trusted channel if it is not already known.
2. Validate the implemented nginx-aware settings on the host: HTTPS preview URLs, external scheme/port, forwarded scheme, and WebSocket behavior. Add protected previews before using private customer data.
3. Build verified Linux release binaries and infrastructure images for the server architecture. Choose a reproducible pinned BuildKit image for the pilot and add storage/cache preflight. Do not install a Go development toolchain on the server just to run the release.
4. Install `/usr/local/bin/deployer`, controller/gateway binaries, `/etc/deployer/server.yaml`, and persistent `/var/lib/deployer/` state. Use a dedicated service account and a systemd unit. That account's Docker access is host-administrative privilege. Home directories hold only CLI contexts and credentials.
5. Bootstrap a new server API credential into an owner-only file. Keep management API and gateway control on loopback. Add the laptop SSH context and test the CLI locally on the server and through the tunnel. No public management API is necessary.
6. Provision and test wildcard certificate renewal. Add one nginx wildcard site, validate with `nginx -t`, then reload nginx only after configuration validation. Verify existing sites as well as the new route.
7. Enroll the first real application repository. Public HTTPS already works; private repositories need explicit deploy-key enrollment and Git host verification. Commit the Dockerfile and supported `deployer.yaml`.
8. Deploy two branches. Verify waiting pages, original-path reload, separate content, authenticated management, expiry, stop/wake, logs, and cleanup. Run a controller restart and then a host-reboot acceptance test before calling the pilot complete.

The local Docker test covers two real containers but uses deterministic source fixtures. Host integration, public DNS/TLS, and private Git have separate acceptance checks. See [the roadmap](../ROADMAP.md) for unfinished pilot and recovery work.
