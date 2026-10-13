package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigDir(t *testing.T) {
	home := filepath.FromSlash("/home/ada")
	for _, c := range []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"darwin", nil, "/home/ada/.config/deployer"},
		{"linux", map[string]string{"XDG_CONFIG_HOME": "/xdg"}, "/xdg/deployer"},
		{"windows", map[string]string{"APPDATA": "/appdata"}, "/appdata/deployer"},
		{"windows", map[string]string{"APPDATA": "/appdata", "XDG_CONFIG_HOME": "/xdg"}, "/xdg/deployer"},
		{"windows", map[string]string{"APPDATA": "/appdata", "DEPLOYER_CONFIG_DIR": "/custom"}, "/custom"},
	} {
		got := configDir(func(k string) string { return c.env[k] }, c.goos, home)
		if got != filepath.FromSlash(c.want) {
			t.Errorf("%s %v: %s, want %s", c.goos, c.env, got, c.want)
		}
	}
}

func TestSupportsLinks(t *testing.T) {
	for env, want := range map[string]bool{"TERM_PROGRAM=iTerm.app": true, "WT_SESSION=1b2c": true, "TERM=xterm-256color": false} {
		name, value, _ := strings.Cut(env, "=")
		e := Env{Getenv: func(k string) string {
			if k == name {
				return value
			}
			return ""
		}}
		if e.supportsLinks() != want {
			t.Errorf("%s: links %v", env, !want)
		}
	}
}
