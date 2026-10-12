// Package container defines how the controller builds and runs deployment
// images. The docker subpackage implements it with the Docker CLI.
package container

import (
	"context"
	"io"
)

// BuildSpec describes one image build.
type BuildSpec struct {
	DeploymentID string
	Dockerfile   string // Absolute path inside the checkout.
	Context      string // Absolute path of the build context directory.
}

// RunSpec describes one application container.
type RunSpec struct {
	DeploymentID string
	Image        string
	Port         int
	Env          map[string]string
}

// Runtime builds and runs deployments. Every method is idempotent and acts
// only on resources owned by this installation and deployment, so retried
// operations adopt what an interrupted attempt created.
type Runtime interface {
	// Build builds an image and returns its immutable ID. Build output is
	// written to log.
	Build(ctx context.Context, spec BuildSpec, log io.Writer) (string, error)
	// Start creates the deployment's network and container if needed and
	// starts the container.
	Start(ctx context.Context, spec RunSpec) error
	// Stop stops the container, keeping it and its image.
	Stop(ctx context.Context, deploymentID string) error
	// Remove deletes the container, network, and image.
	Remove(ctx context.Context, deploymentID string) error
	// Running reports whether the container is running.
	Running(ctx context.Context, deploymentID string) (bool, error)
	// Logs returns the last lines of the container's output.
	Logs(ctx context.Context, deploymentID string, lines int) (string, error)
	// Upstream is the address the gateway uses to reach the container.
	Upstream(deploymentID string, port int) string
	// AttachGateway connects the gateway to the deployment's network, which
	// is needed again after the gateway container is recreated.
	AttachGateway(ctx context.Context, deploymentID string) error
}
