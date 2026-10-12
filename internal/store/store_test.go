package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/filstawski/deployer/internal/domain"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock                   { return &clock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)} }
func open(t *testing.T) (*Store, *clock) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c := newClock()
	s.SetClock(c.now)
	return s, c
}

func TestReopenKeepsState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, _ := s.InstallationID(ctx)
	if _, err := s.CreateProject(ctx, "tok_a", domain.CreateProjectRequest{Name: "web", Repository: "https://github.com/o/web"}, ""); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	again, _ := s.InstallationID(ctx)
	if id == "" || again != id {
		t.Fatalf("installation ID changed: %q -> %q", id, again)
	}
	if p, err := s.GetProject(ctx, "web"); err != nil || p.Repository != "https://github.com/o/web.git" {
		t.Fatalf("project lost after reopen: %+v %v", p, err)
	}
}

func TestTokens(t *testing.T) {
	s, c := open(t)
	ctx := context.Background()
	tok, secret, err := s.CreateToken(ctx, NewToken{Name: "laptop", Scopes: domain.Roles["deployer"], Projects: []string{domain.AllProjects}, TTL: time.Hour, CreatedBy: "local"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Authenticate(ctx, secret)
	if err != nil || got.ID != tok.ID || !got.Has(domain.ScopeDeploymentsWrite) {
		t.Fatalf("Authenticate = %+v, %v", got, err)
	}
	if _, err := s.Authenticate(ctx, domain.NewTokenSecret()); domain.CodeOf(err) != domain.CodeUnauthenticated {
		t.Fatalf("unknown secret: %v", err)
	}
	c.advance(time.Hour)
	if _, err := s.Authenticate(ctx, secret); domain.CodeOf(err) != domain.CodeUnauthenticated {
		t.Fatalf("expired token accepted: %v", err)
	}

	forever, secret2, err := s.CreateToken(ctx, NewToken{Name: "admin", Scopes: []domain.Scope{domain.ScopeAdmin}, Projects: []string{"*"}, CreatedBy: "local"})
	if err != nil || forever.ExpiresAt != nil {
		t.Fatalf("non-expiring token: %+v %v", forever, err)
	}
	if err := s.RevokeToken(ctx, forever.ID, "local"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, secret2); domain.CodeOf(err) != domain.CodeUnauthenticated {
		t.Fatal("revoked token accepted")
	}
	if n, _ := s.CountActiveTokens(ctx); n != 0 {
		t.Fatalf("active tokens = %d", n)
	}
	if _, _, err := s.CreateToken(ctx, NewToken{Name: "x", Scopes: domain.Roles["viewer"], Projects: []string{"prj_missing"}}); domain.CodeOf(err) != domain.CodeInvalidInput {
		t.Fatalf("grant for unknown project accepted: %v", err)
	}
}

func TestProjectIdempotency(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	req := domain.CreateProjectRequest{Name: "web", Repository: "git@github.com:Owner/Web.git"}
	a, err := s.CreateProject(ctx, "tok_1", req, "key-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateProject(ctx, "tok_1", req, "key-1")
	if err != nil || a.ID != b.ID {
		t.Fatalf("idempotent retry: %v %v", b, err)
	}
	if _, err := s.CreateProject(ctx, "tok_1", domain.CreateProjectRequest{Name: "other", Repository: req.Repository}, "key-1"); domain.CodeOf(err) != domain.CodeIdempotencyConflict {
		t.Fatalf("key reuse with different input: %v", err)
	}
	if _, err := s.CreateProject(ctx, "tok_1", req, ""); domain.CodeOf(err) != domain.CodeAlreadyExists {
		t.Fatalf("duplicate name: %v", err)
	}
	// Keys are scoped per actor.
	if _, err := s.CreateProject(ctx, "tok_2", req, "key-1"); domain.CodeOf(err) != domain.CodeAlreadyExists {
		t.Fatalf("another actor's key: %v", err)
	}
}

func addDeployment(t *testing.T, s *Store, project domain.Project, name string) domain.Deployment {
	t.Helper()
	var d domain.Deployment
	err := s.Tx(context.Background(), func(tx *Tx) error {
		d = domain.Deployment{ID: domain.NewID(domain.PrefixDeployment), ProjectID: project.ID, Name: name, Ref: "main", State: domain.StateQueued, TTLSeconds: 3600, CreatedBy: "tok_1", CreatedAt: tx.Now(), UpdatedAt: tx.Now()}
		if err := tx.InsertDeployment(d); err != nil {
			return err
		}
		return tx.InsertOperation(domain.Operation{ID: domain.NewID(domain.PrefixOperation), Kind: domain.OpCreateDeployment, Status: domain.OpQueued, ProjectID: project.ID, DeploymentID: d.ID, Actor: "tok_1", CreatedAt: tx.Now(), UpdatedAt: tx.Now(), Deadline: tx.Now().Add(time.Hour)})
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDeploymentNamesAndUpdates(t *testing.T) {
	s, c := open(t)
	ctx := context.Background()
	p, _ := s.CreateProject(ctx, "tok_1", domain.CreateProjectRequest{Name: "web", Repository: "https://github.com/o/web"}, "")
	d := addDeployment(t, s, p, "design-a")
	err := s.Tx(ctx, func(tx *Tx) error {
		return tx.InsertDeployment(domain.Deployment{ID: "dep_dup", ProjectID: p.ID, Name: "design-a", Ref: "main", State: domain.StateQueued, CreatedAt: tx.Now(), UpdatedAt: tx.Now()})
	})
	if domain.CodeOf(err) != domain.CodeNameTaken {
		t.Fatalf("duplicate live name: %v", err)
	}

	c.advance(time.Minute)
	expires := c.t.Add(time.Hour)
	updated, err := s.UpdateDeployment(ctx, d.ID, func(d *domain.Deployment) error {
		d.State = domain.StateRunning
		d.Commit = "abc"
		d.ExpiresAt = &expires
		d.HealthTimeout = 30 * time.Second
		d.Env = map[string]string{"A": "b"}
		d.Error = domain.Errorf(domain.CodeBuildFailed, "boom")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDeployment(ctx, "design-a")
	if err != nil || got.State != domain.StateRunning || got.Project != "web" || !got.ExpiresAt.Equal(expires) ||
		got.HealthTimeout != 30*time.Second || got.Env["A"] != "b" || got.Error.Code != domain.CodeBuildFailed || !got.UpdatedAt.Equal(c.t) {
		t.Fatalf("round trip: %+v (updated %+v) %v", got, updated, err)
	}

	// Destroying releases the name for a new deployment; lookups by name
	// prefer the live one.
	if _, err := s.UpdateDeployment(ctx, d.ID, func(d *domain.Deployment) error { d.State = domain.StateDestroyed; return nil }); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Minute)
	again := addDeployment(t, s, p, "design-a")
	if got, _ := s.GetDeployment(ctx, "design-a"); got.ID != again.ID {
		t.Fatalf("name lookup returned %s, want live %s", got.ID, again.ID)
	}
	if got, _ := s.GetDeployment(ctx, d.ID); got.State != domain.StateDestroyed {
		t.Fatal("lookup by ID")
	}
	live, _ := s.ListDeployments(ctx, DeploymentFilter{})
	all, _ := s.ListDeployments(ctx, DeploymentFilter{IncludeDestroyed: true, ProjectID: p.ID})
	if len(live) != 1 || len(all) != 2 || all[0].ID != again.ID {
		t.Fatalf("list: live=%d all=%d", len(live), len(all))
	}
	if err := s.DeleteProject(ctx, p.ID, "tok_1"); domain.CodeOf(err) != domain.CodeInvalidState {
		t.Fatalf("deleted project with live deployments: %v", err)
	}
}

func TestOperationClaimOrder(t *testing.T) {
	s, c := open(t)
	ctx := context.Background()
	p, _ := s.CreateProject(ctx, "tok_1", domain.CreateProjectRequest{Name: "web", Repository: "https://github.com/o/web"}, "")
	a := addDeployment(t, s, p, "a")
	c.advance(time.Second)
	b := addDeployment(t, s, p, "b")
	c.advance(time.Second)
	// A second operation for deployment a must wait for the first.
	err := s.Tx(ctx, func(tx *Tx) error {
		return tx.InsertOperation(domain.Operation{ID: "op_second", Kind: domain.OpStopDeployment, Status: domain.OpQueued, ProjectID: p.ID, DeploymentID: a.ID, Actor: "tok_1", CreatedAt: tx.Now(), UpdatedAt: tx.Now(), Deadline: tx.Now().Add(time.Hour)})
	})
	if err != nil {
		t.Fatal(err)
	}
	first, ok, err := s.ClaimOperation(ctx)
	if err != nil || !ok || first.DeploymentID != a.ID || first.Attempts != 1 || first.Status != domain.OpRunning {
		t.Fatalf("first claim: %+v %v %v", first, ok, err)
	}
	second, ok, _ := s.ClaimOperation(ctx)
	if !ok || second.DeploymentID != b.ID {
		t.Fatalf("second claim should skip busy deployment a: %+v", second)
	}
	if _, ok, _ := s.ClaimOperation(ctx); ok {
		t.Fatal("claimed an operation for a busy deployment")
	}
	if err := s.FinishOperation(ctx, first.ID, domain.OpSucceeded, map[string]string{"x": "y"}, nil); err != nil {
		t.Fatal(err)
	}
	third, ok, _ := s.ClaimOperation(ctx)
	if !ok || third.ID != "op_second" {
		t.Fatalf("third claim: %+v", third)
	}
	done, _ := s.GetOperation(ctx, first.ID)
	if done.Status != domain.OpSucceeded || string(done.Result) != `{"x":"y"}` || done.FinishedAt == nil {
		t.Fatalf("finished op: %+v", done)
	}

	// Restart recovery requeues running work and keeps the attempt count.
	if n, err := s.RequeueRunning(ctx); err != nil || n != 2 {
		t.Fatalf("requeue: %d %v", n, err)
	}
	again, _, _ := s.ClaimOperation(ctx)
	if again.Attempts != 2 {
		t.Fatalf("attempts not preserved: %+v", again)
	}

	err = s.Tx(ctx, func(tx *Tx) error {
		return tx.CancelQueuedOperations(a.ID, domain.Errorf(domain.CodeCancelled, "superseded"))
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPrune(t *testing.T) {
	s, c := open(t)
	ctx := context.Background()
	if _, err := s.CreateProject(ctx, "tok_1", domain.CreateProjectRequest{Name: "web", Repository: "https://github.com/o/web"}, "k"); err != nil {
		t.Fatal(err)
	}
	c.advance(48 * time.Hour)
	if err := s.Prune(ctx, 24*time.Hour, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	// After pruning, the key can be reused for a different request.
	if _, err := s.CreateProject(ctx, "tok_1", domain.CreateProjectRequest{Name: "api", Repository: "https://github.com/o/api"}, "k"); err != nil {
		t.Fatal(err)
	}
}
