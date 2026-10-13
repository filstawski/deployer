package sshtunnel

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParse(t *testing.T) {
	got, err := Parse("ssh://root@server.example.com:2222")
	if err != nil || got != (Target{User: "root", Host: "server.example.com", Port: 2222}) || got.String() != "ssh://root@server.example.com:2222" {
		t.Fatalf("Parse = %+v, %v", got, err)
	}
	if got, err := Parse("ssh://prod"); err != nil || got.Host != "prod" {
		t.Fatalf("alias: %+v %v", got, err)
	}
	for _, bad := range []string{"root@host", "ssh://u:pw@host", "ssh://-oProxyCommand=x", "ssh://host/path", "ssh://host:99999"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// TestHelperSSH plays ssh: it serves the forwarded socket, or standard input
// and output for -W, by proxying to DEPLOYER_TEST_UPSTREAM, or fails like ssh
// does.
func TestHelperSSH(t *testing.T) {
	if os.Getenv("DEPLOYER_TEST_SSH") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if (a == "-L" || a == "-W") && os.Getenv("DEPLOYER_TEST_FAIL") == "1" {
			os.Stderr.WriteString("Host key verification failed.\n")
			os.Exit(255)
		}
		if a == "-W" {
			up, err := net.Dial("tcp", os.Getenv("DEPLOYER_TEST_UPSTREAM"))
			if err != nil {
				os.Stderr.WriteString("channel 0: open failed: connect failed: Connection refused\nstdio forwarding failed\n")
				os.Exit(255)
			}
			go func() {
				io.Copy(up, os.Stdin)
				up.(*net.TCPConn).CloseWrite()
			}()
			io.Copy(os.Stdout, up)
			os.Exit(0)
		}
		if a == "-L" {
			socket := strings.SplitN(args[i+1], ":", 2)[0]
			l, err := net.Listen("unix", socket)
			if err != nil {
				os.Exit(1)
			}
			for {
				conn, err := l.Accept()
				if err != nil {
					os.Exit(0)
				}
				go func() {
					up, err := net.Dial("tcp", os.Getenv("DEPLOYER_TEST_UPSTREAM"))
					if err != nil {
						conn.Close()
						return
					}
					go io.Copy(up, conn)
					io.Copy(conn, up)
					conn.Close()
				}()
			}
		}
	}
	os.Exit(2)
}

func fakeSSH(t *testing.T, upstream string, fail bool) {
	t.Helper()
	command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=TestHelperSSH", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "DEPLOYER_TEST_SSH=1", "DEPLOYER_TEST_UPSTREAM="+upstream)
		if fail {
			cmd.Env = append(cmd.Env, "DEPLOYER_TEST_FAIL=1")
		}
		return cmd
	}
	t.Cleanup(func() { command = exec.CommandContext })
}

// get fetches a page through the tunnel.
func get(t *testing.T, tunnel *Tunnel, path string) (string, error) {
	t.Helper()
	resp, err := (&http.Client{Transport: tunnel.Transport}).Get("http://deployer" + path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func TestTunnel(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "through the tunnel") }))
	defer api.Close()
	fakeSSH(t, strings.TrimPrefix(api.URL, "http://"), false)
	tunnel, err := Open(context.Background(), Target{User: "root", Host: "example.com"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if body, err := get(t, tunnel, "/v1/server"); err != nil || body != "through the tunnel" {
		t.Fatalf("body %q, %v", body, err)
	}
	tunnel.Close()
	tunnel.Close() // Idempotent.
}

func TestSocketFailureIsActionable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows forwards standard input and output instead")
	}
	fakeSSH(t, "127.0.0.1:1", true)
	_, err := openSocket(context.Background(), Target{Host: "example.com"}, DefaultAPIPort)
	if err == nil || !strings.Contains(err.Error(), "Host key verification failed") || !strings.Contains(err.Error(), "ssh example.com") {
		t.Fatalf("error: %v", err)
	}
}

// The forwarding Windows uses works everywhere, so it is tested everywhere.
func TestStdio(t *testing.T) {
	var requests atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 2 {
			w.Header().Set("Connection", "close") // The next request needs a new ssh.
		}
		io.WriteString(w, strings.Repeat("x", 100<<10)) // More than a pipe's buffer.
	}))
	defer api.Close()
	fakeSSH(t, strings.TrimPrefix(api.URL, "http://"), false)
	tunnel := openStdio(Target{Host: "example.com"}, DefaultAPIPort)
	for range 3 {
		if body, err := get(t, tunnel, "/v1/server"); err != nil || len(body) != 100<<10 {
			t.Fatalf("body of %d bytes, %v", len(body), err)
		}
	}
	tunnel.Close()
	if _, err := get(t, tunnel, "/v1/server"); err == nil {
		t.Fatal("request after Close succeeded")
	}
}

func TestStdioFailureIsActionable(t *testing.T) {
	fakeSSH(t, "127.0.0.1:1", true)
	tunnel := openStdio(Target{User: "root", Host: "example.com"}, DefaultAPIPort)
	defer tunnel.Close()
	_, err := get(t, tunnel, "/v1/server")
	var failed *Error
	if !errors.As(err, &failed) || !strings.Contains(failed.Error(), "Host key verification failed") || !strings.Contains(failed.Error(), "`ssh root@example.com`") {
		t.Fatalf("error: %v", err)
	}

	// The API being down is reported the same way.
	fakeSSH(t, "127.0.0.1:1", false)
	down := openStdio(Target{Host: "example.com"}, DefaultAPIPort)
	defer down.Close()
	_, err = get(t, down, "/v1/server")
	if !errors.As(err, &failed) || !strings.Contains(failed.Error(), "connect failed") {
		t.Fatalf("error: %v", err)
	}
}
