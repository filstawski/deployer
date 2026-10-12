package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
		if string(b) != want || info.Mode().Perm() != 0o755 {
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
