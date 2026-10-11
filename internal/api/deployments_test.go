package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"deployer/internal/config"
	"deployer/internal/deployments"
	"deployer/internal/domain"
	"deployer/internal/gateway"
	"deployer/internal/store/sqlite"
)

type routeSink struct{ deployments.Runtime }

func (routeSink) Publish(context.Context, []gateway.Route) error { return nil }
func TestDeploymentScopesAndQueueContract(t *testing.T) {
	store, _, admin := fixture(t)
	ctx := context.Background()
	c := config.Defaults()
	c.Runtime.Enabled = true
	service := &deployments.Service{Store: store, Config: c, Runtime: routeSink{}, Log: slog.Default()}
	h := (&Server{Store: store, Config: c, Deployments: service, Log: slog.Default()}).Handler()
	one, _ := store.CreateProject(ctx, "one", "https://example.com/one.git", "actor", "")
	two, _ := store.CreateProject(ctx, "two", "https://example.com/two.git", "actor", "")
	_, reader, _ := store.IssueToken(ctx, sqlite.TokenInput{Name: "reader", Scopes: []string{"deployments:read"}, Projects: []string{one.ID}, TTLSeconds: 60}, "actor", false)
	_, writer, _ := store.IssueToken(ctx, sqlite.TokenInput{Name: "writer", Scopes: []string{"deployments:read", "deployments:write"}, Projects: []string{one.ID}, TTLSeconds: 60}, "actor", false)
	body := `{"project":"one","ref":"design/a","ttl_seconds":60}`
	for _, tc := range []struct {
		token string
		code  int
	}{{"", 401}, {reader, 403}, {writer, 202}} {
		w := request(h, "POST", "/v1/deployments", tc.token, body)
		if w.Code != tc.code {
			t.Fatalf("deploy: %d %s", w.Code, w.Body)
		}
	}
	w := request(h, "POST", "/v1/deployments", writer, body)
	var response struct{ Data sqlite.Operation }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	op := response.Data
	if w = request(h, "GET", "/v1/operations/"+op.ID, reader, ""); w.Code != 200 {
		t.Fatalf("deployment-only reader cannot inspect operation: %s", w.Body)
	}
	d, _ := store.Deployment(ctx, op.DeploymentID)
	d.Environment = map[string]string{"SETTING": "should-not-appear"}
	store.SaveDeployment(ctx, d)
	if w = request(h, "GET", "/v1/deployments/"+d.ID, reader, ""); w.Code != 200 || strings.Contains(w.Body.String(), "should-not-appear") {
		t.Fatalf("inspect leaks settings %s", w.Body)
	}
	other, err := store.QueueDeploy(ctx, domain.DeployInput{Ref: "main", TTLSeconds: 60}, two, "local", "other", "localhost", 8080, 8)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/deployments/" + other.DeploymentID, "/v1/deployments/" + other.DeploymentID + "/logs", "/v1/operations/" + other.ID} {
		if w = request(h, "GET", path, reader, ""); w.Code != 404 {
			t.Fatalf("cross-project read %s: %d", path, w.Code)
		}
	}
	if w = request(h, "GET", "/v1/deployments", reader, ""); w.Code != 200 || strings.Contains(w.Body.String(), other.DeploymentID) {
		t.Fatalf("list leaked another project %s", w.Body)
	}
	if w = request(h, "POST", "/v1/deployments/"+other.DeploymentID+"/actions/stop", writer, `{}`); w.Code != 404 {
		t.Fatalf("cross-project mutation: %d", w.Code)
	}
	if w = request(h, "POST", "/v1/deployments", admin, `{"project":"one","ref":"main","ttl_seconds":60,"privileged":true}`); w.Code != 400 {
		t.Fatalf("unsupported setting accepted %d", w.Code)
	}
}
