package operations

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"deployer/internal/domain"
	"deployer/internal/store/sqlite"
)

type Verifier func(context.Context, domain.Project) (any, error)
type Worker struct {
	Store  *sqlite.Store
	Verify Verifier
	Log    *slog.Logger
	Handle func(context.Context, sqlite.Operation) (any, error)
	Failed func(context.Context, sqlite.Operation, *domain.Error) error
}

func (w *Worker) Run(ctx context.Context) error {
	if err := w.Store.Recover(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		worked, err := w.Step(ctx)
		if err != nil && ctx.Err() == nil {
			w.Log.Error("operation worker failed", "error", err)
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *Worker) Step(ctx context.Context) (bool, error) {
	o, err := w.Store.Claim(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var result any
	var failure *domain.Error
	if !w.Store.Now().Before(o.Deadline) {
		failure = domain.Fail("OPERATION_TIMEOUT", "operation exceeded its deadline")
	} else if o.Attempts > 3 {
		failure = domain.Fail("RETRY_LIMIT", "operation interrupted too often; submit a new operation")
	} else {
		jobCtx, cancel := context.WithTimeout(ctx, o.Deadline.Sub(w.Store.Now()))
		var err error
		if o.Kind == "project.verify" {
			var p domain.Project
			p, err = w.Store.Project(ctx, o.ProjectID)
			if err == nil {
				result, err = w.Verify(jobCtx, p)
			}
		} else if w.Handle != nil {
			result, err = w.Handle(jobCtx, o)
		} else {
			err = domain.Fail("UNSUPPORTED_FEATURE", "deployment runtime is disabled")
		}
		cancel()
		if ctx.Err() != nil {
			return true, ctx.Err()
		} // Leave running work for recovery.
		if err != nil {
			if !errors.As(err, &failure) {
				w.Log.Error("operation failed", "operation", o.ID, "error", err)
				failure = domain.Fail("OPERATION_FAILED", "operation failed; inspect deployment and server logs")
			}
		}
	}
	if failure != nil && w.Failed != nil && o.DeploymentID != "" {
		if err := w.Failed(ctx, o, failure); err != nil {
			w.Log.Error("failure checkpoint failed", "operation", o.ID, "error", err)
		}
	}
	return true, w.Store.Finish(ctx, o, result, failure)
}
