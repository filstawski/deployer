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
// are symlinks or readable by other users are refused.
func Read(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s must be a regular file, not a symlink or directory", path)
	}
	if err := checkPrivate(path, info); err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// ErrExists is returned by WriteNew when the destination already exists.
var ErrExists = fs.ErrExist

// WriteNew creates path, readable only by its owner, and writes secret to
// it. It never replaces an existing file, and removes a partially written one.
func WriteNew(path, secret string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := createPrivate(path)
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

// Replace atomically writes secret to path, readable only by its owner,
// replacing any existing file.
func Replace(path, secret string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := createTemp(dir)
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
	return rename(f.Name(), path)
}

// createTemp creates a new owner-only file in dir with a random name.
func createTemp(dir string) (*os.File, error) {
	for {
		f, err := createPrivate(filepath.Join(dir, ".secret-"+domain.RandomString(12)))
		if !errors.Is(err, fs.ErrExist) {
			return f, err
		}
	}
}
