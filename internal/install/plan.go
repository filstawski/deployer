// Package install prepares and installs the Linux server release. It never
// downloads or upgrades the host's Docker/nginx packages implicitly.
package install

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"deployer/internal/config"
)

var releasePattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9][A-Za-z0-9.-]*)?$`)

type Options struct {
	Domain, Bundle, Version, Cert, Key string
	APIPort, GatewayPort, ControlPort  int
	DryRun                             bool
}
type Plan struct {
	Version    string   `json:"version"`
	Platform   string   `json:"platform"`
	ConfigPath string   `json:"config_path"`
	StateDir   string   `json:"state_dir"`
	ReleaseDir string   `json:"release_dir"`
	TokenFile  string   `json:"token_file"`
	API        string   `json:"api_url"`
	Edge       string   `json:"edge"`
	Steps      []string `json:"steps"`
	Next       []string `json:"next"`
}

func (o Options) Plan() (Plan, error) {
	p := Plan{Version: o.Version, Platform: "linux/systemd", ConfigPath: "/etc/deployer/server.yaml", StateDir: "/var/lib/deployer", ReleaseDir: filepath.Join("/opt/deployer/releases", o.Version), TokenFile: "/var/lib/deployer/operator.token", API: fmt.Sprintf("http://127.0.0.1:%d", o.APIPort), Edge: "pending"}
	if !releasePattern.MatchString(o.Version) {
		return p, fmt.Errorf("setup requires a versioned release binary; build with make release VERSION=v0.1.0-dev")
	}
	c := config.Defaults()
	c.BaseDomain = o.Domain
	c.Listen = fmt.Sprintf("127.0.0.1:%d", o.APIPort)
	c.Runtime.Enabled = true
	c.Runtime.GatewayPort = o.GatewayPort
	c.Runtime.ControlPort = o.ControlPort
	if err := c.Validate(); err != nil {
		return p, err
	}
	if o.Domain == "localhost" || !strings.Contains(o.Domain, ".") {
		return p, fmt.Errorf("--domain must be your preview base domain, such as surdic.run")
	}
	if (o.Cert == "") != (o.Key == "") {
		return p, fmt.Errorf("--tls-cert and --tls-key must be supplied together")
	}
	for _, path := range []string{o.Cert, o.Key} {
		if path != "" && (!filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n\t ;{}\"'$\\")) {
			return p, fmt.Errorf("certificate paths must be absolute and contain no nginx metacharacters or whitespace")
		}
	}
	p.Steps = []string{"Check Linux/systemd, local Docker 28+, Buildx, Git, account tools, free ports, and release files", "Create a dedicated deployer service account and protected state directory", "Install versioned binaries and a gateway image built from the released binary", "Write server settings, bootstrap an owner-only credential, and register a systemd service", "Start Deployer and verify the authenticated API"}
	if o.Cert != "" {
		p.Edge = "nginx"
		p.Steps = append(p.Steps, "Validate the existing nginx certificate and configuration, add one wildcard site, and reload nginx")
	} else {
		p.Next = append(p.Next, "HTTPS is pending: obtain a wildcard certificate, then run deployer setup with --tls-cert and --tls-key")
	}
	p.Next = append(p.Next, "Point wildcard DNS at this server", "Create a separate laptop credential, then run deployer server add NAME --ssh USER@HOST --credential-file FILE; see docs/installation.md")
	return p, nil
}

const Unit = `# Managed by Deployer setup. Existing differing files are never overwritten.
[Unit]
Description=Deployer preview controller
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
User=deployer
Group=deployer
SupplementaryGroups=docker
Environment=HOME=/var/lib/deployer
Environment=DOCKER_HOST=unix:///var/run/docker.sock
Environment=PATH=/usr/local/bin:/usr/bin:/bin
WorkingDirectory=/var/lib/deployer
ExecStart=/opt/deployer/current/deployerd serve --config /etc/deployer/server.yaml
Restart=on-failure
RestartSec=5
TimeoutStopSec=30
UMask=0077
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/deployer

[Install]
WantedBy=multi-user.target
`

func (o Options) Nginx() string {
	return fmt.Sprintf(`# Managed by Deployer setup; one route for every preview.
map $http_upgrade $deployer_connection_upgrade {
    default upgrade;
    '' close;
}
server {
    listen 80;
    server_name *.%s;
    return 308 https://$host$request_uri;
}
server {
    listen 443 ssl;
    server_name *.%s;
    ssl_certificate %s;
    ssl_certificate_key %s;
    location / {
        proxy_pass http://127.0.0.1:%d;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $deployer_connection_upgrade;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_intercept_errors off;
        proxy_buffering off;
        proxy_read_timeout 3600s;
    }
}
`, o.Domain, o.Domain, o.Cert, o.Key, o.GatewayPort)
}
