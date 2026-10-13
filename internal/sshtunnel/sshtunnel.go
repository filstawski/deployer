// Package sshtunnel reaches a server's loopback-only API through OpenSSH.
// The user's SSH configuration and known_hosts apply; host keys are never
// accepted automatically, and ssh never prompts.
//
// On macOS and Linux, each CLI invocation starts one ssh process that
// forwards a private Unix socket, and stops it when the command finishes.
// Windows' OpenSSH can't forward Unix sockets, and a local TCP port could be
// taken over by another program, so there each connection runs `ssh -W`,
// which carries it over ssh's standard input and output instead.
package sshtunnel

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultAPIPort is the server's loopback API port.
const DefaultAPIPort = 7420

var (
	hostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	userPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
)

// Target is an SSH destination, written as ssh://[user@]host[:port]. The
// host may be an alias from ~/.ssh/config.
type Target struct {
	User string
	Host string
	Port int // SSH port; 0 uses the SSH configuration.
}

// Parse parses an ssh:// URL.
func Parse(raw string) (Target, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "ssh" || u.Host == "" || strings.Trim(u.Path, "/") != "" || u.RawQuery != "" {
		return Target{}, fmt.Errorf("SSH targets look like ssh://user@server.example.com")
	}
	t := Target{Host: u.Hostname()}
	if u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword {
			return Target{}, fmt.Errorf("SSH targets must not contain a password; use SSH keys")
		}
		t.User = u.User.Username()
		if !userPattern.MatchString(t.User) {
			return Target{}, fmt.Errorf("invalid SSH user %q", t.User)
		}
	}
	if !hostPattern.MatchString(t.Host) {
		return Target{}, fmt.Errorf("invalid SSH host %q", t.Host)
	}
	if p := u.Port(); p != "" {
		if t.Port, err = strconv.Atoi(p); err != nil || t.Port < 1 || t.Port > 65535 {
			return Target{}, fmt.Errorf("invalid SSH port %q", p)
		}
	}
	return t, nil
}

// String formats the target as an ssh:// URL.
func (t Target) String() string {
	host := t.Host
	if t.Port != 0 {
		host += ":" + strconv.Itoa(t.Port)
	}
	if t.User != "" {
		host = t.User + "@" + host
	}
	return "ssh://" + host
}

// Tunnel is an open SSH forwarding. Close it when done.
type Tunnel struct {
	Transport http.RoundTripper
	close     func()
}

// Close stops ssh and removes the socket.
func (t *Tunnel) Close() { t.close() }

// command builds the ssh process; tests replace it.
var command = exec.CommandContext

// Open starts forwarding connections to the server's loopback API port.
func Open(ctx context.Context, t Target, apiPort int) (*Tunnel, error) {
	if apiPort == 0 {
		apiPort = DefaultAPIPort
	}
	if runtime.GOOS == "windows" {
		return openStdio(t, apiPort), nil
	}
	return openSocket(ctx, t, apiPort)
}

// args returns ssh's command line: no prompts, unknown host keys refused,
// nothing forwarded but the API, then forward and the destination.
func (t Target) args(forward ...string) []string {
	args := []string{
		"-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ExitOnForwardFailure=yes",
		"-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-o", "PermitLocalCommand=no",
		"-o", "ControlMaster=no", "-o", "ControlPath=none",
		"-o", "ConnectTimeout=10", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3"}
	args = append(args, forward...)
	if t.Port != 0 {
		args = append(args, "-p", strconv.Itoa(t.Port))
	}
	return append(args, "--", t.destination())
}

func (t Target) destination() string {
	if t.User != "" {
		return t.User + "@" + t.Host
	}
	return t.Host
}

// failure explains that ssh exited with an error, using what it printed.
func (t Target) failure(stderr string) error {
	lines := strings.FieldsFunc(stderr, func(r rune) bool { return r == '\n' || r == '\r' })
	return &Error{fmt.Sprintf("ssh to %s failed: %s; check that `ssh %s` works without prompts", t.destination(), strings.Join(lines, "; "), t.destination())}
}

// Error reports that ssh failed.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

// openSocket starts one ssh process that forwards a private Unix socket.
func openSocket(ctx context.Context, t Target, apiPort int) (*Tunnel, error) {
	// macOS's per-user temporary directory exceeds the Unix socket path limit.
	dir, err := os.MkdirTemp("/tmp", "deployer-ssh-")
	if err != nil {
		return nil, err
	}
	socket := filepath.Join(dir, "api.sock")
	args := t.args("-N", "-T", "-n", "-L", fmt.Sprintf("%s:127.0.0.1:%d", socket, apiPort))

	procCtx, cancel := context.WithCancel(context.Background())
	cmd := command(procCtx, "ssh", args...)
	cmd.WaitDelay = 2 * time.Second
	var stderr syncBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		os.RemoveAll(dir)
		return nil, fmt.Errorf("start ssh: %w", err)
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()

	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	var once sync.Once
	closeFn := func() {
		once.Do(func() {
			transport.CloseIdleConnections()
			cancel()
			<-exited
			os.RemoveAll(dir)
		})
	}
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	for {
		if info, err := os.Lstat(socket); err == nil && info.Mode()&os.ModeSocket != 0 {
			return &Tunnel{Transport: transport, close: closeFn}, nil
		}
		select {
		case <-exited:
			closeFn()
			return nil, t.failure(stderr.String())
		case <-deadline.C:
			closeFn()
			return nil, fmt.Errorf("ssh %s timed out", t)
		case <-ctx.Done():
			closeFn()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.b.Len() < 8<<10 {
		b.b.Write(p)
	}
	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
