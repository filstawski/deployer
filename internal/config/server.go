// Package config loads and validates the server configuration (server.yaml)
// and project recipes (deployer.yaml).
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultServerPath is where the installer writes the server configuration.
const DefaultServerPath = "/etc/deployer/server.yaml"

// Server is the controller configuration, normally /etc/deployer/server.yaml.
type Server struct {
	Version int `yaml:"version"`
	// Domain is the base domain; previews are served at <name>.<domain>.
	Domain string `yaml:"domain"`
	// StateDir holds the database, credentials, and build logs. Relative
	// paths are resolved against the configuration file's directory.
	StateDir    string      `yaml:"state_dir"`
	API         API         `yaml:"api"`
	Gateway     Gateway     `yaml:"gateway"`
	Previews    Previews    `yaml:"previews"`
	Deployments Deployments `yaml:"deployments"`
}

// API configures the management API.
type API struct {
	// Listen is the loopback address of the management API. Remote clients
	// reach it through an HTTPS reverse proxy or an SSH tunnel.
	Listen string `yaml:"listen"`
	// PublicURL is the HTTPS origin of the API as seen from outside, used for
	// GitHub App callbacks. Defaults to https://api.<domain>.
	PublicURL string `yaml:"public_url,omitempty"`
}

// Gateway configures the preview gateway container.
type Gateway struct {
	// Listen is the host address that receives preview traffic, usually from
	// a local TLS-terminating reverse proxy such as nginx.
	Listen string `yaml:"listen"`
	// ControlListen is the loopback address of the gateway's control API.
	ControlListen string `yaml:"control_listen"`
	// Image overrides the gateway image. When empty, deployerd builds the
	// image from its own executable, which requires a Linux binary that
	// matches the Docker host's architecture.
	Image string `yaml:"image,omitempty"`
}

// Previews describes how visitors reach previews, for building URLs.
type Previews struct {
	Scheme string `yaml:"scheme"`
	// Port is the external port. 0 means 443 for https and the gateway
	// listen port for http.
	Port int `yaml:"port,omitempty"`
}

// Deployments holds limits and defaults for deployments.
type Deployments struct {
	MaxLive          int      `yaml:"max_live"`
	DefaultTTL       Duration `yaml:"default_ttl"`
	MaxTTL           Duration `yaml:"max_ttl"`
	Retention        Duration `yaml:"retention"`
	BuildConcurrency int      `yaml:"build_concurrency"`
	BuildTimeout     Duration `yaml:"build_timeout"`
	CPUs             float64  `yaml:"cpus"`
	MemoryMB         int      `yaml:"memory_mb"`
	PIDs             int      `yaml:"pids"`
	// Egress allows applications to open outbound connections at runtime.
	// Builds can always download dependencies.
	Egress bool `yaml:"egress"`
}

// Defaults returns the default configuration. Domain must still be set.
func Defaults() Server {
	return Server{
		Version:  1,
		StateDir: "/var/lib/deployer",
		API:      API{Listen: "127.0.0.1:7420"},
		Gateway:  Gateway{Listen: "127.0.0.1:8080", ControlListen: "127.0.0.1:7421"},
		Previews: Previews{Scheme: "https"},
		Deployments: Deployments{
			MaxLive:          10,
			DefaultTTL:       Duration(24 * time.Hour),
			MaxTTL:           Duration(7 * 24 * time.Hour),
			Retention:        Duration(7 * 24 * time.Hour),
			BuildConcurrency: 1,
			BuildTimeout:     Duration(20 * time.Minute),
			CPUs:             1,
			MemoryMB:         512,
			PIDs:             256,
		},
	}
}

// LoadServer reads, defaults, and validates a server configuration file.
func LoadServer(path string) (Server, error) {
	c := Defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := decodeStrict(data, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if c.StateDir != "" && !filepath.IsAbs(c.StateDir) {
		c.StateDir = filepath.Join(filepath.Dir(path), c.StateDir)
		if abs, err := filepath.Abs(c.StateDir); err == nil {
			c.StateDir = abs
		}
	}
	if err := c.Validate(); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Marshal renders the configuration as YAML.
func (c Server) Marshal() []byte {
	b, err := yaml.Marshal(c)
	if err != nil {
		panic(err) // Server contains only marshalable fields.
	}
	return b
}

// Validate reports the first invalid setting.
func (c Server) Validate() error {
	d := c.Deployments
	switch {
	case c.Version != 1:
		return fmt.Errorf("version: unsupported value %d (expected 1)", c.Version)
	case validateDomain(c.Domain) != nil:
		return fmt.Errorf("domain: %w", validateDomain(c.Domain))
	case !filepath.IsAbs(c.StateDir) || filepath.Clean(c.StateDir) == "/":
		return fmt.Errorf("state_dir: must be an absolute directory other than /")
	case c.Previews.Scheme != "http" && c.Previews.Scheme != "https":
		return fmt.Errorf("previews.scheme: must be http or https")
	case c.Previews.Port < 0 || c.Previews.Port > 65535:
		return fmt.Errorf("previews.port: must be between 0 and 65535")
	case d.MaxLive < 1 || d.MaxLive > 1000:
		return fmt.Errorf("deployments.max_live: must be between 1 and 1000")
	case d.DefaultTTL.D() < time.Minute || d.MaxTTL.D() < d.DefaultTTL.D():
		return fmt.Errorf("deployments: default_ttl must be at least 1m and no longer than max_ttl")
	case d.Retention.D() < 0:
		return fmt.Errorf("deployments.retention: must not be negative")
	case d.BuildConcurrency < 1 || d.BuildConcurrency > 16:
		return fmt.Errorf("deployments.build_concurrency: must be between 1 and 16")
	case d.BuildTimeout.D() < time.Minute || d.BuildTimeout.D() > 2*time.Hour:
		return fmt.Errorf("deployments.build_timeout: must be between 1m and 2h")
	case d.CPUs <= 0 || d.CPUs > 64 || d.MemoryMB < 32 || d.PIDs < 16:
		return fmt.Errorf("deployments: cpus must be in (0, 64], memory_mb at least 32, and pids at least 16")
	}
	if c.API.PublicURL != "" {
		if err := validateOrigin(c.API.PublicURL); err != nil {
			return fmt.Errorf("api.public_url: %w", err)
		}
	}
	apiPort, err := loopbackPort(c.API.Listen)
	if err != nil {
		return fmt.Errorf("api.listen: %w", err)
	}
	controlPort, err := loopbackPort(c.Gateway.ControlListen)
	if err != nil {
		return fmt.Errorf("gateway.control_listen: %w", err)
	}
	if _, _, err := splitHostPort(c.Gateway.Listen); err != nil {
		return fmt.Errorf("gateway.listen: %w", err)
	}
	gatewayPort := c.GatewayPort()
	if apiPort == controlPort || apiPort == gatewayPort || controlPort == gatewayPort {
		return fmt.Errorf("api.listen, gateway.listen, and gateway.control_listen must use different ports")
	}
	return nil
}

// Hostname returns the preview hostname for a deployment name.
func (c Server) Hostname(name string) string { return name + "." + c.Domain }

// PreviewURL returns the visitor URL for a deployment name.
func (c Server) PreviewURL(name string) string {
	host := c.Hostname(name)
	port := c.Previews.Port
	if port == 0 {
		port = 443
		if c.Previews.Scheme == "http" {
			port = c.GatewayPort()
		}
	}
	if !(c.Previews.Scheme == "https" && port == 443 || c.Previews.Scheme == "http" && port == 80) {
		host = net.JoinHostPort(host, strconv.Itoa(port))
	}
	return c.Previews.Scheme + "://" + host
}

// PublicAPIURL returns the external HTTPS origin of the management API.
func (c Server) PublicAPIURL() string {
	if c.API.PublicURL != "" {
		return strings.TrimRight(c.API.PublicURL, "/")
	}
	return "https://api." + c.Domain
}

// GatewayPort returns the host port that receives preview traffic.
func (c Server) GatewayPort() int {
	_, port, _ := splitHostPort(c.Gateway.Listen)
	return port
}

// ControlURL returns the base URL of the gateway control API.
func (c Server) ControlURL() string { return "http://" + c.Gateway.ControlListen }

// DatabasePath returns the path of the SQLite database.
func (c Server) DatabasePath() string { return filepath.Join(c.StateDir, "state.db") }

// AdminTokenPath returns the path of the administrator token that the
// controller creates on first start.
func (c Server) AdminTokenPath() string { return filepath.Join(c.StateDir, "admin.token") }

func splitHostPort(addr string) (net.IP, int, error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, 0, fmt.Errorf("must be IP:PORT, such as 127.0.0.1:7420")
	}
	ip := net.ParseIP(host)
	port, err := strconv.Atoi(portText)
	if ip == nil || err != nil || port < 1 || port > 65535 {
		return nil, 0, fmt.Errorf("must be IP:PORT with a numeric IP and a port between 1 and 65535")
	}
	return ip, port, nil
}

func loopbackPort(addr string) (int, error) {
	ip, port, err := splitHostPort(addr)
	if err != nil {
		return 0, err
	}
	if !ip.IsLoopback() {
		return 0, fmt.Errorf("must be a loopback address; expose it through an HTTPS reverse proxy instead")
	}
	return port, nil
}

func validateDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("is required, for example previews.example.com")
	}
	if len(domain) > 200 || strings.ToLower(domain) != domain {
		return fmt.Errorf("must be a lowercase hostname of at most 200 characters")
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) == 0 || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") ||
			strings.Trim(label, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
			return fmt.Errorf("%q is not a valid hostname", domain)
		}
	}
	return nil
}

// ValidateDomain checks that domain is a usable preview base domain.
func ValidateDomain(domain string) error { return validateDomain(domain) }

func validateOrigin(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return fmt.Errorf("must be an HTTPS origin such as https://api.example.com")
	}
	return nil
}
