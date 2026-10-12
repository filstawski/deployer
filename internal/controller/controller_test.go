package controller

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/filstawski/deployer/internal/config"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/fakes"
	"github.com/filstawski/deployer/internal/operations"
	"github.com/filstawski/deployer/internal/store"
)

// harness ----------------------------------------------------------------

type harness struct {
	t       *testing.T
	ctrl    *Controller
	store   *store.Store
	runtime *fakes.Runtime
	router  *fakes.Router
	source  *fakes.Source
	project domain.Project
	now     time.Time
	mu      sync.Mutex
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cfg := config.Defaults()
	cfg.Domain = "previews.test"
	cfg.StateDir = t.TempDir()
	cfg.Deployments.MaxLive = 3
	s, err := store.Open(cfg.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	h := &harness{t: t, store: s, runtime: fakes.NewRuntime(), router: fakes.NewRouter(), source: fakes.NewSource(), now: time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)}
	s.SetClock(h.clock)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.ctrl = New(Deps{Store: s, Config: cfg, Runtime: h.runtime, Router: h.router, Source: h.source, Log: log, ProbeInterval: 10 * time.Millisecond})
	d := operations.New(s, log, operations.Options{PollInterval: 5 * time.Millisecond})
	h.ctrl.Register(d)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	h.project, err = s.CreateProject(context.Background(), "tok_1", domain.CreateProjectRequest{Name: "shop", Repository: "https://github.com/acme/shop"}, "")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) clock() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.now
}

func (h *harness) advance(d time.Duration) {
	h.mu.Lock()
	h.now = h.now.Add(d)
	h.mu.Unlock()
}

func (h *harness) deploy(req domain.CreateDeploymentRequest) domain.CreateDeploymentResponse {
	h.t.Helper()
	resp, err := h.ctrl.Deploy(context.Background(), "tok_1", h.project, req, "")
	if err != nil {
		h.t.Fatal(err)
	}
	return resp
}

func (h *harness) wait(opID string) domain.Operation {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := h.store.GetOperation(context.Background(), opID)
		if err != nil {
			h.t.Fatal(err)
		}
		if op.Status.Done() {
			return op
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("operation %s still %s", opID, op.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (h *harness) get(id string) domain.Deployment {
	h.t.Helper()
	d, err := h.ctrl.Deployment(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	return d
}

// tests ------------------------------------------------------------------

func TestDeploymentLifecycle(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	resp := h.deploy(domain.CreateDeploymentRequest{Ref: "design/a", Name: "design-a", TTLSeconds: 3600})
	if resp.Deployment.URL != "https://design-a.previews.test" || resp.Deployment.State != domain.StateQueued {
		t.Fatalf("accepted deployment: %+v", resp.Deployment)
	}
	if r, _ := h.router.Route("design-a.previews.test"); r.State != domain.StateQueued {
		t.Fatalf("hostname not reserved with a progress page: %+v", r)
	}
	if op := h.wait(resp.Operation.ID); op.Status != domain.OpSucceeded {
		t.Fatalf("create: %+v", op.Error)
	}
	d := h.get("design-a")
	if d.State != domain.StateRunning || d.Commit == "" || d.Image == "" || !d.ExpiresAt.Equal(h.now.Add(time.Hour)) || !d.PurgeAt.Equal(h.now.Add(time.Hour+7*24*time.Hour)) {
		t.Fatalf("running deployment: %+v", d)
	}
	env := h.runtime.Env(d.ID)
	if env["PORT"] != "3000" || env["NODE_ENV"] != "production" || env["DEPLOYER_URL"] != d.URL || env["DEPLOYER_COMMIT"] != d.Commit {
		t.Fatalf("container environment: %v", env)
	}
	if r, _ := h.router.Route(d.Hostname); r.State != domain.StateRunning || r.Upstream == "" || !r.ExpiresAt.Equal(*d.ExpiresAt) {
		t.Fatalf("route: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(h.ctrl.dir(d.ID), "source")); !os.IsNotExist(err) {
		t.Fatal("source checkout kept after the build")
	}

	op, err := h.ctrl.Stop(ctx, "tok_1", d.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	h.wait(op.ID)
	if d = h.get(d.ID); d.State != domain.StateStopped || h.runtime.IsRunning(d.ID) {
		t.Fatalf("stop: %s running=%v", d.State, h.runtime.IsRunning(d.ID))
	}
	if _, err := h.ctrl.Stop(ctx, "tok_1", d.ID, ""); domain.CodeOf(err) != domain.CodeInvalidState {
		t.Fatalf("stopping a stopped deployment: %v", err)
	}

	op, _ = h.ctrl.Start(ctx, "tok_1", d.ID, "")
	h.wait(op.ID)
	if d = h.get(d.ID); d.State != domain.StateRunning || h.runtime.Builds() != 1 {
		t.Fatalf("start reused the image: %s builds=%d", d.State, h.runtime.Builds())
	}

	before := *d.ExpiresAt
	if d, err = h.ctrl.Extend(ctx, "tok_1", d.ID, 2*time.Hour); err != nil || !d.ExpiresAt.Equal(before.Add(2*time.Hour)) {
		t.Fatalf("extend: %v %v", d.ExpiresAt, err)
	}
	if _, err = h.ctrl.Extend(ctx, "tok_1", d.ID, 30*24*time.Hour); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Fatalf("extend beyond max_ttl: %v", err)
	}

	op, _ = h.ctrl.Destroy(ctx, "tok_1", d.ID, "")
	h.wait(op.ID)
	if d = h.get(d.ID); d.State != domain.StateDestroyed || h.runtime.HasImage(d.ID) {
		t.Fatalf("destroy: %+v", d)
	}
	if _, ok := h.router.Route(d.Hostname); ok {
		t.Fatal("route kept after destroy")
	}
	// The name is free again.
	again := h.deploy(domain.CreateDeploymentRequest{Ref: "design/a", Name: "design-a"})
	h.wait(again.Operation.ID)
}

func TestFailures(t *testing.T) {
	h := newHarness(t)

	h.runtime.Set(func(r *fakes.Runtime) { r.BuildErr = domain.Errorf(domain.CodeBuildFailed, "the image build failed") })
	resp := h.deploy(domain.CreateDeploymentRequest{Ref: "main"})
	op := h.wait(resp.Operation.ID)
	d := h.get(resp.Deployment.ID)
	if op.Error.Code != domain.CodeBuildFailed || d.State != domain.StateFailed || d.Error.Code != domain.CodeBuildFailed || d.PurgeAt == nil {
		t.Fatalf("build failure: op=%+v deployment=%+v", op.Error, d)
	}
	if !strings.Contains(op.Error.Hint, "deployer logs "+d.Name+" --build") {
		t.Fatalf("build failure hint: %q", op.Error.Hint)
	}
	logs, err := h.ctrl.Logs(context.Background(), d, LogsRequest{Source: LogsBuild, Offset: -1, Lines: 1})
	if err != nil || logs.Logs != "step 2/2\n" {
		t.Fatalf("build log tail: %q %v", logs.Logs, err)
	}
	logs, _ = h.ctrl.Logs(context.Background(), d, LogsRequest{Source: LogsBuild, Offset: 0})
	if !strings.HasPrefix(logs.Logs, "Building main at ") || logs.Offset != int64(len(logs.Logs)) {
		t.Fatalf("build log from offset: %q %d", logs.Logs, logs.Offset)
	}
	h.runtime.Set(func(r *fakes.Runtime) { r.BuildErr = nil })

	resp = h.deploy(domain.CreateDeploymentRequest{Ref: "missing"})
	if op := h.wait(resp.Operation.ID); op.Error.Code != domain.CodeSourceFailed {
		t.Fatalf("missing ref: %+v", op.Error)
	}

	h.source.SetRecipe("")
	resp = h.deploy(domain.CreateDeploymentRequest{Ref: "main"})
	if op := h.wait(resp.Operation.ID); op.Error.Code != domain.CodeInvalidRecipe || !strings.Contains(op.Error.Message, "deployer init") {
		t.Fatalf("missing recipe: %+v", op.Error)
	}
}

func TestHealthcheckAndCrash(t *testing.T) {
	h := newHarness(t)
	h.router.SetUnhealthy(true)
	resp := h.deploy(domain.CreateDeploymentRequest{Ref: "main"})
	op := h.wait(resp.Operation.ID)
	d := h.get(resp.Deployment.ID)
	if op.Error.Code != domain.CodeHealthcheckFailed || !strings.Contains(op.Error.Hint, "0.0.0.0:3000") || d.State != domain.StateFailed || h.runtime.IsRunning(d.ID) {
		t.Fatalf("health check failure: %+v %+v", op.Error, d)
	}

	h.runtime.Set(func(r *fakes.Runtime) { r.ExitOnStart = true })
	resp = h.deploy(domain.CreateDeploymentRequest{Ref: "main"})
	if op := h.wait(resp.Operation.ID); op.Error.Code != domain.CodeContainerExited {
		t.Fatalf("exit during startup: %+v", op.Error)
	}
}

func TestDestroyCancelsBuild(t *testing.T) {
	h := newHarness(t)
	resp := h.deploy(domain.CreateDeploymentRequest{Ref: "main", Name: "slow"})
	h.runtime.Set(func(r *fakes.Runtime) { r.BlockBuild = resp.Deployment.ID })
	for h.get("slow").State != domain.StateBuilding {
		time.Sleep(5 * time.Millisecond)
	}
	destroy, err := h.ctrl.Destroy(context.Background(), "tok_1", "slow", "")
	if err != nil {
		t.Fatal(err)
	}
	// A second destroy returns the pending one.
	if again, _ := h.ctrl.Destroy(context.Background(), "tok_1", "slow", ""); again.ID != destroy.ID {
		t.Fatalf("duplicate destroy operation %s != %s", again.ID, destroy.ID)
	}
	if op := h.wait(resp.Operation.ID); op.Status != domain.OpCancelled {
		t.Fatalf("build not cancelled: %s", op.Status)
	}
	h.wait(destroy.ID)
	if d := h.get(resp.Deployment.ID); d.State != domain.StateDestroyed {
		t.Fatalf("after destroy: %s", d.State)
	}
}

func TestExpiryExtendAndPurge(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	resp := h.deploy(domain.CreateDeploymentRequest{Ref: "main", TTLSeconds: 600})
	h.wait(resp.Operation.ID)
	id := resp.Deployment.ID

	h.advance(11 * time.Minute)
	h.ctrl.Reconcile(ctx)
	if d := h.get(id); d.State != domain.StateExpired || h.runtime.IsRunning(id) {
		t.Fatalf("expiry: %s running=%v", d.State, h.runtime.IsRunning(id))
	}
	if _, err := h.ctrl.Start(ctx, "tok_1", id, ""); domain.CodeOf(err) != domain.CodeInvalidState {
		t.Fatalf("started an expired deployment: %v", err)
	}
	d, err := h.ctrl.Extend(ctx, "tok_1", id, time.Hour)
	if err != nil || d.State != domain.StateStopped || !d.ExpiresAt.Equal(h.now.Add(time.Hour)) {
		t.Fatalf("renew expired deployment: %+v %v", d, err)
	}
	op, err := h.ctrl.Start(ctx, "tok_1", id, "")
	if err != nil {
		t.Fatal(err)
	}
	h.wait(op.ID)

	h.advance(time.Hour + 8*24*time.Hour)
	h.ctrl.Reconcile(ctx) // Expires.
	h.ctrl.Reconcile(ctx) // Purges.
	deadline := time.Now().Add(5 * time.Second)
	for h.get(id).State != domain.StateDestroyed {
		if time.Now().After(deadline) {
			t.Fatalf("not purged: %s", h.get(id).State)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAdmission(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	req := domain.CreateDeploymentRequest{Ref: "feature/Login", TTLSeconds: 3600}
	a, err := h.ctrl.Deploy(ctx, "tok_1", h.project, req, "key")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a.Deployment.Name, "shop-feature-login-") {
		t.Fatalf("generated name: %s", a.Deployment.Name)
	}
	b, err := h.ctrl.Deploy(ctx, "tok_1", h.project, req, "key")
	if err != nil || b.Deployment.ID != a.Deployment.ID || b.Operation.ID != a.Operation.ID {
		t.Fatalf("idempotent retry created another deployment: %v", err)
	}
	if _, err := h.ctrl.Deploy(ctx, "tok_1", h.project, domain.CreateDeploymentRequest{Ref: "main"}, "key"); domain.CodeOf(err) != domain.CodeIdempotencyConflict {
		t.Fatalf("key reuse: %v", err)
	}
	if _, err := h.ctrl.Deploy(ctx, "tok_1", h.project, domain.CreateDeploymentRequest{Ref: "main", Name: a.Deployment.Name}, ""); domain.CodeOf(err) != domain.CodeNameTaken {
		t.Fatalf("taken name: %v", err)
	}
	if _, err := h.ctrl.Deploy(ctx, "tok_1", h.project, domain.CreateDeploymentRequest{Ref: "main", TTLSeconds: 30 * 24 * 3600}, ""); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Fatalf("lifetime above max: %v", err)
	}
	h.deploy(domain.CreateDeploymentRequest{Ref: "main"})
	h.deploy(domain.CreateDeploymentRequest{Ref: "main"})
	if _, err := h.ctrl.Deploy(ctx, "tok_1", h.project, domain.CreateDeploymentRequest{Ref: "main"}, ""); domain.CodeOf(err) != domain.CodeQuotaExceeded {
		t.Fatalf("capacity: %v", err)
	}
}

func TestRecovery(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	resp := h.deploy(domain.CreateDeploymentRequest{Ref: "main", Name: "web"})
	h.wait(resp.Operation.ID)

	// After a reboot the container is gone; the boot pass restarts it.
	h.runtime.Stop(ctx, resp.Deployment.ID)
	h.ctrl.reconcile(ctx, true)
	restart := h.get("web").OperationID
	if restart == resp.Operation.ID {
		t.Fatal("boot pass did not queue a restart")
	}
	if op := h.wait(restart); op.Status != domain.OpSucceeded || !h.runtime.IsRunning(resp.Deployment.ID) {
		t.Fatal("not restarted after boot")
	}

	// During normal operation an exited app is reported, not restarted.
	h.runtime.Stop(ctx, resp.Deployment.ID)
	h.ctrl.Reconcile(ctx)
	if d := h.get("web"); d.State != domain.StateFailed || d.Error.Code != domain.CodeContainerExited {
		t.Fatalf("crash: %+v", d)
	}

	// A deployment stuck in progress without an operation is failed.
	if _, err := h.store.UpdateDeployment(ctx, "web", func(d *domain.Deployment) error { d.State = domain.StateBuilding; return nil }); err != nil {
		t.Fatal(err)
	}
	h.ctrl.Reconcile(ctx)
	if d := h.get("web"); d.State != domain.StateFailed || d.Error.Code != domain.CodeInterrupted {
		t.Fatalf("orphaned in-progress deployment: %+v", d)
	}
}

func TestVerifyProject(t *testing.T) {
	h := newHarness(t)
	op, err := h.ctrl.VerifyProject(context.Background(), "tok_1", h.project, "")
	if err != nil {
		t.Fatal(err)
	}
	if op = h.wait(op.ID); op.Status != domain.OpSucceeded || !strings.Contains(string(op.Result), "head_commit") {
		t.Fatalf("verify: %+v", op)
	}
}

func TestReconcilerRespectsOperationsInFlight(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	resp := h.deploy(domain.CreateDeploymentRequest{Ref: "main", Name: "web"})
	h.wait(resp.Operation.ID)
	id := resp.Deployment.ID

	// While a handler holds the deployment, the reconciler leaves it alone,
	// even though its container is down.
	h.runtime.Stop(ctx, id)
	m := h.ctrl.lock(id)
	m.Lock()
	h.ctrl.Reconcile(ctx)
	if d := h.get(id); d.State != domain.StateRunning {
		t.Fatalf("reconciled a locked deployment: %s", d.State)
	}
	m.Unlock()

	// A stale snapshot cannot overwrite newer state: the deployment was
	// stopped after the reconciler read it as running.
	stale := h.get(id)
	if _, err := h.store.UpdateDeployment(ctx, id, func(d *domain.Deployment) error { d.State = domain.StateStopped; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := h.ctrl.reconcileOne(ctx, stale, false); err != nil {
		t.Fatal(err)
	}
	if d := h.get(id); d.State != domain.StateStopped || d.Error != nil {
		t.Fatalf("stale reconcile overwrote state: %s %v", d.State, d.Error)
	}

	// Likewise, an extension that lands after the snapshot wins over expiry.
	h.advance(25 * time.Hour)
	stale = h.get(id)
	if _, err := h.ctrl.Extend(ctx, "tok_1", id, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := h.ctrl.reconcileOne(ctx, stale, false); err != nil {
		t.Fatal(err)
	}
	if d := h.get(id); d.State != domain.StateStopped {
		t.Fatalf("expired despite extension: %s", d.State)
	}
}
