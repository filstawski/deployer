package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// UpgradeOptions configures an upgrade.
type UpgradeOptions struct {
	Version string
	Bundle  string
	DryRun  bool
}

// databaseFiles are copied while the service is stopped.
var databaseFiles = []string{"state.db", "state.db-wal", "state.db-shm"}

// Upgrade switches the installation to a new release. It backs up the
// database while the service is stopped, and if the new release does not
// start, it restores the previous release and database.
func (i *Installer) Upgrade(ctx context.Context, u UpgradeOptions) (Result, error) {
	receipt, err := i.readReceipt()
	if err != nil {
		return Result{}, err
	}
	if receipt == nil || receipt.Status != "installed" {
		return Result{}, fmt.Errorf("no completed installation found; run deployer server install first")
	}
	if !releasePattern.MatchString(u.Version) {
		return Result{}, fmt.Errorf("upgrading requires a release build (this is %q)", u.Version)
	}
	if u.Version == receipt.Version {
		return Result{}, fmt.Errorf("Deployer %s is already installed", u.Version)
	}
	old, previous := receipt.Version, i.liveOptions(receipt.Options)
	o := previous
	o.Version, o.Bundle = u.Version, u.Bundle
	res := Result{Status: "planned", Version: u.Version, Steps: []string{
		"Install binaries in " + filepath.Join(ReleasesDir, u.Version),
		"Stop " + Service + " and back up the database to " + StateDir + "/backups",
		"Switch to " + u.Version + " and start " + Service,
		"If it does not start, restore " + old + " and the database backup",
	}}
	if u.DryRun {
		return res, nil
	}
	if i.sys.OS() != "linux" || !i.sys.IsRoot() {
		return Result{}, fmt.Errorf("deployer server upgrade runs as root on the server")
	}
	if out, err := i.run(ctx, filepath.Join(u.Bundle, "deployerd"), "version"); err != nil || strings.TrimSpace(out) != u.Version {
		return Result{}, fmt.Errorf("the bundle's deployerd is not version %s", u.Version)
	}

	i.step("Installing Deployer %s", u.Version)
	if err := i.installRelease(u.Bundle, u.Version); err != nil {
		return Result{}, err
	}
	i.step("Stopping %s and backing up the database", Service)
	if _, err := i.run(ctx, "systemctl", "stop", Service); err != nil {
		return Result{}, err
	}
	backup, err := i.backupDatabase(old)
	if err != nil {
		i.sys.Run(ctx, "systemctl", "start", Service)
		return Result{}, err
	}

	i.step("Starting Deployer %s", u.Version)
	err = i.activateRelease(u.Version)
	if err == nil {
		err = i.writeUnit(ctx)
	}
	if err == nil {
		_, err = i.run(ctx, "systemctl", "start", Service)
	}
	if err == nil {
		err = i.waitService(ctx, o)
	}
	if err != nil {
		i.step("The new release did not start; restoring %s", old)
		if rollbackErr := i.rollback(ctx, previous, backup); rollbackErr != nil {
			return Result{}, fmt.Errorf("upgrade failed (%v) and restoring %s also failed (%v); the database backup is in %s", err, old, rollbackErr, backup)
		}
		return Result{}, fmt.Errorf("upgrade failed; %s was restored: %w", old, err)
	}
	if o.TLSCert != "" {
		if err := i.configureNginx(ctx, o); err != nil {
			return Result{}, err
		}
	}
	now := time.Now().UTC()
	receipt.Version, receipt.Options, receipt.UpgradedAt = u.Version, o, &now
	if err := i.writeReceipt(receipt); err != nil {
		return Result{}, err
	}
	i.prune(u.Version, old)
	res.Status = "upgraded"
	res.Next = []string{"Upgrade the CLI on your laptop with the client installer"}
	return res, nil
}

func (i *Installer) backupDatabase(version string) (string, error) {
	dir := i.path(filepath.Join(StateDir, "backups", time.Now().UTC().Format("20060102-150405.000")+"-"+version))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	for _, name := range databaseFiles {
		data, err := os.ReadFile(i.path(filepath.Join(StateDir, name)))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err := writeAtomic(filepath.Join(dir, name), data, 0o600); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func (i *Installer) rollback(ctx context.Context, previous Options, backup string) error {
	i.sys.Run(ctx, "systemctl", "stop", Service)
	if err := i.activateRelease(previous.Version); err != nil {
		return err
	}
	for _, name := range databaseFiles {
		dst := i.path(filepath.Join(StateDir, name))
		data, err := os.ReadFile(filepath.Join(backup, name))
		if errors.Is(err, os.ErrNotExist) {
			os.Remove(dst)
			continue
		}
		if err != nil {
			return err
		}
		if err := writeAtomic(dst, data, 0o600); err != nil {
			return err
		}
		i.sys.Run(ctx, "chown", ServiceUser+":"+ServiceUser, dst)
	}
	if _, err := i.run(ctx, "systemctl", "start", Service); err != nil {
		return err
	}
	return i.waitService(ctx, previous)
}

// prune removes releases other than the current and previous ones, and all
// but the five newest database backups.
func (i *Installer) prune(current, previous string) {
	if entries, err := os.ReadDir(i.path(ReleasesDir)); err == nil {
		for _, e := range entries {
			if e.Name() != current && e.Name() != previous {
				os.RemoveAll(i.path(filepath.Join(ReleasesDir, e.Name())))
			}
		}
	}
	dir := i.path(filepath.Join(StateDir, "backups"))
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 5 {
		names := make([]string, len(entries))
		for n, e := range entries {
			names[n] = e.Name()
		}
		sort.Strings(names)
		for _, name := range names[:len(names)-5] {
			os.RemoveAll(filepath.Join(dir, name))
		}
	}
}
