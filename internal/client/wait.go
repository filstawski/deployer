package client

import (
	"context"
	"time"

	"github.com/filstawski/deployer/internal/domain"
)

// Wait polls an operation until it finishes, calling progress after each
// poll. It returns the final operation. A failed or cancelled operation is
// returned together with its error. If ctx ends first, the operation keeps
// running on the server and Wait returns a WAIT_TIMEOUT error.
func (c *Client) Wait(ctx context.Context, id string, progress func(domain.Operation)) (domain.Operation, error) {
	interval := 500 * time.Millisecond
	for {
		op, err := c.Operation(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return op, waitTimeout(id)
			}
			return op, err
		}
		if progress != nil {
			progress(op)
		}
		if op.Status.Done() {
			if op.Error != nil {
				return op, op.Error
			}
			return op, nil
		}
		select {
		case <-ctx.Done():
			return op, waitTimeout(id)
		case <-time.After(interval):
		}
		interval = min(interval*5/4, 2*time.Second)
	}
}

func waitTimeout(id string) error {
	return domain.Errorf(domain.CodeWaitTimeout, "stopped waiting; operation %s continues on the server", id).
		WithHint("resume with: deployer operation wait %s", id)
}
