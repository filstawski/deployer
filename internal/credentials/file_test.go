package credentials

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnerOnlyAndNoOverwrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "token")
	f, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	secret := "dpl_" + strings.Repeat("a", 64)
	if err = Write(f, secret); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got, err := Read(p); err != nil || got != secret {
		t.Fatalf("read failed %v", err)
	}
	if f, err := Create(p); err == nil {
		f.Close()
		t.Fatal("overwrote existing token")
	}
	if err = os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = Read(p); err == nil {
		t.Fatal("accepted public credential file")
	}
}
