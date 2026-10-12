package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/secretfile"
)

// savedContext is a server the CLI has logged in to. The token itself lives
// in a separate owner-only file.
type savedContext struct {
	URL       string `json:"url"`
	APIPort   int    `json:"api_port,omitempty"` // Remote API port, for ssh:// URLs.
	TokenFile string `json:"token_file"`
}

type contexts struct {
	Current  string                   `json:"current,omitempty"`
	Contexts map[string]*savedContext `json:"contexts"`
}

var contextName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func (e Env) contextsPath() string { return filepath.Join(e.ConfigDir, "config.json") }

func (e Env) tokenPath(name string) string {
	return filepath.Join(e.ConfigDir, "tokens", name+".token")
}

func (e Env) loadContexts() (*contexts, error) {
	c := &contexts{Contexts: map[string]*savedContext{}}
	data, err := os.ReadFile(e.contextsPath())
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("%s: %w", e.contextsPath(), err)
	}
	if c.Contexts == nil {
		c.Contexts = map[string]*savedContext{}
	}
	return c, nil
}

func (e Env) saveContexts(c *contexts) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return secretfile.Replace(e.contextsPath(), string(data))
}

// names returns the context names in order.
func (c *contexts) names() []string {
	names := make([]string, 0, len(c.Contexts))
	for name := range c.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c *contexts) get(name string) (*savedContext, error) {
	ctx, ok := c.Contexts[name]
	if !ok {
		return nil, domain.Errorf(domain.CodeNotFound, "no saved context named %q", name).WithHint("see saved contexts with: deployer context list")
	}
	return ctx, nil
}
