package install

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"deployer/internal/command"
	"deployer/internal/config"
	"deployer/internal/credentials"
	"gopkg.in/yaml.v3"
)

type Result struct {
	Plan   Plan   `json:"plan"`
	Status string `json:"status"`
}
type receipt struct {
	Options    Options `json:"options"`
	ConfigHash string  `json:"config_hash"`
	Image      string  `json:"image"`
	Ready      bool    `json:"ready"`
}
type Installer struct {
	root     string
	platform string
	uid      int
	run      func(context.Context, string, ...string) (string, error)
	look     func(string) (string, error)
	ports    func([]int) error
	ready    func(context.Context, string, string) error
	log      io.Writer
}

func New(log io.Writer) *Installer {
	return &Installer{platform: runtime.GOOS, uid: os.Geteuid(), log: log, look: exec.LookPath, ports: checkPorts, ready: waitReady, run: func(ctx context.Context, name string, args ...string) (string, error) {
		return command.Run(ctx, "", nil, nil, name, args...)
	}}
}
func (i *Installer) path(p string) string {
	if i.root != "" {
		return filepath.Join(i.root, p)
	}
	return p
}
func (i *Installer) runStep(ctx context.Context, name string, args ...string) (string, error) {
	out, err := i.run(ctx, name, args...)
	if err != nil {
		return out, fmt.Errorf("%s failed: %w: %s", name, err, out)
	}
	return out, nil
}
func (i *Installer) Apply(ctx context.Context, o Options) (Result, error) {
	p, err := o.Plan()
	result := Result{Plan: p, Status: "planned"}
	if err != nil {
		return result, err
	}
	if o.DryRun {
		return result, nil
	}
	if i.platform != "linux" || i.uid != 0 {
		return result, fmt.Errorf("server setup requires root on Linux with systemd; use --dry-run to inspect the plan")
	}
	if o.Bundle == "" {
		return result, fmt.Errorf("--bundle containing all three release binaries is required")
	}
	bundle, err := filepath.Abs(o.Bundle)
	if err != nil {
		return result, err
	}
	for _, name := range []string{"deployer", "deployerd", "deployer-gateway"} {
		info, e := os.Lstat(filepath.Join(bundle, name))
		if e != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
			return result, fmt.Errorf("bundle is missing executable regular file %s", name)
		}
	}
	for _, name := range []string{"docker", "git", "systemctl", "getent", "useradd", "runuser", "chown"} {
		if _, err = i.look(name); err != nil {
			return result, fmt.Errorf("missing dependency %s; install it using your distribution's package manager", name)
		}
	}
	if _, err = i.runStep(ctx, "systemctl", "show", "--property=Version", "--value"); err != nil {
		return result, fmt.Errorf("systemd must be running: %w", err)
	}
	// Force the local system Docker socket for every installation check; do not
	// inherit a root user's selected remote context or DOCKER_HOST.
	out, err := i.runStep(ctx, "docker", "--host", "unix:///var/run/docker.sock", "version", "--format", "{{.Server.Os}} {{.Server.Arch}} {{.Server.Version}}")
	if err != nil {
		return result, err
	}
	fields := strings.Fields(out)
	major := 0
	if len(fields) == 3 {
		major, _ = strconv.Atoi(strings.Split(fields[2], ".")[0])
	}
	if len(fields) != 3 || fields[0] != "linux" || major < 28 {
		return result, fmt.Errorf("a local Linux Docker Engine 28 or newer is required")
	}
	if _, err = i.runStep(ctx, "docker", "--host", "unix:///var/run/docker.sock", "buildx", "version"); err != nil {
		return result, fmt.Errorf("Docker Buildx is required: %w", err)
	}
	if _, err = i.runStep(ctx, "getent", "group", "docker"); err != nil {
		return result, fmt.Errorf("the local Docker socket must be accessible through the docker group")
	}
	recordPath := i.path("/etc/deployer/install.json")
	var saved receipt
	existing := false
	var observedReceipt []byte
	if b, e := os.ReadFile(recordPath); e == nil {
		existing = true
		observedReceipt = append([]byte(nil), b...)
		if e = json.Unmarshal(b, &saved); e != nil {
			return result, fmt.Errorf("invalid installation receipt: %w", e)
		}
		old := saved.Options
		if old.Version != o.Version {
			return result, fmt.Errorf("server upgrade from %s to %s requires the future upgrade command; this installer only resumes the same release", old.Version, o.Version)
		}
		if old.Domain != o.Domain || old.APIPort != o.APIPort || old.GatewayPort != o.GatewayPort || old.ControlPort != o.ControlPort {
			return result, fmt.Errorf("existing domain/ports differ; setup will not overwrite them")
		}
		if o.Cert == "" && old.Cert != "" {
			o.Cert = old.Cert
			o.Key = old.Key
			p, _ = o.Plan()
			result.Plan = p
		}
		if old.Cert != "" && (old.Cert != o.Cert || old.Key != o.Key) {
			return result, fmt.Errorf("existing certificate paths differ; certificate migration is not automatic")
		}
	} else if !os.IsNotExist(e) {
		return result, e
	}
	if o.Cert != "" {
		if _, err = i.look("nginx"); err != nil {
			return result, fmt.Errorf("nginx is required when TLS certificate paths are supplied")
		}
		if err = validateCertificate(o); err != nil {
			return result, err
		}
		if _, err = i.runStep(ctx, "nginx", "-t"); err != nil {
			return result, err
		}
		if _, err = os.Stat(i.path("/etc/nginx/conf.d")); err != nil {
			return result, fmt.Errorf("this setup requires nginx's /etc/nginx/conf.d directory")
		}
	}
	if !existing {
		for _, path := range []string{"/etc/deployer/server.yaml", "/var/lib/deployer", "/opt/deployer/current", "/usr/local/bin/deployer", "/usr/local/bin/deployerd", "/etc/systemd/system/deployer.service"} {
			if _, e := os.Lstat(i.path(path)); e == nil {
				return result, fmt.Errorf("unmanaged installation path already exists: %s", path)
			} else if !os.IsNotExist(e) {
				return result, e
			}
		}
		if err = i.ports([]int{o.APIPort, o.GatewayPort, o.ControlPort}); err != nil {
			return result, err
		}
		if _, err = i.run(ctx, "getent", "passwd", "deployer"); err == nil {
			return result, fmt.Errorf("a deployer account already exists without an installation receipt; refusing to adopt it")
		}
	}
	if existing && saved.ConfigHash != "" {
		b, e := os.ReadFile(i.path(p.ConfigPath))
		if e != nil {
			return result, e
		}
		if hash(b) != saved.ConfigHash && hash(b) != hash(serverConfig(o, saved.Image)) {
			return result, fmt.Errorf("server.yaml was edited; setup preserves it and refuses automatic changes")
		}
	}
	fmt.Fprintln(i.log, "Preflight passed. Installing Deployer", o.Version)
	if err = os.MkdirAll(i.path("/etc/deployer"), 0755); err != nil {
		return result, err
	}
	lock, err := os.OpenFile(i.path("/etc/deployer/install.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return result, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return result, fmt.Errorf("another installation is running")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if existing {
		current, e := os.ReadFile(recordPath)
		if e != nil {
			return result, e
		}
		if hash(current) != hash(observedReceipt) {
			return result, fmt.Errorf("installation changed during preflight; rerun setup")
		}
	}
	// Recheck after locking; don't race another first installation's preflight.
	if !existing {
		if _, err = os.Stat(recordPath); err == nil {
			return result, fmt.Errorf("another setup completed; rerun to inspect it")
		}
		saved.Options = o
		if err = writeJSON(recordPath, saved); err != nil {
			return result, err
		}
	}
	if _, err = i.run(ctx, "getent", "passwd", "deployer"); err != nil {
		if _, err = i.runStep(ctx, "useradd", "--system", "--user-group", "--home-dir", "/var/lib/deployer", "--shell", "/usr/sbin/nologin", "--groups", "docker", "deployer"); err != nil {
			return result, err
		}
	}
	if err = os.MkdirAll(i.path(p.StateDir), 0700); err != nil {
		return result, err
	}
	if _, err = i.runStep(ctx, "chown", "deployer:deployer", i.path(p.StateDir)); err != nil {
		return result, err
	}
	// This also catches Docker installations whose socket/group policy would let
	// root's preflight pass but prevent the service account from running Docker.
	if _, err = i.runStep(ctx, "runuser", "-u", "deployer", "--", "docker", "--host", "unix:///var/run/docker.sock", "info", "--format", "{{.OSType}}"); err != nil {
		return result, err
	}
	if _, err = i.runStep(ctx, "runuser", "-u", "deployer", "--", "docker", "--host", "unix:///var/run/docker.sock", "buildx", "version"); err != nil {
		return result, fmt.Errorf("Buildx must be installed for the service account: %w", err)
	}
	if err = os.MkdirAll(i.path(p.ReleaseDir), 0755); err != nil {
		return result, err
	}
	for _, name := range []string{"deployer", "deployerd", "deployer-gateway"} {
		b, e := os.ReadFile(filepath.Join(bundle, name))
		if e != nil {
			return result, e
		}
		if e = writeOnce(i.path(filepath.Join(p.ReleaseDir, name)), b, 0755); e != nil {
			return result, e
		}
	}
	image := saved.Image
	if image == "" {
		dir := i.path(filepath.Join(p.ReleaseDir, "gateway"))
		if err = os.MkdirAll(dir, 0755); err != nil {
			return result, err
		}
		b, e := os.ReadFile(filepath.Join(bundle, "deployer-gateway"))
		if e != nil {
			return result, e
		}
		if e = writeOnce(filepath.Join(dir, "gateway"), b, 0755); e != nil {
			return result, e
		}
		if e = writeOnce(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\nCOPY gateway /gateway\nUSER 65532:65532\nENTRYPOINT [\"/gateway\"]\n"), 0644); e != nil {
			return result, e
		}
		tag := "deployer-gateway:" + o.Version
		if _, err = i.runStep(ctx, "docker", "--host", "unix:///var/run/docker.sock", "build", "--network", "none", "--tag", tag, dir); err != nil {
			return result, err
		}
		image, err = i.runStep(ctx, "docker", "--host", "unix:///var/run/docker.sock", "image", "inspect", "--format", "{{.Id}}", tag)
		if err != nil {
			return result, err
		}
		image = strings.TrimSpace(image)
		if !regexpImage(image) {
			return result, fmt.Errorf("Docker returned an invalid gateway image identity")
		}
		saved.Image = image
		if err = writeJSON(recordPath, saved); err != nil {
			return result, err
		}
	}
	b := serverConfig(o, image)
	if existing && saved.Ready && saved.ConfigHash == hash(b) {
		fmt.Fprintln(i.log, "Existing release and configuration match; preserving state and credentials.")
	}
	if old, e := os.ReadFile(i.path(p.ConfigPath)); e == nil && saved.ConfigHash == "" && hash(old) != hash(b) {
		return result, fmt.Errorf("existing server.yaml is not recorded by this installer")
	}
	if saved.ConfigHash != "" && saved.ConfigHash != hash(b) {
		if _, err = i.runStep(ctx, "systemctl", "stop", "deployer.service"); err != nil {
			return result, err
		}
	}
	if err = atomicWrite(i.path(p.ConfigPath), b, 0644); err != nil {
		return result, err
	}
	saved.ConfigHash = hash(b)
	saved.Options = o
	if err = writeJSON(recordPath, saved); err != nil {
		return result, err
	}
	if err = managedLink(i.path("/opt/deployer/current"), p.ReleaseDir); err != nil {
		return result, err
	}
	for _, name := range []string{"deployer", "deployerd"} {
		if err = managedLink(i.path("/usr/local/bin/"+name), "/opt/deployer/current/"+name); err != nil {
			return result, err
		}
	}
	if err = writeOnce(i.path("/etc/systemd/system/deployer.service"), []byte(Unit), 0644); err != nil {
		return result, err
	}
	tokenPath := i.path(p.TokenFile)
	if _, err = os.Stat(tokenPath); os.IsNotExist(err) {
		if _, e := os.Stat(i.path(filepath.Join(p.StateDir, "state.db"))); e == nil {
			return result, fmt.Errorf("database already exists but bootstrap credential is missing; recover with deployerd token create")
		}
		if _, err = i.runStep(ctx, "runuser", "-u", "deployer", "--", i.path(filepath.Join(p.ReleaseDir, "deployerd")), "token", "bootstrap", "--config", i.path(p.ConfigPath), "--output", tokenPath); err != nil {
			return result, err
		}
	} else if err != nil {
		return result, err
	}
	if _, err = i.runStep(ctx, "systemctl", "daemon-reload"); err != nil {
		return result, err
	}
	if _, err = i.runStep(ctx, "systemctl", "enable", "--now", "deployer.service"); err != nil {
		return result, err
	}
	if err = i.ready(ctx, p.API, tokenPath); err != nil {
		return result, fmt.Errorf("service did not become ready; inspect journalctl -u deployer.service: %w", err)
	}
	if o.Cert != "" {
		if err = i.nginx(ctx, o); err != nil {
			return result, err
		}
	}
	saved.Ready = true
	if err = writeJSON(recordPath, saved); err != nil {
		return result, err
	}
	result.Status = "installed"
	return result, nil
}
func regexpImage(s string) bool {
	if !strings.HasPrefix(s, "sha256:") || len(s) != 71 {
		return false
	}
	_, err := hex.DecodeString(s[7:])
	return err == nil
}
func hash(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(b, '\n'), 0600)
}
func atomicWrite(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".deployer-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func writeOnce(path string, b []byte, mode os.FileMode) error {
	if st, e := os.Lstat(path); e == nil {
		if !st.Mode().IsRegular() {
			return fmt.Errorf("refusing non-regular file %s", path)
		}
		old, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if hash(old) != hash(b) {
			return fmt.Errorf("existing file differs; leaving it untouched: %s", path)
		}
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	return atomicWrite(path, b, mode)
}
func managedLink(path, target string) error {
	if actual, e := os.Readlink(path); e == nil {
		if actual == target {
			return nil
		}
		return fmt.Errorf("existing symlink differs: %s", path)
	}
	if _, e := os.Lstat(path); e == nil {
		return fmt.Errorf("refusing to replace existing path: %s", path)
	} else if !os.IsNotExist(e) {
		return e
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.Symlink(target, path)
}
func checkPorts(ports []int) error {
	var listeners []net.Listener
	defer func() {
		for _, l := range listeners {
			l.Close()
		}
	}()
	for _, p := range ports {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			return fmt.Errorf("port %d is unavailable: %w", p, err)
		}
		listeners = append(listeners, l)
	}
	return nil
}
func waitReady(ctx context.Context, base, tokenFile string) error {
	secret, err := credentials.Read(tokenFile)
	if err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for {
		req, _ := http.NewRequestWithContext(wait, "GET", base+"/v1/server", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		resp, e := client.Do(req)
		if e == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
			if resp.StatusCode == 401 {
				return fmt.Errorf("bootstrap credential has expired or was revoked; issue a new credential locally")
			}
		}
		select {
		case <-wait.Done():
			return wait.Err()
		case <-time.After(time.Second):
		}
	}
}
func validateCertificate(o Options) error {
	pair, err := tls.LoadX509KeyPair(o.Cert, o.Key)
	if err != nil {
		return fmt.Errorf("cannot load matching certificate/key: %w", err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	wildcard := false
	for _, name := range cert.DNSNames {
		if name == "*."+o.Domain {
			wildcard = true
		}
	}
	if !wildcard {
		return fmt.Errorf("certificate must include *.%s", o.Domain)
	}
	if err = cert.VerifyHostname("deployer-probe." + o.Domain); err != nil {
		return err
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return fmt.Errorf("certificate is not currently valid")
	}
	return nil
}
func (i *Installer) nginx(ctx context.Context, o Options) error {
	path := i.path("/etc/nginx/conf.d/deployer.conf")
	_, before := os.Lstat(path)
	if err := writeOnce(path, []byte(o.Nginx()), 0644); err != nil {
		return err
	}
	if _, err := i.runStep(ctx, "nginx", "-t"); err != nil {
		if os.IsNotExist(before) {
			_ = os.Remove(path)
		}
		return fmt.Errorf("nginx validation failed; existing running configuration was preserved: %w", err)
	}
	dump, e := i.run(ctx, "nginx", "-T")
	if e != nil || !strings.Contains(dump, "# configuration file "+path+":") {
		if os.IsNotExist(before) {
			_ = os.Remove(path)
		}
		return fmt.Errorf("nginx does not load %s; existing configuration was preserved", path)
	}
	_, err := i.runStep(ctx, "systemctl", "reload", "nginx")
	return err
}

func serverConfig(o Options, image string) []byte {
	c := config.Defaults()
	c.Listen = fmt.Sprintf("127.0.0.1:%d", o.APIPort)
	c.StateDir = "/var/lib/deployer"
	c.BaseDomain = o.Domain
	c.Runtime.Enabled = true
	c.Runtime.GatewayImage = image
	c.Runtime.GatewayPort = o.GatewayPort
	c.Runtime.ControlPort = o.ControlPort
	if o.Cert != "" {
		c.Preview.Scheme = "https"
		c.Preview.Port = 443
	}
	b, _ := yaml.Marshal(c)
	return b
}
