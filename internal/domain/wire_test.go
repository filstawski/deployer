package domain

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the files in testdata/wire")

// TestWireFixtures keeps testdata/wire in step with the JSON the API sends.
// The Android app's tests decode these files, so renaming a field fails here
// first. After an intentional change, run
//
//	go test ./internal/domain -run TestWireFixtures -update
//
// and update the app's models in android/ to match.
func TestWireFixtures(t *testing.T) {
	at := func(hour, minute int) *time.Time {
		v := time.Date(2026, 10, 28, hour, minute, 0, 0, time.UTC)
		return &v
	}
	ready := at(13, 2)
	expires, purge := ready.Add(24*time.Hour), ready.Add(8*24*time.Hour)
	token := Token{
		ID: "tok_3f9a2c7d1e5b", Name: "pixel", Scopes: Roles["deployer"], Projects: []string{AllProjects},
		CreatedBy: "tok_admin000001", CreatedAt: *at(9, 0),
	}
	running := Deployment{
		ID: "dep_k7m2q9x4abcd", Name: "feat-checkout", ProjectID: "prj_9s6vn7ax49yx", Project: "storefront",
		Ref: "feat/checkout", Commit: "9f3c2e1b7d63b83b6f7e029c4be79244204d87c9", State: StateRunning,
		Hostname: "feat-checkout.previews.example.com", URL: "https://feat-checkout.previews.example.com",
		TTLSeconds: 86400, CreatedBy: token.ID, CreatedAt: *at(13, 0), UpdatedAt: *at(13, 2),
		ReadyAt: ready, ExpiresAt: &expires, PurgeAt: &purge, OperationID: "op_8cf3q2m9xkwz",
		Image: "sha256:3a3d6ed1", Port: 3000, HealthPath: "/healthz",
	}
	building := Deployment{
		ID: "dep_b2x8n4v6qwer", Name: "fix-cart-total", ProjectID: "prj_9s6vn7ax49yx", Project: "storefront",
		Ref: "fix/cart-total", State: StateBuilding,
		Hostname: "fix-cart-total.previews.example.com", URL: "https://fix-cart-total.previews.example.com",
		TTLSeconds: 86400, CreatedBy: token.ID, CreatedAt: *at(14, 25), UpdatedAt: *at(14, 25), OperationID: "op_2hd7s9k3mnbv",
	}
	failed := Deployment{
		ID: "dep_f5c1z7l0poiu", Name: "docs-v2", ProjectID: "prj_4t8wq1ez62rd", Project: "docs-site",
		Ref: "v2", Commit: "c03f5a2e8d1b4f6a9c7e0b3d5f2a8c1e4b7d9f0a", State: StateFailed,
		Error:    &Error{Code: CodeHealthcheckFailed, Message: "GET /healthz did not succeed within 1m0s", Hint: "check the app's logs: deployer logs docs-v2"},
		Hostname: "docs-v2.previews.example.com", URL: "https://docs-v2.previews.example.com",
		TTLSeconds: 86400, CreatedBy: token.ID, CreatedAt: *at(12, 0), UpdatedAt: *at(12, 3), OperationID: "op_7yt5r3e1wqas",
	}
	operation := Operation{
		ID: "op_9kq3w7e2rtyu", Kind: OpUpdateDeployment, Status: OpSucceeded, ProjectID: running.ProjectID,
		DeploymentID: running.ID, Actor: token.ID, Attempts: 1,
		Request:   json.RawMessage(`{"ref":""}`),
		Result:    json.RawMessage(`{"previous_commit":"7d63b83b6f7e029c4be79244204d87c9ef71a460","commit":"9f3c2e1b7d63b83b6f7e029c4be79244204d87c9","changed":true}`),
		CreatedAt: *at(14, 0), UpdatedAt: *at(14, 2), StartedAt: at(14, 0), FinishedAt: at(14, 2), Deadline: *at(16, 0),
	}

	project := Project{ID: running.ProjectID, Name: "storefront", Repository: "https://github.com/acme/storefront.git", CreatedAt: *at(9, 30)}
	fixtures := map[string]any{
		"projects.json": Envelope[[]Project]{RequestID: "req_r5t6y7u8i9op", Data: []Project{project}},
		"branches.json": Envelope[[]Branch]{RequestID: "req_b1n2m3k4j5hg", Data: []Branch{
			{Name: "main", Commit: "77be0104c3f2a9e8d7b6c5a4f3e2d1c0b9a8f7e6", Default: true},
			{Name: "feat/checkout", Commit: running.Commit},
			{Name: "fix/cart-total", Commit: "a41d9e0c7b5f3e1d9c7b5a3f1e9d7c5b3a1f9e7d"},
		}},
		"create_deployment.json": Envelope[CreateDeploymentResponse]{RequestID: "req_c9v8b7n6m5lk", Data: CreateDeploymentResponse{
			Deployment: building,
			Operation: Operation{
				ID: building.OperationID, Kind: OpCreateDeployment, Status: OpQueued, ProjectID: building.ProjectID,
				DeploymentID: building.ID, Actor: token.ID, CreatedAt: *at(14, 25), UpdatedAt: *at(14, 25), Deadline: *at(16, 25),
			},
		}},
		"server.json": Envelope[ServerStatus]{RequestID: "req_k7m2q9x4abcd", Data: ServerStatus{
			Version: "v0.4.0", APIVersion: APIVersion, Domain: "previews.example.com",
			PreviewURL: "https://{name}.previews.example.com", APIURL: "https://api.previews.example.com",
			Deployments: DeploymentsStatus{Live: 3, Max: 10, DefaultTTL: 86400, MaxTTL: 7 * 86400},
			GitHub:      true, Token: token,
		}},
		"deployments.json": Envelope[[]Deployment]{RequestID: "req_m3n8b2v5cxza", Data: []Deployment{running, building, failed}},
		"deployment.json":  Envelope[Deployment]{RequestID: "req_p9o8i7u6ytre", Data: running},
		"operation.json":   Envelope[Operation]{RequestID: "req_q1w2e3r4t5yu", Data: operation},
		"logs.json":        Envelope[LogsResponse]{RequestID: "req_z9x8c7v6bnml", Data: LogsResponse{Logs: "#1 load Dockerfile\n#2 RUN npm ci\n", Offset: 34}},
		"error.json": Envelope[any]{RequestID: "req_a1s2d3f4g5hj", Error: Errorf(CodeInvalidState, "feat-checkout is already stopped").
			WithHint("start it with: deployer start feat-checkout")},
	}
	for name, v := range fixtures {
		got, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, '\n')
		path := filepath.Join("testdata", "wire", name)
		if *update {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v; run go test ./internal/domain -run TestWireFixtures -update", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s no longer matches the API's JSON. If the change is intended, run\n"+
				"  go test ./internal/domain -run TestWireFixtures -update\n"+
				"and update the Android app's models to match.\ngot:\n%s", path, got)
		}
	}
}
