package cli

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/release"
	"github.com/filstawski/deployer/internal/version"
	"github.com/spf13/cobra"
)

func (a *app) upgradeCommand() *cobra.Command {
	var target string
	var check bool
	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade deployer to the latest release",
		Long: `Upgrade this deployer CLI to the latest release, or to the one given with
--version. The download is verified against the release's checksums.

On a Deployer server, this upgrades the server itself, with a database backup
and automatic rollback, and must run as root. It is the same as
deployer server upgrade.`,
		Example: `  deployer upgrade
  deployer upgrade --check
  sudo deployer upgrade            # on the server`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			rel := release.New(a.env.ReleaseURL)
			latest, err := a.targetVersion(ctx, rel, target)
			if err != nil {
				return err
			}
			current := version.Version
			if check {
				res := map[string]any{"version": current, "latest": latest, "update_available": latest != current}
				return a.out.result(res, func() {
					if latest == current {
						a.out.line("deployer %s is up to date", current)
					} else {
						a.out.line("deployer %s is installed; %s is available. Upgrade with: deployer upgrade", current, latest)
					}
				})
			}
			if a.isServerInstall() {
				return a.upgradeServer(ctx, rel, latest)
			}
			if latest == current {
				return a.out.result(map[string]string{"version": current}, func() { a.out.line("deployer %s is up to date", current) })
			}
			return a.upgradeClient(ctx, rel, current, latest)
		},
	}
	cmd.Flags().StringVar(&target, "version", "", "release to install, such as v0.3.0 (default: the latest)")
	cmd.Flags().BoolVar(&check, "check", false, "only report whether a newer release exists")
	return cmd
}

func (a *app) targetVersion(ctx context.Context, rel *release.Client, target string) (string, error) {
	if target != "" {
		if !release.ValidVersion(target) {
			return "", usage("--version must look like v1.2.3")
		}
		return target, nil
	}
	latest, err := rel.Latest(ctx)
	if err != nil {
		return "", domain.Errorf(domain.CodeUnavailable, "cannot find the latest release: %v", err)
	}
	return latest, nil
}

// isServerInstall reports whether this binary belongs to a server
// installation, which must be upgraded as a whole.
func (a *app) isServerInstall() bool {
	return a.env.ServerReleases != "" && strings.HasPrefix(a.env.Executable, a.env.ServerReleases+string(filepath.Separator))
}

// upgradeClient replaces this executable with the release's binary.
func (a *app) upgradeClient(ctx context.Context, rel *release.Client, current, target string) error {
	exe := a.env.Executable
	if exe == "" {
		return domain.Errorf(domain.CodeClient, "cannot find this executable")
	}
	a.progress("Downloading deployer %s for %s/%s", target, runtime.GOOS, runtime.GOARCH)
	err := rel.Download(ctx, target, map[string]string{release.Asset("deployer", target, runtime.GOOS, runtime.GOARCH): exe})
	if errors.Is(err, fs.ErrPermission) {
		return domain.Errorf(domain.CodeClient, "cannot replace %s: permission denied", exe).
			WithHint("run it with sudo, or reinstall with the installer into a directory you own")
	}
	if err != nil {
		return domain.Errorf(domain.CodeUnavailable, "%v", err)
	}
	return a.out.result(map[string]string{"previous": current, "version": target, "path": exe}, func() {
		a.out.success("Upgraded deployer from %s to %s", current, target)
	})
}

// upgradeServer downloads a server release and lets its own deployer run the
// upgrade, so improvements to the upgrade procedure apply immediately.
func (a *app) upgradeServer(ctx context.Context, rel *release.Client, target string) error {
	if !a.env.Root {
		return domain.Errorf(domain.CodeForbidden, "upgrading the server must run as root").WithHint("run: sudo deployer upgrade")
	}
	if target == version.Version {
		return a.out.result(map[string]string{"version": target}, func() { a.out.line("Deployer %s is already installed", target) })
	}
	// Stage next to the releases: /tmp may be mounted noexec.
	dir, err := os.MkdirTemp(filepath.Dir(a.env.ServerReleases), ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	a.progress("Downloading Deployer %s for linux/%s", target, runtime.GOARCH)
	assets := map[string]string{}
	for _, bin := range []string{"deployer", "deployerd"} {
		assets[release.Asset(bin, target, "linux", runtime.GOARCH)] = filepath.Join(dir, bin)
	}
	if err := rel.Download(ctx, target, assets); err != nil {
		return domain.Errorf(domain.CodeUnavailable, "%v", err)
	}
	args := []string{"server", "upgrade", "--bundle", dir}
	if a.json {
		args = append(args, "--json")
	}
	if a.quiet {
		args = append(args, "--quiet")
	}
	child := exec.CommandContext(ctx, filepath.Join(dir, "deployer"), args...)
	child.Stdin, child.Stdout, child.Stderr = a.env.Stdin, a.env.Stdout, a.env.Stderr
	var exit *exec.ExitError
	if err := child.Run(); errors.As(err, &exit) {
		return exitCode(exit.ExitCode())
	} else if err != nil {
		return err
	}
	return nil
}
