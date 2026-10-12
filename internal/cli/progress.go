package cli

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/filstawski/deployer/internal/client"
	"github.com/filstawski/deployer/internal/domain"
)

// status shows what a long-running command is waiting for on stderr. On a
// terminal it is a single animated line; otherwise each change is printed
// once, which keeps CI and agent logs readable.
type status struct {
	w        io.Writer
	live     bool
	disabled bool
	start    time.Time

	mu    sync.Mutex
	text  string
	frame int
	stop  chan struct{}
	done  sync.WaitGroup
}

var frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// newStatus starts a status line. animate selects the in-place line on a
// terminal; output that interleaves other text must not animate.
func (a *app) newStatus(animate bool) *status {
	s := &status{w: a.env.Stderr, live: animate && a.env.Interactive, disabled: a.quiet, start: a.env.Now(), stop: make(chan struct{})}
	if s.live && !s.disabled {
		s.done.Add(1)
		go func() {
			defer s.done.Done()
			t := time.NewTicker(100 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-s.stop:
					return
				case <-t.C:
					s.mu.Lock()
					s.frame++
					s.render()
					s.mu.Unlock()
				}
			}
		}()
	}
	return s
}

// set changes the status text.
func (s *status) set(text string) {
	if s.disabled {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if text == s.text {
		return
	}
	s.text = text
	if s.live {
		s.render()
	} else {
		fmt.Fprintf(s.w, "%s (%s)\n", text, s.elapsed())
	}
}

// print writes a permanent line above the status line.
func (s *status) print(text string) {
	if s.disabled {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.live {
		fmt.Fprint(s.w, "\r\033[K")
	}
	fmt.Fprint(s.w, text)
	if s.live {
		s.render()
	}
}

// finish removes the status line.
func (s *status) finish() {
	close(s.stop)
	s.done.Wait()
	if s.live && !s.disabled {
		fmt.Fprint(s.w, "\r\033[K")
	}
}

func (s *status) render() {
	if s.text != "" {
		fmt.Fprintf(s.w, "\r\033[K%s %s (%s)", frames[s.frame%len(frames)], s.text, s.elapsed())
	}
}

func (s *status) elapsed() string { return domain.FormatDuration(time.Since(s.start)) }

// describe explains a deployment state while waiting.
func describe(d domain.Deployment) string {
	switch d.State {
	case domain.StateQueued:
		return "Waiting for a build slot"
	case domain.StateBuilding:
		return "Building " + refAt(d)
	case domain.StateStarting:
		return "Starting and waiting for " + d.HealthPath + " to respond"
	default:
		return string(d.State)
	}
}

// waitDeployment waits for an operation on a deployment, describing the
// deployment's progress and, when follow is set, streaming its build output.
// It returns the deployment as it is once the operation finishes.
func (a *app) waitDeployment(parent context.Context, c *client.Client, op domain.Operation, timeout time.Duration, follow bool) (domain.Deployment, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	s := a.newStatus(!follow) // A spinner between lines of build output is unreadable.
	defer s.finish()
	var offset int64
	for {
		current, err := c.Operation(ctx, op.ID)
		if err == nil && follow {
			if logs, lerr := c.Logs(ctx, op.DeploymentID, client.LogsOptions{Build: true, Offset: &offset}); lerr == nil && logs.Logs != "" {
				s.print(logs.Logs)
				offset = logs.Offset
			}
		}
		d, derr := c.Deployment(ctx, op.DeploymentID)
		if ctx.Err() != nil {
			why := "stopped waiting"
			if parent.Err() == nil {
				why += " after " + domain.FormatDuration(timeout)
			}
			return d, domain.Errorf(domain.CodeWaitTimeout, "%s; the operation continues on the server", why).
				WithHint("resume with: deployer operation wait %s", op.ID)
		}
		if err != nil {
			return d, err
		}
		if derr != nil {
			return d, derr
		}
		if current.Status.Done() {
			if current.Error != nil {
				return d, current.Error
			}
			return d, nil
		}
		if d.State.InProgress() {
			s.set(describe(d))
		} else {
			s.set(waitingFor(current.Kind))
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func waitingFor(kind domain.OperationKind) string {
	switch kind {
	case domain.OpStopDeployment:
		return "Stopping"
	case domain.OpDestroyDeployment:
		return "Removing containers, network, and image"
	case domain.OpVerifyProject:
		return "Checking repository access"
	default:
		return "Waiting for " + string(kind)
	}
}

// waitOperation waits for any operation with a simple status line.
func (a *app) waitOperation(ctx context.Context, c *client.Client, id string, timeout time.Duration) (domain.Operation, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	s := a.newStatus(true)
	defer s.finish()
	return c.Wait(ctx, id, func(op domain.Operation) {
		if !op.Status.Done() {
			s.set(waitingFor(op.Kind))
		}
	})
}
