package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Recipe intentionally supports only a Dockerfile and one HTTP service. Unknown
// fields fail closed rather than silently ignoring Compose privileges/policies.
type Recipe struct {
	Version int    `yaml:"version" json:"version"`
	Project string `yaml:"project,omitempty" json:"project,omitempty"`
	Source  struct {
		Dockerfile string `yaml:"dockerfile" json:"dockerfile"`
		Context    string `yaml:"context" json:"context"`
	} `yaml:"source" json:"source"`
	HTTP struct {
		Service     string `yaml:"service" json:"service"`
		Port        int    `yaml:"port" json:"port"`
		Healthcheck struct {
			Path    string `yaml:"path" json:"path"`
			Timeout string `yaml:"timeout" json:"timeout"`
		} `yaml:"healthcheck" json:"healthcheck"`
	} `yaml:"http" json:"http"`
	Environment map[string]string `yaml:"environment,omitempty" json:"environment,omitempty"`
}

func LoadRecipe(root string) (Recipe, error) {
	var r Recipe
	r.Source.Context = "."
	r.HTTP.Service = "web"
	r.HTTP.Healthcheck.Path = "/health"
	r.HTTP.Healthcheck.Timeout = "90s"
	path, err := SafePath(root, "deployer.yaml")
	if err != nil {
		return r, err
	}
	f, err := os.Open(path)
	if err != nil {
		return r, fmt.Errorf("missing deployer.yaml: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return r, err
	}
	if len(b) > 65536 {
		return r, fmt.Errorf("deployer.yaml exceeds 64 KiB")
	}
	d := yaml.NewDecoder(bytes.NewReader(b))
	d.KnownFields(true)
	if err = d.Decode(&r); err != nil {
		return r, err
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return r, fmt.Errorf("one YAML document is required")
	}
	if r.Version != 1 || r.Source.Dockerfile == "" || r.HTTP.Service != "web" || r.HTTP.Port < 1 || r.HTTP.Port > 65535 {
		return r, fmt.Errorf("recipe requires version 1, source.dockerfile, service web, and a valid internal port")
	}
	if _, err = SafePath(root, r.Source.Context); err != nil {
		return r, err
	}
	if _, err = SafePath(root, r.Source.Dockerfile); err != nil {
		return r, err
	}
	if !strings.HasPrefix(r.HTTP.Healthcheck.Path, "/") || strings.HasPrefix(r.HTTP.Healthcheck.Path, "//") || strings.ContainsAny(r.HTTP.Healthcheck.Path, "\r\n#") {
		return r, fmt.Errorf("healthcheck.path must be a local absolute HTTP path")
	}
	t, err := time.ParseDuration(r.HTTP.Healthcheck.Timeout)
	if err != nil || t < time.Second || t > 5*time.Minute {
		return r, fmt.Errorf("healthcheck.timeout must be between 1s and 5m")
	}
	if len(r.Environment) > 100 {
		return r, fmt.Errorf("at most 100 environment settings are supported")
	}
	for k, v := range r.Environment {
		if k == "" || strings.ContainsAny(k, "=\x00\r\n") || strings.HasPrefix(k, "DEPLOYER_") || strings.ContainsRune(v, 0) {
			return r, fmt.Errorf("invalid or reserved environment variable")
		}
	}
	return r, nil
}

// SafePath resolves symlinks before checking the repository boundary.
func SafePath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("recipe paths must be relative to the repository")
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return "", err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(base, relative))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(base, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("recipe path escapes repository")
	}
	return full, nil
}
