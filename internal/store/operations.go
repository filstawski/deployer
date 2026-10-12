package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/filstawski/deployer/internal/domain"
)

const operationColumns = `id, kind, status, project_id, deployment_id, actor, attempts, error_code, error_message, error_hint, request, checkpoint, result,
	created_at, updated_at, started_at, finished_at, deadline`

// GetOperation returns an operation by ID.
func (s *Store) GetOperation(ctx context.Context, id string) (domain.Operation, error) {
	return getOperation(ctx, s.db, id)
}

// Operation returns an operation by ID within the transaction.
func (t *Tx) Operation(id string) (domain.Operation, error) { return getOperation(t.ctx, t.q, id) }

func getOperation(ctx context.Context, q querier, id string) (domain.Operation, error) {
	o, err := scanOperation(q.QueryRowContext(ctx, "SELECT "+operationColumns+" FROM operations WHERE id = ?", id))
	return o, notFound(err, "operation %q not found", id)
}

// InsertOperation stores a new queued operation.
func (t *Tx) InsertOperation(o domain.Operation) error {
	var request any
	if len(o.Request) > 0 {
		request = string(o.Request)
	}
	_, err := t.q.ExecContext(t.ctx, `INSERT INTO operations (id, kind, status, project_id, deployment_id, actor, request, created_at, updated_at, deadline)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.ID, o.Kind, o.Status, o.ProjectID, o.DeploymentID, o.Actor, request, ms(o.CreatedAt), ms(o.UpdatedAt), ms(o.Deadline))
	return err
}

// PendingOperations returns the queued and running operations of a
// deployment, oldest first.
func (t *Tx) PendingOperations(deploymentID string) ([]domain.Operation, error) {
	return listOperations(t.ctx, t.q, "WHERE deployment_id = ? AND status IN ('queued', 'running') ORDER BY created_at, id", deploymentID)
}

// CountPendingOperations returns the number of queued and running operations.
func (t *Tx) CountPendingOperations() (int, error) {
	var n int
	err := t.q.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM operations WHERE status IN ('queued', 'running')").Scan(&n)
	return n, err
}

// CancelQueuedOperations marks a deployment's queued operations cancelled.
// Running operations are cancelled by the dispatcher.
func (t *Tx) CancelQueuedOperations(deploymentID string, reason *domain.Error) error {
	_, err := t.q.ExecContext(t.ctx, `UPDATE operations SET status = 'cancelled', error_code = ?, error_message = ?, updated_at = ?, finished_at = ?
		WHERE deployment_id = ? AND status = 'queued'`, reason.Code, reason.Message, ms(t.now), ms(t.now), deploymentID)
	return err
}

// ClaimOperation marks the oldest runnable queued operation as running and
// returns it. An operation is runnable when no other operation of the same
// deployment is running, so each deployment's operations run in order. It
// returns false when nothing is runnable.
func (s *Store) ClaimOperation(ctx context.Context) (domain.Operation, bool, error) {
	var o domain.Operation
	err := s.Tx(ctx, func(tx *Tx) error {
		var err error
		o, err = scanOperation(tx.q.QueryRowContext(ctx, "SELECT "+operationColumns+` FROM operations o
			WHERE o.status = 'queued' AND (o.deployment_id = '' OR NOT EXISTS (
				SELECT 1 FROM operations r WHERE r.status = 'running' AND r.deployment_id = o.deployment_id))
			ORDER BY o.created_at, o.id LIMIT 1`))
		if err != nil {
			return err
		}
		o.Status = domain.OpRunning
		o.Attempts++
		o.UpdatedAt = tx.now
		if o.StartedAt == nil {
			o.StartedAt = &tx.now
		}
		_, err = tx.q.ExecContext(ctx, "UPDATE operations SET status = 'running', attempts = ?, updated_at = ?, started_at = ? WHERE id = ?",
			o.Attempts, ms(tx.now), ms(*o.StartedAt), o.ID)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Operation{}, false, nil
	}
	return o, err == nil, err
}

// FinishOperation records the final status of a running operation. result
// is stored as JSON when non-nil.
func (s *Store) FinishOperation(ctx context.Context, id string, status domain.OperationStatus, result any, failure *domain.Error) error {
	var resultJSON any
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		resultJSON = string(b)
	}
	var code, message, hint string
	if failure != nil {
		code, message, hint = string(failure.Code), failure.Message, failure.Hint
	}
	return s.Tx(ctx, func(tx *Tx) error {
		o, err := tx.Operation(id)
		if err != nil {
			return err
		}
		if _, err := tx.q.ExecContext(ctx, `UPDATE operations SET status = ?, result = ?, error_code = ?, error_message = ?, error_hint = ?, updated_at = ?, finished_at = ?
			WHERE id = ? AND status = 'running'`, status, resultJSON, code, message, hint, ms(tx.now), ms(tx.now), id); err != nil {
			return err
		}
		return tx.Event(o.Actor, string(o.Kind)+"."+string(status), id)
	})
}

// SetCheckpoint records a running operation's progress as JSON, so that its
// handler can resume from it after an interruption.
func (s *Store) SetCheckpoint(ctx context.Context, id string, checkpoint any) error {
	b, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE operations SET checkpoint = ?, updated_at = ? WHERE id = ? AND status = 'running'", string(b), ms(s.now()), id)
	return err
}

// RequeueRunning returns operations left running by a previous process to
// the queue. Call it only while holding the controller lock, before
// dispatching.
func (s *Store) RequeueRunning(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE operations SET status = 'queued', updated_at = ? WHERE status = 'running'", ms(s.now()))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func listOperations(ctx context.Context, q querier, where string, args ...any) ([]domain.Operation, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+operationColumns+" FROM operations "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Operation
	for rows.Next() {
		o, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func scanOperation(row scanner) (domain.Operation, error) {
	var o domain.Operation
	var code, message, hint string
	var request, checkpoint, result sql.NullString
	var created, updated, deadline int64
	var started, finished sql.NullInt64
	err := row.Scan(&o.ID, &o.Kind, &o.Status, &o.ProjectID, &o.DeploymentID, &o.Actor, &o.Attempts, &code, &message, &hint, &request, &checkpoint, &result,
		&created, &updated, &started, &finished, &deadline)
	if err != nil {
		return o, err
	}
	if code != "" {
		o.Error = &domain.Error{Code: domain.Code(code), Message: message, Hint: hint}
	}
	if request.Valid {
		o.Request = json.RawMessage(request.String)
	}
	if checkpoint.Valid {
		o.Checkpoint = json.RawMessage(checkpoint.String)
	}
	if result.Valid {
		o.Result = json.RawMessage(result.String)
	}
	o.CreatedAt, o.UpdatedAt, o.Deadline = fromMS(created), fromMS(updated), fromMS(deadline)
	o.StartedAt, o.FinishedAt = fromNullMS(started), fromNullMS(finished)
	return o, nil
}

// BusyDeployments returns the IDs of deployments with queued or running
// operations.
func (s *Store) BusyDeployments(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT deployment_id FROM operations WHERE deployment_id != '' AND status IN ('queued', 'running')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	busy := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		busy[id] = true
	}
	return busy, rows.Err()
}

// Busy reports whether a deployment has queued or running operations.
func (s *Store) Busy(ctx context.Context, deploymentID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM operations WHERE deployment_id = ? AND status IN ('queued', 'running')", deploymentID).Scan(&n)
	return n > 0, err
}
