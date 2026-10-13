package secretfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filstawski/deployer/internal/domain"
)

func TestWriteAndReadToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "admin.token")
	secret := domain.NewTokenSecret()
	if err := WriteNew(path, secret); err != nil {
		t.Fatal(err)
	}
	if err := WriteNew(path, secret); !errors.Is(err, ErrExists) {
		t.Fatalf("WriteNew replaced an existing file: %v", err)
	}
	got, err := ReadToken(path)
	if err != nil || got != secret {
		t.Fatalf("ReadToken = %q, %v", got, err)
	}
	exposeToOthers(t, path)
	if _, err := ReadToken(path); err == nil || !strings.Contains(err.Error(), "readable by other users") {
		t.Fatalf("credential readable by others accepted: %v", err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Logf("skipping the symlink check: %v", err) // Windows without Developer Mode
	} else if _, err := Read(link); err == nil {
		t.Fatal("symlinked credential accepted")
	}
	// Replacing a file makes it private again.
	if err := Replace(path, "dpl_nope"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(path); err == nil || !strings.Contains(err.Error(), "does not contain a Deployer token") {
		t.Fatalf("malformed token: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}
