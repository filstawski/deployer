package controller

import (
	"cmp"
	"context"
	"encoding/json"
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
		record := func(v version) error {
			var err error
			d, err = c.store.UpdateDeployment(ctx, d.ID, func(d *domain.Deployment) error {
				v.apply(d)
				return nil
			})
			return err
		}
		v, err := c.build(ctx, d, buildOptions{ref: d.Ref, pinned: d.Commit, announce: true, fetched: record})
		if err != nil {
			return nil, err
		}
		if err := record(v); err != nil {
			return nil, err
		}
	}
	return nil, c.startAndWait(ctx, d)
}

// version is one build of a deployment: a commit, the settings from its
// recipe, and the image built from it. Update checkpoints record versions as
// JSON.
type version struct {
	Ref           string            `json:"ref"`
	Commit        string            `json:"commit"`
	Image         string            `json:"image"`
	Port          int               `json:"port"`
	HealthPath    string            `json:"health_path"`
	HealthTimeout time.Duration     `json:"health_timeout"`
	Env           map[string]string `json:"env"`
}

func versionOf(d domain.Deployment) version {
	return version{Ref: d.Ref, Commit: d.Commit, Image: d.Image, Port: d.Port, HealthPath: d.HealthPath, HealthTimeout: d.HealthTimeout, Env: d.Env}
}

// apply makes v the deployment's current version.
func (v version) apply(d *domain.Deployment) {
	d.Ref, d.Commit, d.Image = v.Ref, v.Commit, v.Image
	d.Port, d.HealthPath, d.HealthTimeout, d.Env = v.Port, v.HealthPath, v.HealthTimeout, v.Env
}

// buildOptions selects what build fetches and what it shows meanwhile.
type buildOptions struct {
	ref    string
	pinned string // The commit to fetch; the ref's latest when empty.
	// announce shows the building state. It is off while an earlier version
	// keeps serving.
	announce bool
	// fetched, if set, is called once the commit and recipe are known and
	// before the image is built. An error ends the build.
	fetched func(version) error
}

// build fetches a commit and builds its image while holding a build slot.
// It returns the new version without recording it on the deployment.
func (c *Controller) build(ctx context.Context, d domain.Deployment, o buildOptions) (version, error) {
	select {
	case c.builds <- struct{}{}:
		defer func() { <-c.builds }()
	case <-ctx.Done():
		return version{}, ctx.Err()
	}
	project, err := c.store.GetProject(ctx, d.ProjectID)
	if err != nil {
		return version{}, err
	}
	if err := os.MkdirAll(c.dir(d.ID), 0o700); err != nil {
		return version{}, err
	}
	if o.announce {
		if _, err := c.setState(ctx, d.ID, domain.StateBuilding); err != nil {
			return version{}, err
		}
		c.publish(ctx)
	}

	source := filepath.Join(c.dir(d.ID), "source")
	defer os.RemoveAll(source)
	commit, err := c.source.Fetch(ctx, project.Repository, o.ref, o.pinned, source)
	if err != nil {
		return version{}, err
	}
	if err := normalizePermissions(source); err != nil {
		return version{}, err
	}
	recipe, err := config.LoadRecipe(source)
	if err != nil {
		return version{}, domain.Errorf(domain.CodeInvalidRecipe, "%v", err).
			WithHint("check deployer.yaml on %s; run deployer validate in the repository", o.ref)
	}
	v := version{
		Ref: o.ref, Commit: commit, Port: recipe.HTTP.Port, HealthPath: recipe.HTTP.Healthcheck.Path,
		HealthTimeout: recipe.HTTP.Healthcheck.Timeout.D(), Env: recipe.Environment,
	}
	if o.fetched != nil {
		if err := o.fetched(v); err != nil {
			return v, err
		}
	}

	logFile, err := os.OpenFile(c.buildLog(d.ID), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return v, err
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "Building %s at %s\n", o.ref, commit)
	buildCtx, cancel := context.WithTimeout(ctx, c.cfg.Deployments.BuildTimeout.D())
	defer cancel()
	image, err := c.runtime.Build(buildCtx, container.BuildSpec{DeploymentID: d.ID, Dockerfile: recipe.Dockerfile, Context: recipe.Context},
		&execx.LimitedWriter{W: logFile, N: maxBuildLog})
	switch {
	case errors.Is(buildCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil:
		return v, domain.Errorf(domain.CodeBuildFailed, "the build took longer than %s", domain.FormatDuration(c.cfg.Deployments.BuildTimeout.D()))
	case err != nil:
		if e, ok := domain.AsError(err); ok && e.Code == domain.CodeBuildFailed {
			return v, e.WithHint("see the build output: deployer logs %s --build", d.Name)
		}
		return v, err
	}
	v.Image = image
	return v, nil
}

// errUnchanged ends an update whose ref still points at the running commit.
var errUnchanged = errors.New("unchanged")

// updatePlan is recorded on an update operation once the new version is
// built. A retry after an interruption resumes from it: it finishes starting
// the next version, or finishes returning to the previous one.
type updatePlan struct {
	Previous version             `json:"previous"`
	Next     version             `json:"next"`
	Result   domain.UpdateResult `json:"result"`
	// Serving is set when the previous version was running, so a failed
	// switch returns to it.
	Serving bool `json:"serving"`
	// Failure is why the next version failed, once returning began.
	Failure *domain.Error `json:"failure,omitempty"`
}

// handleUpdate moves a deployment to the latest commit of its ref, or of the
// ref in the request. A running deployment keeps serving while the new
// version builds. Its container is then replaced, and if the new version
// doesn't become healthy, the previous one is started again. Other
// deployments are rebuilt and started like new ones. The name, URL, and
// lifetime stay the same.
func (c *Controller) handleUpdate(ctx context.Context, op domain.Operation) (any, error) {
	d, err := c.store.GetDeployment(ctx, op.DeploymentID)
	if err != nil || d.State == domain.StateDestroyed {
		return nil, err
	}
	if len(op.Checkpoint) > 0 {
		var plan updatePlan
		if err := json.Unmarshal(op.Checkpoint, &plan); err != nil {
			return nil, err
		}
		return c.finishUpdate(ctx, op, d.Name, plan)
	}
	if d.ExpiredAt(c.store.Now()) {
		return nil, expired(d)
	}
	var req domain.UpdateDeploymentRequest
	if len(op.Request) > 0 {
		if err := json.Unmarshal(op.Request, &req); err != nil {
			return nil, err
		}
	}
	serving := d.State == domain.StateRunning
	if serving {
		running, err := c.runtime.Running(ctx, d.ID)
		if err != nil {
			return nil, err
		}
		if !running {
			// The host restarted while the update waited, and the reconciler
			// leaves busy deployments to their operation.
			if err := c.startAndWait(ctx, d); err != nil {
				return nil, err
			}
		}
	}
	result := domain.UpdateResult{PreviousCommit: d.Commit}
	next, err := c.build(ctx, d, buildOptions{ref: cmp.Or(req.Ref, d.Ref), announce: !serving, fetched: func(v version) error {
		result.Commit = v.Commit
		if serving && v.Commit == d.Commit && v.Ref == d.Ref {
			return errUnchanged
		}
		return nil
	}})
	switch {
	case errors.Is(err, errUnchanged):
		// Queueing the update cleared the log; say why it stays short.
		os.WriteFile(c.buildLog(d.ID), fmt.Appendf(nil, "%s still points at %s; nothing was rebuilt\n", d.Ref, d.Commit), 0o600)
		return result, nil
	case err != nil && serving && ctx.Err() == nil:
		return nil, keptPrevious(err, d.Name, d.Commit, false)
	case err != nil:
		return nil, err
	}
	result.Changed = true
	plan := updatePlan{Previous: versionOf(d), Next: next, Result: result, Serving: serving}
	if err := c.store.SetCheckpoint(ctx, op.ID, plan); err != nil {
		c.removeImage(ctx, d.ID, next.Image, d.Image)
		return nil, err
	}
	return c.finishUpdate(ctx, op, d.Name, plan)
}

// finishUpdate starts the plan's next version and, if that fails while the
// previous version was serving, starts the previous one again. Every step can
// run again after an interruption.
func (c *Controller) finishUpdate(ctx context.Context, op domain.Operation, name string, plan updatePlan) (any, error) {
	id := op.DeploymentID
	// Images are removed only once the outcome is final: a retry after an
	// interruption may still need both.
	settled := func() { c.removeUnused(context.WithoutCancel(ctx), id, plan.Previous.Image, plan.Next.Image) }
	if plan.Failure == nil {
		recorded, err := c.startVersion(ctx, id, plan.Next)
		switch {
		case err == nil:
			settled()
			return plan.Result, nil
		case errors.Is(ctx.Err(), context.Canceled):
			return nil, err // Superseded by destroy, or the server is stopping.
		case !plan.Serving || !recorded:
			// Not recorded means it expired during the build; the previous
			// version is untouched, and the reconciler stops it.
			settled()
			return nil, err
		}
		if _, ok := domain.AsError(err); !ok && !errors.Is(err, context.DeadlineExceeded) {
			c.log.Error("start the new version", "deployment", id, "error", err)
		}
		plan.Failure = failureOf(err)
		c.appendAppOutput(context.WithoutCancel(ctx), id, plan.Next.Commit)
		if err := c.store.SetCheckpoint(context.WithoutCancel(ctx), op.ID, plan); err != nil {
			settled()
			return nil, err
		}
	}
	// Returning to the previous version also runs when the operation is out
	// of time, which is when it matters most.
	back := ctx
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		var cancel context.CancelFunc
		back, cancel = context.WithTimeout(context.WithoutCancel(ctx), plan.Previous.HealthTimeout+2*time.Minute)
		defer cancel()
	}
	if _, err := c.startVersion(back, id, plan.Previous); err != nil {
		if errors.Is(back.Err(), context.Canceled) {
			return nil, err
		}
		c.log.Warn("start the previous version after a failed update", "deployment", id, "error", err)
		settled()
		return nil, plan.Failure
	}
	settled()
	return nil, keptPrevious(plan.Failure, name, plan.Previous.Commit, true)
}

// startVersion records v as the deployment's version and starts it. Starting
// a version that already runs only checks its health again. recorded is false
// when nothing changed, because the deployment had expired.
func (c *Controller) startVersion(ctx context.Context, id string, v version) (recorded bool, err error) {
	d, err := c.store.UpdateDeployment(ctx, id, func(d *domain.Deployment) error {
		// Checked here, since the lifetime may have been extended meanwhile.
		if d.ExpiredAt(c.store.Now()) {
			return expired(*d)
		}
		v.apply(d)
		d.State, d.Error = domain.StateStarting, nil
		return nil
	})
	if err != nil {
		return false, err
	}
	return true, c.startAndWait(ctx, d)
}

// removeUnused deletes the images among images that the deployment no
// longer records. Docker refuses to delete an image a container still uses.
func (c *Controller) removeUnused(ctx context.Context, id string, images ...string) {
	d, err := c.store.GetDeployment(ctx, id)
	if err != nil || d.State == domain.StateDestroyed {
		return
	}
	for _, image := range images {
		c.removeImage(ctx, id, image, d.Image)
	}
}

// removeImage deletes an image an update replaced, unless it is the one in
// use, which happens when a build is fully cached.
func (c *Controller) removeImage(ctx context.Context, deploymentID, image, inUse string) {
	if image == "" || image == inUse {
		return
	}
	if err := c.runtime.RemoveImage(ctx, deploymentID, image); err != nil {
		c.log.Warn("remove replaced image", "deployment", deploymentID, "error", err)
	}
}

// appendAppOutput adds a failed version's output to the build log, because
// starting the previous version replaces its container and its logs.
func (c *Controller) appendAppOutput(ctx context.Context, id, commit string) {
	out, err := c.runtime.Logs(ctx, id, 200)
	if err != nil || out == "" {
		return
	}
	f, err := os.OpenFile(c.buildLog(id), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n--- Output of %s before the previous version was started again ---\n%s", short(commit), out)
}

// keptPrevious explains a failed update after which the previous version,
// at commit, still serves. appOutput points to the failed version's output.
func keptPrevious(err error, name, commit string, appOutput bool) error {
	e, ok := domain.AsError(err)
	if !ok {
		return err // Unexpected; the dispatcher logs it.
	}
	kept := *e
	kept.Message += fmt.Sprintf("; %s still runs %s", name, short(commit))
	if appOutput {
		kept.Hint = fmt.Sprintf("the new version's output is at the end of: deployer logs %s --build", name)
	}
	return &kept
}

// failureOf returns err as the error an operation records.
func failureOf(err error) *domain.Error {
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.Errorf(domain.CodeTimeout, "the operation did not finish before its deadline")
	}
	if e, ok := domain.AsError(err); ok {
		return e
	}
	return domain.Errorf(domain.CodeInternal, "the operation failed unexpectedly; the server log has details")
}

func expired(d domain.Deployment) error {
	return domain.Errorf(domain.CodeInvalidState, "%s has expired", d.Name).WithHint("extend it first: deployer extend %s --by 1d", d.Name)
}

func short(commit string) string { return commit[:min(12, len(commit))] }

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
// is retained for diagnosis until its purge time. A failed update after which
// the previous version still serves leaves the deployment as it was.
func (c *Controller) onFailure(ctx context.Context, op domain.Operation, failure *domain.Error) {
	if op.DeploymentID == "" || failure.Code == domain.CodeCancelled {
		return
	}
	log := c.log.With("deployment", op.DeploymentID, "operation", op.ID)
	d, err := c.store.UpdateDeployment(ctx, op.DeploymentID, func(d *domain.Deployment) error {
		if d.State == domain.StateDestroyed {
			return errSkip
		}
		if op.Kind == domain.OpUpdateDeployment && d.State == domain.StateRunning {
			return errSkip // The previous version still serves.
		}
		d.Error = failure
		switch op.Kind {
		case domain.OpCreateDeployment, domain.OpStartDeployment, domain.OpUpdateDeployment:
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
