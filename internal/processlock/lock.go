// Package processlock prevents two controllers from dispatching against one
// SQLite state directory. The initial server targets Linux and macOS.
package processlock

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func Acquire(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "controller.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another controller is using this state directory: %w", err)
	}
	return f, nil
}
