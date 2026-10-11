package install

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradePreservesStateAndRollsBackOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[fail], func(t *testing.T) {
			i, o, calls := fixture(t)
			ctx := context.Background()
			if _, e := i.Apply(ctx, o); e != nil {
				t.Fatal(e)
			}
			original, _ := os.ReadFile(i.path("/etc/deployer/server.yaml"))
			token, _ := os.ReadFile(i.path("/var/lib/deployer/operator.token"))
			os.WriteFile(i.path("/var/lib/deployer/state.db"), []byte("database snapshot"), 0600)
			checks := 0
			i.ready = func(context.Context, string, string) error {
				checks++
				if fail && checks == 2 {
					return errors.New("new release failed")
				}
				return nil
			}
			result, err := i.Upgrade(ctx, o.Bundle, "v0.1.0-next", false)
			target, _ := os.Readlink(i.path("/opt/deployer/current"))
			if fail {
				if err == nil || !strings.Contains(err.Error(), "previous release restored") || target != "/opt/deployer/releases/"+o.Version {
					t.Fatal("failed rollback", err, target)
				}
				after, _ := os.ReadFile(i.path("/etc/deployer/server.yaml"))
				if string(after) != string(original) {
					t.Fatal("config not restored")
				}
			} else if err != nil || result.Status != "upgraded" || target != "/opt/deployer/releases/v0.1.0-next" {
				t.Fatal(result, err, target)
			}
			after, _ := os.ReadFile(i.path("/var/lib/deployer/operator.token"))
			if string(after) != string(token) {
				t.Fatal("credential replaced")
			}
			backups, _ := filepath.Glob(i.path("/etc/deployer/backups/*/state.db"))
			if len(backups) != 1 {
				t.Fatal("database backup missing")
			}
			if _, e := os.Stat(i.path("/etc/deployer/upgrade-pending.json")); !os.IsNotExist(e) {
				t.Fatal("pending journal not cleared")
			}
			b, _ := os.ReadFile(i.path("/etc/deployer/install.json"))
			var r receipt
			json.Unmarshal(b, &r)
			expected := "v0.1.0-next"
			if fail {
				expected = o.Version
			}
			if r.Options.Version != expected {
				t.Fatal("receipt mismatch")
			}
			for _, call := range *calls {
				if strings.Contains(call, "nginx") {
					t.Fatal("upgrade touched nginx")
				}
			}
		})
	}
}
func TestUpgradeRefusesUnfinishedJournalAndPreservesCustomConfig(t *testing.T) {
	i, o, _ := fixture(t)
	ctx := context.Background()
	if _, e := i.Apply(ctx, o); e != nil {
		t.Fatal(e)
	}
	path := i.path("/etc/deployer/server.yaml")
	b, _ := os.ReadFile(path)
	b = []byte(strings.Replace(string(b), "max_deployments: 8", "max_deployments: 12", 1))
	os.WriteFile(path, b, 0644)
	journal := i.path("/etc/deployer/upgrade-pending.json")
	os.WriteFile(journal, []byte("{}"), 0600)
	if _, e := i.Upgrade(ctx, o.Bundle, "v0.1.0-next", false); e == nil {
		t.Fatal("ignored interrupted upgrade")
	}
	os.Remove(journal)
	if _, e := i.Upgrade(ctx, o.Bundle, "v0.1.0-next", false); e != nil {
		t.Fatal(e)
	}
	b, _ = os.ReadFile(path)
	if !strings.Contains(string(b), "max_deployments: 12") {
		t.Fatal("custom config lost")
	}
}
