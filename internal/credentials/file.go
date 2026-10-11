package credentials

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Read refuses group/world-readable credential files and symlinks. Tokens are
// never returned in normal CLI output or stored in the contexts file.
func Read(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("credential file must be a regular owner-only file (chmod 600)")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(b))
	if !strings.HasPrefix(s, "dpl_") || len(s) != 68 {
		return "", fmt.Errorf("invalid Deployer token file")
	}
	return s, nil
}

func Create(path string) (*os.File, error) {
	if path == "" {
		return nil, fmt.Errorf("an output path is required; credentials are never printed")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
}

func Write(f *os.File, secret string) error {
	if _, err := f.WriteString(secret + "\n"); err != nil {
		return err
	}
	return f.Sync()
}
