package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/store"
)

// maxPendingOperations bounds the queue so a misbehaving client cannot grow
// it without limit.
const maxPendingOperations = 200

// Deploy admits a new deployment and queues its build. The hostname is
// reserved immediately and serves a progress page until the app is ready.
func (c *Controller) Deploy(ctx context.Context, actor string, project domain.Project, req domain.CreateDeploymentRequest, key string) (domain.CreateDeploymentResponse, error) {
	var resp domain.CreateDeploymentResponse
	if err := domain.ValidateRef(req.Ref); err != nil {
		return resp, err
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	limits := c.cfg.Deployments
	switch {
	case req.TTLSeconds == 0:
		ttl = limits.DefaultTTL.D()
	case ttl < time.Minute || ttl > limits.MaxTTL.D():
		return resp, domain.Errorf(domain.CodeInvalidInput, "lifetime must be between 1m and %s", domain.FormatDuration(limits.MaxTTL.D())).
			WithHint("an administrator can raise deployments.max_ttl in /etc/deployer/server.yaml")
	}
	if req.Name != "" {
		if err := domain.ValidateDeploymentName(req.Name); err != nil {
			return resp, err
		}
	}
	hash := store.RequestHash(domain.OpCreateDeployment, project.ID, req.Ref, req.Name, int64(ttl.Seconds()))

	// A generated name can collide with a live deployment; retry with a new one.
	for attempt := 0; ; attempt++ {
		name := req.Name
		if name == "" {
			name = generateName(project.Name, req.Ref)
		}
		err := c.store.Tx(ctx, func(tx *store.Tx) error {
			if opID, found, err := tx.Idempotent(actor, key, hash); found || err != nil {
				if err != nil {
					return err
				}
				if resp.Operation, err = tx.Operation(opID); err != nil {
					return err
				}
				resp.Deployment, err = tx.Deployment(resp.Operation.DeploymentID)
				return err
			}
			if err := c.admit(tx); err != nil {
				return err
			}
			now := tx.Now()
			d := domain.Deployment{
				ID: domain.NewID(domain.PrefixDeployment), Name: name, ProjectID: project.ID, Project: project.Name,
				Ref: req.Ref, State: domain.StateQueued, TTLSeconds: int64(ttl.Seconds()),
				CreatedBy: actor, CreatedAt: now, UpdatedAt: now,
			}
			op := newOperation(tx, domain.OpCreateDeployment, actor, project.ID, d.ID, c.createDeadline())
			d.OperationID = op.ID
			if err := tx.InsertDeployment(d); err != nil {
				return err
			}
			if err := tx.InsertOperation(op); err != nil {
				return err
			}
			if err := tx.RecordIdempotency(actor, key, hash, op.ID); err != nil {
				return err
			}
			resp.Deployment, resp.Operation = d, op
			return tx.Event(actor, "deployment.created", d.ID)
		})
		if domain.CodeOf(err) == domain.CodeNameTaken && req.Name == "" && attempt < 5 {
			continue
		}
		if err != nil {
			return resp, err
		}
		break
	}
	c.publish(ctx)
	c.notify()
	resp.Deployment = c.Present(resp.Deployment)
	return resp, nil
}

func (c *Controller) admit(tx *store.Tx) error {
	live, err := tx.CountLiveDeployments()
	if err != nil {
		return err
	}
	if max := c.cfg.Deployments.MaxLive; live >= max {
		return domain.Errorf(domain.CodeQuotaExceeded, "this server is at its limit of %d deployments", max).
			WithHint("destroy deployments you no longer need: deployer list, then deployer destroy NAME")
	}
	pending, err := tx.CountPendingOperations()
	if err != nil {
		return err
	}
	if pending >= maxPendingOperations {
		return domain.Errorf(domain.CodeQuotaExceeded, "the operation queue is full; try again later")
	}
	return nil
}

// createDeadline allows for queueing behind other builds.
func (c *Controller) createDeadline() time.Duration {
	return max(2*time.Hour, 3*c.cfg.Deployments.BuildTimeout.D())
}

// generateName returns a readable, probably unique name such as
// "shop-design-a-k7m2q". Uniqueness is enforced by the store.
func generateName(project, ref string) string {
	refPart := ref
	if domain.IsCommitSHA(ref) {
		refPart = ref[:7]
	}
	name := domain.Slug(project, 20)
	if s := domain.Slug(refPart, 30); s != "" {
		name += "-" + s
	}
	return name + "-" + domain.RandomString(5)
}

func newOperation(tx *store.Tx, kind domain.OperationKind, actor, projectID, deploymentID string, timeout time.Duration) domain.Operation {
	now := tx.Now()
	return domain.Operation{
		ID: domain.NewID(domain.PrefixOperation), Kind: kind, Status: domain.OpQueued,
		ProjectID: projectID, DeploymentID: deploymentID, Actor: actor,
		CreatedAt: now, UpdatedAt: now, Deadline: now.Add(timeout),
	}
}

// Stop queues stopping a running deployment.
func (c *Controller) Stop(ctx context.Context, actor, id, key string) (domain.Operation, error) {
	return c.enqueue(ctx, actor, id, key, domain.OpStopDeployment, nil, func(d domain.Deployment, pending []domain.Operation) (*domain.Operation, error) {
		if len(pending) > 0 {
			return nil, busy(d)
		}
		if d.State != domain.StateRunning {
			return nil, domain.Errorf(domain.CodeInvalidState, "%s is %s, not running", d.Name, d.State)
		}
		return nil, nil
	})
}

// Start queues starting a stopped or failed deployment from its built image.
func (c *Controller) Start(ctx context.Context, actor, id, key string) (domain.Operation, error) {
	return c.enqueue(ctx, actor, id, key, domain.OpStartDeployment, nil, func(d domain.Deployment, pending []domain.Operation) (*domain.Operation, error) {
		switch {
		case len(pending) > 0:
			return nil, busy(d)
		case d.State == domain.StateRunning:
			return nil, domain.Errorf(domain.CodeInvalidState, "%s is already running", d.Name)
		case d.State == domain.StateExpired || d.ExpiredAt(c.store.Now()):
			return nil, expired(d)
		case d.Image == "":
			return nil, domain.Errorf(domain.CodeInvalidState, "%s was never built successfully", d.Name).WithHint("deploy again: deployer deploy --ref %s", d.Ref)
		}
		return nil, nil
	})
}

// Update queues moving a deployment to the latest commit of its ref, or of
// req.Ref, keeping its name, URL, and lifetime; see handleUpdate.
func (c *Controller) Update(ctx context.Context, actor, id, key string, req domain.UpdateDeploymentRequest) (domain.Operation, error) {
	if req.Ref != "" {
		if err := domain.ValidateRef(req.Ref); err != nil {
			return domain.Operation{}, err
		}
	}
	return c.enqueue(ctx, actor, id, key, domain.OpUpdateDeployment, req, func(d domain.Deployment, pending []domain.Operation) (*domain.Operation, error) {
		switch {
		case len(pending) > 0:
			return nil, busy(d)
		case d.State == domain.StateExpired || d.ExpiredAt(c.store.Now()):
			return nil, expired(d)
		}
		// Clear the previous build's log before the operation can run, so
		// that whoever follows the new build reads only its output. A retried
		// request never gets here.
		if err := os.Truncate(c.buildLog(d.ID), 0); err != nil && !errors.Is(err, fs.ErrNotExist) {
			c.log.Warn("clear the build log", "deployment", d.ID, "error", err)
		}
		return nil, nil
	})
}

// Destroy queues removing a deployment and its resources. It supersedes any
// other queued or running operation of the deployment.
func (c *Controller) Destroy(ctx context.Context, actor, id, key string) (domain.Operation, error) {
	var running []string
	op, err := c.enqueue(ctx, actor, id, key, domain.OpDestroyDeployment, nil, func(d domain.Deployment, pending []domain.Operation) (*domain.Operation, error) {
		for i := range pending {
			if pending[i].Kind == domain.OpDestroyDeployment {
				return &pending[i], nil
			}
		}
		for _, p := range pending {
			if p.Status == domain.OpRunning {
				running = append(running, p.ID)
			}
		}
		return nil, nil
	})
	// Queued operations were cancelled in the transaction; cancel the running
	// one by ID, so that this destroy can never cancel itself.
	if err == nil && c.queue != nil {
		for _, id := range running {
			c.queue.CancelOperation(id)
		}
	}
	return op, err
}

// rule validates an action against a deployment and its pending operations.
// It may return an existing operation to reuse instead of creating one.
type rule func(d domain.Deployment, pending []domain.Operation) (*domain.Operation, error)

// enqueue queues an operation on a deployment after check accepts it.
// request, if not nil, holds the operation's parameters.
func (c *Controller) enqueue(ctx context.Context, actor, id, key string, kind domain.OperationKind, request any, check rule) (domain.Operation, error) {
	hash := store.RequestHash(kind, id)
	var params json.RawMessage
	if request != nil {
		var err error
		if params, err = json.Marshal(request); err != nil {
			return domain.Operation{}, err
		}
		hash = store.RequestHash(kind, id, params)
	}
	timeout := 10 * time.Minute
	if kind == domain.OpUpdateDeployment {
		timeout = c.createDeadline()
	}
	var op domain.Operation
	err := c.store.Tx(ctx, func(tx *store.Tx) error {
		if opID, found, err := tx.Idempotent(actor, key, hash); found || err != nil {
			if err == nil {
				op, err = tx.Operation(opID)
			}
			return err
		}
		d, err := tx.Deployment(id)
		if err != nil {
			return err
		}
		if d.State == domain.StateDestroyed {
			return domain.Errorf(domain.CodeInvalidState, "%s has been destroyed", d.Name)
		}
		pending, err := tx.PendingOperations(d.ID)
		if err != nil {
			return err
		}
		reuse, err := check(d, pending)
		if err != nil {
			return err
		}
		if reuse != nil {
			op = *reuse
			return tx.RecordIdempotency(actor, key, hash, op.ID)
		}
		if kind == domain.OpDestroyDeployment {
			if err := tx.CancelQueuedOperations(d.ID, domain.Errorf(domain.CodeCancelled, "superseded by destroy")); err != nil {
				return err
			}
		}
		op = newOperation(tx, kind, actor, d.ProjectID, d.ID, timeout)
		op.Request = params
		d.OperationID = op.ID
		if err := tx.InsertOperation(op); err != nil {
			return err
		}
		if err := tx.SaveDeployment(d); err != nil {
			return err
		}
		if err := tx.RecordIdempotency(actor, key, hash, op.ID); err != nil {
			return err
		}
		return tx.Event(actor, string(kind), d.ID)
	})
	if err == nil {
		c.notify()
	}
	return op, err
}

func busy(d domain.Deployment) error {
	return domain.Errorf(domain.CodeBusy, "%s has an operation in progress", d.Name).
		WithHint("wait for it to finish: deployer operation wait %s", d.OperationID)
}

// Extend lengthens a deployment's lease. Before the deployment is ready it
// lengthens the lifetime it will receive. An expired deployment that has not
// been purged gets a new lease starting now and can then be started again.
func (c *Controller) Extend(ctx context.Context, actor, id string, by time.Duration) (domain.Deployment, error) {
	if by < time.Minute {
		return domain.Deployment{}, domain.Errorf(domain.CodeInvalidInput, "extend by at least 1m")
	}
	limit := c.cfg.Deployments.MaxTTL.D()
	d, err := c.store.UpdateDeployment(ctx, id, func(d *domain.Deployment) error {
		now := c.store.Now()
		switch {
		case d.State == domain.StateDestroyed:
			return domain.Errorf(domain.CodeInvalidState, "%s has been destroyed", d.Name)
		case d.ExpiresAt == nil:
			if d.TTL()+by > limit {
				return domain.Errorf(domain.CodeInvalidInput, "lifetime cannot exceed %s", domain.FormatDuration(limit)).
					WithHint("an administrator can raise deployments.max_ttl in /etc/deployer/server.yaml")
			}
			d.TTLSeconds += int64(by.Seconds())
			return nil
		}
		next := d.ExpiresAt.Add(by)
		if d.ExpiredAt(now) {
			next = now.Add(by)
			if d.State == domain.StateExpired {
				d.State = domain.StateStopped
			}
		}
		if next.Sub(now) > limit {
			return domain.Errorf(domain.CodeInvalidInput, "a deployment can be extended to at most %s from now", domain.FormatDuration(limit)).
				WithHint("an administrator can raise deployments.max_ttl in /etc/deployer/server.yaml")
		}
		purge := next.Add(c.cfg.Deployments.Retention.D())
		d.ExpiresAt, d.PurgeAt = &next, &purge
		return nil
	})
	if err != nil {
		return d, err
	}
	c.publish(ctx)
	return c.Present(d), nil
}

// Branches lists a project's branches, reading them from the repository now.
func (c *Controller) Branches(ctx context.Context, project domain.Project) ([]domain.Branch, error) {
	return c.source.Branches(ctx, project.Repository)
}

// VerifyProject queues a check that the server can read a project's
// repository.
func (c *Controller) VerifyProject(ctx context.Context, actor string, project domain.Project, key string) (domain.Operation, error) {
	hash := store.RequestHash(domain.OpVerifyProject, project.ID)
	var op domain.Operation
	err := c.store.Tx(ctx, func(tx *store.Tx) error {
		if opID, found, err := tx.Idempotent(actor, key, hash); found || err != nil {
			if err == nil {
				op, err = tx.Operation(opID)
			}
			return err
		}
		if pending, err := tx.CountPendingOperations(); err != nil {
			return err
		} else if pending >= maxPendingOperations {
			return domain.Errorf(domain.CodeQuotaExceeded, "the operation queue is full; try again later")
		}
		op = newOperation(tx, domain.OpVerifyProject, actor, project.ID, "", 2*time.Minute)
		if err := tx.InsertOperation(op); err != nil {
			return err
		}
		return tx.RecordIdempotency(actor, key, hash, op.ID)
	})
	if err == nil {
		c.notify()
	}
	return op, err
}
