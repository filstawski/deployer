// Package fakes provides in-memory stand-ins for the controller's container
// runtime, gateway router, and Git source, for tests.
package fakes

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/filstawski/deployer/internal/container"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/gateway"
)

// Runtime is a container.Runtime that tracks containers in memory.
type Runtime struct {
	mu      sync.Mutex
	running map[string]bool
	images  map[string]map[string]bool // Deployment ID → image IDs.
	image   map[string]string          // Deployment ID → the running image.
	env     map[string]map[string]string
	builds  int

	// BuildErr makes builds fail.
	BuildErr error
	// BlockBuild makes the build of this deployment wait for cancellation.
	BlockBuild string
	// Gate, when set, holds every build until it receives a value.
	Gate chan struct{}
	// ExitOnStart makes containers stop right after starting.
	ExitOnStart bool
	// StartErr, when set, decides whether starting a container fails.
	StartErr func(spec container.RunSpec) error
}

var _ container.Runtime = (*Runtime)(nil)

// NewRuntime returns an empty runtime.
func NewRuntime() *Runtime {
	return &Runtime{running: map[string]bool{}, images: map[string]map[string]bool{}, image: map[string]string{}, env: map[string]map[string]string{}}
}

// Set changes the runtime's behavior under its lock.
func (r *Runtime) Set(fn func(r *Runtime)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(r)
}

// Build implements container.Runtime.
func (r *Runtime) Build(ctx context.Context, spec container.BuildSpec, log io.Writer) (string, error) {
	fmt.Fprintln(log, "step 1/2")
	fmt.Fprintln(log, "step 2/2")
	r.mu.Lock()
	r.builds++
	block, gate, err := r.BlockBuild == spec.DeploymentID, r.Gate, r.BuildErr
	r.mu.Unlock()
	if block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	image := fmt.Sprintf("sha256:%s-%d", spec.DeploymentID, r.builds)
	if r.images[spec.DeploymentID] == nil {
		r.images[spec.DeploymentID] = map[string]bool{}
	}
	r.images[spec.DeploymentID][image] = true
	return image, nil
}

// Start implements container.Runtime. Like Docker, it replaces the
// container when the spec changes.
func (r *Runtime) Start(_ context.Context, spec container.RunSpec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.images[spec.DeploymentID][spec.Image] {
		return domain.Errorf(domain.CodeStartFailed, "no such image %s", spec.Image)
	}
	r.image[spec.DeploymentID] = spec.Image
	r.env[spec.DeploymentID] = spec.Env
	if r.StartErr != nil {
		if err := r.StartErr(spec); err != nil {
			r.running[spec.DeploymentID] = false
			return err
		}
	}
	r.running[spec.DeploymentID] = !r.ExitOnStart
	return nil
}

// Stop implements container.Runtime.
func (r *Runtime) Stop(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running[id] = false
	return nil
}

// Remove implements container.Runtime.
func (r *Runtime) Remove(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.running, id)
	delete(r.images, id)
	delete(r.image, id)
	return nil
}

// RemoveImage implements container.Runtime.
func (r *Runtime) RemoveImage(_ context.Context, id, image string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.image[id] == image {
		return fmt.Errorf("image %s is in use", image)
	}
	delete(r.images[id], image)
	return nil
}

// Running implements container.Runtime.
func (r *Runtime) Running(_ context.Context, id string) (bool, error) { return r.IsRunning(id), nil }

// Logs implements container.Runtime.
func (r *Runtime) Logs(context.Context, string, int) (string, error) {
	return "listening on :3000\n", nil
}

// Upstream implements container.Runtime.
func (r *Runtime) Upstream(id string, port int) string {
	return fmt.Sprintf("http://%s:%d", strings.ReplaceAll(id, "_", "-"), port)
}

// AttachGateway implements container.Runtime.
func (r *Runtime) AttachGateway(context.Context, string) error { return nil }

// IsRunning reports whether a deployment's container runs.
func (r *Runtime) IsRunning(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running[id]
}

// HasImage reports whether a deployment has any image.
func (r *Runtime) HasImage(id string) bool { return len(r.Images(id)) > 0 }

// Images returns a deployment's image IDs, sorted.
func (r *Runtime) Images(id string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Sorted(maps.Keys(r.images[id]))
}

// Image returns the image a deployment's container was last started from.
func (r *Runtime) Image(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.image[id]
}

// Env returns the environment a deployment's container was started with.
func (r *Runtime) Env(id string) map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.env[id]
}

// Builds returns the number of builds run.
func (r *Runtime) Builds() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.builds
}

// Router records published routes. Probes succeed for routes with an
// upstream unless Unhealthy is set.
type Router struct {
	mu        sync.Mutex
	routes    map[string]gateway.Route
	unhealthy bool
}

// NewRouter returns an empty router.
func NewRouter() *Router { return &Router{routes: map[string]gateway.Route{}} }

// Publish implements controller.Router.
func (f *Router) Publish(_ context.Context, routes []gateway.Route) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes = map[string]gateway.Route{}
	for _, r := range routes {
		f.routes[r.Host] = r
	}
	return nil
}

// Probe implements controller.Router.
func (f *Router) Probe(_ context.Context, host string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.routes[host]; !ok || r.Upstream == "" || f.unhealthy {
		return fmt.Errorf("connection refused")
	}
	return nil
}

// SetUnhealthy makes probes fail.
func (f *Router) SetUnhealthy(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unhealthy = v
}

// Route returns the published route for host.
func (f *Router) Route(host string) (gateway.Route, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.routes[host]
	return r, ok
}

// Recipe is a valid deployer.yaml for the fake source.
const Recipe = "version: 1\nsource:\n  dockerfile: Dockerfile\nhttp:\n  port: 3000\n  healthcheck:\n    timeout: 1s\nenvironment:\n  NODE_ENV: production\n"

// Source writes a checkout containing Recipe and a Dockerfile. The ref
// "missing" does not exist.
type Source struct {
	mu      sync.Mutex
	recipe  string
	commits map[string]string
}

// NewSource returns a source serving Recipe.
func NewSource() *Source { return &Source{recipe: Recipe, commits: map[string]string{}} }

// Push makes ref point at a new commit, as if someone pushed to it, and
// returns the commit.
func (s *Source) Push(ref string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	commit := Commit(ref + "+" + s.commits[ref])
	s.commits[ref] = commit
	return commit
}

// SetRecipe replaces the deployer.yaml content; empty omits the file.
func (s *Source) SetRecipe(recipe string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recipe = recipe
}

// Fetch implements controller.Source. The commit is derived from the ref.
func (s *Source) Fetch(_ context.Context, _, ref, pinned, dest string) (string, error) {
	if ref == "missing" {
		return "", domain.Errorf(domain.CodeSourceFailed, "%q was not found in the repository", ref)
	}
	s.mu.Lock()
	recipe, head := s.recipe, s.commits[ref]
	s.mu.Unlock()
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return "", err
	}
	if recipe != "" {
		os.WriteFile(filepath.Join(dest, "deployer.yaml"), []byte(recipe), 0o644)
	}
	os.WriteFile(filepath.Join(dest, "Dockerfile"), []byte("FROM scratch\n"), 0o644)
	switch {
	case pinned != "":
		return pinned, nil
	case head != "":
		return head, nil
	}
	return Commit(ref), nil
}

// Verify implements controller.Source.
func (s *Source) Verify(context.Context, string) (string, error) { return Commit("main"), nil }

// Commit returns the fake commit SHA for a ref.
func Commit(ref string) string {
	sum := sha1.Sum([]byte(ref))
	return hex.EncodeToString(sum[:])
}
