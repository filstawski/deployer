// Package controller owns the deployment lifecycle: admitting deployments,
// building and starting them, enforcing leases, and keeping the gateway's
// routes in sync with the store.
//
// API requests change state through short transactions and enqueue
// operations; the dispatcher runs the operations through the handlers
// registered by Register; and a reconciliation loop repairs drift between the
// store, Docker, and the gateway.
package controller

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/filstawski/deployer/internal/config"
	"github.com/filstawski/deployer/internal/container"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/gateway"
	"github.com/filstawski/deployer/internal/operations"
	"github.com/filstawski/deployer/internal/store"
)

// SystemActor is the actor of operations the controller starts itself.
const SystemActor = "system"

// Router publishes routes to the gateway and checks application health
// through it, since only the gateway can reach deployment networks.
type Router interface {
	Publish(ctx context.Context, routes []gateway.Route) error
	Probe(ctx context.Context, host string) error
}

// Source fetches and verifies repositories.
type Source interface {
	Fetch(ctx context.Context, repository, ref, pinned, dest string) (string, error)
	Verify(ctx context.Context, repository string) (string, error)
}

// Queue is the part of the dispatcher the controller drives.
type Queue interface {
	Notify()
	CancelOperation(id string)
}

// Deps are the controller's collaborators.
type Deps struct {
	Store   *store.Store
	Config  config.Server
	Runtime container.Runtime
	Router  Router
	Source  Source
	Log     *slog.Logger
	// ProbeInterval is the pause between health checks; tests shorten it.
	ProbeInterval time.Duration
}

// Controller implements the deployment lifecycle. Create one with New and
// connect it to a dispatcher with Register.
type Controller struct {
	store   *store.Store
	cfg     config.Server
	runtime container.Runtime
	router  Router
	source  Source
	log     *slog.Logger
	queue   Queue

	builds        chan struct{} // Semaphore bounding concurrent builds.
	locks         sync.Map      // Deployment ID → *sync.Mutex; see lock.
	publishMu     sync.Mutex
	probeInterval time.Duration
	lastPrune     time.Time
}

// New returns a controller.
func New(d Deps) *Controller {
	if d.ProbeInterval <= 0 {
		d.ProbeInterval = 500 * time.Millisecond
	}
	return &Controller{
		store: d.Store, cfg: d.Config, runtime: d.Runtime, router: d.Router, source: d.Source, log: d.Log,
		builds:        make(chan struct{}, d.Config.Deployments.BuildConcurrency),
		probeInterval: d.ProbeInterval,
	}
}

// Register installs the controller's operation handlers on d.
func (c *Controller) Register(d *operations.Dispatcher) {
	c.queue = d
	d.Handle(domain.OpCreateDeployment, c.locked(c.handleCreate))
	d.Handle(domain.OpStartDeployment, c.locked(c.handleStart))
	d.Handle(domain.OpStopDeployment, c.locked(c.handleStop))
	d.Handle(domain.OpDestroyDeployment, c.locked(c.handleDestroy))
	d.Handle(domain.OpUpdateDeployment, c.locked(c.handleUpdate))
	d.Handle(domain.OpVerifyProject, c.handleVerify)
	d.OnFailure(c.onFailure)
}

// lock returns a deployment's mutex. Operation handlers hold it while they
// run, and the reconciler acts on a deployment only while holding it, so the
// two never change the same deployment at the same time.
func (c *Controller) lock(deploymentID string) *sync.Mutex {
	m, _ := c.locks.LoadOrStore(deploymentID, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// locked wraps a deployment operation handler with the deployment's lock.
func (c *Controller) locked(h operations.Handler) operations.Handler {
	return func(ctx context.Context, op domain.Operation) (any, error) {
		m := c.lock(op.DeploymentID)
		m.Lock()
		defer m.Unlock()
		return h(ctx, op)
	}
}

// Present fills in the fields derived from configuration.
func (c *Controller) Present(d domain.Deployment) domain.Deployment {
	d.Hostname = c.cfg.Hostname(d.Name)
	d.URL = c.cfg.PreviewURL(d.Name)
	return d
}

// Deployment returns a deployment by ID or name.
func (c *Controller) Deployment(ctx context.Context, ref string) (domain.Deployment, error) {
	d, err := c.store.GetDeployment(ctx, ref)
	return c.Present(d), err
}

// Deployments lists deployments.
func (c *Controller) Deployments(ctx context.Context, f store.DeploymentFilter) ([]domain.Deployment, error) {
	list, err := c.store.ListDeployments(ctx, f)
	for i := range list {
		list[i] = c.Present(list[i])
	}
	return list, err
}

// PublishRoutes sends the gateway a complete route snapshot.
func (c *Controller) PublishRoutes(ctx context.Context) error {
	c.publishMu.Lock()
	defer c.publishMu.Unlock()
	list, err := c.store.ListDeployments(ctx, store.DeploymentFilter{})
	if err != nil {
		return err
	}
	routes := make([]gateway.Route, 0, len(list))
	for _, d := range list {
		r := gateway.Route{Host: c.cfg.Hostname(d.Name), State: d.State, HealthPath: d.HealthPath, ExpiresAt: d.ExpiresAt}
		if d.Image != "" && d.Port != 0 {
			r.Upstream = c.runtime.Upstream(d.ID, d.Port)
		}
		routes = append(routes, r)
	}
	return c.router.Publish(ctx, routes)
}

// publish is PublishRoutes for paths where the store is authoritative and the
// next reconciliation retries a failed publish.
func (c *Controller) publish(ctx context.Context) {
	if err := c.PublishRoutes(ctx); err != nil && ctx.Err() == nil {
		c.log.Warn("publish routes", "error", err)
	}
}

func (c *Controller) notify() {
	if c.queue != nil {
		c.queue.Notify()
	}
}

func (c *Controller) dir(deploymentID string) string {
	return filepath.Join(c.cfg.StateDir, "deployments", deploymentID)
}

func (c *Controller) buildLog(deploymentID string) string {
	return filepath.Join(c.dir(deploymentID), "build.log")
}

func (c *Controller) removeDir(deploymentID string) error {
	return os.RemoveAll(c.dir(deploymentID))
}
