package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"deployer/internal/domain"
)

type Operation struct {
	ID           string          `json:"id"`
	Actor        string          `json:"actor"`
	ProjectID    string          `json:"project_id"`
	Kind         string          `json:"kind"`
	Status       string          `json:"status"`
	Attempts     int             `json:"attempts"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	Deadline     time.Time       `json:"deadline"`
	Result       json.RawMessage `json:"result,omitempty"`
	Error        *domain.Error   `json:"error,omitempty"`
	DeploymentID string          `json:"deployment_id,omitempty"`
}

const opColumns = "id,actor,project_id,kind,status,attempts,created_at,updated_at,deadline,result,error,deployment_id"

func scanOp(row scanner) (Operation, error) {
	var o Operation
	var created, updated, deadline string
	var result, failure sql.NullString
	err := row.Scan(&o.ID, &o.Actor, &o.ProjectID, &o.Kind, &o.Status, &o.Attempts, &created, &updated, &deadline, &result, &failure, &o.DeploymentID)
	if err != nil {
		return o, err
	}
	o.CreatedAt = parseTime(created)
	o.UpdatedAt = parseTime(updated)
	o.Deadline = parseTime(deadline)
	if result.Valid {
		o.Result = json.RawMessage(result.String)
	}
	if failure.Valid {
		if err = json.Unmarshal([]byte(failure.String), &o.Error); err != nil {
			return o, err
		}
	}
	return o, nil
}
func (s *Store) Operation(ctx context.Context, id string) (Operation, error) {
	o, err := scanOp(s.DB.QueryRowContext(ctx, "SELECT "+opColumns+" FROM operations WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return o, domain.Fail("NOT_FOUND", "operation not found")
	}
	return o, err
}

func (s *Store) EnqueueVerify(ctx context.Context, project, actor, key string) (Operation, error) {
	var o Operation
	if key == "" || len(key) > 200 {
		return o, domain.Fail("INVALID_INPUT", "Idempotency-Key is required and must be at most 200 bytes")
	}
	hash := digest("project.verify:" + project)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return o, err
	}
	defer tx.Rollback()
	var oldHash, id string
	err = tx.QueryRowContext(ctx, "SELECT hash,result_id FROM requests WHERE actor=? AND key=?", actor, key).Scan(&oldHash, &id)
	if err == nil {
		if oldHash != hash {
			return o, domain.Fail("IDEMPOTENCY_CONFLICT", "this idempotency key was used with different input")
		}
		return scanOp(tx.QueryRowContext(ctx, "SELECT "+opColumns+" FROM operations WHERE id=?", id))
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return o, err
	}
	var pending int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM operations WHERE status IN ('queued','running')").Scan(&pending); err != nil {
		return o, err
	}
	if pending >= 100 {
		return o, domain.Fail("QUOTA_EXCEEDED", "operation queue is full")
	}
	now := s.Now()
	o = Operation{ID: domain.ID("op"), Actor: actor, ProjectID: project, Kind: "project.verify", Status: "queued", CreatedAt: now, UpdatedAt: now, Deadline: now.Add(2 * time.Minute)}
	_, err = tx.ExecContext(ctx, "INSERT INTO operations(id,actor,project_id,kind,status,created_at,updated_at,deadline) VALUES(?,?,?,?,?,?,?,?)", o.ID, o.Actor, o.ProjectID, o.Kind, o.Status, stamp(now), stamp(now), stamp(o.Deadline))
	if err != nil {
		return o, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO requests(actor,key,hash,result_id) VALUES(?,?,?,?)", actor, key, hash, o.ID); err != nil {
		return o, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO events(actor,kind,target,created_at) VALUES(?,?,?,?)", actor, "operation.queued", o.ID, stamp(now)); err != nil {
		return o, err
	}
	return o, tx.Commit()
}

// Recover is called only by the process holding the controller lock. The sole
// handlers checkpoint immutable commits/images and adopt labelled resources on retry.
func (s *Store) Recover(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, "UPDATE operations SET status='queued',updated_at=? WHERE status='running'", stamp(s.Now()))
	return err
}
func (s *Store) Claim(ctx context.Context) (Operation, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Operation{}, err
	}
	defer tx.Rollback()
	o, err := scanOp(tx.QueryRowContext(ctx, "SELECT "+opColumns+" FROM operations WHERE status='queued' ORDER BY created_at,id LIMIT 1"))
	if err != nil {
		return o, err
	}
	o.Status = "running"
	o.Attempts++
	o.UpdatedAt = s.Now()
	if _, err = tx.ExecContext(ctx, "UPDATE operations SET status='running',attempts=?,updated_at=? WHERE id=?", o.Attempts, stamp(o.UpdatedAt), o.ID); err != nil {
		return o, err
	}
	return o, tx.Commit()
}
func (s *Store) Finish(ctx context.Context, o Operation, result any, failure *domain.Error) error {
	status := "succeeded"
	var r, e any
	if result != nil {
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		r = string(b)
	}
	if failure != nil {
		status = "failed"
		b, _ := json.Marshal(failure)
		e = string(b)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE operations SET status=?,result=?,error=?,updated_at=? WHERE id=? AND status='running'", status, r, e, stamp(s.Now()), o.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO events(actor,kind,target,created_at) VALUES(?,?,?,?)", o.Actor, "operation."+status, o.ID, stamp(s.Now())); err != nil {
		return err
	}
	return tx.Commit()
}
