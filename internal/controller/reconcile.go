package controller

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/store"
)

// Maintenance runs periodic housekeeping, such as pruning build caches.
type Maintenance interface {
	PruneBuildCache(ctx context.Context, unusedFor time.Duration) error
}

// Run reconciles at startup and then every interval until ctx is cancelled.
// The first pass also restarts deployments that should be running, which
// recovers previews after a host reboot.
func (c *Controller) Run(ctx context.Context, interval time.Duration, maintenance Maintenance) {
	c.reconcile(ctx, true)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		c.reconcile(ctx, false)
		if time.Since(c.lastPrune) > 24*time.Hour {
			c.lastPrune = time.Now()
			c.housekeeping(ctx, maintenance)
		}
	}
}

// Reconcile runs one reconciliation pass. Tests call it directly.
func (c *Controller) Reconcile(ctx context.Context) { c.reconcile(ctx, false) }

// Boot prepares runtime state after the controller starts: it reconnects the
// gateway to every deployment network and runs the first reconciliation.
func (c *Controller) Boot(ctx context.Context) error {
	list, err := c.store.ListDeployments(ctx, store.DeploymentFilter{})
	if err != nil {
		return err
	}
	for _, d := range list {
		if err := c.runtime.AttachGateway(ctx, d.ID); err != nil {
			c.log.Warn("reconnect gateway", "deployment", d.ID, "error", err)
		}
	}
	return c.PublishRoutes(ctx)
}

func (c *Controller) reconcile(ctx context.Context, boot bool) {
	list, err := c.store.ListDeployments(ctx, store.DeploymentFilter{})
	if err != nil {
		c.log.Error("reconcile: list deployments", "error", err)
		return
	}
	busy, err := c.store.BusyDeployments(ctx)
	if err != nil {
		c.log.Error("reconcile: list operations", "error", err)
		return
	}
	for _, d := range list {
		// An operation in progress owns its deployment; the gateway still
		// enforces expiry on its own.
		if busy[d.ID] {
			continue
		}
		if err := c.reconcileLocked(ctx, d.ID, boot); err != nil && ctx.Err() == nil {
			c.log.Warn("reconcile deployment", "deployment", d.ID, "error", err)
		}
	}
	c.publish(ctx)
}

// reconcileLocked reconciles one deployment while holding its lock. The list
// read at the start of the pass may be stale by now, so the deployment and
// its operations are read again.
func (c *Controller) reconcileLocked(ctx context.Context, id string, boot bool) error {
	m := c.lock(id)
	if !m.TryLock() {
		return nil // An operation is running; it owns the deployment.
	}
	defer m.Unlock()
	if busy, err := c.store.Busy(ctx, id); err != nil || busy {
		return err
	}
	d, err := c.store.GetDeployment(ctx, id)
	if err != nil || d.State == domain.StateDestroyed {
		return err
	}
	return c.reconcileOne(ctx, d, boot)
}

// transition updates a deployment only if it is still in one of the given
// states, since synchronous API calls such as extend don't take the lock.
func (c *Controller) transition(ctx context.Context, id string, from []domain.State, fn func(d *domain.Deployment)) (bool, error) {
	_, err := c.store.UpdateDeployment(ctx, id, func(d *domain.Deployment) error {
		if !slices.Contains(from, d.State) {
			return errSkip
		}
		fn(d)
		return nil
	})
	if errors.Is(err, errSkip) {
		return false, nil
	}
	return err == nil, err
}

func (c *Controller) reconcileOne(ctx context.Context, d domain.Deployment, boot bool) error {
	now := c.store.Now()
	switch {
	case d.PurgeAt != nil && !now.Before(*d.PurgeAt):
		_, err := c.Destroy(ctx, SystemActor, d.ID, "")
		return err

	case d.ExpiredAt(now) && d.State != domain.StateExpired && d.State != domain.StateFailed:
		_, err := c.store.UpdateDeployment(ctx, d.ID, func(d *domain.Deployment) error {
			if !d.ExpiredAt(c.store.Now()) || d.State == domain.StateExpired || d.State == domain.StateFailed {
				return errSkip // Extended in the meantime.
			}
			d.State = domain.StateExpired
			return nil
		})
		if errors.Is(err, errSkip) || err != nil {
			return ignoreSkip(err)
		}
		c.log.Info("deployment expired", "deployment", d.ID)
		return c.runtime.Stop(ctx, d.ID)

	case d.State.InProgress():
		// No operation is driving it, so its operation ended without
		// recording an outcome.
		_, err := c.transition(ctx, d.ID, []domain.State{domain.StateQueued, domain.StateBuilding, domain.StateStarting}, func(d *domain.Deployment) {
			d.State = domain.StateFailed
			d.Error = domain.Errorf(domain.CodeInterrupted, "the deployment's operation ended unexpectedly")
			if d.PurgeAt == nil {
				purge := now.Add(c.cfg.Deployments.Retention.D())
				d.PurgeAt = &purge
			}
		})
		return err

	case d.State == domain.StateRunning:
		running, err := c.runtime.Running(ctx, d.ID)
		if err != nil || running {
			return err
		}
		if boot {
			c.log.Info("restarting deployment after controller start", "deployment", d.ID)
			_, err := c.enqueue(ctx, SystemActor, d.ID, "", domain.OpStartDeployment, nil, func(domain.Deployment, []domain.Operation) (*domain.Operation, error) { return nil, nil })
			return err
		}
		_, err = c.transition(ctx, d.ID, []domain.State{domain.StateRunning}, func(d *domain.Deployment) {
			d.State = domain.StateFailed
			d.Error = domain.Errorf(domain.CodeContainerExited, "the app stopped unexpectedly").
				WithHint("see its output with deployer logs %s, then deployer start %s", d.Name, d.Name)
		})
		return err

	default: // Stopped, expired, or failed: nothing should be running.
		running, err := c.runtime.Running(ctx, d.ID)
		if err != nil || !running {
			return err
		}
		return c.runtime.Stop(ctx, d.ID)
	}
}

func (c *Controller) housekeeping(ctx context.Context, maintenance Maintenance) {
	if err := c.store.Prune(ctx, 7*24*time.Hour, 90*24*time.Hour); err != nil {
		c.log.Warn("prune store", "error", err)
	}
	if maintenance != nil {
		if err := maintenance.PruneBuildCache(ctx, 7*24*time.Hour); err != nil {
			c.log.Warn("prune build cache", "error", err)
		}
	}
}

func ignoreSkip(err error) error {
	if errors.Is(err, errSkip) {
		return nil
	}
	return err
}
