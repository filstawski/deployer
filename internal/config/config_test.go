package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRejectsUnsafeOrUnknownSettings(t *testing.T) {
	for name, body := range map[string]string{
		"public API":    "listen: 0.0.0.0:7420",
		"typo":          "lissten: 127.0.0.1:7420",
		"named port":    "listen: 127.0.0.1:http",
		"root state":    "state_dir: /",
		"new version":   "version: 9",
		"domain URL":    "base_domain: https://example.com",
		"multiple docs": "version: 1\n---\nversion: 1",
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "server.yaml")
			if err := os.WriteFile(p, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(p); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}
func TestRelativeStateIsRelativeToConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "server.yaml")
	if err := os.WriteFile(p, []byte("version: 1\nstate_dir: ./state\nbase_domain: previews.example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.StateDir != filepath.Join(dir, "state") {
		t.Fatalf("wrong path %s", c.StateDir)
	}
	if !strings.HasPrefix(c.Listen, "127.0.0.1:") {
		t.Fatal("unsafe listen default")
	}
}
