// Package install installs, upgrades, and removes Deployer on a Linux host
// with systemd. It never installs or reconfigures Docker or nginx packages;
// it only adds Deployer's own files, service, and an optional nginx site.
package install

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Host paths. They are rooted under Installer.root in tests.
const (
	ConfigDir   = "/etc/deployer"
	ConfigFile  = "/etc/deployer/server.yaml"
	ReceiptFile = "/etc/deployer/install.json"
	StateDir    = "/var/lib/deployer"
	OptDir      = "/opt/deployer"
	ReleasesDir = "/opt/deployer/releases"
	CurrentLink = "/opt/deployer/current"
	BinDir      = "/usr/local/bin"
	UnitFile    = "/etc/systemd/system/deployer.service"
	NginxFile   = "/etc/nginx/conf.d/deployer.conf"
	ServiceUser = "deployer"
	Service     = "deployer.service"
)

// Binaries are the executables in a release bundle.
var Binaries = []string{"deployer", "deployerd"}

// System is the installer's access to the host, replaced in tests.
type System interface {
	// Run runs a command and returns its combined output.
	Run(ctx context.Context, name string, args ...string) (string, error)
	LookPath(name string) error
	IsRoot() bool
	OS() string
	// PortsFree checks that loopback TCP ports can be bound.
	PortsFree(ports ...int) error
}

// Installer changes the host. Create one with New.
type Installer struct {
	sys  System
	root string
	log  io.Writer
	// ready waits until the installed service answers.
	ready func(ctx context.Context, apiURL string) error
}

// New returns an installer for this host. Progress is written to log.
func New(log io.Writer) *Installer {
	return &Installer{sys: hostSystem{}, log: log, ready: waitReady}
}

func (i *Installer) path(p string) string { return filepath.Join(i.root, p) }

func (i *Installer) step(format string, args ...any) {
	fmt.Fprintf(i.log, "• "+format+"\n", args...)
}

func (i *Installer) run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := i.sys.Run(ctx, name, args...)
	if err != nil {
		return out, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(out))
	}
	return out, nil
}

type hostSystem struct{}

func (hostSystem) Run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

func (hostSystem) LookPath(name string) error { _, err := exec.LookPath(name); return err }
func (hostSystem) IsRoot() bool               { return os.Geteuid() == 0 }
func (hostSystem) OS() string                 { return runtime.GOOS }

func (hostSystem) PortsFree(ports ...int) error {
	for _, p := range ports {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			return fmt.Errorf("port %d is in use; choose another with the matching --*-port flag", p)
		}
		l.Close()
	}
	return nil
}
