package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deployer/internal/api"
	"deployer/internal/client"
	"deployer/internal/config"
	"deployer/internal/domain"
	"deployer/internal/operations"
	"deployer/internal/store/sqlite"
)

func TestCLIToRealAPI(t *testing.T) {
	t.Setenv("DEPLOYER_SERVER", "")
	t.Setenv("DEPLOYER_TOKEN_FILE", "")
	dir := t.TempDir()
	s, err := sqlite.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, secret, err := s.IssueToken(context.Background(), sqlite.TokenInput{Name: "operator", Scopes: []string{"admin"}, Projects: []string{"*"}, TTLSeconds: 3600}, "local", true)
	if err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(dir, "operator.token")
	if err = os.WriteFile(tokenFile, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer((&api.Server{Store: s, Config: config.Defaults(), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}).Handler())
	defer server.Close()
	contexts := filepath.Join(dir, "contexts.json")
	run := func(want int, args ...string) client.Result {
		t.Helper()
		var out, errOut bytes.Buffer
		args = append([]string{"--contexts-file", contexts, "--json"}, args...)
		code := Run(context.Background(), args, &out, &errOut)
		if code != want {
			t.Fatalf("%v: exit %d, want %d\n%s\n%s", args, code, want, out.String(), errOut.String())
		}
		if strings.Contains(out.String(), secret) {
			t.Fatal("credential leaked to stdout")
		}
		var r client.Result
		if err = json.Unmarshal(out.Bytes(), &r); err != nil {
			t.Fatalf("invalid machine output: %s", out.String())
		}
		return r
	}
	run(0, "server", "add", "local", "--url", server.URL, "--credential-file", tokenFile)
	run(0, "server", "status")
	run(3, "--server", "https://another.example.com", "server", "status")
	created := run(0, "project", "add", "portal", "--repo", "https://example.com/portal.git", "--idempotency-key", "create")
	var p domain.Project
	if err = json.Unmarshal(created.Data, &p); err != nil {
		t.Fatal(err)
	}
	run(0, "project", "add", "portal", "--repo", "https://example.com/portal.git", "--idempotency-key", "create")
	agentFile := filepath.Join(dir, "agent.token")
	issued := run(0, "token", "create", "reader", "--output", agentFile, "--scopes", "projects:read", "--projects", p.ID)
	var issue struct {
		Token domain.Token `json:"token"`
	}
	if err = json.Unmarshal(issued.Data, &issue); err != nil {
		t.Fatal(err)
	}
	agentSecret, err := os.ReadFile(agentFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(issued.Data, bytes.TrimSpace(agentSecret)) {
		t.Fatal("new token leaked in output")
	}
	run(0, "--token-file", agentFile, "project", "list")
	run(3, "--token-file", agentFile, "project", "add", "denied", "--repo", "https://example.com/denied.git")
	run(0, "token", "revoke", issue.Token.ID)
	run(3, "--token-file", agentFile, "project", "list")
	queued := run(0, "project", "verify", p.ID, "--idempotency-key", "verify")
	var op sqlite.Operation
	if err = json.Unmarshal(queued.Data, &op); err != nil {
		t.Fatal(err)
	}
	timedOut := run(4, "operation", "inspect", op.ID, "--wait", "--timeout", "10ms")
	if timedOut.Error == nil || !strings.Contains(timedOut.Error.Message, op.ID) {
		t.Fatal("timeout lost operation identity")
	}
	worker := &operations.Worker{Store: s, Verify: func(context.Context, domain.Project) (any, error) { return map[string]bool{"verified": true}, nil }}
	if _, err = worker.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	finished := run(0, "operation", "inspect", op.ID, "--wait")
	if !bytes.Contains(finished.Data, []byte(`"succeeded"`)) {
		t.Fatalf("not complete %s", finished.Data)
	}
	run(2, "deploy", "portal")
}

func TestSSHContextsAndCredentialBinding(t *testing.T) {
	t.Setenv("DEPLOYER_SERVER", "")
	t.Setenv("DEPLOYER_TOKEN_FILE", "")
	dir := t.TempDir()
	contexts := filepath.Join(dir, "contexts.json")
	token := filepath.Join(dir, "token")
	secret := "dpl_" + strings.Repeat("a", 64)
	if err := os.WriteFile(token, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(want int, args ...string) {
		t.Helper()
		var out, errOut bytes.Buffer
		args = append([]string{"--json", "--contexts-file", contexts}, args...)
		if got := Run(context.Background(), args, &out, &errOut); got != want {
			t.Fatalf("%v: code %d wanted %d: %s %s", args, got, want, &out, &errOut)
		}
		if strings.Contains(out.String()+errOut.String(), secret) {
			t.Fatal("token leaked")
		}
		var envelope domain.Envelope
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
	}
	run(0, "server", "add", "pilot", "--ssh", "root@187.77.92.158", "--credential-file", token)
	c, err := loadContexts(contexts)
	if err != nil {
		t.Fatal(err)
	}
	if c.Servers["pilot"].RemotePort != 7420 || c.Servers["pilot"].SSH != "root@187.77.92.158" {
		t.Fatal(c)
	}
	contents, _ := os.ReadFile(contexts)
	if strings.Contains(string(contents), secret) {
		t.Fatal("token stored in contexts")
	}
	run(3, "--server", "http://127.0.0.1:7420", "server", "status")
	run(2, "server", "add", "mixed", "--ssh", "host", "--url", "https://example.com", "--credential-file", token)
	run(2, "server", "add", "bad-port", "--ssh", "host", "--remote-port", "0", "--credential-file", token)
	run(2, "server", "add", "direct-port", "--remote-port", "7420", "--credential-file", token)
	// An explicit URL and explicit token must completely override saved SSH.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("missing credential")
		}
		io.WriteString(w, `{"schema_version":1,"data":{"direct":true}}`)
	}))
	defer srv.Close()
	run(0, "--server", srv.URL, "--token-file", token, "server", "status")
	// Malformed stored contexts are rejected before attempting a connection.
	c.Servers["pilot"] = Connection{SSH: "host", URL: srv.URL, TokenFile: token, RemotePort: 7420}
	if err := saveContexts(contexts, c); err != nil {
		t.Fatal(err)
	}
	run(2, "server", "status")
}
