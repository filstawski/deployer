// Package sshtunnel connects a client to the private management API using the
// user's OpenSSH configuration and verified host keys. Each CLI invocation owns
// its SSH process and a socket inside a private temporary directory.
package sshtunnel

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var hostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var userPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

func Validate(target string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("SSH remote API port must be between 1 and 65535")
	}
	host := target
	if user, rest, ok := strings.Cut(target, "@"); ok {
		if !userPattern.MatchString(user) {
			return fmt.Errorf("invalid SSH username")
		}
		host = rest
	}
	if !hostPattern.MatchString(host) && !(strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") && net.ParseIP(host[1:len(host)-1]) != nil) {
		return fmt.Errorf("SSH target must be a host, SSH config alias, or user@host; configure SSH ports and keys in ~/.ssh/config")
	}
	return nil
}

func arguments(target string, port int, socket string) []string {
	return []string{"-N", "-T", "-n", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes",
		"-o", "ExitOnForwardFailure=yes", "-o", "ForwardAgent=no", "-o", "ForwardX11=no",
		"-o", "PermitLocalCommand=no", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3",
		"-o", "ConnectTimeout=10", "-o", "ControlMaster=no", "-o", "ControlPath=none",
		"-o", "ControlPersist=no", "-o", "ForkAfterAuthentication=no",
		"-L", fmt.Sprintf("%s:127.0.0.1:%d", socket, port), "--", target}
}

// Open never binds a public or loopback TCP port. The returned cleanup must be
// called after HTTP requests finish, including when a command fails.
func Open(ctx context.Context, target string, port int) (*http.Transport, func(), error) {
	return open(ctx, target, port, exec.CommandContext, 15*time.Second)
}

func open(ctx context.Context, target string, port int, command func(context.Context, string, ...string) *exec.Cmd, timeout time.Duration) (*http.Transport, func(), error) {
	if err := Validate(target, port); err != nil {
		return nil, nil, err
	}
	// macOS's per-user temporary path can exceed the Unix socket length limit.
	dir, err := os.MkdirTemp("/tmp", "deployer-ssh-")
	if err != nil {
		return nil, nil, err
	}
	socket := filepath.Join(dir, "api.sock")
	child, cancel := context.WithCancel(ctx)
	cmd := command(child, "ssh", arguments(target, port, socket)...)
	cmd.WaitDelay = 2 * time.Second
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		cancel()
		os.RemoveAll(dir)
		return nil, nil, fmt.Errorf("cannot start OpenSSH: %w", err)
	}
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); close(done) }()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			transport.CloseIdleConnections()
			cancel()
			<-done
			os.RemoveAll(dir)
		})
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-done:
			cleanup()
			return nil, nil, fmt.Errorf("SSH connection failed (%v): %s; verify the host key and SSH key access with your normal SSH client first", waitErr, strings.TrimSpace(stderr.String()))
		case <-ctx.Done():
			cleanup()
			return nil, nil, ctx.Err()
		case <-timer.C:
			cleanup()
			return nil, nil, fmt.Errorf("SSH connection timed out; check key access and forwarding permissions: %s", strings.TrimSpace(stderr.String()))
		case <-tick.C:
			if st, err := os.Lstat(socket); err == nil && st.Mode()&os.ModeSocket != 0 {
				return transport, cleanup, nil
			}
		}
	}
}

type limitedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if remaining := 8192 - b.b.Len(); remaining > 0 {
		b.b.Write(p[:min(len(p), remaining)])
	}
	return n, nil
}
func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
