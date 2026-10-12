// Package operations executes durable operations from the store.
//
// Operations are claimed oldest first. Different deployments' operations run
// concurrently, while each deployment's operations run one at a time and in
// order. When the process stops, running operations are left as they are and
// requeued by the next process, so handlers must be safe to repeat.
package operations

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/store"
)

// Handler performs one operation. A non-nil result is stored as JSON.
// Handlers must return promptly when ctx is cancelled.
type Handler func(ctx context.Context, op domain.Operation) (result any, err error)

// FailureHook is called when an operation fails or is cancelled, before the
// outcome is recorded, with a context that outlives the operation's own.
type FailureHook func(ctx context.Context, op domain.Operation, failure *domain.Error)

// Options configures a Dispatcher.
type Options struct {
	// Concurrency is the maximum number of operations running at once.
	Concurrency int
	// MaxAttempts bounds how often an interrupted operation is retried.
	MaxAttempts int
	// PollInterval is how often the queue is checked without a Notify.
	PollInterval time.Duration
}

// Dispatcher runs queued operations. Create one with New.
type Dispatcher struct {
	store    *store.Store
	log      *slog.Logger
	opts     Options
	handlers map[domain.OperationKind]Handler
	onFail   FailureHook
	wake     chan struct{}

	mu        sync.Mutex
	running   map[string]context.CancelFunc // by operation ID
	cancelled map[string]bool               // operation IDs cancelled on request
	wg        sync.WaitGroup
}

// New returns a dispatcher. Register handlers before calling Run.
func New(s *store.Store, log *slog.Logger, opts Options) *Dispatcher {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 8
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 3
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = time.Second
	}
	return &Dispatcher{
		store: s, log: log, opts: opts,
		handlers:  map[domain.OperationKind]Handler{},
		wake:      make(chan struct{}, 1),
		running:   map[string]context.CancelFunc{},
		cancelled: map[string]bool{},
	}
}

// Handle registers the handler for an operation kind.
func (d *Dispatcher) Handle(kind domain.OperationKind, h Handler) { d.handlers[kind] = h }

// OnFailure registers a hook for failed and cancelled operations.
func (d *Dispatcher) OnFailure(hook FailureHook) { d.onFail = hook }

// Notify tells the dispatcher that new work may be available.
func (d *Dispatcher) Notify() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// CancelOperation cancels a running operation, which then finishes with
// status cancelled. It does nothing if the operation is not running.
func (d *Dispatcher) CancelOperation(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if cancel, ok := d.running[id]; ok {
		d.cancelled[id] = true
		cancel()
	}
}

// Run requeues interrupted operations and dispatches until ctx is cancelled.
// It waits for running handlers to return before returning.
func (d *Dispatcher) Run(ctx context.Context) error {
	if n, err := d.store.RequeueRunning(ctx); err != nil {
		return err
	} else if n > 0 {
		d.log.Info("resuming interrupted operations", "count", n)
	}
	defer d.wg.Wait()
	ticker := time.NewTicker(d.opts.PollInterval)
	defer ticker.Stop()
	for {
		d.dispatch(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-d.wake:
		case <-ticker.C:
		}
	}
}

// dispatch starts queued operations until the queue is empty or every slot
// is taken.
func (d *Dispatcher) dispatch(ctx context.Context) {
	for ctx.Err() == nil {
		d.mu.Lock()
		full := len(d.running) >= d.opts.Concurrency
		d.mu.Unlock()
		if full {
			return
		}
		op, ok, err := d.store.ClaimOperation(ctx)
		if err != nil {
			if ctx.Err() == nil {
				d.log.Error("claim operation", "error", err)
			}
			return
		}
		if !ok {
			return
		}
		opCtx, cancel := context.WithCancel(ctx)
		d.mu.Lock()
		d.running[op.ID] = cancel
		d.mu.Unlock()
		d.wg.Add(1)
		go d.execute(ctx, opCtx, cancel, op)
	}
}

func (d *Dispatcher) execute(parent, ctx context.Context, cancel context.CancelFunc, op domain.Operation) {
	defer d.wg.Done()
	defer cancel()
	log := d.log.With("operation", op.ID, "kind", op.Kind, "deployment", op.DeploymentID)
	result, err := d.invoke(ctx, op)

	d.mu.Lock()
	wasCancelled := d.cancelled[op.ID]
	delete(d.running, op.ID)
	delete(d.cancelled, op.ID)
	d.mu.Unlock()
	defer d.Notify()

	if parent.Err() != nil && !wasCancelled {
		log.Info("operation interrupted by shutdown; it resumes on restart")
		return
	}
	finishCtx, done := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer done()
	status, failure := domain.OpSucceeded, (*domain.Error)(nil)
	switch {
	case wasCancelled:
		status, failure = domain.OpCancelled, domain.Errorf(domain.CodeCancelled, "the operation was cancelled")
	case err != nil:
		status, failure = domain.OpFailed, toFailure(err)
		if failure.Code == domain.CodeInternal {
			log.Error("operation failed", "error", err)
		} else {
			log.Info("operation failed", "code", failure.Code, "message", failure.Message)
		}
	}
	if failure != nil {
		result = nil
		// Update dependent state first, so that whoever sees the operation
		// finish also sees its consequences.
		if d.onFail != nil {
			d.onFail(finishCtx, op, failure)
		}
	}
	if err := d.store.FinishOperation(finishCtx, op.ID, status, result, failure); err != nil {
		log.Error("record operation result", "error", err)
	}
}

func (d *Dispatcher) invoke(ctx context.Context, op domain.Operation) (any, error) {
	h, ok := d.handlers[op.Kind]
	switch {
	case !ok:
		return nil, domain.Errorf(domain.CodeUnsupported, "this server cannot run %s operations", op.Kind)
	case op.Attempts > d.opts.MaxAttempts:
		return nil, domain.Errorf(domain.CodeInterrupted, "the operation was interrupted %d times; submit it again", op.Attempts-1)
	}
	remaining := op.Deadline.Sub(d.store.Now())
	if remaining <= 0 {
		return nil, domain.Errorf(domain.CodeTimeout, "the operation did not finish before its deadline")
	}
	ctx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	result, err := h(ctx, op)
	if e, ok := domain.AsError(err); ok && e.Code == domain.CodeTimeout {
		return nil, e // The handler explained the timeout itself.
	}
	if errors.Is(err, context.DeadlineExceeded) || err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, domain.Errorf(domain.CodeTimeout, "the operation did not finish before its deadline")
	}
	return result, err
}

func toFailure(err error) *domain.Error {
	if e, ok := domain.AsError(err); ok {
		return e
	}
	return domain.Errorf(domain.CodeInternal, "the operation failed unexpectedly; the server log has details")
}
