package deployments

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deployer/internal/config"
	"deployer/internal/domain"
	"deployer/internal/gateway"
	"deployer/internal/operations"
	"deployer/internal/store/sqlite"
)

type fakeRuntime struct {
	Runtime
	builds, starts, stops, removes int
	failProbe, interruptBuild      bool
	now                            *time.Time
}

func (r *fakeRuntime) Publish(context.Context, []gateway.Route) error { return nil }
func (r *fakeRuntime) Upstream(domain.Deployment) string              { return "http://app:8080" }
func (r *fakeRuntime) Build(ctx context.Context, d domain.Deployment, recipe config.Recipe, root string, log io.Writer) (string, error) {
	r.builds++
	*r.now = r.now.Add(5 * time.Minute)
	if r.interruptBuild {
		return "", context.Canceled
	}
	return "sha256:image", nil
}
func (r *fakeRuntime) Start(context.Context, domain.Deployment) error           { r.starts++; return nil }
func (r *fakeRuntime) Stop(context.Context, domain.Deployment) error            { r.stops++; return nil }
func (r *fakeRuntime) Remove(context.Context, domain.Deployment) error          { r.removes++; return nil }
func (r *fakeRuntime) Running(context.Context, domain.Deployment) (bool, error) { return true, nil }
func (r *fakeRuntime) Probe(ctx context.Context, host string) error {
	if r.failProbe {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
func TestInterruptedBuildKeepsCommitAndLeaseStartsAtReadiness(t *testing.T) {
	ctx := context.Background()
	c := config.Defaults()
	c.StateDir = t.TempDir()
	store, err := sqlite.Open(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	store.Now = func() time.Time { return now }
	runtime := &fakeRuntime{now: &now, interruptBuild: true}
	p, _ := store.CreateProject(ctx, "demo", "https://example.com/demo.git", "actor", "")
	o, err := store.QueueDeploy(ctx, domain.DeployInput{Ref: "main", TTLSeconds: 60}, p, "actor", "deploy", "localhost", 8080, 8)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	service := &Service{Store: store, Config: c, Runtime: runtime, Log: slog.Default(), Fetch: func(ctx context.Context, repo, ref, pinned, dest string) (string, error) {
		calls++
		if calls > 1 && pinned != strings.Repeat("a", 40) {
			t.Fatal("retry did not pass recorded commit")
		}
		os.MkdirAll(dest, 0700)
		os.WriteFile(filepath.Join(dest, "Dockerfile"), []byte("FROM scratch"), 0600)
		os.WriteFile(filepath.Join(dest, "deployer.yaml"), []byte("version: 1\nsource:\n  dockerfile: Dockerfile\nhttp:\n  port: 8080\n"), 0600)
		return strings.Repeat("a", 40), nil
	}}
	claimed, err := store.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Handle(ctx, claimed)
	if err == nil {
		t.Fatal("expected interrupted build")
	}
	d, _ := store.Deployment(ctx, o.DeploymentID)
	if d.ExpiresAt != nil || d.Commit == "" {
		t.Fatal("lease started during build or checkpoint missing")
	}
	runtime.interruptBuild = false
	store.Recover(ctx)
	worker := &operations.Worker{Store: store, Handle: service.Handle, Failed: service.Failed, Log: slog.Default()}
	if _, err = worker.Step(ctx); err != nil {
		t.Fatal(err)
	}
	d, _ = store.Deployment(ctx, d.ID)
	if d.State != "running" || !d.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("lease did not begin at readiness: %+v", d)
	}
	// Expiry remains enforceable while another lifecycle operation is queued.
	_, err = store.QueueAction(ctx, d.ID, "stop", "actor", "stop", 0)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if err = service.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	d, _ = store.Deployment(ctx, d.ID)
	if d.State != "expired" || runtime.stops != 1 {
		t.Fatalf("queued action prevented expiration: %+v", d)
	}
}
func TestReadinessFailureBecomesTerminalAndStopsContainer(t *testing.T) {
	ctx := context.Background()
	c := config.Defaults()
	c.StateDir = t.TempDir()
	store, err := sqlite.Open(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	runtime := &fakeRuntime{now: &now, failProbe: true}
	p, _ := store.CreateProject(ctx, "demo", "https://example.com/demo.git", "actor", "")
	o, _ := store.QueueDeploy(ctx, domain.DeployInput{Ref: "main", TTLSeconds: 60}, p, "actor", "deploy", "localhost", 8080, 8)
	d, _ := store.Deployment(ctx, o.DeploymentID)
	d.Image = "sha256:image"
	d.Port = 8080
	d.HealthPath = "/health"
	d.HealthTimeout = "5ms"
	store.SaveDeployment(ctx, d)
	service := &Service{Store: store, Config: c, Runtime: runtime, Log: slog.Default()}
	worker := &operations.Worker{Store: store, Handle: service.Handle, Failed: service.Failed, Log: slog.Default()}
	if _, err = worker.Step(ctx); err != nil {
		t.Fatal(err)
	}
	d, _ = store.Deployment(ctx, d.ID)
	op, _ := store.Operation(ctx, o.ID)
	if d.State != "failed" || op.Error == nil || op.Error.Code != "READINESS_FAILED" || runtime.stops != 1 || d.PurgeAt == nil {
		t.Fatalf("readiness failure not cleaned up: %+v %+v", d, op)
	}
}

func TestActionRetryWhileWorkerHoldsLock(t *testing.T) {
	ctx := context.Background()
	c := config.Defaults()
	c.StateDir = t.TempDir()
	store, err := sqlite.Open(c.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p, _ := store.CreateProject(ctx, "demo", "https://example.com/demo.git", "actor", "")
	o, _ := store.QueueDeploy(ctx, domain.DeployInput{Ref: "main", TTLSeconds: 60}, p, "actor", "create", "localhost", 8080, 8)
	claimed, _ := store.Claim(ctx)
	store.Finish(ctx, claimed, nil, nil)
	service := &Service{Store: store}
	op, err := service.Action(ctx, o.DeploymentID, "stop", "actor", "same", 0)
	if err != nil {
		t.Fatal(err)
	}
	lock := service.lock(o.DeploymentID)
	lock.Lock()
	defer lock.Unlock()
	retry, err := service.Action(ctx, o.DeploymentID, "stop", "actor", "same", 0)
	if err != nil || retry.ID != op.ID {
		t.Fatalf("busy retry lost accepted operation: %+v %v", retry, err)
	}
	if _, err = service.Action(ctx, o.DeploymentID, "destroy", "actor", "different", 0); err == nil {
		t.Fatal("conflicting action was accepted while worker held lock")
	}
}
