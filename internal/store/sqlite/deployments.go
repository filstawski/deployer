package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"deployer/internal/domain"
)

func scanDeployment(row scanner) (domain.Deployment, error) {
	var d domain.Deployment
	var data string
	err := row.Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return d, domain.Fail("NOT_FOUND", "deployment not found")
	}
	if err != nil {
		return d, err
	}
	err = json.Unmarshal([]byte(data), &d)
	return d, err
}
func (s *Store) Deployment(ctx context.Context, id string) (domain.Deployment, error) {
	return scanDeployment(s.DB.QueryRowContext(ctx, "SELECT data FROM deployments WHERE id=?", id))
}
func (s *Store) Deployments(ctx context.Context) ([]domain.Deployment, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT data FROM deployments ORDER BY id")
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
func (s *Store) SaveDeployment(ctx context.Context, d domain.Deployment) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE deployments SET data=? WHERE id=?", string(b), d.ID)
	return err
}

func (s *Store) QueueDeploy(ctx context.Context, in domain.DeployInput, project domain.Project, actor, key, base string, port, maxDeployments int, schemes ...string) (Operation, error) {
	var o Operation
	if err := domain.ValidateRef(in.Ref); err != nil {
		return o, err
	}
	if in.TTLSeconds < 1 || in.TTLSeconds > 7*24*3600 {
		return o, domain.Fail("INVALID_INPUT", "TTL must be between 1s and 7d")
	}
	if key == "" || len(key) > 200 {
		return o, domain.Fail("INVALID_INPUT", "Idempotency-Key is required (maximum 200 bytes)")
	}
	in.Project = project.ID
	body, _ := json.Marshal(in)
	hash := digest("deployment.create:" + string(body))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return o, err
	}
	defer tx.Rollback()
	if old, found, err := existingOperation(ctx, tx, actor, key, hash); found || err != nil {
		return old, err
	}
	var n int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM deployments WHERE json_extract(data,'$.state') != 'destroyed'").Scan(&n); err != nil {
		return o, err
	}
	if n >= maxDeployments {
		return o, domain.Fail("QUOTA_EXCEEDED", "deployment capacity reached; destroy an unused preview first")
	}
	id := domain.ID("dep")
	sub := in.Subdomain
	if sub == "" {
		slug := strings.ToLower(strings.ReplaceAll(in.Ref, "/", "-"))
		slug = strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
				return r
			}
			return '-'
		}, slug)
		sub = project.Name[:min(20, len(project.Name))] + "-" + slug[:min(20, len(slug))] + "-" + id[len(id)-8:]
	}
	if err = domain.ValidateName(sub); err != nil {
		return o, err
	}
	switch sub {
	case "api", "admin", "www", "deployer":
		return o, domain.Fail("INVALID_INPUT", "reserved subdomain")
	}
	host := sub + "." + base
	if len(host) > 253 {
		return o, domain.Fail("INVALID_INPUT", "hostname is too long")
	}
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM deployments WHERE hostname=?", host).Scan(&n); err != nil {
		return o, err
	}
	if n > 0 {
		return o, domain.Fail("SUBDOMAIN_TAKEN", "hostname is already reserved")
	}
	now := s.Now()
	o = Operation{ID: domain.ID("op"), Actor: actor, ProjectID: project.ID, DeploymentID: id, Kind: "deployment.create", Status: "queued", CreatedAt: now, UpdatedAt: now, Deadline: now.Add(20 * time.Minute)}
	scheme := "http"
	if len(schemes) > 0 {
		scheme = schemes[0]
	}
	authority := host
	if !(scheme == "https" && port == 443 || scheme == "http" && port == 80) {
		authority = fmt.Sprintf("%s:%d", host, port)
	}
	d := domain.Deployment{ID: id, ProjectID: project.ID, Ref: in.Ref, Hostname: host, URL: scheme + "://" + authority, State: "queued", TTLSeconds: in.TTLSeconds, CreatedAt: now, OperationID: o.ID, Deadline: o.Deadline}
	b, _ := json.Marshal(d)
	if _, err = tx.ExecContext(ctx, "INSERT INTO deployments(id,project_id,hostname,data) VALUES(?,?,?,?)", id, project.ID, host, string(b)); err != nil {
		return o, err
	}
	if err = insertOperation(ctx, tx, o, key, hash); err != nil {
		return o, err
	}
	return o, tx.Commit()
}
func existingOperation(ctx context.Context, tx *sql.Tx, actor, key, hash string) (Operation, bool, error) {
	var oldHash, id string
	err := tx.QueryRowContext(ctx, "SELECT hash,result_id FROM requests WHERE actor=? AND key=?", actor, key).Scan(&oldHash, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, false, nil
	}
	if err != nil {
		return Operation{}, false, err
	}
	if oldHash != hash {
		return Operation{}, true, domain.Fail("IDEMPOTENCY_CONFLICT", "idempotency key was used for different input")
	}
	o, err := scanOp(tx.QueryRowContext(ctx, "SELECT "+opColumns+" FROM operations WHERE id=?", id))
	return o, true, err
}
func insertOperation(ctx context.Context, tx *sql.Tx, o Operation, key, hash string) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO operations(id,actor,project_id,kind,status,created_at,updated_at,deadline,deployment_id) VALUES(?,?,?,?,?,?,?,?,?)", o.ID, o.Actor, o.ProjectID, o.Kind, o.Status, stamp(o.CreatedAt), stamp(o.UpdatedAt), stamp(o.Deadline), o.DeploymentID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO requests(actor,key,hash,result_id) VALUES(?,?,?,?)", o.Actor, key, hash, o.ID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO events(actor,kind,target,created_at) VALUES(?,?,?,?)", o.Actor, "operation.queued", o.ID, stamp(o.CreatedAt))
	return err
}
func (s *Store) QueueAction(ctx context.Context, id, action, actor, key string, seconds int64) (Operation, error) {
	if action != "extend" && seconds != 0 {
		return Operation{}, domain.Fail("INVALID_INPUT", "seconds is supported only for extend")
	}
	if key == "" || len(key) > 200 {
		return Operation{}, domain.Fail("INVALID_INPUT", "Idempotency-Key is required (maximum 200 bytes)")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Operation{}, err
	}
	defer tx.Rollback()
	hash := digest(fmt.Sprintf("deployment.%s:%s:%d", action, id, seconds))
	if old, found, err := existingOperation(ctx, tx, actor, key, hash); found || err != nil {
		return old, err
	}
	d, err := scanDeployment(tx.QueryRowContext(ctx, "SELECT data FROM deployments WHERE id=?", id))
	if err != nil {
		return Operation{}, err
	}
	if d.State == "destroyed" {
		return Operation{}, domain.Fail("INVALID_STATE", "deployment is destroyed")
	}
	var pending int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM operations WHERE deployment_id=? AND status IN ('queued','running')", id).Scan(&pending); err != nil {
		return Operation{}, err
	}
	if pending != 0 {
		return Operation{}, domain.Fail("OPERATION_IN_PROGRESS", "wait for the current deployment operation to finish")
	}
	switch action {
	case "stop", "destroy":
	case "wake":
		if d.Image == "" || d.ExpiresAt == nil || !s.Now().Before(*d.ExpiresAt) {
			return Operation{}, domain.Fail("INVALID_STATE", "wake requires a built image and an unexpired lease")
		}
	case "extend":
		if seconds < 1 || seconds > 7*24*3600 || d.ExpiresAt == nil || !s.Now().Before(*d.ExpiresAt) || d.ExpiresAt.Add(time.Duration(seconds)*time.Second).After(s.Now().Add(7*24*time.Hour)) {
			return Operation{}, domain.Fail("INVALID_INPUT", "extend requires an active lease and a new deadline within 7 days")
		}
	default:
		return Operation{}, domain.Fail("UNSUPPORTED_FEATURE", "unsupported deployment action")
	}
	now := s.Now()
	o := Operation{ID: domain.ID("op"), Actor: actor, ProjectID: d.ProjectID, DeploymentID: id, Kind: "deployment." + action, Status: "queued", CreatedAt: now, UpdatedAt: now, Deadline: now.Add(5 * time.Minute)}
	if action == "extend" {
		next := d.ExpiresAt.Add(time.Duration(seconds) * time.Second)
		d.ExpiresAt = &next
		purge := next.Add(7 * 24 * time.Hour)
		d.PurgeAt = &purge
	}
	d.OperationID = o.ID
	b, _ := json.Marshal(d)
	if _, err = tx.ExecContext(ctx, "UPDATE deployments SET data=? WHERE id=?", string(b), id); err != nil {
		return o, err
	}
	if err = insertOperation(ctx, tx, o, key, hash); err != nil {
		return o, err
	}
	return o, tx.Commit()
}

// ActionRetry resolves an already accepted action even while its worker holds
// the deployment lock. A retry must not become an OPERATION_IN_PROGRESS error.
func (s *Store) ActionRetry(ctx context.Context, id, action, actor, key string, seconds int64) (Operation, bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Operation{}, false, err
	}
	defer tx.Rollback()
	return existingOperation(ctx, tx, actor, key, digest(fmt.Sprintf("deployment.%s:%s:%d", action, id, seconds)))
}
