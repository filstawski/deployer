//go:build !windows

package secretfile

import (
	"os"
	"testing"
)

func exposeToOthers(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
}
