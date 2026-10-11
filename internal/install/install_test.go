package install

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"deployer/internal/config"
)

func options() Options {
	return Options{Domain: "demo.example", Version: "v0.1.0-test", APIPort: 7420, GatewayPort: 8080, ControlPort: 7421}
}
func fixture(t *testing.T) (*Installer, Options, *[]string) {
	t.Helper()
	o := options()
	o.Bundle = t.TempDir()
	for _, name := range []string{"deployer", "deployerd", "deployer-gateway"} {
		if err := os.WriteFile(filepath.Join(o.Bundle, name), []byte("release executable "+name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	calls := []string{}
	account := false
	i := &Installer{root: t.TempDir(), platform: "linux", uid: 0, log: io.Discard, look: func(s string) (string, error) { return "/usr/bin/" + s, nil }, ports: func([]int) error { return nil }, ready: func(context.Context, string, string) error { return nil }}
	i.run = func(ctx context.Context, name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		calls = append(calls, call)
		switch {
		case call == "getent passwd deployer":
			if !account {
				return "", errors.New("not found")
			}
			return "deployer:x:995:995::/var/lib/deployer:/usr/sbin/nologin", nil
		case name == "useradd":
			account = true
		case strings.Contains(call, "version --format"):
			return "linux amd64 29.0.0", nil
		case strings.Contains(call, "image inspect --format"):
			return "sha256:" + strings.Repeat("a", 64), nil
		case strings.Contains(call, "token bootstrap"):
			if err := os.WriteFile(i.path("/var/lib/deployer/operator.token"), []byte("dpl_"+strings.Repeat("b", 64)), 0600); err != nil {
				return "", err
			}
		}
		return "ok", nil
	}
	return i, o, &calls
}
func TestDryRunNeverTouchesHost(t *testing.T) {
	o := options()
	o.DryRun = true
	i := New(io.Discard)
	i.run = func(context.Context, string, ...string) (string, error) {
		t.Fatal("dry-run executed host command")
		return "", nil
	}
	r, err := i.Apply(context.Background(), o)
	if err != nil || r.Status != "planned" || r.Plan.Edge != "pending" {
		t.Fatalf("plan: %+v %v", r, err)
	}
	o.Cert = "/etc/certs/fullchain.pem"
	o.Key = "/etc/certs/key.pem"
	r, err = i.Apply(context.Background(), o)
	if err != nil || r.Plan.Edge != "nginx" {
		t.Fatalf("TLS plan: %+v %v", r, err)
	}
}

func TestRestrictiveBootstrapUmaskAndPermissionRepair(t *testing.T) {
	i, o, _ := fixture(t)
	previous := syscall.Umask(0077)
	defer syscall.Umask(previous)
	ctx := context.Background()
	if _, err := i.Apply(ctx, o); err != nil {
		t.Fatal(err)
	}
	public := []string{"/etc/deployer", "/opt/deployer", "/opt/deployer/releases", "/opt/deployer/releases/" + o.Version}
	check := func() {
		t.Helper()
		for _, path := range public {
			st, err := os.Stat(i.path(path))
			if err != nil || st.Mode().Perm() != 0755 {
				t.Fatalf("service cannot traverse %s: %v %v", path, st, err)
			}
		}
		for path, mode := range map[string]os.FileMode{"/var/lib/deployer": 0700, "/var/lib/deployer/operator.token": 0600, "/etc/deployer/install.json": 0600} {
			st, err := os.Stat(i.path(path))
			if err != nil || st.Mode().Perm() != mode {
				t.Fatalf("private permissions changed for %s: %v %v", path, st, err)
			}
		}
	}
	check()
	for _, path := range public {
		if err := os.Chmod(i.path(path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := i.Apply(ctx, o); err != nil {
		t.Fatal(err)
	}
	check()
}
func TestInstallAndRepeatPreserveCredentialsAndData(t *testing.T) {
	i, o, calls := fixture(t)
	ctx := context.Background()
	r, err := i.Apply(ctx, o)
	if err != nil || r.Status != "installed" {
		t.Fatalf("setup: %+v %v", r, err)
	}
	token, _ := os.ReadFile(i.path(r.Plan.TokenFile))
	data := i.path("/var/lib/deployer/customer-data")
	os.WriteFile(data, []byte("retain me"), 0600)
	before := len(*calls)
	if _, err = i.Apply(ctx, o); err != nil {
		t.Fatal("repeat", err)
	}
	again, _ := os.ReadFile(i.path(r.Plan.TokenFile))
	if string(token) != string(again) {
		t.Fatal("repeat replaced credential")
	}
	b, _ := os.ReadFile(data)
	if string(b) != "retain me" {
		t.Fatal("repeat lost state")
	}
	for _, call := range (*calls)[before:] {
		if strings.Contains(call, "token bootstrap") || strings.Contains(call, "useradd") {
			t.Fatal("repeat bootstrapped again", call)
		}
	}
	c, err := config.Load(i.path(r.Plan.ConfigPath))
	if err != nil || !c.Runtime.Enabled || !strings.HasPrefix(c.Runtime.GatewayImage, "sha256:") {
		t.Fatalf("server config: %+v %v", c, err)
	}
	unit, _ := os.ReadFile(i.path("/etc/systemd/system/deployer.service"))
	if !strings.Contains(string(unit), "User=deployer") || !strings.Contains(string(unit), "DOCKER_HOST=unix:///var/run/docker.sock") {
		t.Fatal("service configuration missing")
	}
	o.Version = "v0.2.0"
	if _, err = i.Apply(ctx, o); err == nil || !strings.Contains(err.Error(), "upgrade") {
		t.Fatal("unverified upgrade accepted", err)
	}
}
func TestRejectsEditedConfigurationAndUnmanagedPaths(t *testing.T) {
	i, o, _ := fixture(t)
	ctx := context.Background()
	r, err := i.Apply(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	path := i.path(r.Plan.ConfigPath)
	os.WriteFile(path, []byte("operator edited this"), 0644)
	if _, err = i.Apply(ctx, o); err == nil || !strings.Contains(err.Error(), "edited") {
		t.Fatal("changed config overwritten", err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "operator edited this" {
		t.Fatal("lost edited config")
	}
	other, opts, _ := fixture(t)
	path = other.path("/usr/local/bin/deployer")
	os.MkdirAll(filepath.Dir(path), 0755)
	os.WriteFile(path, []byte("unrelated"), 0755)
	if _, err = other.Apply(ctx, opts); err == nil {
		t.Fatal("unrelated executable adopted")
	}
	if _, err = os.Stat(other.path("/etc/deployer/install.json")); !os.IsNotExist(err) {
		t.Fatal("preflight mutated installation")
	}
}
func TestInterruptedInstallResumes(t *testing.T) {
	i, o, calls := fixture(t)
	original := i.run
	failed := false
	i.run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "systemctl" && len(args) > 0 && args[0] == "enable" && !failed {
			failed = true
			return "", errors.New("simulated systemd interruption")
		}
		return original(ctx, name, args...)
	}
	if _, err := i.Apply(context.Background(), o); err == nil {
		t.Fatal("expected interruption")
	}
	if _, err := i.Apply(context.Background(), o); err != nil {
		t.Fatal("cannot resume", err)
	}
	count := 0
	for _, call := range *calls {
		if strings.Contains(call, "token bootstrap") {
			count++
		}
	}
	if count != 1 {
		t.Fatal("resumed install reissued credential")
	}
	var saved receipt
	b, _ := os.ReadFile(i.path("/etc/deployer/install.json"))
	json.Unmarshal(b, &saved)
	if !saved.Ready {
		t.Fatal("receipt not completed")
	}
}
func TestPlanRejectsNginxInjectionAndBadPorts(t *testing.T) {
	for _, change := range []func(*Options){func(o *Options) { o.Domain = "demo.example;bad" }, func(o *Options) { o.Cert = "/tmp/cert;bad"; o.Key = "/tmp/key" }, func(o *Options) { o.ControlPort = o.APIPort }, func(o *Options) { o.Version = "../../escape" }} {
		o := options()
		change(&o)
		if _, err := o.Plan(); err == nil {
			t.Fatal("unsafe options accepted", o)
		}
	}
}

func TestTLSAdditionAndNginxFailurePreserveExistingSites(t *testing.T) {
	i, o, calls := fixture(t)
	ctx := context.Background()
	if _, err := i.Apply(ctx, o); err != nil {
		t.Fatal(err)
	}
	cert, key := testCertificate(t, o.Domain)
	o.Cert = cert
	o.Key = key
	dir := i.path("/etc/nginx/conf.d")
	os.MkdirAll(dir, 0755)
	other := filepath.Join(dir, "customer.conf")
	os.WriteFile(other, []byte("existing site"), 0644)
	original := i.run
	checks := 0
	fail := true
	i.run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "nginx" && len(args) > 0 {
			if args[0] == "-t" {
				checks++
				if checks == 2 && fail {
					return "bad configuration", errors.New("nginx rejected config")
				}
			}
			if args[0] == "-T" {
				return "# configuration file " + filepath.Join(dir, "deployer.conf") + ":\n", nil
			}
		}
		return original(ctx, name, args...)
	}
	if _, err := i.Apply(ctx, o); err == nil {
		t.Fatal("invalid nginx config accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "deployer.conf")); !os.IsNotExist(err) {
		t.Fatal("failed nginx file was left installed")
	}
	b, _ := os.ReadFile(other)
	if string(b) != "existing site" {
		t.Fatal("existing nginx site changed")
	}
	fail = false
	if r, err := i.Apply(ctx, o); err != nil || r.Plan.Edge != "nginx" {
		t.Fatalf("TLS resume: %+v %v", r, err)
	}
	c, err := config.Load(i.path("/etc/deployer/server.yaml"))
	if err != nil || c.PreviewScheme() != "https" || c.PreviewPort() != 443 {
		t.Fatal("HTTPS origin not configured", c, err)
	}
	bootstraps := 0
	for _, call := range *calls {
		if strings.Contains(call, "token bootstrap") {
			bootstraps++
		}
	}
	if bootstraps != 1 {
		t.Fatal("adding TLS rotated bootstrap token")
	}
}

func testCertificate(t *testing.T, domain string) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"*." + domain}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600)
	os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600)
	return certPath, keyPath
}
