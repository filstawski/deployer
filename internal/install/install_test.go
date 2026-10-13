package install

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The installer sets up a Linux server. The CLI, which includes it, also runs
// on Windows, where the installer only refuses to run.
func TestMain(m *testing.M) {
	if runtime.GOOS == "windows" {
		fmt.Println("skipping: the server installs on Linux")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fakeSystem struct {
	mu       sync.Mutex
	commands []string
	users    map[string]bool
	versions map[string]string // deployerd path -> version
	fail     map[string]error  // command prefix -> error
	notRoot  bool
	crashing bool // systemd keeps restarting the service
	restarts int
}

func (f *fakeSystem) Run(_ context.Context, name string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.commands = append(f.commands, cmd)
	for prefix, err := range f.fail {
		if strings.HasPrefix(cmd, prefix) {
			return "simulated failure", err
		}
	}
	switch {
	case name == "docker" && args[0] == "version":
		return "linux 29.1.0\n", nil
	case name == "getent" && args[0] == "passwd" && !f.users[args[1]]:
		return "", errors.New("exit status 2")
	case name == "useradd":
		f.users[args[len(args)-1]] = true
	case strings.HasSuffix(name, "deployerd") && args[0] == "version":
		return f.versions[name] + "\n", nil
	case name == "systemctl" && args[0] == "show" && f.crashing:
		f.restarts++
		return fmt.Sprintf("%d\n", f.restarts), nil
	case name == "journalctl":
		return "deployerd: start gateway container: permission denied\n", nil
	}
	return "", nil
}

func (f *fakeSystem) LookPath(string) error  { return nil }
func (f *fakeSystem) IsRoot() bool           { return !f.notRoot }
func (f *fakeSystem) OS() string             { return "linux" }
func (f *fakeSystem) PortsFree(...int) error { return nil }
func (f *fakeSystem) ran(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.commands {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

type fixture struct {
	t    *testing.T
	inst *Installer
	sys  *fakeSystem
	root string
	// failVersion makes the service fail to start on that version.
	failVersion string
	// readyURL is the last URL checked for readiness.
	readyURL string
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{t: t, root: t.TempDir(), sys: &fakeSystem{users: map[string]bool{}, versions: map[string]string{}, fail: map[string]error{}}}
	f.inst = &Installer{sys: f.sys, root: f.root, log: io.Discard, watchInterval: 5 * time.Millisecond, ready: func(ctx context.Context, url string) error {
		f.readyURL = url
		if f.sys.crashing {
			<-ctx.Done()
			return ctx.Err()
		}
		if f.current() == f.failVersion {
			return errors.New("connection refused")
		}
		return nil
	}}
	return f
}

func (f *fixture) bundle(version string) string {
	dir := f.t.TempDir()
	for _, bin := range Binaries {
		os.WriteFile(filepath.Join(dir, bin), []byte("#!/bin/sh\necho "+version+"\n"), 0o755)
	}
	f.sys.versions[filepath.Join(dir, "deployerd")] = version
	return dir
}

func (f *fixture) current() string {
	target, _ := os.Readlink(filepath.Join(f.root, CurrentLink))
	return filepath.Base(target)
}

func (f *fixture) file(path string) string {
	b, err := os.ReadFile(filepath.Join(f.root, path))
	if err != nil {
		f.t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func options(bundle string) Options {
	return Options{Version: "v1.0.0", Bundle: bundle, Domain: "previews.example.com", APIPort: 7420, GatewayPort: 8080, ControlPort: 7421}
}

// certificate writes a self-signed wildcard certificate under root.
func (f *fixture) certificate(domain string) (string, string) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "*." + domain}, DNSNames: []string{"*." + domain},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	keyDER, _ := x509.MarshalECPrivateKey(key)
	dir := filepath.Join(f.root, "etc/letsencrypt/live", domain)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "fullchain.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	os.WriteFile(filepath.Join(dir, "privkey.pem"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	return "/etc/letsencrypt/live/" + domain + "/fullchain.pem", "/etc/letsencrypt/live/" + domain + "/privkey.pem"
}

func TestValidation(t *testing.T) {
	f := newFixture(t)
	bad := []Options{
		{Version: "dev", Domain: "example.com", APIPort: 1, GatewayPort: 2, ControlPort: 3},
		{Version: "v1.0.0", Domain: "Example.com", APIPort: 1, GatewayPort: 2, ControlPort: 3},
		{Version: "v1.0.0", Domain: "example.com", APIPort: 1, GatewayPort: 1, ControlPort: 3},
		{Version: "v1.0.0", Domain: "example.com", APIPort: 1, GatewayPort: 2, ControlPort: 3, TLSCert: "/a"},
		{Version: "v1.0.0", Domain: "example.com", APIPort: 1, GatewayPort: 2, ControlPort: 3, TLSCert: "/a b", TLSKey: "/k"},
	}
	for _, o := range bad {
		if _, err := f.inst.Install(context.Background(), o); err == nil {
			t.Errorf("accepted %+v", o)
		}
	}
	o := options("")
	o.DryRun = true
	res, err := f.inst.Install(context.Background(), o)
	if err != nil || res.Status != "planned" || len(f.sys.commands) != 0 {
		t.Fatalf("dry run: %+v %v %v", res, err, f.sys.commands)
	}
}

func TestInstallUpgradeUninstall(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	o := options(f.bundle("v1.0.0"))
	o.TLSCert, o.TLSKey = f.certificate("previews.example.com")
	o.APIHost = "api.previews.example.com"

	res, err := f.inst.Install(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "installed" || res.API != "https://api.previews.example.com" || f.current() != "v1.0.0" {
		t.Fatalf("install: %+v current=%s", res, f.current())
	}
	for _, want := range []string{"useradd --system", "usermod --append --groups docker deployer", "systemctl restart deployer.service", "nginx -t", "systemctl reload nginx"} {
		if !f.sys.ran(want) {
			t.Errorf("did not run %q", want)
		}
	}
	if cfg := f.file(ConfigFile); !strings.Contains(cfg, "domain: previews.example.com") || !strings.Contains(cfg, "public_url: https://api.previews.example.com") {
		t.Fatalf("server.yaml:\n%s", cfg)
	}
	if site := f.file(NginxFile); !strings.Contains(site, "server_name *.previews.example.com;") || !strings.Contains(site, "server_name api.previews.example.com;") || !strings.Contains(site, "proxy_pass http://127.0.0.1:8080;") {
		t.Fatalf("nginx site:\n%s", site)
	}
	if link, _ := os.Readlink(filepath.Join(f.root, BinDir, "deployer")); link != "/opt/deployer/current/deployer" {
		t.Fatalf("CLI link: %q", link)
	}

	// Running it again resumes without changing anything.
	if _, err := f.inst.Install(ctx, o); err != nil {
		t.Fatalf("rerun: %v", err)
	}
	other := o
	other.Version = "v1.1.0"
	if _, err := f.inst.Install(ctx, other); err == nil || !strings.Contains(err.Error(), "upgrade") {
		t.Fatalf("install of another version: %v", err)
	}

	// A failing upgrade restores the previous release and database.
	os.WriteFile(filepath.Join(f.root, StateDir, "state.db"), []byte("v1 data"), 0o600)
	f.failVersion = "v2.0.0"
	if _, err := f.inst.Upgrade(ctx, UpgradeOptions{Version: "v2.0.0", Bundle: f.bundle("v2.0.0")}); err == nil || !strings.Contains(err.Error(), "v1.0.0 was restored") {
		t.Fatalf("failed upgrade: %v", err)
	}
	if f.current() != "v1.0.0" || f.file(StateDir+"/state.db") != "v1 data" {
		t.Fatalf("rollback: current=%s", f.current())
	}

	f.failVersion = ""
	res, err = f.inst.Upgrade(ctx, UpgradeOptions{Version: "v2.0.0", Bundle: f.bundle("v2.0.0")})
	if err != nil || res.Status != "upgraded" || f.current() != "v2.0.0" {
		t.Fatalf("upgrade: %+v %v", res, err)
	}
	if r, _ := f.inst.readReceipt(); r.Version != "v2.0.0" || r.UpgradedAt == nil {
		t.Fatalf("receipt after upgrade: %+v", r)
	}
	backups, _ := os.ReadDir(filepath.Join(f.root, StateDir, "backups"))
	if len(backups) != 2 {
		t.Fatalf("backups: %d", len(backups))
	}
	if _, err := f.inst.Upgrade(ctx, UpgradeOptions{Version: "v3.0.0", Bundle: f.bundle("v2.9.9")}); err == nil {
		t.Fatal("upgraded with a bundle of the wrong version")
	}

	res, err = f.inst.Uninstall(ctx, UninstallOptions{})
	if err != nil || res.Status != "uninstalled" {
		t.Fatalf("uninstall: %v", err)
	}
	if !f.sys.ran(filepath.Join(f.root, CurrentLink, "deployerd") + " cleanup") {
		t.Fatal("uninstall did not remove Docker resources")
	}
	for _, gone := range []string{OptDir, UnitFile, NginxFile, BinDir + "/deployer"} {
		if _, err := os.Lstat(filepath.Join(f.root, gone)); !os.IsNotExist(err) {
			t.Errorf("%s still exists", gone)
		}
	}
	if f.file(StateDir+"/state.db") != "v1 data" {
		t.Fatal("uninstall without --purge deleted data")
	}

	// A reinstall reuses the kept data and configuration.
	o.Version, o.Bundle = "v2.0.0", f.bundle("v2.0.0")
	if _, err := f.inst.Install(ctx, o); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if _, err := f.inst.Uninstall(ctx, UninstallOptions{Purge: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.root, StateDir)); !os.IsNotExist(err) {
		t.Fatal("purge kept the state directory")
	}
}

func TestInstallRefusesForeignInstallation(t *testing.T) {
	f := newFixture(t)
	os.MkdirAll(filepath.Join(f.root, ConfigDir), 0o755)
	os.WriteFile(filepath.Join(f.root, ConfigFile), []byte("old"), 0o644)
	if _, err := f.inst.Install(context.Background(), options(f.bundle("v1.0.0"))); err == nil || !strings.Contains(err.Error(), "not installed by this installer") {
		t.Fatalf("adopted a foreign installation: %v", err)
	}
}

func TestNginxFailureLeavesNothingBehind(t *testing.T) {
	f := newFixture(t)
	o := options(f.bundle("v1.0.0"))
	o.TLSCert, o.TLSKey = f.certificate("previews.example.com")
	f.sys.fail["nginx -t"] = errors.New("exit status 1")
	if _, err := f.inst.Install(context.Background(), o); err == nil || !strings.Contains(err.Error(), "nginx rejected") {
		t.Fatalf("nginx failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, NginxFile)); !os.IsNotExist(err) {
		t.Fatal("rejected nginx site left in place")
	}
	if f.sys.ran("systemctl reload nginx") {
		t.Fatal("reloaded nginx with a rejected configuration")
	}
}

func TestCertificateMustCoverDomain(t *testing.T) {
	f := newFixture(t)
	o := options(f.bundle("v1.0.0"))
	o.TLSCert, o.TLSKey = f.certificate("other.example.com")
	if _, err := f.inst.Install(context.Background(), o); err == nil || !strings.Contains(err.Error(), "does not cover") {
		t.Fatalf("mismatched certificate: %v", err)
	}
}

func TestPilotReceiptIsRecognized(t *testing.T) {
	f := newFixture(t)
	os.MkdirAll(filepath.Join(f.root, ConfigDir), 0o755)
	os.WriteFile(filepath.Join(f.root, ReceiptFile), []byte(`{"options":{"Domain":"x.example.com","Version":"v0.1.0-dev.3"},"ready":true}`), 0o600)
	_, err := f.inst.Install(context.Background(), options(f.bundle("v1.0.0")))
	if err == nil || !strings.Contains(err.Error(), "older Deployer pilot") {
		t.Fatalf("pilot receipt: %v", err)
	}
}

func TestSettingsComeFromServerConfig(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	o := options(f.bundle("v1.0.0"))
	if _, err := f.inst.Install(ctx, o); err != nil {
		t.Fatal(err)
	}
	// An administrator moves the API port; upgrades must follow it.
	cfg := strings.Replace(f.file(ConfigFile), "listen: 127.0.0.1:7420", "listen: 127.0.0.1:7500", 1)
	os.WriteFile(filepath.Join(f.root, ConfigFile), []byte(cfg), 0o644)
	if _, err := f.inst.Upgrade(ctx, UpgradeOptions{Version: "v1.1.0", Bundle: f.bundle("v1.1.0")}); err != nil {
		t.Fatal(err)
	}
	if f.readyURL != "http://127.0.0.1:7500" {
		t.Fatalf("readiness checked %s", f.readyURL)
	}

	// A reinstall must not silently mix kept settings with new ones.
	if _, err := f.inst.Uninstall(ctx, UninstallOptions{}); err != nil {
		t.Fatal(err)
	}
	o.Version, o.Bundle, o.Domain = "v1.1.0", f.bundle("v1.1.0"), "other.example.com"
	if _, err := f.inst.Install(ctx, o); err == nil || !strings.Contains(err.Error(), "uses domain previews.example.com") {
		t.Fatalf("reinstall with another domain: %v", err)
	}
	// The refused attempt changed nothing, so the right options still work.
	o.Domain, o.APIPort = "previews.example.com", 7500
	if _, err := f.inst.Install(ctx, o); err != nil {
		t.Fatalf("reinstall with the kept settings: %v", err)
	}
}

func TestCrashLoopFailsFastAndCanBeRetried(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.sys.crashing = true
	start := time.Now()
	_, err := f.inst.Install(ctx, options(f.bundle("v1.0.0")))
	if err == nil || !strings.Contains(err.Error(), "keeps crashing") || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("crash loop: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("waited for the full timeout instead of noticing the crash loop")
	}

	// A fixed release completes the failed installation, and restarts the
	// crash-looping service onto it.
	f.sys.crashing = false
	o := options(f.bundle("v1.0.1"))
	o.Version = "v1.0.1"
	if res, err := f.inst.Install(ctx, o); err != nil || res.Status != "installed" || f.current() != "v1.0.1" {
		t.Fatalf("retry with a fixed release: %+v %v", res, err)
	}
	if !f.sys.ran("systemctl restart deployer.service") {
		t.Fatal("service not restarted onto the new release")
	}
	// Once installed, changing versions requires an upgrade.
	o2 := options(f.bundle("v1.0.2"))
	o2.Version = "v1.0.2"
	if _, err := f.inst.Install(ctx, o2); err == nil || !strings.Contains(err.Error(), "upgrade") {
		t.Fatalf("install over a completed installation: %v", err)
	}
}
