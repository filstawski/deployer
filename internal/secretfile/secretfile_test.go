package secretfile

import (
	"errors"
	"os"
	"path/filepath"
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
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(path); err == nil {
		t.Fatal("group-readable credential accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(link); err == nil {
		t.Fatal("symlinked credential accepted")
	}
	if err := Replace(path, "dpl_nope"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(path); err == nil {
		t.Fatal("malformed token accepted")
	}
}
