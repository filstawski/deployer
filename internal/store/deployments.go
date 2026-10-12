package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/filstawski/deployer/internal/domain"
)

const deploymentColumns = `d.id, d.project_id, p.name, d.name, d.ref, d.commit_sha, d.state, d.error_code, d.error_message, d.error_hint,
	d.image, d.port, d.health_path, d.health_timeout_ms, d.env, d.ttl_seconds, d.created_by, d.created_at,
	d.updated_at, d.ready_at, d.expires_at, d.purge_at, d.operation_id`

const deploymentFrom = " FROM deployments d JOIN projects p ON p.id = d.project_id "

// DeploymentFilter selects deployments to list.
type DeploymentFilter struct {
	ProjectID        string
	IncludeDestroyed bool
}

// GetDeployment returns a deployment by ID or name. A name matches the live
// deployment with that name, or else the most recently destroyed one.
func (s *Store) GetDeployment(ctx context.Context, ref string) (domain.Deployment, error) {
	return getDeployment(ctx, s.db, ref)
}

// Deployment returns a deployment by ID or name within the transaction.
func (t *Tx) Deployment(ref string) (domain.Deployment, error) { return getDeployment(t.ctx, t.q, ref) }

func getDeployment(ctx context.Context, q querier, ref string) (domain.Deployment, error) {
	d, err := scanDeployment(q.QueryRowContext(ctx, "SELECT "+deploymentColumns+deploymentFrom+
		"WHERE d.id = ?1 OR d.name = ?1 ORDER BY d.id = ?1 DESC, d.state != 'destroyed' DESC, d.created_at DESC LIMIT 1", ref))
	return d, notFound(err, "deployment %q not found", ref)
}

// ListDeployments returns deployments, newest first.
func (s *Store) ListDeployments(ctx context.Context, f DeploymentFilter) ([]domain.Deployment, error) {
	var where []string
	var args []any
	if !f.IncludeDestroyed {
		where = append(where, "d.state != 'destroyed'")
	}
	if f.ProjectID != "" {
		where = append(where, "d.project_id = ?")
		args = append(args, f.ProjectID)
	}
	query := "SELECT " + deploymentColumns + deploymentFrom
	if len(where) > 0 {
		query += "WHERE " + strings.Join(where, " AND ")
	}
	rows, err := s.db.QueryContext(ctx, query+" ORDER BY d.created_at DESC, d.id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Deployment{}
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CountLiveDeployments returns the number of deployments holding resources.
func (t *Tx) CountLiveDeployments() (int, error) {
	var n int
	err := t.q.QueryRowContext(t.ctx, "SELECT COUNT(*) FROM deployments WHERE state != 'destroyed'").Scan(&n)
	return n, err
}

// InsertDeployment stores a new deployment. It fails with NAME_TAKEN if a
// live deployment already uses the name.
func (t *Tx) InsertDeployment(d domain.Deployment) error {
	env, _ := json.Marshal(d.Env)
	_, err := t.q.ExecContext(t.ctx, `INSERT INTO deployments (id, project_id, name, ref, state, ttl_seconds, env, created_by, created_at, updated_at, operation_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.ProjectID, d.Name, d.Ref, d.State, d.TTLSeconds, string(env), d.CreatedBy, ms(d.CreatedAt), ms(d.UpdatedAt), d.OperationID)
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: deployments.name") {
		return domain.Errorf(domain.CodeNameTaken, "a live deployment is already named %q", d.Name).
			WithHint("choose another --name, or destroy the existing deployment")
	}
	return err
}

// SaveDeployment writes every mutable field of d.
func (t *Tx) SaveDeployment(d domain.Deployment) error { return saveDeployment(t.ctx, t.q, d, t.now) }

// UpdateDeployment applies fn to the current deployment and saves the result
// atomically. Returning an error from fn aborts the update.
func (s *Store) UpdateDeployment(ctx context.Context, id string, fn func(*domain.Deployment) error) (domain.Deployment, error) {
	var d domain.Deployment
	err := s.Tx(ctx, func(tx *Tx) error {
		var err error
		if d, err = tx.Deployment(id); err != nil {
			return err
		}
		if err := fn(&d); err != nil {
			return err
		}
		return tx.SaveDeployment(d)
	})
	return d, err
}

func saveDeployment(ctx context.Context, q querier, d domain.Deployment, now time.Time) error {
	env, _ := json.Marshal(d.Env)
	var code, message, hint string
	if d.Error != nil {
		code, message, hint = string(d.Error.Code), d.Error.Message, d.Error.Hint
	}
	_, err := q.ExecContext(ctx, `UPDATE deployments SET commit_sha = ?, state = ?, error_code = ?, error_message = ?, error_hint = ?, image = ?, port = ?,
		health_path = ?, health_timeout_ms = ?, env = ?, ttl_seconds = ?, updated_at = ?, ready_at = ?, expires_at = ?, purge_at = ?, operation_id = ?
		WHERE id = ?`,
		d.Commit, d.State, code, message, hint, d.Image, d.Port, d.HealthPath, d.HealthTimeout.Milliseconds(), string(env), d.TTLSeconds,
		ms(now), msOrNil(d.ReadyAt), msOrNil(d.ExpiresAt), msOrNil(d.PurgeAt), d.OperationID, d.ID)
	return err
}

func scanDeployment(row scanner) (domain.Deployment, error) {
	var d domain.Deployment
	var code, message, hint, env string
	var timeoutMS, created, updated int64
	var ready, expires, purge sql.NullInt64
	err := row.Scan(&d.ID, &d.ProjectID, &d.Project, &d.Name, &d.Ref, &d.Commit, &d.State, &code, &message, &hint,
		&d.Image, &d.Port, &d.HealthPath, &timeoutMS, &env, &d.TTLSeconds, &d.CreatedBy, &created,
		&updated, &ready, &expires, &purge, &d.OperationID)
	if err != nil {
		return d, err
	}
	if code != "" {
		d.Error = &domain.Error{Code: domain.Code(code), Message: message, Hint: hint}
	}
	if err := json.Unmarshal([]byte(env), &d.Env); err != nil {
		return d, err
	}
	d.HealthTimeout = time.Duration(timeoutMS) * time.Millisecond
	d.CreatedAt, d.UpdatedAt = fromMS(created), fromMS(updated)
	d.ReadyAt, d.ExpiresAt, d.PurgeAt = fromNullMS(ready), fromNullMS(expires), fromNullMS(purge)
	return d, nil
}
