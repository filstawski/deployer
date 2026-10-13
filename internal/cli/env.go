package cli

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/filstawski/deployer/internal/install"
	"github.com/filstawski/deployer/internal/release"
	"golang.org/x/term"
)

// Env is everything the CLI takes from its process, so tests can run it
// in-process.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	// ConfigDir holds saved contexts and tokens: ~/.config/deployer, or
	// %APPDATA%\deployer on Windows.
	ConfigDir string
	// Interactive reports whether a person is at the terminal, which enables
	// prompts and in-place progress output.
	Interactive bool
	// Terminal reports whether stdout is a terminal, which enables panels,
	// styled tables, and color. Output for pipes and files stays plain.
	Terminal bool
	// Width is the terminal's width in columns, or 0 if unknown.
	Width int
	// Environ is the process environment, used to detect color support.
	Environ []string
	// Prompter asks questions; nil disables prompts.
	Prompter Prompter
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
	// Windows consoles show colors and redraw the status line only when
	// asked to; this does nothing elsewhere.
	lipgloss.EnableLegacyWindowsANSI(os.Stdout)
	lipgloss.EnableLegacyWindowsANSI(os.Stderr)
	env := Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv,
		Interactive: term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd())),
		Terminal:    term.IsTerminal(int(os.Stdout.Fd())),
		Environ:     os.Environ(),
		OpenBrowser: openBrowser, Now: time.Now,
	}
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
		env.Width = w
	} else if w, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil {
		env.Width = w
	}
	if env.Interactive {
		env.Prompter = huhPrompter{in: os.Stdin, out: os.Stderr}
	}
	env.WorkDir, _ = os.Getwd()
	home, _ := os.UserHomeDir()
	env.ConfigDir = configDir(os.Getenv, runtime.GOOS, home)
	if runtime.GOOS == "linux" {
		env.Root = os.Geteuid() == 0
		env.ServerReleases = install.ReleasesDir
		if env.Root {
			env.ServerConfig = "/etc/deployer/server.yaml"
		}
	}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		env.Executable = exe
		release.RemoveReplaced(exe) // What deployer upgrade left behind on Windows
	}
	env.ReleaseURL = os.Getenv("DEPLOYER_RELEASE_URL")
	return env
}

// configDir returns where the CLI keeps contexts and tokens.
func configDir(getenv func(string) string, goos, home string) string {
	if dir := getenv("DEPLOYER_CONFIG_DIR"); dir != "" {
		return dir
	}
	base := getenv("XDG_CONFIG_HOME")
	if base == "" && goos == "windows" {
		base = getenv("APPDATA")
	}
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "deployer")
}

// supportsLinks reports whether the terminal shows OSC 8 hyperlinks. Others
// may print them as garbage, so this is an allowlist.
func (e Env) supportsLinks() bool {
	switch e.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "WezTerm", "vscode", "ghostty", "WarpTerminal":
		return true
	}
	if e.Getenv("WT_SESSION") != "" { // Windows Terminal
		return true
	}
	term := e.Getenv("TERM")
	return strings.Contains(term, "kitty") || strings.Contains(term, "ghostty") || e.Getenv("VTE_VERSION") != ""
}
