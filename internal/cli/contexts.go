package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"deployer/internal/client"
	"deployer/internal/domain"
	"deployer/internal/sshtunnel"
)

type Connection struct {
	URL        string `json:"url,omitempty"`
	SSH        string `json:"ssh,omitempty"`
	RemotePort int    `json:"remote_port,omitempty"`
	TokenFile  string `json:"token_file"`
}
type Contexts struct {
	Version int                   `json:"version"`
	Current string                `json:"current"`
	Servers map[string]Connection `json:"servers"`
}

func loadContexts(path string) (Contexts, error) {
	c := Contexts{Version: 1, Servers: map[string]Connection{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.Version != 1 {
		return c, fmt.Errorf("unsupported contexts version %d", c.Version)
	}
	if c.Servers == nil {
		c.Servers = map[string]Connection{}
	}
	return c, nil
}
func saveContexts(path string, c Contexts) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".contexts-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func addContext(path, name string, connection Connection) error {
	if err := domain.ValidateName(name); err != nil {
		return err
	}
	if err := connection.validate(); err != nil {
		return err
	}
	abs, err := filepath.Abs(connection.TokenFile)
	if err != nil {
		return err
	}
	c, err := loadContexts(path)
	if err != nil {
		return err
	}
	if _, ok := c.Servers[name]; ok {
		return fmt.Errorf("server context already exists")
	}
	connection.TokenFile = abs
	c.Servers[name] = connection
	if c.Current == "" {
		c.Current = name
	}
	return saveContexts(path, c)
}

func (c Connection) validate() error {
	if c.SSH != "" {
		if c.URL != "" {
			return domain.Fail("INVALID_INPUT", "choose --ssh or --url, not both")
		}
		if err := sshtunnel.Validate(c.SSH, c.RemotePort); err != nil {
			return domain.Fail("INVALID_INPUT", err.Error())
		}
		return nil
	}
	if c.RemotePort != 0 {
		return domain.Fail("INVALID_INPUT", "--remote-port requires --ssh")
	}
	if err := client.ValidateURL(c.URL); err != nil {
		return domain.Fail("INVALID_INPUT", err.Error())
	}
	return nil
}
