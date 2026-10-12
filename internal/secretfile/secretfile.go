// Package secretfile reads and writes credential files that only their owner
// can access. Secrets are never printed or stored in ordinary config files.
package secretfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/filstawski/deployer/internal/domain"
)

// ReadToken reads an API token secret from an owner-only regular file.
func ReadToken(path string) (string, error) {
	s, err := Read(path)
	if err != nil {
		return "", err
	}
	if !domain.ValidTokenSecret(s) {
		return "", fmt.Errorf("%s does not contain a Deployer token", path)
	}
	return s, nil
}

// Read returns the trimmed contents of an owner-only regular file. Files that
// are symlinks or readable by group or others are refused.
func Read(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s must be a regular file, not a symlink or directory", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s is readable by other users; run chmod 600 %s", path, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// ErrExists is returned by WriteNew when the destination already exists.
var ErrExists = fs.ErrExist

// WriteNew creates path with mode 0600 and writes secret to it. It never
// replaces an existing file, and removes a partially written one.
func WriteNew(path, secret string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s already exists: %w", path, ErrExists)
		}
		return err
	}
	_, err = f.WriteString(secret + "\n")
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}

// Replace atomically writes secret to path with mode 0600, replacing any
// existing file.
func Replace(path, secret string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".secret-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(secret + "\n")
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
