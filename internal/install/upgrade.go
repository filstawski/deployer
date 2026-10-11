package install

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"deployer/internal/config"
	"gopkg.in/yaml.v3"
)

// Upgrade installs a prepared release. It keeps domain, ports, nginx, credentials
// and deployment state, and rolls binaries/config back if startup fails.
func (i *Installer) Upgrade(ctx context.Context, bundle, version string, dry bool) (result Result, err error) {
	if !releasePattern.MatchString(version) {
		return result, fmt.Errorf("upgrade requires a versioned release binary")
	}
	recordPath := i.path("/etc/deployer/install.json")
	original, err := os.ReadFile(recordPath)
	if err != nil {
		return result, err
	}
	var old receipt
	if err = json.Unmarshal(original, &old); err != nil {
		return result, err
	}
	if !old.Ready {
		return result, fmt.Errorf("finish the current installation before upgrading")
	}
	options := old.Options
	options.Version = version
	options.Bundle = bundle
	plan, err := options.Plan()
	if err != nil {
		return result, err
	}
	plan.Steps = []string{"Validate the existing managed installation and new release", "Stage versioned binaries and gateway image", "Stop the controller and back up its database", "Switch release and gateway configuration, restart and verify", "Restore previous binaries/configuration if startup fails"}
	result = Result{Plan: plan, Status: "planned"}
	if dry {
		return result, nil
	}
	if i.platform != "linux" || i.uid != 0 {
		return result, fmt.Errorf("server upgrade requires root on Linux")
	}
	if bundle == "" {
		return result, fmt.Errorf("upgrade requires --bundle")
	}
	if old.Options.Version == version {
		return result, fmt.Errorf("release %s is already installed", version)
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
	observed, err := os.ReadFile(recordPath)
	if err != nil || hash(observed) != hash(original) {
		return result, fmt.Errorf("installation changed during preflight")
	}
	journal := i.path("/etc/deployer/upgrade-pending.json")
	if _, e := os.Lstat(journal); !os.IsNotExist(e) {
		return result, fmt.Errorf("unfinished upgrade detected; inspect /etc/deployer/upgrade-pending.json and recover before retrying")
	}
	current := i.path("/opt/deployer/current")
	previous, err := os.Readlink(current)
	if err != nil || previous != filepath.Join("/opt/deployer/releases", old.Options.Version) {
		return result, fmt.Errorf("current release does not match installation receipt")
	}
	configPath := i.path(plan.ConfigPath)
	originalConfig, err := os.ReadFile(configPath)
	if err != nil {
		return result, err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return result, err
	}
	if cfg.StateDir != "/var/lib/deployer" {
		return result, fmt.Errorf("upgrade requires the managed state directory")
	}
	if _, err = i.runStep(ctx, "systemctl", "is-active", "--quiet", "deployer.service"); err != nil {
		return result, err
	}
	if err = i.ready(ctx, plan.API, i.path(plan.TokenFile)); err != nil {
		return result, err
	}
	releaseDir := i.path(plan.ReleaseDir)
	if err = managedDirectory(releaseDir, 0755); err != nil {
		return result, err
	}
	for _, name := range []string{"deployer", "deployerd", "deployer-gateway"} {
		src := filepath.Join(bundle, name)
		st, e := os.Lstat(src)
		if e != nil || !st.Mode().IsRegular() || st.Mode()&0111 == 0 {
			return result, fmt.Errorf("missing release executable %s", name)
		}
		b, e := os.ReadFile(src)
		if e != nil {
			return result, e
		}
		if e = writeOnce(filepath.Join(releaseDir, name), b, 0755); e != nil {
			return result, e
		}
	}
	imageDir := filepath.Join(releaseDir, "gateway")
	if err = managedDirectory(imageDir, 0755); err != nil {
		return result, err
	}
	gateway, err := os.ReadFile(filepath.Join(releaseDir, "deployer-gateway"))
	if err != nil {
		return result, err
	}
	if err = writeOnce(filepath.Join(imageDir, "gateway"), gateway, 0755); err != nil {
		return result, err
	}
	if err = writeOnce(filepath.Join(imageDir, "Dockerfile"), []byte("FROM scratch\nCOPY gateway /gateway\nUSER 65532:65532\nENTRYPOINT [\"/gateway\"]\n"), 0644); err != nil {
		return result, err
	}
	tag := "deployer-gateway:" + version
	if _, err = i.runStep(ctx, "docker", "--host", "unix:///var/run/docker.sock", "build", "--network", "none", "--tag", tag, imageDir); err != nil {
		return result, err
	}
	image, err := i.runStep(ctx, "docker", "--host", "unix:///var/run/docker.sock", "image", "inspect", "--format", "{{.Id}}", tag)
	if err != nil {
		return result, err
	}
	image = strings.TrimSpace(image)
	if !regexpImage(image) {
		return result, fmt.Errorf("invalid gateway image identity")
	}
	// Preserve all user settings, replacing only the gateway image.
	cfg.Runtime.GatewayImage = image
	newConfig, err := yaml.Marshal(cfg)
	if err != nil {
		return result, err
	}
	backup := i.path(filepath.Join("/etc/deployer/backups", time.Now().UTC().Format("20060102T150405.000000000")))
	if err = managedDirectory(backup, 0700); err != nil {
		return result, err
	}
	if err = atomicWrite(filepath.Join(backup, "server.yaml"), originalConfig, 0600); err != nil {
		return result, err
	}
	if err = atomicWrite(filepath.Join(backup, "install.json"), original, 0600); err != nil {
		return result, err
	}
	if err = writeJSON(journal, map[string]string{"previous_release": previous, "next_release": plan.ReleaseDir, "backup": backup}); err != nil {
		return result, err
	}
	stopped := false
	defer func() {
		if err == nil {
			return
		}
		if !stopped {
			_ = os.Remove(journal)
			return
		}
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
		defer cancel()
		var failures []string
		if _, e := i.runStep(rollbackCtx, "systemctl", "stop", "deployer.service"); e != nil {
			failures = append(failures, "stop service")
		}
		if e := atomicWrite(configPath, originalConfig, 0644); e != nil {
			failures = append(failures, "restore configuration")
		}
		if e := replaceLink(current, previous); e != nil {
			failures = append(failures, "restore release link")
		}
		if e := atomicWrite(recordPath, original, 0600); e != nil {
			failures = append(failures, "restore receipt")
		}
		if _, e := i.runStep(rollbackCtx, "systemctl", "start", "deployer.service"); e != nil {
			failures = append(failures, "restart previous release")
		} else if e = i.ready(rollbackCtx, plan.API, i.path(plan.TokenFile)); e != nil {
			failures = append(failures, "verify previous release")
		}
		if len(failures) == 0 {
			_ = os.Remove(journal)
			err = fmt.Errorf("upgrade failed; previous release restored: %w", err)
		} else {
			err = fmt.Errorf("upgrade failed (%v); recovery required (%s); see %s", err, strings.Join(failures, ", "), journal)
		}
	}()
	// Mark before stopping: a failed command may still have stopped the service.
	stopped = true
	if _, err = i.runStep(ctx, "systemctl", "stop", "deployer.service"); err != nil {
		return result, err
	}
	for _, name := range []string{"state.db", "state.db-wal", "state.db-shm"} {
		b, e := os.ReadFile(i.path(filepath.Join(plan.StateDir, name)))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return result, e
		}
		if e = atomicWrite(filepath.Join(backup, name), b, 0600); e != nil {
			return result, e
		}
	}
	if err = atomicWrite(configPath, newConfig, 0644); err != nil {
		return result, err
	}
	if err = replaceLink(current, plan.ReleaseDir); err != nil {
		return result, err
	}
	if _, err = i.runStep(ctx, "systemctl", "start", "deployer.service"); err != nil {
		return result, err
	}
	if err = i.ready(ctx, plan.API, i.path(plan.TokenFile)); err != nil {
		return result, err
	}
	next := old
	next.Options = options
	next.Image = image
	next.ConfigHash = hash(newConfig)
	if err = writeJSON(recordPath, next); err != nil {
		return result, err
	}
	if err = os.Remove(journal); err != nil {
		return result, err
	}
	result.Status = "upgraded"
	return result, nil
}
func replaceLink(path, target string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".release-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	defer os.Remove(name)
	if err = os.Symlink(target, name); err != nil {
		return err
	}
	return os.Rename(name, path)
}
