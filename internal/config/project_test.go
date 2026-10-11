package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecipeBoundaryAndUnsupportedFields(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch"), 0600)
	os.WriteFile(filepath.Join(outside, "Dockerfile"), []byte("FROM scratch"), 0600)
	valid := "version: 1\nsource:\n  dockerfile: Dockerfile\nhttp:\n  port: 8080\n"
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{{"valid", valid, true}, {"privileges", valid + "privileged: true\n", false}, {"bad_port", strings.Replace(valid, "8080", "0", 1), false}, {"absolute", strings.Replace(valid, "dockerfile: Dockerfile", "dockerfile: /etc/passwd", 1), false}, {"second_document", valid + "---\nversion: 1\n", false}, {"unsafe_health", valid + "  healthcheck:\n    path: //external/path\n", false}} {
		t.Run(tc.name, func(t *testing.T) {
			os.WriteFile(filepath.Join(root, "deployer.yaml"), []byte(tc.body), 0600)
			_, err := LoadRecipe(root)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := SafePath(root, "escape/Dockerfile"); err == nil {
		t.Fatal("symlink escaped build boundary")
	}
}
