package operations

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"deployer/internal/domain"
	"deployer/internal/store/sqlite"
)

func TestWorkerCompletesAndDoesNotRunExpiredWork(t *testing.T) {
	s, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	p, _ := s.CreateProject(ctx, "portal", "https://example.com/repo.git", "actor", "")
	first, _ := s.EnqueueVerify(ctx, p.ID, "actor", "first")
	called := 0
	w := &Worker{Store: s, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Verify: func(context.Context, domain.Project) (any, error) {
		called++
		return map[string]bool{"verified": true}, nil
	}}
	if _, err = w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	op, _ := s.Operation(ctx, first.ID)
	if op.Status != "succeeded" || called != 1 {
		t.Fatalf("bad result %+v", op)
	}
	second, _ := s.EnqueueVerify(ctx, p.ID, "actor", "second")
	now = now.Add(3 * time.Minute)
	if _, err = w.Step(ctx); err != nil {
		t.Fatal(err)
	}
	op, _ = s.Operation(ctx, second.ID)
	if op.Status != "failed" || op.Error.Code != "OPERATION_TIMEOUT" || called != 1 {
		t.Fatalf("expired operation ran: %+v", op)
	}
}
func TestShutdownLeavesWorkRecoverable(t *testing.T) {
	s, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	p, _ := s.CreateProject(ctx, "portal", "https://example.com/repo.git", "actor", "")
	o, _ := s.EnqueueVerify(ctx, p.ID, "actor", "key")
	w := &Worker{Store: s, Verify: func(context.Context, domain.Project) (any, error) { cancel(); return nil, context.Canceled }}
	_, _ = w.Step(ctx)
	op, _ := s.Operation(context.Background(), o.ID)
	if op.Status != "running" {
		t.Fatalf("work lost on shutdown %+v", op)
	}
	if err = s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, _ = s.Operation(context.Background(), o.ID)
	if op.Status != "queued" {
		t.Fatalf("work not requeued %+v", op)
	}
}
