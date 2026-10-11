package sshtunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deployer/internal/client"
)

// A subprocess simulates OpenSSH's local socket and failures without connecting
// to a host or relying on the developer's SSH configuration.
func TestSSHProcess(t *testing.T) {
	mode := os.Getenv("DEPLOYER_SSH_HELPER")
	if mode == "" {
		return
	}
	if mode == "fail" {
		fmt.Fprintln(os.Stderr, "Host key verification failed.")
		os.Exit(255)
	}
	if mode == "stall" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	var socket string
	for n, arg := range os.Args {
		if arg == "-L" {
			socket, _, _ = strings.Cut(os.Args[n+1], ":")
			break
		}
	}
	l, err := net.Listen("unix", socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	err = http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `{"schema_version":1,"data":{"connected":true}}`)
	}))
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}

func helper(t *testing.T, mode string, socket *string, proc **exec.Cmd) func(context.Context, string, ...string) *exec.Cmd {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name != "ssh" {
			t.Fatalf("unexpected executable %s", name)
		}
		for n, arg := range args {
			if arg == "-L" {
				*socket, _, _ = strings.Cut(args[n+1], ":")
			}
		}
		cmd := exec.CommandContext(ctx, executable, append([]string{"-test.run=^TestSSHProcess$", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "DEPLOYER_SSH_HELPER="+mode)
		*proc = cmd
		return cmd
	}
}

func TestPrivateSocketAndCleanup(t *testing.T) {
	var socket string
	var proc *exec.Cmd
	transport, closeTunnel, err := open(context.Background(), "root@host", 7420, helper(t, "serve", &socket, &proc), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTunnel()
	info, err := os.Stat(filepath.Dir(socket))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("socket directory not private: %v %v", info, err)
	}
	c, err := client.New("http://127.0.0.1:7420", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	c.HTTP.Transport = transport
	for range 2 {
		result, err := c.Do(context.Background(), "GET", "/v1/server", "", nil)
		if err != nil || !strings.Contains(string(result.Data), `"connected":true`) {
			t.Fatalf("API over socket: %s %v", result.Data, err)
		}
	}
	closeTunnel()
	closeTunnel() // Cleanup is safe on all return paths.
	if _, err := os.Stat(filepath.Dir(socket)); !os.IsNotExist(err) {
		t.Fatalf("socket remains: %v", err)
	}
	if proc.ProcessState == nil {
		t.Fatal("SSH process was not reaped")
	}
}

func TestConnectionFailureCleanup(t *testing.T) {
	for _, mode := range []string{"fail", "stall", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var socket string
			var proc *exec.Cmd
			factory := helper(t, mode, &socket, &proc)
			if mode == "cancel" {
				factory = helper(t, "stall", &socket, &proc)
				time.AfterFunc(100*time.Millisecond, cancel)
			}
			_, _, err := open(ctx, "host", 7420, factory, 300*time.Millisecond)
			if err == nil {
				t.Fatal("expected error")
			}
			if mode == "fail" && !strings.Contains(err.Error(), "Host key verification failed") {
				t.Fatal(err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Dir(socket)); !os.IsNotExist(err) {
				t.Fatalf("socket remains: %v", err)
			}
			if proc.ProcessState == nil {
				t.Fatal("SSH process not reaped")
			}
		})
	}
}

func TestTargetValidationAndSSHOptions(t *testing.T) {
	for _, target := range []string{"root@187.77.92.158", "my-host", "user@[::1]"} {
		if err := Validate(target, 7420); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"", "-oProxyCommand=bad", "host\nother", "user@host@other", "user@host:22", "$(command)", "root@"} {
		if Validate(target, 7420) == nil {
			t.Fatalf("accepted %q", target)
		}
	}
	if Validate("host", 0) == nil || Validate("host", 65536) == nil {
		t.Fatal("accepted invalid port")
	}
	args := strings.Join(arguments("host", 7420, "/tmp/private/api.sock"), " ")
	for _, want := range []string{"StrictHostKeyChecking=yes", "ForwardAgent=no", "BatchMode=yes", "ControlPath=none", "ControlPersist=no", "ForkAfterAuthentication=no", "ExitOnForwardFailure=yes", "-L /tmp/private/api.sock:127.0.0.1:7420 -- host"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %s in %s", want, args)
		}
	}
}
