package store

import (
	"context"

	"github.com/filstawski/deployer/internal/domain"
)

const projectColumns = "id, name, repository, created_at"

// CreateProject registers a repository. With an idempotency key, repeating
// the same request returns the original project.
func (s *Store) CreateProject(ctx context.Context, actor string, req domain.CreateProjectRequest, idempotencyKey string) (domain.Project, error) {
	if err := domain.ValidateProjectName(req.Name); err != nil {
		return domain.Project{}, err
	}
	repo, err := domain.NormalizeRepository(req.Repository)
	if err != nil {
		return domain.Project{}, err
	}
	hash := RequestHash("project.create", req.Name, repo)
	var p domain.Project
	err = s.Tx(ctx, func(tx *Tx) error {
		if id, found, err := tx.Idempotent(actor, idempotencyKey, hash); found || err != nil {
			if err == nil {
				p, err = tx.Project(id)
			}
			return err
		}
		if _, err := tx.Project(req.Name); err == nil {
			return domain.Errorf(domain.CodeAlreadyExists, "a project named %q already exists", req.Name)
		}
		p = domain.Project{ID: domain.NewID(domain.PrefixProject), Name: req.Name, Repository: repo, CreatedAt: tx.now}
		if _, err := tx.q.ExecContext(ctx, "INSERT INTO projects ("+projectColumns+") VALUES (?, ?, ?, ?)", p.ID, p.Name, p.Repository, ms(p.CreatedAt)); err != nil {
			return err
		}
		if err := tx.RecordIdempotency(actor, idempotencyKey, hash, p.ID); err != nil {
			return err
		}
		return tx.Event(actor, "project.created", p.ID)
	})
	return p, err
}

// GetProject returns a project by ID or name.
func (s *Store) GetProject(ctx context.Context, ref string) (domain.Project, error) {
	return getProject(ctx, s.db, ref)
}

// Project returns a project by ID or name within the transaction.
func (t *Tx) Project(ref string) (domain.Project, error) { return getProject(t.ctx, t.q, ref) }

func getProject(ctx context.Context, q querier, ref string) (domain.Project, error) {
	var p domain.Project
	var created int64
	err := q.QueryRowContext(ctx, "SELECT "+projectColumns+" FROM projects WHERE id = ? OR name = ?", ref, ref).Scan(&p.ID, &p.Name, &p.Repository, &created)
	p.CreatedAt = fromMS(created)
	return p, notFound(err, "project %q not found", ref)
}

// ListProjects returns all projects ordered by name.
func (s *Store) ListProjects(ctx context.Context) ([]domain.Project, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+projectColumns+" FROM projects ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Project{}
	for rows.Next() {
		var p domain.Project
		var created int64
		if err := rows.Scan(&p.ID, &p.Name, &p.Repository, &created); err != nil {
			return nil, err
		}
		p.CreatedAt = fromMS(created)
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteProject removes a project and its history. It fails while the
// project has live deployments or unfinished operations.
func (s *Store) DeleteProject(ctx context.Context, id, actor string) error {
	return s.Tx(ctx, func(tx *Tx) error {
		if _, err := tx.Project(id); err != nil {
			return err
		}
		var live, pending int
		if err := tx.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM deployments WHERE project_id = ? AND state != 'destroyed'", id).Scan(&live); err != nil {
			return err
		}
		if err := tx.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM operations WHERE project_id = ? AND status IN ('queued', 'running')", id).Scan(&pending); err != nil {
			return err
		}
		if live > 0 {
			return domain.Errorf(domain.CodeInvalidState, "project has %d live deployment(s)", live).WithHint("destroy them first: deployer list --project %s", id)
		}
		if pending > 0 {
			return domain.Errorf(domain.CodeBusy, "project has unfinished operations; try again when they finish")
		}
		for _, q := range []string{
			"DELETE FROM idempotency_keys WHERE resource_id = ?1 OR resource_id IN (SELECT id FROM operations WHERE project_id = ?1)",
			"DELETE FROM operations WHERE project_id = ?1",
			"DELETE FROM deployments WHERE project_id = ?1",
			"DELETE FROM projects WHERE id = ?1",
		} {
			if _, err := tx.q.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		return tx.Event(actor, "project.deleted", id)
	})
}
