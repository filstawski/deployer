package install

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Receipt records what the installer did, without secrets.
type Receipt struct {
	Version     string     `json:"version"`
	Status      string     `json:"status"` // installing or installed
	Options     Options    `json:"options"`
	InstalledAt time.Time  `json:"installed_at"`
	UpgradedAt  *time.Time `json:"upgraded_at,omitempty"`
}

func (i *Installer) readReceipt() (*Receipt, error) {
	data, err := os.ReadFile(i.path(ReceiptFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var r Receipt
	if err := json.Unmarshal(data, &r); err != nil || r.Version == "" {
		return nil, fmt.Errorf("%s was written by an older Deployer pilot release; remove that installation first (see \"Replacing a pilot installation\" in docs/installation.md)", ReceiptFile)
	}
	return &r, nil
}

func (i *Installer) writeReceipt(r *Receipt) error {
	data, _ := json.MarshalIndent(r, "", "  ")
	return writeAtomic(i.path(ReceiptFile), append(data, '\n'), 0o600)
}

// writeAtomic replaces path with data.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".deployer-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// writeIfChanged writes data unless path already holds it, and reports
// whether it wrote.
func writeIfChanged(path string, data []byte, mode os.FileMode) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	return true, writeAtomic(path, data, mode)
}

// copyFile copies an executable into place atomically.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	data, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	return writeAtomic(dst, data, 0o755)
}

// replaceSymlink points path at target, replacing an existing symlink. It
// refuses to replace a regular file or directory.
func replaceSymlink(target, path string) error {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("%s exists and is not a symlink; remove it first", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".new"
	os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
