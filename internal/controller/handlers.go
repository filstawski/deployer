package controller

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/filstawski/deployer/internal/config"
	"github.com/filstawski/deployer/internal/container"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/execx"
)

// maxBuildLog bounds a build log on disk.
const maxBuildLog = 8 << 20

// handleCreate builds and starts a new deployment. Each step records its
// result, so a retry after a crash resumes: a recorded commit is fetched
// again exactly, and a recorded image is not rebuilt.
func (c *Controller) handleCreate(ctx context.Context, op domain.Operation) (any, error) {
	d, err := c.store.GetDeployment(ctx, op.DeploymentID)
	if err != nil {
		return nil, err
	}
	switch d.State {
	case domain.StateRunning, domain.StateDestroyed:
		return nil, nil // Finished before an interruption, or superseded.
	}
	if d.Image == "" {
		if d, err = c.build(ctx, d); err != nil {
			return nil, err
		}
	}
	return nil, c.startAndWait(ctx, d)
}

func (c *Controller) build(ctx context.Context, d domain.Deployment) (domain.Deployment, error) {
	select {
	case c.builds <- struct{}{}:
		defer func() { <-c.builds }()
	case <-ctx.Done():
		return d, ctx.Err()
	}
	project, err := c.store.GetProject(ctx, d.ProjectID)
	if err != nil {
		return d, err
	}
	if err := os.MkdirAll(c.dir(d.ID), 0o700); err != nil {
		return d, err
	}
	if d, err = c.setState(ctx, d.ID, domain.StateBuilding); err != nil {
		return d, err
	}
	c.publish(ctx)

	source := filepath.Join(c.dir(d.ID), "source")
	defer os.RemoveAll(source)
	commit, err := c.source.Fetch(ctx, project.Repository, d.Ref, d.Commit, source)
	if err != nil {
		return d, err
	}
	if err := normalizePermissions(source); err != nil {
		return d, err
	}
	recipe, err := config.LoadRecipe(source)
	if err != nil {
		return d, domain.Errorf(domain.CodeInvalidRecipe, "%v", err).
			WithHint("check deployer.yaml on %s; run deployer validate in the repository", d.Ref)
	}
	if d, err = c.store.UpdateDeployment(ctx, d.ID, func(d *domain.Deployment) error {
		d.Commit = commit
		d.Port = recipe.HTTP.Port
		d.HealthPath = recipe.HTTP.Healthcheck.Path
		d.HealthTimeout = recipe.HTTP.Healthcheck.Timeout.D()
		d.Env = recipe.Environment
		return nil
	}); err != nil {
		return d, err
	}

	logFile, err := os.OpenFile(c.buildLog(d.ID), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return d, err
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "Building %s at %s\n", d.Ref, commit)
	buildCtx, cancel := context.WithTimeout(ctx, c.cfg.Deployments.BuildTimeout.D())
	defer cancel()
	image, err := c.runtime.Build(buildCtx, container.BuildSpec{DeploymentID: d.ID, Dockerfile: recipe.Dockerfile, Context: recipe.Context},
		&execx.LimitedWriter{W: logFile, N: maxBuildLog})
	switch {
	case errors.Is(buildCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		return d, domain.Errorf(domain.CodeBuildFailed, "the build took longer than %s", domain.FormatDuration(c.cfg.Deployments.BuildTimeout.D()))
	case err != nil:
		if e, ok := domain.AsError(err); ok && e.Code == domain.CodeBuildFailed {
			return d, e.WithHint("see the build output: deployer logs %s --build", d.Name)
		}
		return d, err
	}
	return c.store.UpdateDeployment(ctx, d.ID, func(d *domain.Deployment) error {
		d.Image = image
		return nil
	})
}

// startAndWait starts the container and waits until its health check passes.
// The lease starts at the first successful health check.
func (c *Controller) startAndWait(ctx context.Context, d domain.Deployment) error {
	d, err := c.store.UpdateDeployment(ctx, d.ID, func(d *domain.Deployment) error {
		d.State, d.Error = domain.StateStarting, nil
		return nil
	})
	if err != nil {
		return err
	}
	// The gateway must know the upstream before it can probe it.
	if err := c.PublishRoutes(ctx); err != nil {
		return domain.Errorf(domain.CodeUnavailable, "the gateway is unreachable: %v", err)
	}
	if err := c.runtime.Start(ctx, container.RunSpec{DeploymentID: d.ID, Image: d.Image, Port: d.Port, Env: c.environment(d)}); err != nil {
		return err
	}
	if err := c.waitHealthy(ctx, d); err != nil {
		return err
	}
	d, err = c.store.UpdateDeployment(ctx, d.ID, func(d *domain.Deployment) error {
		now := c.store.Now()
		if d.ReadyAt == nil {
			expires, purge := now.Add(d.TTL()), now.Add(d.TTL()+c.cfg.Deployments.Retention.D())
			d.ReadyAt, d.ExpiresAt, d.PurgeAt = &now, &expires, &purge
		}
		d.State = domain.StateRunning
		return nil
	})
	if err != nil {
		return err
	}
	c.publish(ctx)
	return nil
}

func (c *Controller) waitHealthy(ctx context.Context, d domain.Deployment) error {
	timeout := d.HealthTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	deadline := time.Now().Add(timeout)
	host := c.cfg.Hostname(d.Name)
	var last error
	for i := 0; ; i++ {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		last = c.router.Probe(probe, host)
		cancel()
		if last == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if i%4 == 3 {
			if running, err := c.runtime.Running(ctx, d.ID); err == nil && !running {
				return domain.Errorf(domain.CodeContainerExited, "the app exited during startup").
					WithHint("see its output: deployer logs %s", d.Name)
			}
		}
		if time.Now().After(deadline) {
			return domain.Errorf(domain.CodeHealthcheckFailed, "GET %s on port %d did not succeed within %s (%v)", d.HealthPath, d.Port, domain.FormatDuration(timeout), last).
				WithHint("make sure the app listens on 0.0.0.0:%d and answers %s; see deployer logs %s", d.Port, d.HealthPath, d.Name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.probeInterval):
		}
	}
}

// environment returns the container environment: the recipe's variables
// plus those Deployer provides.
func (c *Controller) environment(d domain.Deployment) map[string]string {
	env := make(map[string]string, len(d.Env)+5)
	for k, v := range d.Env {
		env[k] = v
	}
	env["PORT"] = strconv.Itoa(d.Port)
	env["DEPLOYER_URL"] = c.cfg.PreviewURL(d.Name)
	env["DEPLOYER_DEPLOYMENT_ID"] = d.ID
	env["DEPLOYER_REF"] = d.Ref
	env["DEPLOYER_COMMIT"] = d.Commit
	return env
}

func (c *Controller) handleStart(ctx context.Context, op domain.Operation) (any, error) {
	d, err := c.store.GetDeployment(ctx, op.DeploymentID)
	if err != nil {
		return nil, err
	}
	if d.ExpiredAt(c.store.Now()) {
		return nil, domain.Errorf(domain.CodeInvalidState, "%s has expired", d.Name)
	}
	// After a reboot the recorded state can be running while the container
	// is not, which is exactly when the controller restarts it.
	if d.State == domain.StateRunning {
		if running, err := c.runtime.Running(ctx, d.ID); err != nil || running {
			return nil, err
		}
	}
	return nil, c.startAndWait(ctx, d)
}

func (c *Controller) handleStop(ctx context.Context, op domain.Operation) (any, error) {
	d, err := c.setState(ctx, op.DeploymentID, domain.StateStopped)
	if err != nil {
		return nil, err
	}
	c.publish(ctx)
	return nil, c.runtime.Stop(ctx, d.ID)
}

func (c *Controller) handleDestroy(ctx context.Context, op domain.Operation) (any, error) {
	d, err := c.store.GetDeployment(ctx, op.DeploymentID)
	if err != nil || d.State == domain.StateDestroyed {
		return nil, err
	}
	if err := c.runtime.Remove(ctx, d.ID); err != nil {
		return nil, err
	}
	if err := c.removeDir(d.ID); err != nil {
		return nil, err
	}
	if _, err := c.store.UpdateDeployment(ctx, d.ID, func(d *domain.Deployment) error {
		d.State, d.Env, d.PurgeAt = domain.StateDestroyed, nil, nil
		return nil
	}); err != nil {
		return nil, err
	}
	c.publish(ctx)
	return nil, nil
}

func (c *Controller) handleVerify(ctx context.Context, op domain.Operation) (any, error) {
	project, err := c.store.GetProject(ctx, op.ProjectID)
	if err != nil {
		return nil, err
	}
	head, err := c.source.Verify(ctx, project.Repository)
	if err != nil {
		return nil, err
	}
	return domain.VerifyResult{HeadCommit: head}, nil
}

// onFailure records why an operation failed on its deployment. A failed
// build or start leaves the deployment failed and its container stopped; it
// is retained for diagnosis until its purge time.
func (c *Controller) onFailure(ctx context.Context, op domain.Operation, failure *domain.Error) {
	if op.DeploymentID == "" || failure.Code == domain.CodeCancelled {
		return
	}
	log := c.log.With("deployment", op.DeploymentID, "operation", op.ID)
	d, err := c.store.UpdateDeployment(ctx, op.DeploymentID, func(d *domain.Deployment) error {
		if d.State == domain.StateDestroyed {
			return errSkip
		}
		d.Error = failure
		switch op.Kind {
		case domain.OpCreateDeployment, domain.OpStartDeployment:
			now := c.store.Now()
			if d.ExpiredAt(now) {
				d.State = domain.StateExpired
			} else {
				d.State = domain.StateFailed
			}
			if d.PurgeAt == nil {
				purge := now.Add(c.cfg.Deployments.Retention.D())
				d.PurgeAt = &purge
			}
		}
		return nil
	})
	if errors.Is(err, errSkip) {
		return
	}
	if err != nil {
		log.Error("record failure", "error", err)
		return
	}
	c.publish(ctx)
	if d.State == domain.StateFailed || d.State == domain.StateExpired {
		if err := c.runtime.Stop(ctx, d.ID); err != nil {
			log.Warn("stop failed deployment", "error", err)
		}
	}
}

var errSkip = errors.New("skip")

func (c *Controller) setState(ctx context.Context, id string, state domain.State) (domain.Deployment, error) {
	return c.store.UpdateDeployment(ctx, id, func(d *domain.Deployment) error {
		d.State = state
		return nil
	})
}

// normalizePermissions gives a checkout the modes Git records, 0644 or 0755,
// and 0755 for directories. The service runs with umask 077, which would
// otherwise make files owner-only, and apps running as a non-root USER could
// not read their own files.
func normalizePermissions(root string) error {
	return filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.Type()&fs.ModeSymlink != 0 {
			return err
		}
		mode := fs.FileMode(0o755)
		if !e.IsDir() {
			info, err := e.Info()
			if err != nil {
				return err
			}
			if info.Mode()&0o111 == 0 {
				mode = 0o644
			}
		}
		return os.Chmod(path, mode)
	})
}
