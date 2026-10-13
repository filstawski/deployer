package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Server serves a fake release for tests: VERSION, SHA256SUMS, and assets.
func server(t *testing.T, version string, assets map[string]string, tamper string) *httptest.Server {
	t.Helper()
	var sums strings.Builder
	for name, body := range assets {
		sum := sha256.Sum256([]byte(body))
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/latest/download/VERSION":
			fmt.Fprintln(w, version)
		case r.URL.Path == "/download/"+version+"/SHA256SUMS":
			fmt.Fprint(w, sums.String())
		case strings.HasPrefix(r.URL.Path, "/download/"+version+"/"):
			name := strings.TrimPrefix(r.URL.Path, "/download/"+version+"/")
			body, ok := assets[name]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if name == tamper {
				body += "x"
			}
			fmt.Fprint(w, body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLatestAndDownload(t *testing.T) {
	srv := server(t, "v1.2.3", map[string]string{"deployer_v1.2.3_linux_amd64": "cli", "deployerd_v1.2.3_linux_amd64": "daemon"}, "")
	c := New(srv.URL)
	ctx := context.Background()
	if v, err := c.Latest(ctx); err != nil || v != "v1.2.3" {
		t.Fatalf("Latest = %q, %v", v, err)
	}
	dir := t.TempDir()
	err := c.Download(ctx, "v1.2.3", map[string]string{
		Asset("deployer", "v1.2.3", "linux", "amd64"):  filepath.Join(dir, "deployer"),
		Asset("deployerd", "v1.2.3", "linux", "amd64"): filepath.Join(dir, "deployerd"),
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"deployer": "cli", "deployerd": "daemon"} {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		info, _ := os.Stat(filepath.Join(dir, name))
		if string(b) != want || runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
			t.Errorf("%s: %q %v", name, b, info.Mode())
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("temporary files left behind: %d entries", len(entries))
	}
}

func TestDownloadRejectsBadFiles(t *testing.T) {
	srv := server(t, "v1.2.3", map[string]string{"a": "one", "b": "two"}, "b")
	dir := t.TempDir()
	dest := filepath.Join(dir, "a")
	os.WriteFile(dest, []byte("old"), 0o755)
	err := New(srv.URL).Download(context.Background(), "v1.2.3", map[string]string{"a": dest, "b": filepath.Join(dir, "b")})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("tampered asset: %v", err)
	}
	// Nothing is replaced unless every file verified.
	if b, _ := os.ReadFile(dest); string(b) != "old" {
		t.Fatal("replaced a file although another failed verification")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temporary files left behind: %d entries", len(entries))
	}
	if err := New(srv.URL).Download(context.Background(), "v1.2.3", map[string]string{"missing": dest}); err == nil {
		t.Fatal("downloaded an asset without a checksum")
	}
	if err := New(srv.URL).Download(context.Background(), "latest", nil); err == nil {
		t.Fatal("accepted an invalid version")
	}
}

func TestAssetNames(t *testing.T) {
	if got := Asset("deployer", "v1.2.3", "linux", "arm64"); got != "deployer_v1.2.3_linux_arm64" {
		t.Errorf("linux: %s", got)
	}
	if got := Asset("deployer", "v1.2.3", "windows", "amd64"); got != "deployer_v1.2.3_windows_amd64.exe" {
		t.Errorf("windows: %s", got)
	}
}

// TestHelperRunning plays a running deployer until its input closes.
func TestHelperRunning(t *testing.T) {
	if os.Getenv("DEPLOYER_TEST_RUNNING") == "1" {
		os.Stdout.WriteString("running\n")
		os.Stdin.Read(make([]byte, 1))
		os.Exit(0)
	}
}

// A CLI upgrading itself replaces the file it is running from, which Windows
// only allows by moving it aside first. Upgrading twice while the first
// version still runs moves the second one aside under another name.
func TestReplaceRunningProgram(t *testing.T) {
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "deployer.exe")
	if err := os.WriteFile(dest, self, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func() (stop func()) {
		cmd := exec.Command(dest, "-test.run=^TestHelperRunning$")
		cmd.Env = append(os.Environ(), "DEPLOYER_TEST_RUNNING=1")
		stdin, _ := cmd.StdinPipe()
		stdout, _ := cmd.StdoutPipe()
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		stop = sync.OnceFunc(func() { stdin.Close(); cmd.Wait() })
		t.Cleanup(stop)
		if _, err := stdout.Read(make([]byte, 8)); err != nil {
			t.Fatal(err)
		}
		return stop
	}
	srv := server(t, "v1.2.3", map[string]string{"deployer.exe": string(self)}, "")
	upgrade := func() {
		t.Helper()
		if err := New(srv.URL).Download(context.Background(), "v1.2.3", map[string]string{"deployer.exe": dest}); err != nil {
			t.Fatalf("replacing a running program: %v", err)
		}
	}

	first := run()
	upgrade()
	second := run()
	upgrade()
	if b, _ := os.ReadFile(dest); len(b) != len(self) {
		t.Fatalf("not replaced: %d bytes", len(b))
	}
	first()
	second()
	RemoveReplaced(dest)
	if entries, _ := os.ReadDir(filepath.Dir(dest)); len(entries) != 1 {
		t.Fatalf("left behind: %v", entries)
	}
}
