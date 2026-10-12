// Package docker implements container.Runtime with the Docker CLI. It never
// runs a shell and never prunes globally: every resource it changes must
// carry this installation's ownership labels.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/filstawski/deployer/internal/container"
	"github.com/filstawski/deployer/internal/domain"
	"github.com/filstawski/deployer/internal/execx"
)

// Ownership labels on every resource Deployer creates.
const (
	labelInstallation = "io.deployer.installation"
	labelDeployment   = "io.deployer.deployment"
	labelSpec         = "io.deployer.spec"
)

// Limits bounds each application container.
type Limits struct {
	CPUs     float64
	MemoryMB int
	PIDs     int
}

// Options configures the runtime.
type Options struct {
	InstallationID string
	// StateDir holds the gateway token and the gateway image build context.
	StateDir string
	Limits   Limits
	// Egress lets applications open outbound connections.
	Egress bool
	// Gateway settings; see EnsureGateway.
	Gateway GatewayOptions
}

// Docker is the Docker-backed runtime.
type Docker struct {
	opts    Options
	gateway string
	builder string
}

var _ container.Runtime = (*Docker)(nil)

// New returns a runtime. Call EnsureGateway and EnsureBuilder before use.
func New(opts Options) *Docker {
	return &Docker{opts: opts, gateway: "deployer-gateway-" + opts.InstallationID, builder: "deployer-builder-" + opts.InstallationID}
}

func appName(id string) string   { return "deployer-" + id }
func imageName(id string) string { return "deployer/" + id + ":latest" }

// alias is the container's DNS name on its network. Deployment IDs contain
// an underscore, which is not valid in hostnames.
func alias(id string) string { return strings.ReplaceAll(id, "_", "-") }

func (d *Docker) run(ctx context.Context, log io.Writer, args ...string) (string, error) {
	return execx.Run(ctx, execx.Cmd{Name: "docker", Args: args, Log: log})
}

func (d *Docker) labels(deploymentID string) []string {
	return []string{"--label", labelInstallation + "=" + d.opts.InstallationID, "--label", labelDeployment + "=" + deploymentID}
}

// inspectLabels returns a resource's labels, or found=false if it does not
// exist. It fails if the resource exists but belongs to someone else.
func (d *Docker) inspectLabels(ctx context.Context, kind, name, deploymentID string) (map[string]string, bool, error) {
	format := "{{json .Config.Labels}}"
	if kind == "network" {
		format = "{{json .Labels}}"
	}
	out, err := d.run(ctx, nil, kind, "inspect", "--format", format, name)
	if err != nil {
		if missing(out) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(out), &labels); err != nil {
		return nil, false, fmt.Errorf("docker %s inspect %s: %w", kind, name, err)
	}
	if labels[labelInstallation] != d.opts.InstallationID || labels[labelDeployment] != deploymentID {
		return nil, false, fmt.Errorf("refusing to modify %s %q: it is not owned by this Deployer installation", kind, name)
	}
	return labels, true, nil
}

func missing(output string) bool {
	return strings.Contains(output, "No such") || strings.Contains(output, "not found")
}

// Build implements container.Runtime.
func (d *Docker) Build(ctx context.Context, spec container.BuildSpec, log io.Writer) (string, error) {
	args := []string{"buildx", "build", "--builder", d.builder, "--load", "--progress", "plain",
		"--tag", imageName(spec.DeploymentID), "--file", spec.Dockerfile}
	args = append(args, d.labels(spec.DeploymentID)...)
	if _, err := d.run(ctx, log, append(args, spec.Context)...); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", domain.Errorf(domain.CodeBuildFailed, "the image build failed")
	}
	out, err := d.run(ctx, nil, "image", "inspect", "--format", "{{.Id}}", imageName(spec.DeploymentID))
	return strings.TrimSpace(out), err
}

// Start implements container.Runtime.
func (d *Docker) Start(ctx context.Context, spec container.RunSpec) error {
	id := spec.DeploymentID
	if _, exists, err := d.inspectLabels(ctx, "network", appName(id), id); err != nil {
		return err
	} else if !exists {
		args := []string{"network", "create", "--driver", "bridge"}
		if !d.opts.Egress {
			// No route out, and no host address on the bridge.
			args = append(args, "--internal", "--opt", "com.docker.network.bridge.gateway_mode_ipv4=isolated")
		}
		args = append(args, d.labels(id)...)
		if _, err := d.run(ctx, nil, append(args, appName(id))...); err != nil {
			return err
		}
	}
	if err := d.AttachGateway(ctx, id); err != nil {
		return err
	}
	labels, exists, err := d.inspectLabels(ctx, "container", appName(id), id)
	if err != nil {
		return err
	}
	wantSpec := runSpecHash(spec, d.opts)
	if exists && labels[labelSpec] != wantSpec {
		if _, err := d.run(ctx, nil, "rm", "--force", appName(id)); err != nil {
			return err
		}
		exists = false
	}
	if !exists {
		if _, err := d.run(ctx, nil, d.createArgs(spec, wantSpec)...); err != nil {
			return domain.Errorf(domain.CodeStartFailed, "could not create the container: %s", strings.TrimSpace(lastLine(err.Error())))
		}
	}
	if _, err := d.run(ctx, nil, "start", appName(id)); err != nil {
		return domain.Errorf(domain.CodeStartFailed, "could not start the container: %s", strings.TrimSpace(lastLine(err.Error())))
	}
	return nil
}

func (d *Docker) createArgs(spec container.RunSpec, specHash string) []string {
	l := d.opts.Limits
	memory := strconv.Itoa(l.MemoryMB) + "m"
	args := []string{"create", "--name", appName(spec.DeploymentID),
		"--network", appName(spec.DeploymentID), "--network-alias", alias(spec.DeploymentID),
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--memory", memory, "--memory-swap", memory, "--cpus", strconv.FormatFloat(l.CPUs, 'f', -1, 64), "--pids-limit", strconv.Itoa(l.PIDs),
		"--log-driver", "json-file", "--log-opt", "max-size=10m", "--log-opt", "max-file=3",
		// The controller decides when containers run, including after reboots.
		"--restart", "no",
		"--label", labelSpec + "=" + specHash,
	}
	// Keep Docker's default capabilities except those applications rarely need.
	for _, c := range []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID", "NET_BIND_SERVICE", "KILL"} {
		args = append(args, "--cap-add", c)
	}
	args = append(args, d.labels(spec.DeploymentID)...)
	keys := make([]string, 0, len(spec.Env))
	for k := range spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--env", k+"="+spec.Env[k])
	}
	return append(args, spec.Image)
}

// runSpecHash identifies a container's configuration, so a changed image or
// setting replaces the container instead of reusing a stale one.
func runSpecHash(spec container.RunSpec, opts Options) string {
	b, _ := json.Marshal([]any{spec, opts.Limits, opts.Egress})
	return hashBytes(b)[:16]
}

// Stop implements container.Runtime.
func (d *Docker) Stop(ctx context.Context, id string) error {
	if _, exists, err := d.inspectLabels(ctx, "container", appName(id), id); err != nil || !exists {
		return err
	}
	_, err := d.run(ctx, nil, "stop", "--time", "10", appName(id))
	return err
}

// Remove implements container.Runtime.
func (d *Docker) Remove(ctx context.Context, id string) error {
	if _, exists, err := d.inspectLabels(ctx, "container", appName(id), id); err != nil {
		return err
	} else if exists {
		if _, err := d.run(ctx, nil, "rm", "--force", appName(id)); err != nil {
			return err
		}
	}
	if _, exists, err := d.inspectLabels(ctx, "network", appName(id), id); err != nil {
		return err
	} else if exists {
		if _, err := d.run(ctx, nil, "network", "disconnect", "--force", appName(id), d.gateway); err != nil && !missing(err.Error()) && !strings.Contains(err.Error(), "is not connected") {
			return err
		}
		if _, err := d.run(ctx, nil, "network", "rm", appName(id)); err != nil {
			return err
		}
	}
	if _, exists, err := d.inspectLabels(ctx, "image", imageName(id), id); err != nil {
		return err
	} else if exists {
		if _, err := d.run(ctx, nil, "image", "rm", "--force", imageName(id)); err != nil && !missing(err.Error()) {
			return err
		}
	}
	return nil
}

// Running implements container.Runtime.
func (d *Docker) Running(ctx context.Context, id string) (bool, error) {
	if _, exists, err := d.inspectLabels(ctx, "container", appName(id), id); err != nil || !exists {
		return false, err
	}
	out, err := d.run(ctx, nil, "container", "inspect", "--format", "{{.State.Running}}", appName(id))
	return strings.TrimSpace(out) == "true", err
}

// Logs implements container.Runtime.
func (d *Docker) Logs(ctx context.Context, id string, lines int) (string, error) {
	if _, exists, err := d.inspectLabels(ctx, "container", appName(id), id); err != nil {
		return "", err
	} else if !exists {
		return "", domain.Errorf(domain.CodeNotFound, "the deployment has no container yet")
	}
	return d.run(ctx, nil, "logs", "--tail", strconv.Itoa(lines), appName(id))
}

// Upstream implements container.Runtime.
func (d *Docker) Upstream(id string, port int) string {
	return "http://" + alias(id) + ":" + strconv.Itoa(port)
}

// AttachGateway implements container.Runtime.
func (d *Docker) AttachGateway(ctx context.Context, id string) error {
	if _, exists, err := d.inspectLabels(ctx, "network", appName(id), id); err != nil || !exists {
		return err
	}
	out, err := d.run(ctx, nil, "container", "inspect", "--format", "{{json .NetworkSettings.Networks}}", d.gateway)
	if err != nil {
		return err
	}
	var networks map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &networks); err != nil {
		return err
	}
	if _, ok := networks[appName(id)]; ok {
		return nil
	}
	_, err = d.run(ctx, nil, "network", "connect", appName(id), d.gateway)
	return err
}

// RemoveAll deletes every container, network, and image owned by this
// installation, plus its builder. It is used by uninstall and tests.
func (d *Docker) RemoveAll(ctx context.Context) error {
	filter := "label=" + labelInstallation + "=" + d.opts.InstallationID
	for _, kind := range []string{"container", "network", "image"} {
		out, err := d.run(ctx, nil, kind, "ls", "--all", "--quiet", "--filter", filter)
		if kind == "network" {
			out, err = d.run(ctx, nil, kind, "ls", "--quiet", "--filter", filter)
		}
		if err != nil {
			return err
		}
		ids := strings.Fields(out)
		if len(ids) == 0 {
			continue
		}
		rm := []string{kind, "rm"}
		if kind != "network" {
			rm = append(rm, "--force")
		}
		if _, err := d.run(ctx, nil, append(rm, ids...)...); err != nil && !missing(err.Error()) {
			return err
		}
	}
	if _, err := d.run(ctx, nil, "buildx", "rm", "--force", d.builder); err != nil && !missing(err.Error()) {
		return err
	}
	return nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.Index(s, "Error response from daemon: "); i >= 0 {
		s = s[i+len("Error response from daemon: "):]
	}
	return s
}
