package deployments

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"deployer/internal/command"
	"deployer/internal/config"
	"deployer/internal/domain"
	"deployer/internal/gateway"
	"deployer/internal/store/sqlite"
)

type Runtime interface {
	Build(context.Context, domain.Deployment, config.Recipe, string, io.Writer) (string, error)
	Start(context.Context, domain.Deployment) error
	Stop(context.Context, domain.Deployment) error
	Remove(context.Context, domain.Deployment) error
	Running(context.Context, domain.Deployment) (bool, error)
	Upstream(domain.Deployment) string
	Publish(context.Context, []gateway.Route) error
	Probe(context.Context, string) error
	Logs(context.Context, domain.Deployment) (string, error)
}
type Fetch func(context.Context, string, string, string, string) (string, error)
type Service struct {
	Store   *sqlite.Store
	Config  config.Server
	Runtime Runtime
	Fetch   Fetch
	Log     *slog.Logger
	locks   sync.Map
	publish sync.Mutex
}

func (s *Service) lock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}
func (s *Service) directory(id string) string {
	return filepath.Join(s.Config.StateDir, "deployments", id)
}
func (s *Service) Publish(ctx context.Context) error {
	s.publish.Lock()
	defer s.publish.Unlock()
	all, err := s.Store.Deployments(ctx)
	if err != nil {
		return err
	}
	routes := []gateway.Route{}
	for _, d := range all {
		if d.State == "destroyed" {
			continue
		}
		deadline := d.Deadline
		if d.ExpiresAt != nil {
			deadline = *d.ExpiresAt
		}
		r := gateway.Route{Hostname: d.Hostname, State: d.State, HealthPath: d.HealthPath, Deadline: deadline}
		if d.Image != "" {
			r.Upstream = s.Runtime.Upstream(d)
		}
		routes = append(routes, r)
	}
	return s.Runtime.Publish(ctx, routes)
}
func (s *Service) save(ctx context.Context, d domain.Deployment) error {
	if err := s.Store.SaveDeployment(ctx, d); err != nil {
		return err
	}
	return s.Publish(ctx)
}
func (s *Service) Action(ctx context.Context, id, action, actor, key string, seconds int64) (sqlite.Operation, error) {
	if op, found, err := s.Store.ActionRetry(ctx, id, action, actor, key, seconds); found || err != nil {
		return op, err
	}
	lock := s.lock(id)
	if !lock.TryLock() {
		return sqlite.Operation{}, domain.Fail("OPERATION_IN_PROGRESS", "deployment operation is still running")
	}
	defer lock.Unlock()
	return s.Store.QueueAction(ctx, id, action, actor, key, seconds)
}
func (s *Service) Handle(ctx context.Context, o sqlite.Operation) (any, error) {
	lock := s.lock(o.DeploymentID)
	lock.Lock()
	defer lock.Unlock()
	d, err := s.Store.Deployment(ctx, o.DeploymentID)
	if err != nil {
		return nil, err
	}
	switch strings.TrimPrefix(o.Kind, "deployment.") {
	case "create":
		err = s.create(ctx, &d)
	case "wake":
		if d.ExpiresAt == nil || !s.Store.Now().Before(*d.ExpiresAt) {
			err = domain.Fail("INVALID_STATE", "deployment lease expired")
		} else {
			err = s.start(ctx, &d)
		}
	case "stop":
		d.State = "stopped"
		if err = s.save(ctx, d); err == nil {
			err = s.Runtime.Stop(ctx, d)
		}
	case "destroy":
		d.State = "destroying"
		now := s.Store.Now()
		d.PurgeAt = &now
		if err = s.save(ctx, d); err == nil {
			err = s.destroy(ctx, &d)
		}
	case "extend":
		err = s.Publish(ctx)
	default:
		err = domain.Fail("UNSUPPORTED_FEATURE", "unknown operation")
	}
	if err != nil {
		return nil, err
	}
	d.Environment = nil
	return d, nil
}
func (s *Service) create(ctx context.Context, d *domain.Deployment) error {
	if d.State == "running" {
		return nil
	} // Crash after readiness, before operation completion.
	if d.Image == "" {
		d.State = "building"
		if err := s.save(ctx, *d); err != nil {
			return err
		}
		dir := s.directory(d.ID)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		source := filepath.Join(dir, "source")
		p, err := s.Store.Project(ctx, d.ProjectID)
		if err != nil {
			return err
		}
		commit, err := s.Fetch(ctx, p.Repository, d.Ref, d.Commit, source)
		if err != nil {
			return err
		}
		d.Commit = commit
		if err = s.Store.SaveDeployment(ctx, *d); err != nil {
			return err
		}
		recipe, err := config.LoadRecipe(source)
		if err != nil {
			return domain.Fail("INVALID_RECIPE", err.Error())
		}
		d.Port = recipe.HTTP.Port
		d.HealthPath = recipe.HTTP.Healthcheck.Path
		d.HealthTimeout = recipe.HTTP.Healthcheck.Timeout
		d.Environment = recipe.Environment
		log, err := os.OpenFile(filepath.Join(dir, "build.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		image, err := s.Runtime.Build(ctx, *d, recipe, source, &command.LimitWriter{W: log, Remaining: 1 << 20})
		log.Close()
		if err != nil {
			return err
		}
		d.Image = image
		if err = s.Store.SaveDeployment(ctx, *d); err != nil {
			return err
		}
	}
	return s.start(ctx, d)
}
func (s *Service) start(ctx context.Context, d *domain.Deployment) error {
	d.State = "starting"
	d.Error = nil
	if err := s.save(ctx, *d); err != nil {
		return err
	}
	if err := s.Runtime.Start(ctx, *d); err != nil {
		return err
	}
	duration, err := time.ParseDuration(d.HealthTimeout)
	if err != nil {
		return err
	}
	ready, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	for {
		if d.ExpiresAt != nil && !s.Store.Now().Before(*d.ExpiresAt) {
			return domain.Fail("INVALID_STATE", "deployment expired while starting")
		}
		if err = s.Runtime.Probe(ready, d.Hostname); err == nil {
			break
		}
		select {
		case <-ready.Done():
			return domain.Fail("READINESS_FAILED", "application did not become healthy; inspect container logs")
		case <-time.After(300 * time.Millisecond):
		}
	}
	if d.ExpiresAt == nil {
		expires := s.Store.Now().Add(time.Duration(d.TTLSeconds) * time.Second)
		d.ExpiresAt = &expires
		purge := expires.Add(7 * 24 * time.Hour)
		d.PurgeAt = &purge
	}
	d.State = "running"
	return s.save(ctx, *d)
}
func (s *Service) Failed(ctx context.Context, o sqlite.Operation, failure *domain.Error) error {
	lock := s.lock(o.DeploymentID)
	lock.Lock()
	defer lock.Unlock()
	d, err := s.Store.Deployment(ctx, o.DeploymentID)
	if err != nil {
		return err
	}
	d.State = "failed"
	d.Error = failure
	if d.PurgeAt == nil {
		purge := s.Store.Now().Add(7 * 24 * time.Hour)
		d.PurgeAt = &purge
	}
	// Persist closure even if gateway or Docker is down. Reconciliation retries cleanup.
	if err = s.Store.SaveDeployment(ctx, d); err != nil {
		return err
	}
	if err = s.Publish(ctx); err != nil {
		s.Log.Error("route failure publication", "error", err)
	}
	stopCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err = s.Runtime.Stop(stopCtx, d); err != nil {
		s.Log.Error("failed deployment stop", "deployment", d.ID, "error", err)
	}
	return nil
}
func (s *Service) destroy(ctx context.Context, d *domain.Deployment) error {
	if err := s.Runtime.Remove(ctx, *d); err != nil {
		return err
	}
	if err := os.RemoveAll(s.directory(d.ID)); err != nil {
		return err
	}
	d.State = "destroyed"
	d.Environment = nil
	return s.save(ctx, *d)
}
func (s *Service) Reconcile(ctx context.Context) error {
	all, err := s.Store.Deployments(ctx)
	if err != nil {
		return err
	}
	for _, entry := range all {
		lock := s.lock(entry.ID)
		if !lock.TryLock() {
			continue
		}
		err = s.reconcileOne(ctx, entry.ID)
		lock.Unlock()
		if err != nil {
			s.Log.Error("deployment reconciliation", "deployment", entry.ID, "error", err)
		}
	}
	return s.Publish(ctx)
}
func (s *Service) reconcileOne(ctx context.Context, id string) error {
	d, err := s.Store.Deployment(ctx, id)
	if err != nil {
		return err
	}
	if d.State == "destroyed" {
		return nil
	}
	if d.PurgeAt != nil && !s.Store.Now().Before(*d.PurgeAt) {
		d.State = "destroying"
		if err = s.save(ctx, d); err != nil {
			return err
		}
		return s.destroy(ctx, &d)
	}
	if d.ExpiresAt != nil && !s.Store.Now().Before(*d.ExpiresAt) && d.State != "failed" && d.State != "expired" {
		d.State = "expired"
		if err = s.save(ctx, d); err != nil {
			return err
		}
	}
	// Expiration always stops containers, even while a management action is queued.
	if d.State == "expired" {
		running, err := s.Runtime.Running(ctx, d)
		if err != nil {
			return err
		}
		if running {
			return s.Runtime.Stop(ctx, d)
		}
		return nil
	}
	// Do not race pending actions (notably extend) or recoverable builds.
	var pending int
	if err = s.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM operations WHERE deployment_id=? AND status IN ('queued','running')", id).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return nil
	}
	switch d.State {
	case "stopped", "failed":
		running, err := s.Runtime.Running(ctx, d)
		if err != nil {
			return err
		}
		if running {
			return s.Runtime.Stop(ctx, d)
		}
	case "queued", "building", "starting", "destroying":
		d.State = "failed"
		d.Error = domain.Fail("INTERRUPTED", "operation ended without a completed deployment")
		if d.PurgeAt == nil {
			purge := s.Store.Now().Add(7 * 24 * time.Hour)
			d.PurgeAt = &purge
		}
		if err = s.save(ctx, d); err != nil {
			return err
		}
		return s.Runtime.Stop(ctx, d)
	case "running":
		running, err := s.Runtime.Running(ctx, d)
		if err != nil {
			return err
		}
		if !running {
			d.State = "failed"
			d.Error = domain.Fail("CONTAINER_EXITED", "container exited; inspect logs, then wake to retry")
			return s.save(ctx, d)
		}
	}
	return nil
}
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		job, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := s.Reconcile(job); err != nil && ctx.Err() == nil {
			s.Log.Error("reconciliation failed", "error", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (s *Service) Logs(ctx context.Context, d domain.Deployment, build bool) (string, error) {
	if !build {
		return s.Runtime.Logs(ctx, d)
	}
	f, err := os.Open(filepath.Join(s.directory(d.ID), "build.log"))
	if os.IsNotExist(err) {
		return "", domain.Fail("NOT_FOUND", "build log is not available yet")
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1<<20))
	return string(b), err
}
