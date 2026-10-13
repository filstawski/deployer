package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// RecipeFile is the project recipe's file name at the repository root.
const RecipeFile = "deployer.yaml"

const maxRecipeSize = 64 << 10

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Recipe is a project's deployer.yaml. It describes one Dockerfile-built HTTP
// service. Unknown fields are rejected rather than silently ignored.
type Recipe struct {
	Version int `yaml:"version"`
	// Project is informational; the server identifies projects by registration.
	Project     string            `yaml:"project,omitempty"`
	Source      RecipeSource      `yaml:"source"`
	HTTP        RecipeHTTP        `yaml:"http"`
	Environment map[string]string `yaml:"environment,omitempty"`
}

// RecipeSource locates the Dockerfile and build context, relative to the
// repository root. They are independent, which supports monorepos.
type RecipeSource struct {
	Dockerfile string `yaml:"dockerfile"`
	Context    string `yaml:"context,omitempty"`
}

// RecipeHTTP describes the HTTP service the gateway routes to.
type RecipeHTTP struct {
	// Service names the routed service; only "web" is supported.
	Service     string      `yaml:"service,omitempty"`
	Port        int         `yaml:"port"`
	Healthcheck Healthcheck `yaml:"healthcheck,omitempty"`
}

// Healthcheck is the readiness check that gates traffic.
type Healthcheck struct {
	Path    string   `yaml:"path,omitempty"`
	Timeout Duration `yaml:"timeout,omitempty"`
}

// ReservedEnvironment lists variables Deployer sets for every deployment.
var ReservedEnvironment = []string{"PORT", "DEPLOYER_URL", "DEPLOYER_DEPLOYMENT_ID", "DEPLOYER_REF", "DEPLOYER_COMMIT"}

// ParseRecipe decodes, defaults, and validates recipe contents. Paths are
// checked against a checkout separately with Resolve.
func ParseRecipe(data []byte) (Recipe, error) {
	r := Recipe{
		Source: RecipeSource{Context: "."},
		HTTP:   RecipeHTTP{Service: "web", Healthcheck: Healthcheck{Path: "/health", Timeout: Duration(90 * time.Second)}},
	}
	if len(data) > maxRecipeSize {
		return r, fmt.Errorf("%s is larger than 64 KiB", RecipeFile)
	}
	if err := decodeStrict(data, &r); err != nil {
		return r, err
	}
	return r, r.validate()
}

var drivePattern = regexp.MustCompile(`^[A-Za-z]:`)

func (r Recipe) validate() error {
	hc := r.HTTP.Healthcheck
	switch {
	case r.Version != 1:
		return fmt.Errorf("version: must be 1")
	case r.Source.Dockerfile == "":
		return fmt.Errorf("source.dockerfile: is required, for example Dockerfile")
	case r.HTTP.Service != "web":
		return fmt.Errorf("http.service: only \"web\" is supported")
	case r.HTTP.Port < 1 || r.HTTP.Port > 65535:
		return fmt.Errorf("http.port: must be the port your app listens on inside the container (1-65535)")
	case !strings.HasPrefix(hc.Path, "/") || strings.HasPrefix(hc.Path, "//") || strings.ContainsAny(hc.Path, " \r\n#?"):
		return fmt.Errorf("http.healthcheck.path: must be an absolute path such as /health")
	case hc.Timeout.D() < time.Second || hc.Timeout.D() > 5*time.Minute:
		return fmt.Errorf("http.healthcheck.timeout: must be between 1s and 5m")
	case len(r.Environment) > 100:
		return fmt.Errorf("environment: at most 100 variables are supported")
	}
	// The server builds on Linux, so a recipe's paths mean the same on every
	// platform: relative, with forward slashes.
	for _, p := range []struct{ field, path string }{{"source.dockerfile", r.Source.Dockerfile}, {"source.context", r.Source.Context}} {
		switch {
		case p.path == "" || path.IsAbs(p.path) || drivePattern.MatchString(p.path):
			return fmt.Errorf("%s: must be a path relative to the repository root", p.field)
		case strings.Contains(p.path, `\`):
			return fmt.Errorf("%s: use forward slashes, as in %s", p.field, strings.ReplaceAll(p.path, `\`, "/"))
		}
	}
	for k, v := range r.Environment {
		switch {
		case !envName.MatchString(k):
			return fmt.Errorf("environment: %q is not a valid variable name", k)
		case strings.HasPrefix(k, "DEPLOYER_") || k == "PORT":
			return fmt.Errorf("environment: %s is set by Deployer and cannot be overridden", k)
		case strings.ContainsRune(v, 0):
			return fmt.Errorf("environment: %s contains a NUL byte", k)
		}
	}
	return nil
}

// ResolvedRecipe is a recipe whose paths were checked against a checkout.
type ResolvedRecipe struct {
	Recipe
	Root       string
	Dockerfile string // Absolute path to the Dockerfile.
	Context    string // Absolute path to the build context directory.
}

// LoadRecipe reads deployer.yaml from a checkout and resolves its paths.
func LoadRecipe(root string) (ResolvedRecipe, error) {
	path, err := SafePath(root, RecipeFile)
	if errors.Is(err, fs.ErrNotExist) {
		return ResolvedRecipe{}, fmt.Errorf("%s not found at the repository root; run deployer init to create one", RecipeFile)
	}
	if err != nil {
		return ResolvedRecipe{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return ResolvedRecipe{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxRecipeSize+1))
	if err != nil {
		return ResolvedRecipe{}, err
	}
	r, err := ParseRecipe(data)
	if err != nil {
		return ResolvedRecipe{}, fmt.Errorf("%s: %w", RecipeFile, err)
	}
	return r.Resolve(root)
}

// Resolve checks that the Dockerfile and context exist inside root.
func (r Recipe) Resolve(root string) (ResolvedRecipe, error) {
	out := ResolvedRecipe{Recipe: r, Root: root}
	var err error
	if out.Dockerfile, err = SafePath(root, r.Source.Dockerfile); err != nil {
		return out, fmt.Errorf("%s: source.dockerfile: %w", RecipeFile, err)
	}
	if st, err := os.Stat(out.Dockerfile); err != nil || !st.Mode().IsRegular() {
		return out, fmt.Errorf("%s: source.dockerfile: %s is not a file", RecipeFile, r.Source.Dockerfile)
	}
	if out.Context, err = SafePath(root, r.Source.Context); err != nil {
		return out, fmt.Errorf("%s: source.context: %w", RecipeFile, err)
	}
	if st, err := os.Stat(out.Context); err != nil || !st.IsDir() {
		return out, fmt.Errorf("%s: source.context: %s is not a directory", RecipeFile, r.Source.Context)
	}
	return out, nil
}

// SafePath resolves relative inside root, following symlinks, and fails if
// the result escapes root. Repository content is untrusted input.
func SafePath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("path must be relative to the repository root")
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if base, err = filepath.Abs(base); err != nil {
		return "", err
	}
	full, err := filepath.EvalSymlinks(filepath.Join(base, relative))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%s does not exist: %w", relative, fs.ErrNotExist)
		}
		return "", err
	}
	rel, err := filepath.Rel(base, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s points outside the repository", relative)
	}
	return full, nil
}
