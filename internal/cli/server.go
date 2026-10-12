package cli

import (
	"os"
	"path/filepath"

	"github.com/filstawski/deployer/internal/install"
	"github.com/filstawski/deployer/internal/release"
	"github.com/filstawski/deployer/internal/version"
	"github.com/spf13/cobra"
)

func (a *app) serverCommand() *cobra.Command {
	cmd := group("server", "Install, upgrade, or remove Deployer on this server")
	cmd.Long = `Install, upgrade, or remove Deployer on a Linux server. These commands run as
root on the server itself, usually through install.sh, which downloads and
verifies a release first.`
	var o install.Options
	var noAPI bool
	installCmd := &cobra.Command{
		Use:   "install --domain DOMAIN",
		Short: "Install Deployer on this server",
		Example: `  deployer server install --domain previews.example.com --dry-run
  deployer server install --domain previews.example.com \
    --tls-cert /etc/letsencrypt/live/previews.example.com/fullchain.pem \
    --tls-key /etc/letsencrypt/live/previews.example.com/privkey.pem`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if o.Domain == "" {
				return usage("--domain is required, such as --domain previews.example.com")
			}
			o.Version = version.Version
			if o.Bundle == "" {
				o.Bundle = executableDir()
			}
			if !noAPI && o.TLSCert != "" && o.APIHost == "" {
				o.APIHost = "api." + o.Domain
			}
			if noAPI {
				o.APIHost = ""
			}
			res, err := install.New(a.env.Stderr).Install(cmd.Context(), o)
			if err != nil {
				return err
			}
			return a.printInstall(res)
		},
	}
	f := installCmd.Flags()
	f.StringVar(&o.Domain, "domain", "", "base domain; previews are served at NAME.DOMAIN")
	f.StringVar(&o.TLSCert, "tls-cert", "", "wildcard certificate for *.DOMAIN (PEM); configures nginx")
	f.StringVar(&o.TLSKey, "tls-key", "", "private key for --tls-cert (PEM)")
	f.StringVar(&o.APIHost, "api-host", "", "public hostname of the management API (default: api.DOMAIN)")
	f.BoolVar(&noAPI, "no-public-api", false, "do not publish the API; reach it over SSH instead")
	f.IntVar(&o.APIPort, "api-port", 7420, "loopback port of the management API")
	f.IntVar(&o.GatewayPort, "gateway-port", 8080, "loopback port where nginx sends preview traffic")
	f.IntVar(&o.ControlPort, "control-port", 7421, "loopback port of the gateway control API")
	f.StringVar(&o.Bundle, "bundle", "", "directory with the release binaries (default: this executable's directory)")
	f.BoolVar(&o.DryRun, "dry-run", false, "show what would be done without changing anything")

	var u install.UpgradeOptions
	var target string
	upgrade := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade this server to the latest release",
		Long: `Upgrade the installed server to the latest release, or to --version. The
database is backed up first, and the previous release is restored if the new
one does not start. Preview containers keep running.

With --bundle, install the release binaries in that directory instead of
downloading them.`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			u.Version = version.Version
			if u.Bundle == "" {
				rel := release.New(a.env.ReleaseURL)
				v, err := a.targetVersion(cmd.Context(), rel, target)
				if err != nil {
					return err
				}
				if !u.DryRun {
					return a.upgradeServer(cmd.Context(), rel, v)
				}
				u.Version = v // Plan the upgrade to the release that would be downloaded.
			}
			res, err := install.New(a.env.Stderr).Upgrade(cmd.Context(), u)
			if err != nil {
				return err
			}
			return a.printInstall(res)
		},
	}
	upgrade.Flags().StringVar(&target, "version", "", "release to upgrade to (default: the latest)")
	upgrade.Flags().StringVar(&u.Bundle, "bundle", "", "directory with release binaries to install instead of downloading")
	upgrade.Flags().BoolVar(&u.DryRun, "dry-run", false, "show what would be done without changing anything")

	var un install.UninstallOptions
	var yes bool
	uninstall := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Deployer and all previews from this server",
		Long: `Remove Deployer's service, binaries, nginx site, and every preview container,
network, and image. The database, GitHub connection, and settings are kept
for a reinstall unless --purge is given.`,
		Args: exactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !yes && !un.DryRun {
				return usage("this removes every preview on the server; pass --yes to continue")
			}
			res, err := install.New(a.env.Stderr).Uninstall(cmd.Context(), un)
			if err != nil {
				return err
			}
			return a.printInstall(res)
		},
	}
	uninstall.Flags().BoolVar(&un.Purge, "purge", false, "also delete the database, credentials, settings, and service account")
	uninstall.Flags().BoolVar(&un.DryRun, "dry-run", false, "show what would be done without changing anything")
	uninstall.Flags().BoolVarP(&yes, "yes", "y", false, "confirm removal")

	cmd.AddCommand(installCmd, upgrade, uninstall)
	return cmd
}

func (a *app) printInstall(res install.Result) error {
	return a.out.result(res, func() {
		switch res.Status {
		case "planned":
			a.out.line("Deployer %s would:", res.Version)
			for _, s := range res.Steps {
				a.out.line("  • %s", s)
			}
		case "installed":
			a.out.line("✓ Deployer %s is installed", res.Version)
		case "upgraded":
			a.out.line("✓ Upgraded to Deployer %s", res.Version)
		case "uninstalled":
			a.out.line("✓ Deployer was removed")
		}
		if res.Status != "planned" && res.Previews != "" {
			a.out.fields([][2]string{{"  Previews", res.Previews}, {"  API", res.API}})
		}
		if len(res.Next) > 0 {
			a.out.line("\nNext:")
			for n, s := range res.Next {
				a.out.line("  %d. %s", n+1, s)
			}
		}
	})
}

func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}
