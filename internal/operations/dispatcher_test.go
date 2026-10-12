package operations

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/store"
)

func setup(t *testing.T) (*store.Store, domain.Project) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	p, err := s.CreateProject(context.Background(), "tok", domain.CreateProjectRequest{Name: "web", Repository: "https://github.com/o/web"}, "")
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

func enqueue(t *testing.T, s *store.Store, p domain.Project, kind domain.OperationKind, deployment string, deadline time.Duration) domain.Operation {
	t.Helper()
	var op domain.Operation
	err := s.Tx(context.Background(), func(tx *store.Tx) error {
		op = domain.Operation{ID: domain.NewID(domain.PrefixOperation), Kind: kind, Status: domain.OpQueued, ProjectID: p.ID, DeploymentID: deployment, Actor: "tok", CreatedAt: tx.Now(), UpdatedAt: tx.Now(), Deadline: tx.Now().Add(deadline)}
		return tx.InsertOperation(op)
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond) // Distinct creation times keep the order deterministic.
	return op
}

func waitFor(t *testing.T, s *store.Store, id string, want domain.OperationStatus) domain.Operation {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		op, err := s.GetOperation(context.Background(), id)
		if err == nil && op.Status == want {
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation %s: status %s, want %s", id, op.Status, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func start(t *testing.T, d *Dispatcher) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return cancel
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestOrderingAndConcurrency(t *testing.T) {
	s, p := setup(t)
	release := make(chan struct{})
	var mu sync.Mutex
	var order []string
	d := New(s, quiet, Options{PollInterval: 10 * time.Millisecond})
	d.Handle(domain.OpStopDeployment, func(ctx context.Context, op domain.Operation) (any, error) {
		mu.Lock()
		order = append(order, op.DeploymentID+":"+op.ID)
		mu.Unlock()
		if op.DeploymentID == "dep_a" {
			<-release
		}
		return nil, nil
	})
	a1 := enqueue(t, s, p, domain.OpStopDeployment, "dep_a", time.Hour)
	a2 := enqueue(t, s, p, domain.OpStopDeployment, "dep_a", time.Hour)
	b1 := enqueue(t, s, p, domain.OpStopDeployment, "dep_b", time.Hour)
	start(t, d)

	// dep_b runs while dep_a's first operation blocks; dep_a's second waits.
	waitFor(t, s, b1.ID, domain.OpSucceeded)
	if op, _ := s.GetOperation(context.Background(), a2.ID); op.Status != domain.OpQueued {
		t.Fatalf("second operation of a busy deployment started: %s", op.Status)
	}
	close(release)
	waitFor(t, s, a2.ID, domain.OpSucceeded)
	mu.Lock()
	defer mu.Unlock()
	if order[0] != "dep_a:"+a1.ID || order[2] != "dep_a:"+a2.ID {
		t.Fatalf("order: %v", order)
	}
}

func TestFailureCancellationAndTimeout(t *testing.T) {
	s, p := setup(t)
	var mu sync.Mutex
	failures := map[string]domain.Code{}
	d := New(s, quiet, Options{PollInterval: 10 * time.Millisecond})
	d.OnFailure(func(ctx context.Context, op domain.Operation, f *domain.Error) {
		mu.Lock()
		failures[op.ID] = f.Code
		mu.Unlock()
	})
	started := make(chan struct{})
	d.Handle(domain.OpCreateDeployment, func(ctx context.Context, op domain.Operation) (any, error) {
		switch op.DeploymentID {
		case "dep_block":
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		case "dep_fail":
			return nil, domain.Errorf(domain.CodeBuildFailed, "boom")
		case "dep_bug":
			return nil, errors.New("unexpected")
		default:
			<-ctx.Done()
			return nil, ctx.Err()
		}
	})
	d.Handle(domain.OpVerifyProject, func(ctx context.Context, op domain.Operation) (any, error) {
		return domain.VerifyResult{HeadCommit: "abc"}, nil
	})
	block := enqueue(t, s, p, domain.OpCreateDeployment, "dep_block", time.Hour)
	fail := enqueue(t, s, p, domain.OpCreateDeployment, "dep_fail", time.Hour)
	bug := enqueue(t, s, p, domain.OpCreateDeployment, "dep_bug", time.Hour)
	slow := enqueue(t, s, p, domain.OpCreateDeployment, "dep_slow", 100*time.Millisecond)
	verify := enqueue(t, s, p, domain.OpVerifyProject, "", time.Hour)
	unknown := enqueue(t, s, p, domain.OpStartDeployment, "dep_x", time.Hour)
	start(t, d)

	<-started
	d.CancelOperation(block.ID)
	if op := waitFor(t, s, block.ID, domain.OpCancelled); op.Error.Code != domain.CodeCancelled {
		t.Fatalf("cancelled: %+v", op.Error)
	}
	if op := waitFor(t, s, fail.ID, domain.OpFailed); op.Error.Code != domain.CodeBuildFailed {
		t.Fatalf("domain failure: %+v", op.Error)
	}
	if op := waitFor(t, s, bug.ID, domain.OpFailed); op.Error.Code != domain.CodeInternal {
		t.Fatalf("unexpected error should be internal: %+v", op.Error)
	}
	if op := waitFor(t, s, slow.ID, domain.OpFailed); op.Error.Code != domain.CodeTimeout {
		t.Fatalf("timeout: %+v", op.Error)
	}
	if op := waitFor(t, s, verify.ID, domain.OpSucceeded); string(op.Result) != `{"head_commit":"abc"}` {
		t.Fatalf("result: %s", op.Result)
	}
	if op := waitFor(t, s, unknown.ID, domain.OpFailed); op.Error.Code != domain.CodeUnsupported {
		t.Fatalf("unknown kind: %+v", op.Error)
	}
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if failures[block.ID] != domain.CodeCancelled || failures[fail.ID] != domain.CodeBuildFailed || len(failures) != 5 {
		t.Fatalf("failure hook calls: %v", failures)
	}
}

func TestShutdownLeavesWorkForRecovery(t *testing.T) {
	s, p := setup(t)
	started := make(chan struct{})
	d := New(s, quiet, Options{PollInterval: 10 * time.Millisecond})
	d.Handle(domain.OpCreateDeployment, func(ctx context.Context, op domain.Operation) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	op := enqueue(t, s, p, domain.OpCreateDeployment, "dep_a", time.Hour)
	stop := start(t, d)
	<-started
	stop()
	time.Sleep(50 * time.Millisecond)
	if got, _ := s.GetOperation(context.Background(), op.ID); got.Status != domain.OpRunning {
		t.Fatalf("interrupted operation was finalized: %s", got.Status)
	}

	// The next dispatcher resumes it; after too many attempts it gives up.
	done := make(chan struct{})
	next := New(s, quiet, Options{PollInterval: 10 * time.Millisecond, MaxAttempts: 1})
	next.Handle(domain.OpCreateDeployment, func(ctx context.Context, op domain.Operation) (any, error) {
		close(done)
		return nil, nil
	})
	start(t, next)
	if got := waitFor(t, s, op.ID, domain.OpFailed); got.Error.Code != domain.CodeInterrupted || got.Attempts != 2 {
		t.Fatalf("retry limit: %+v", got)
	}
	select {
	case <-done:
		t.Fatal("handler ran beyond the attempt limit")
	default:
	}
}
