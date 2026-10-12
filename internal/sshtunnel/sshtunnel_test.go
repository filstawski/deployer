package sshtunnel

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
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

// TestHelperSSH plays ssh: it serves the forwarded socket by proxying to
// DEPLOYER_TEST_UPSTREAM, or fails like ssh does.
func TestHelperSSH(t *testing.T) {
	if os.Getenv("DEPLOYER_TEST_SSH") != "1" {
		return
	}
	args := os.Args
	for i, a := range args {
		if a == "-L" {
			socket := strings.SplitN(args[i+1], ":", 2)[0]
			if os.Getenv("DEPLOYER_TEST_FAIL") == "1" {
				os.Stderr.WriteString("Host key verification failed.\n")
				os.Exit(255)
			}
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

func TestTunnel(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "through the tunnel") }))
	defer api.Close()
	fakeSSH(t, strings.TrimPrefix(api.URL, "http://"), false)
	tunnel, err := Open(context.Background(), Target{User: "root", Host: "example.com"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: tunnel.Transport}
	resp, err := client.Get("http://deployer/v1/server")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "through the tunnel" {
		t.Fatalf("body %q", body)
	}
	tunnel.Close()
	tunnel.Close() // Idempotent.
}

func TestTunnelFailureIsActionable(t *testing.T) {
	fakeSSH(t, "127.0.0.1:1", true)
	_, err := Open(context.Background(), Target{Host: "example.com"}, 0)
	if err == nil || !strings.Contains(err.Error(), "Host key verification failed") || !strings.Contains(err.Error(), "ssh example.com") {
		t.Fatalf("error: %v", err)
	}
}
