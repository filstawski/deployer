package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Server struct {
	Version    int     `yaml:"version" json:"version"`
	Listen     string  `yaml:"listen" json:"listen"`
	StateDir   string  `yaml:"state_dir" json:"state_dir"`
	BaseDomain string  `yaml:"base_domain" json:"base_domain"`
	Runtime    Runtime `yaml:"runtime" json:"runtime"`
	Preview    Preview `yaml:"preview" json:"preview"`
}

type Preview struct {
	Scheme string `yaml:"scheme" json:"scheme"`
	Port   int    `yaml:"port" json:"port"`
}

type Runtime struct {
	Enabled        bool   `yaml:"enabled" json:"enabled"`
	GatewayImage   string `yaml:"gateway_image" json:"gateway_image"`
	GatewayPort    int    `yaml:"gateway_port" json:"gateway_port"`
	ControlPort    int    `yaml:"control_port" json:"control_port"`
	MaxDeployments int    `yaml:"max_deployments" json:"max_deployments"`
}

func Defaults() Server {
	return Server{Version: 1, Listen: "127.0.0.1:7420", StateDir: "/var/lib/deployer", BaseDomain: "localhost", Runtime: Runtime{GatewayImage: "deployer-gateway:dev", GatewayPort: 8080, ControlPort: 7421, MaxDeployments: 8}}
}

func Load(path string) (Server, error) {
	c := Defaults()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return c, err
		}
		d := yaml.NewDecoder(bytes.NewReader(b))
		d.KnownFields(true)
		if err := d.Decode(&c); err != nil {
			return c, fmt.Errorf("server configuration: %w", err)
		}
		var extra any
		if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
			return c, fmt.Errorf("server configuration must contain exactly one YAML document")
		}
		if c.StateDir != "" && !filepath.IsAbs(c.StateDir) {
			abs, err := filepath.Abs(filepath.Join(filepath.Dir(path), c.StateDir))
			if err != nil {
				return c, err
			}
			c.StateDir = abs
		}
	}
	return c, c.Validate()
}

func (c Server) Validate() error {
	if c.Preview.Scheme != "" && c.Preview.Scheme != "http" && c.Preview.Scheme != "https" {
		return fmt.Errorf("preview.scheme must be http or https")
	}
	if c.Preview.Port < 0 || c.Preview.Port > 65535 {
		return fmt.Errorf("invalid preview.port")
	}
	if c.Runtime.Enabled && (c.Runtime.GatewayImage == "" || c.Runtime.GatewayPort < 1 || c.Runtime.GatewayPort > 65535 || c.Runtime.ControlPort < 1 || c.Runtime.ControlPort > 65535 || c.Runtime.ControlPort == c.Runtime.GatewayPort || c.Runtime.MaxDeployments < 1 || c.Runtime.MaxDeployments > 100) {
		return fmt.Errorf("invalid runtime settings")
	}
	if c.Version != 1 {
		return fmt.Errorf("unsupported server configuration version %d", c.Version)
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen must be a loopback IP and port: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("management API must bind to a loopback IP; remote access uses SSH")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("listen port must be between 1 and 65535")
	}
	if c.Runtime.Enabled && (p == c.Runtime.GatewayPort || p == c.Runtime.ControlPort) {
		return fmt.Errorf("runtime ports must differ from the API port")
	}
	if !filepath.IsAbs(c.StateDir) || filepath.Clean(c.StateDir) == string(filepath.Separator) {
		return fmt.Errorf("state_dir must be an absolute non-root directory")
	}
	if c.BaseDomain == "" || len(c.BaseDomain) > 253 || strings.ContainsAny(c.BaseDomain, "/:@ *\t\r\n") || strings.ToLower(c.BaseDomain) != c.BaseDomain {
		return fmt.Errorf("base_domain must be a lowercase hostname without scheme or port")
	}
	for _, part := range strings.Split(c.BaseDomain, ".") {
		if len(part) == 0 || len(part) > 63 || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return fmt.Errorf("invalid base_domain")
		}
		for _, r := range part {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return fmt.Errorf("invalid base_domain")
			}
		}
	}
	return nil
}

func (c Server) PreviewScheme() string {
	if c.Preview.Scheme == "" {
		return "http"
	}
	return c.Preview.Scheme
}
func (c Server) PreviewPort() int {
	if c.Preview.Port != 0 {
		return c.Preview.Port
	}
	if c.PreviewScheme() == "https" {
		return 443
	}
	return c.Runtime.GatewayPort
}

func (c Server) PreviewURL(host string) string {
	scheme := c.PreviewScheme()
	port := c.PreviewPort()
	if !(scheme == "https" && port == 443 || scheme == "http" && port == 80) {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return scheme + "://" + host
}
