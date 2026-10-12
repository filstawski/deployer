package install

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/config"
)

var releasePattern = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

// Options configures an installation.
type Options struct {
	Version string `json:"version"`
	Bundle  string `json:"-"`
	Domain  string `json:"domain"`
	TLSCert string `json:"tls_cert,omitempty"`
	TLSKey  string `json:"tls_key,omitempty"`
	// APIHost is the public hostname of the management API, such as
	// api.example.com. Empty keeps the API reachable only locally or over SSH.
	APIHost     string `json:"api_host,omitempty"`
	APIPort     int    `json:"api_port"`
	GatewayPort int    `json:"gateway_port"`
	ControlPort int    `json:"control_port"`
	DryRun      bool   `json:"-"`
}

// Result describes what an installer command did or would do.
type Result struct {
	Status   string   `json:"status"`
	Version  string   `json:"version"`
	Previews string   `json:"previews,omitempty"`
	API      string   `json:"api,omitempty"`
	Steps    []string `json:"steps,omitempty"`
	Next     []string `json:"next,omitempty"`
}

func (o *Options) validate() error {
	if !releasePattern.MatchString(o.Version) {
		return fmt.Errorf("installing requires a release build (this is %q); build one with make release VERSION=v0.0.0-test", o.Version)
	}
	if err := config.ValidateDomain(o.Domain); err != nil {
		return fmt.Errorf("--domain: %w", err)
	}
	if (o.TLSCert == "") != (o.TLSKey == "") {
		return fmt.Errorf("--tls-cert and --tls-key go together")
	}
	if o.TLSCert == "" {
		o.APIHost = ""
	}
	for _, p := range []string{o.TLSCert, o.TLSKey} {
		if p != "" && (!filepath.IsAbs(p) || strings.ContainsAny(p, " \t\r\n;{}\"'$\\#")) {
			return fmt.Errorf("certificate paths must be absolute and contain no spaces or special characters: %q", p)
		}
	}
	ports := map[int]bool{}
	for _, p := range []int{o.APIPort, o.GatewayPort, o.ControlPort} {
		if p < 1 || p > 65535 || ports[p] {
			return fmt.Errorf("--api-port, --gateway-port, and --control-port must be distinct ports")
		}
		ports[p] = true
	}
	return nil
}

func (o Options) plan() []string {
	steps := []string{
		"Check for Linux, systemd, Docker Engine 28+ with Buildx, and Git",
		"Create the " + ServiceUser + " service account (member of the docker group)",
		"Install binaries in " + filepath.Join(ReleasesDir, o.Version) + " and link them from " + BinDir,
		"Write " + ConfigFile + " and " + UnitFile,
		"Start " + Service + "; it creates " + StateDir + "/admin.token on first start",
	}
	if o.TLSCert != "" {
		site := "previews (*." + o.Domain + ")"
		if o.APIHost != "" {
			site += " and the API (" + o.APIHost + ")"
		}
		steps = append(steps, "Add "+NginxFile+" for "+site+", test it, and reload nginx")
	}
	return steps
}

func (o Options) result(status string) Result {
	r := Result{Status: status, Version: o.Version, Previews: "https://NAME." + o.Domain, Steps: o.plan()}
	if o.APIHost != "" {
		r.API = "https://" + o.APIHost
	}
	r.Next = append(r.Next, "Point a wildcard DNS record *."+o.Domain+" at this server")
	if o.TLSCert == "" {
		r.Next = append(r.Next, "Get a wildcard certificate for *."+o.Domain+", then run deployer server install again with --tls-cert and --tls-key")
	}
	r.Next = append(r.Next, "Create a token for your laptop: deployer token create laptop")
	if r.API != "" {
		r.Next = append(r.Next, "On your laptop: deployer login "+r.API)
	} else {
		r.Next = append(r.Next, "On your laptop: deployer login ssh://root@THIS-SERVER")
	}
	return r
}

// Install installs Deployer. Running it again with the same options resumes
// an interrupted installation; it never discards state or credentials.
func (i *Installer) Install(ctx context.Context, o Options) (Result, error) {
	if err := o.validate(); err != nil {
		return Result{}, err
	}
	if o.DryRun {
		return o.result("planned"), nil
	}
	receipt, err := i.readReceipt()
	if err != nil {
		return Result{}, err
	}
	reinstall := receipt != nil && receipt.Status == "uninstalled"
	if receipt != nil && !reinstall {
		switch {
		case receipt.Version != o.Version:
			return Result{}, fmt.Errorf("Deployer %s is installed; use deployer server upgrade to change versions", receipt.Version)
		case receipt.Options.Domain != o.Domain || receipt.Options.APIPort != o.APIPort ||
			receipt.Options.GatewayPort != o.GatewayPort || receipt.Options.ControlPort != o.ControlPort:
			return Result{}, fmt.Errorf("Deployer is installed with different settings; edit %s and restart the service instead", ConfigFile)
		}
	}
	if err := i.preflight(ctx, o, receipt == nil, reinstall); err != nil {
		return Result{}, err
	}
	// A resumed or repeated installation keeps its server.yaml; refuse
	// options that disagree with it rather than mixing the two.
	if live := i.liveOptions(o); live.Domain != o.Domain || live.APIPort != o.APIPort || live.GatewayPort != o.GatewayPort || live.ControlPort != o.ControlPort {
		return Result{}, fmt.Errorf("%s uses domain %s and ports %d/%d/%d; pass the same --domain and ports, or edit that file first",
			ConfigFile, live.Domain, live.APIPort, live.GatewayPort, live.ControlPort)
	}
	if receipt == nil {
		receipt = &Receipt{InstalledAt: time.Now().UTC()}
	}
	receipt.Version, receipt.Options, receipt.Status = o.Version, o, "installing"
	if err := i.writeReceipt(receipt); err != nil {
		return Result{}, err
	}

	i.step("Creating the %s service account", ServiceUser)
	if _, err := i.sys.Run(ctx, "getent", "passwd", ServiceUser); err != nil {
		if _, err := i.run(ctx, "useradd", "--system", "--user-group", "--home-dir", StateDir, "--no-create-home", "--shell", "/usr/sbin/nologin", ServiceUser); err != nil {
			return Result{}, err
		}
	}
	if _, err := i.run(ctx, "usermod", "--append", "--groups", "docker", ServiceUser); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(i.path(StateDir), 0o700); err != nil {
		return Result{}, err
	}
	if _, err := i.run(ctx, "chown", ServiceUser+":"+ServiceUser, i.path(StateDir)); err != nil {
		return Result{}, err
	}

	i.step("Installing Deployer %s", o.Version)
	if err := i.installRelease(o.Bundle, o.Version); err != nil {
		return Result{}, err
	}
	if err := i.activateRelease(o.Version); err != nil {
		return Result{}, err
	}
	if _, err := os.Stat(i.path(ConfigFile)); os.IsNotExist(err) {
		if err := writeAtomic(i.path(ConfigFile), serverConfig(o), 0o644); err != nil {
			return Result{}, err
		}
	} else {
		i.step("Keeping the existing %s", ConfigFile)
	}

	i.step("Starting %s", Service)
	if err := i.writeUnit(ctx); err != nil {
		return Result{}, err
	}
	if _, err := i.run(ctx, "systemctl", "enable", "--now", Service); err != nil {
		return Result{}, err
	}
	if err := i.waitService(ctx, o); err != nil {
		return Result{}, err
	}
	if o.TLSCert != "" {
		i.step("Configuring nginx")
		if err := i.configureNginx(ctx, o); err != nil {
			return Result{}, err
		}
	}
	receipt.Status = "installed"
	if err := i.writeReceipt(receipt); err != nil {
		return Result{}, err
	}
	return o.result("installed"), nil
}

// preflight checks the host. A fresh install refuses to adopt files from an
// installation it did not create; a reinstall reuses kept configuration.
func (i *Installer) preflight(ctx context.Context, o Options, fresh, reinstall bool) error {
	if i.sys.OS() != "linux" || !i.sys.IsRoot() {
		return fmt.Errorf("deployer server install runs as root on a Linux server; add --dry-run to see the plan")
	}
	for _, tool := range []string{"systemctl", "docker", "git", "getent", "useradd", "usermod", "chown"} {
		if err := i.sys.LookPath(tool); err != nil {
			return fmt.Errorf("%s is required but not installed", tool)
		}
	}
	out, err := i.run(ctx, "docker", "version", "--format", "{{.Server.Os}} {{.Server.Version}}")
	if err != nil {
		return fmt.Errorf("Docker is not running or not reachable: %w", err)
	}
	fields := strings.Fields(out)
	major := 0
	if len(fields) == 2 {
		major, _ = strconv.Atoi(strings.SplitN(fields[1], ".", 2)[0])
	}
	if len(fields) != 2 || fields[0] != "linux" || major < 28 {
		return fmt.Errorf("Docker Engine 28 or newer is required (found %q)", strings.TrimSpace(out))
	}
	if _, err := i.run(ctx, "docker", "buildx", "version"); err != nil {
		return fmt.Errorf("Docker Buildx is required (install the docker-buildx-plugin package): %w", err)
	}
	if _, err := i.run(ctx, "getent", "group", "docker"); err != nil {
		return fmt.Errorf("the docker group is missing; Deployer uses it to reach the Docker socket")
	}
	if o.TLSCert != "" {
		if err := i.sys.LookPath("nginx"); err != nil {
			return fmt.Errorf("nginx is required with --tls-cert")
		}
		if err := checkCertificate(i.path(o.TLSCert), i.path(o.TLSKey), o.Domain); err != nil {
			return err
		}
	}
	for _, bin := range Binaries {
		info, err := os.Stat(filepath.Join(o.Bundle, bin))
		if err != nil || info.Mode()&0o111 == 0 {
			return fmt.Errorf("the release bundle %s has no executable %s", o.Bundle, bin)
		}
	}
	if fresh {
		for _, p := range []string{ConfigFile, StateDir + "/state.db", UnitFile} {
			if _, err := os.Stat(i.path(p)); err == nil {
				return fmt.Errorf("%s exists but was not installed by this installer; remove the previous installation first (see docs/installation.md)", p)
			}
		}
	}
	if fresh || reinstall {
		if err := i.sys.PortsFree(o.APIPort, o.GatewayPort, o.ControlPort); err != nil {
			return err
		}
	}
	return nil
}

func checkCertificate(certFile, keyFile, domain string) error {
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("cannot use the certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	if err := cert.VerifyHostname("deployer-check." + domain); err != nil {
		return fmt.Errorf("the certificate does not cover *.%s", domain)
	}
	if now := time.Now(); now.After(cert.NotAfter) || now.Before(cert.NotBefore) {
		return fmt.Errorf("the certificate is not valid now (valid %s to %s)", cert.NotBefore.Format(time.DateOnly), cert.NotAfter.Format(time.DateOnly))
	}
	return nil
}

func (i *Installer) installRelease(bundle, version string) error {
	dir := i.path(filepath.Join(ReleasesDir, version))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, bin := range Binaries {
		if err := copyFile(filepath.Join(bundle, bin), filepath.Join(dir, bin)); err != nil {
			return err
		}
	}
	return nil
}

// activateRelease points /opt/deployer/current at a release and links the
// binaries into /usr/local/bin.
func (i *Installer) activateRelease(version string) error {
	if err := replaceSymlink(filepath.Join(ReleasesDir, version), i.path(CurrentLink)); err != nil {
		return err
	}
	for _, bin := range Binaries {
		if err := replaceSymlink(filepath.Join(CurrentLink, bin), i.path(filepath.Join(BinDir, bin))); err != nil {
			return err
		}
	}
	return nil
}

func (i *Installer) writeUnit(ctx context.Context) error {
	if _, err := writeIfChanged(i.path(UnitFile), []byte(unitFile), 0o644); err != nil {
		return err
	}
	_, err := i.run(ctx, "systemctl", "daemon-reload")
	return err
}

func (i *Installer) waitService(ctx context.Context, o Options) error {
	i.step("Waiting for Deployer to start (a new release builds its gateway image first)")
	ctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	if err := i.ready(ctx, fmt.Sprintf("http://127.0.0.1:%d", o.APIPort)); err != nil {
		return fmt.Errorf("Deployer did not start: %w; see journalctl -u %s", err, Service)
	}
	return nil
}

// liveOptions returns o with the domain and ports from server.yaml, which
// administrators may have edited since installation.
func (i *Installer) liveOptions(o Options) Options {
	cfg, err := config.LoadServer(i.path(ConfigFile))
	if err != nil {
		return o
	}
	port := func(addr string) int {
		_, p, _ := net.SplitHostPort(addr)
		n, _ := strconv.Atoi(p)
		return n
	}
	o.Domain = cfg.Domain
	o.APIPort = port(cfg.API.Listen)
	o.GatewayPort = cfg.GatewayPort()
	o.ControlPort = port(cfg.Gateway.ControlListen)
	return o
}

func (i *Installer) configureNginx(ctx context.Context, o Options) error {
	path := i.path(NginxFile)
	previous, readErr := os.ReadFile(path)
	changed, err := writeIfChanged(path, []byte(nginxSite(o)), 0o644)
	if err != nil || !changed {
		return err
	}
	if _, err := i.run(ctx, "nginx", "-t"); err != nil {
		if readErr == nil {
			writeAtomic(path, previous, 0o644)
		} else {
			os.Remove(path)
		}
		return fmt.Errorf("nginx rejected the configuration, so it was not applied: %w", err)
	}
	_, err = i.run(ctx, "systemctl", "reload", "nginx")
	return err
}
