//go:build !windows

package secretfile

import (
	"fmt"
	"io/fs"
	"os"
)

// createPrivate creates path for writing with mode 0600, failing if it
// exists.
func createPrivate(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

func rename(from, to string) error { return os.Rename(from, to) }

// checkPrivate refuses a file that its group or others can access.
func checkPrivate(path string, info fs.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is readable by other users; run chmod 600 %s", path, path)
	}
	return nil
}
