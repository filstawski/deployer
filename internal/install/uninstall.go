package install

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// UninstallOptions configures removal.
type UninstallOptions struct {
	// Purge also deletes the database, credentials, configuration, and the
	// service account.
	Purge  bool
	DryRun bool
}

// Uninstall removes the service, binaries, nginx site, and every Docker
// resource the installation created. Without Purge it keeps the database,
// GitHub connection, and configuration for a later reinstall.
func (i *Installer) Uninstall(ctx context.Context, u UninstallOptions) (Result, error) {
	receipt, err := i.readReceipt()
	if err != nil {
		return Result{}, err
	}
	if receipt == nil {
		return Result{}, fmt.Errorf("no installation found at %s", ReceiptFile)
	}
	res := Result{Status: "planned", Version: receipt.Version, Steps: []string{
		"Stop and remove " + Service,
		"Remove this installation's preview containers, networks, images, gateway, and builder",
		"Remove " + OptDir + ", the links in " + BinDir + ", and " + NginxFile,
	}}
	if u.Purge {
		res.Steps = append(res.Steps, "Delete "+StateDir+", "+ConfigDir+", and the "+ServiceUser+" account")
	} else {
		res.Steps = append(res.Steps, "Keep "+StateDir+" and "+ConfigDir+" for a later reinstall")
	}
	if u.DryRun {
		return res, nil
	}
	if i.sys.OS() != "linux" || !i.sys.IsRoot() {
		return Result{}, fmt.Errorf("deployer server uninstall runs as root on the server")
	}
	i.step("Stopping %s", Service)
	i.sys.Run(ctx, "systemctl", "disable", "--now", Service)
	i.step("Removing Docker resources")
	if _, err := i.run(ctx, i.path(filepath.Join(CurrentLink, "deployerd")), "cleanup", "--config", i.path(ConfigFile)); err != nil {
		return Result{}, err
	}
	os.Remove(i.path(UnitFile))
	i.sys.Run(ctx, "systemctl", "daemon-reload")
	if _, err := os.Stat(i.path(NginxFile)); err == nil {
		i.step("Removing the nginx site")
		os.Remove(i.path(NginxFile))
		if _, err := i.run(ctx, "nginx", "-t"); err == nil {
			i.sys.Run(ctx, "systemctl", "reload", "nginx")
		}
	}
	for _, bin := range Binaries {
		os.Remove(i.path(filepath.Join(BinDir, bin)))
	}
	if err := os.RemoveAll(i.path(OptDir)); err != nil {
		return Result{}, err
	}
	if u.Purge {
		i.step("Deleting data and the %s account", ServiceUser)
		for _, dir := range []string{StateDir, ConfigDir} {
			if err := os.RemoveAll(i.path(dir)); err != nil {
				return Result{}, err
			}
		}
		i.sys.Run(ctx, "userdel", ServiceUser)
	} else {
		receipt.Status = "uninstalled"
		if err := i.writeReceipt(receipt); err != nil {
			return Result{}, err
		}
	}
	res.Status = "uninstalled"
	return res, nil
}
