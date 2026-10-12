package install

import (
	"fmt"
	"strings"

	"github.com/filstawski/deployer/internal/config"
)

// unitFile is the systemd service. The service account belongs to the docker
// group, which is equivalent to root on this host: Deployer is trusted
// infrastructure, not a sandbox.
const unitFile = `# Managed by deployer server install; changes are overwritten on upgrade.
[Unit]
Description=Deployer controller
Documentation=https://github.com/filstawski/deployer
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
User=deployer
Group=deployer
SupplementaryGroups=docker
ExecStart=/opt/deployer/current/deployerd serve --config /etc/deployer/server.yaml
Restart=on-failure
RestartSec=5s
TimeoutStopSec=60s
WorkingDirectory=/var/lib/deployer
Environment=HOME=/var/lib/deployer
Environment=DOCKER_HOST=unix:///var/run/docker.sock
UMask=0077
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/deployer

[Install]
WantedBy=multi-user.target
`

// serverConfig renders the initial server.yaml.
func serverConfig(o Options) []byte {
	c := config.Defaults()
	c.Domain = o.Domain
	c.StateDir = StateDir
	c.API.Listen = fmt.Sprintf("127.0.0.1:%d", o.APIPort)
	if o.APIHost != "" {
		c.API.PublicURL = "https://" + o.APIHost
	}
	c.Gateway.Listen = fmt.Sprintf("127.0.0.1:%d", o.GatewayPort)
	c.Gateway.ControlListen = fmt.Sprintf("127.0.0.1:%d", o.ControlPort)
	c.Previews.Scheme = "https"
	return append([]byte("# Deployer server configuration. Restart after editing: systemctl restart deployer\n"), c.Marshal()...)
}

// nginxSite routes every preview hostname to the gateway and, optionally,
// api.<domain> to the management API. The API still requires a token for
// everything except /healthz and the GitHub setup links.
func nginxSite(o Options) string {
	var b strings.Builder
	fmt.Fprintf(&b, `# Managed by deployer server install; changes are overwritten.
map $http_upgrade $deployer_connection {
    default upgrade;
    ''      close;
}

server {
    listen 80;
    server_name *.%[1]s;
    return 308 https://$host$request_uri;
}

# Previews: every <name>.%[1]s goes to the Deployer gateway.
server {
    listen 443 ssl;
    server_name *.%[1]s;
    ssl_certificate     %[2]s;
    ssl_certificate_key %[3]s;
    client_max_body_size 100m;

    location / {
        proxy_pass http://127.0.0.1:%[4]d;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $deployer_connection;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
        proxy_buffering off;
        proxy_request_buffering off;
        proxy_read_timeout 1h;
        proxy_send_timeout 1h;
    }
}
`, o.Domain, o.TLSCert, o.TLSKey, o.GatewayPort)
	if o.APIHost != "" {
		fmt.Fprintf(&b, `
limit_req_zone $binary_remote_addr zone=deployer_api:10m rate=10r/s;

# Management API for the deployer CLI.
server {
    listen 443 ssl;
    server_name %[1]s;
    ssl_certificate     %[2]s;
    ssl_certificate_key %[3]s;
    client_max_body_size 1m;

    location /v1/ {
        limit_req zone=deployer_api burst=50 nodelay;
        limit_req_status 429;
        proxy_pass http://127.0.0.1:%[4]d;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto https;
        proxy_read_timeout 120s;
    }
    location = /healthz {
        proxy_pass http://127.0.0.1:%[4]d;
    }
    location / {
        return 404;
    }
}
`, o.APIHost, o.TLSCert, o.TLSKey, o.APIPort)
	}
	return b.String()
}
