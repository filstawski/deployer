package cli

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/filstawski/deployer/internal/install"
	"golang.org/x/term"
)

// Env is everything the CLI takes from its process, so tests can run it
// in-process.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	// ConfigDir holds saved contexts and tokens.
	ConfigDir string
	// Interactive reports whether a person is at the terminal, which enables
	// prompts and in-place progress output.
	Interactive bool
	// ReadSecret reads a line without echoing it.
	ReadSecret func(prompt string) (string, error)
	// OpenBrowser opens a URL; nil disables opening browsers.
	OpenBrowser func(url string) error
	// ServerConfig is the local server configuration consulted when running
	// as root on a Deployer server; empty disables the lookup.
	ServerConfig string
	// WorkDir is the directory for Git detection and local files.
	WorkDir string
	Now     func() time.Time
	// Executable is the resolved path of this deployer binary.
	Executable string
	// Root reports whether the process runs as root.
	Root bool
	// ReleaseURL overrides where releases are downloaded from.
	ReleaseURL string
	// ServerReleases is where a server installation keeps its releases; a
	// deployer binary inside it is part of that installation.
	ServerReleases string
}

// ProcessEnv returns the Env of the current process.
func ProcessEnv() Env {
	env := Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv,
		Interactive: term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd())),
		OpenBrowser: openBrowser, Now: time.Now,
	}
	env.WorkDir, _ = os.Getwd()
	env.ConfigDir = os.Getenv("DEPLOYER_CONFIG_DIR")
	if env.ConfigDir == "" {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, ".config")
		}
		env.ConfigDir = filepath.Join(base, "deployer")
	}
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		env.ServerConfig = "/etc/deployer/server.yaml"
	}
	env.Root = os.Geteuid() == 0
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		env.Executable = exe
	}
	env.ReleaseURL = os.Getenv("DEPLOYER_RELEASE_URL")
	env.ServerReleases = install.ReleasesDir
	env.ReadSecret = func(prompt string) (string, error) {
		io.WriteString(os.Stderr, prompt)
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		io.WriteString(os.Stderr, "\n")
		return string(b), err
	}
	return env
}
