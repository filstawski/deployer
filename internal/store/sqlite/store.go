package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"deployer/internal/domain"
	_ "modernc.org/sqlite"
)

type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "state.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, Now: func() time.Time { return time.Now().UTC() }}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) migrate() error {
	if _, err := s.DB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 2 {
		return fmt.Errorf("database schema %d is newer than this server supports", version)
	}
	if version == 0 {
		_, err = tx.Exec(`
CREATE TABLE projects (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, repository TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, hash TEXT NOT NULL UNIQUE, scopes TEXT NOT NULL, projects TEXT NOT NULL, created_at TEXT NOT NULL, expires_at TEXT, revoked INTEGER NOT NULL DEFAULT 0);
CREATE TABLE events (sequence INTEGER PRIMARY KEY AUTOINCREMENT, actor TEXT NOT NULL, kind TEXT NOT NULL, target TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE requests (actor TEXT NOT NULL, key TEXT NOT NULL, hash TEXT NOT NULL, result_id TEXT NOT NULL, PRIMARY KEY(actor,key));
CREATE TABLE operations (id TEXT PRIMARY KEY, actor TEXT NOT NULL, project_id TEXT NOT NULL REFERENCES projects(id), kind TEXT NOT NULL, status TEXT NOT NULL, attempts INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, deadline TEXT NOT NULL, result TEXT, error TEXT);
CREATE INDEX operations_queue ON operations(status,created_at);
PRAGMA user_version=1;
`)
		if err != nil {
			return err
		}
	}
	if version < 2 {
		_, err = tx.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE deployments (id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id),hostname TEXT NOT NULL UNIQUE,data TEXT NOT NULL);
ALTER TABLE operations ADD COLUMN deployment_id TEXT NOT NULL DEFAULT '';
PRAGMA user_version=2;`)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO settings(key,value) VALUES('installation_id',?)", domain.ID("inst")); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) InstallationID() (string, error) {
	var id string
	err := s.DB.QueryRow("SELECT value FROM settings WHERE key='installation_id'").Scan(&id)
	return id, err
}
func stamp(t time.Time) string     { return t.UTC().Format(time.RFC3339Nano) }
func parseTime(s string) time.Time { t, _ := time.Parse(time.RFC3339Nano, s); return t }
func digest(s string) string       { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

type TokenInput struct {
	Name       string   `json:"name"`
	Scopes     []string `json:"scopes"`
	Projects   []string `json:"projects"`
	TTLSeconds int64    `json:"ttl_seconds"`
}

func (s *Store) IssueToken(ctx context.Context, in TokenInput, actor string, bootstrap bool) (domain.Token, string, error) {
	var t domain.Token
	if err := domain.ValidateName(in.Name); err != nil {
		return t, "", err
	}
	if err := domain.ValidateGrants(in.Scopes, in.Projects); err != nil {
		return t, "", err
	}
	if in.TTLSeconds < 1 || in.TTLSeconds > 365*24*3600 {
		return t, "", domain.Fail("INVALID_INPUT", "token lifetime must be between one second and 365 days")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return t, "", err
	}
	secret := "dpl_" + hex.EncodeToString(b)
	now := s.Now()
	expires := now.Add(time.Duration(in.TTLSeconds) * time.Second)
	t = domain.Token{ID: domain.ID("tok"), Name: in.Name, Scopes: in.Scopes, Projects: in.Projects, CreatedAt: now, ExpiresAt: &expires}
	scopes, _ := json.Marshal(in.Scopes)
	projects, _ := json.Marshal(in.Projects)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return t, "", err
	}
	defer tx.Rollback()
	if bootstrap {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM tokens").Scan(&n); err != nil {
			return t, "", err
		}
		if n != 0 {
			return t, "", domain.Fail("ALREADY_INITIALIZED", "operator token already initialized; use the local token create command for administrator recovery")
		}
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO tokens(id,name,hash,scopes,projects,created_at,expires_at) VALUES(?,?,?,?,?,?,?)", t.ID, t.Name, digest(secret), string(scopes), string(projects), stamp(now), stamp(expires))
	if err != nil {
		return t, "", err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO events(actor,kind,target,created_at) VALUES(?,?,?,?)", actor, "token.created", t.ID, stamp(now)); err != nil {
		return t, "", err
	}
	if err = tx.Commit(); err != nil {
		return t, "", err
	}
	return t, secret, nil
}

type scanner interface{ Scan(...any) error }

func scanToken(row scanner) (domain.Token, error) {
	var t domain.Token
	var scopes, projects, created string
	var expires sql.NullString
	err := row.Scan(&t.ID, &t.Name, &scopes, &projects, &created, &expires, &t.Revoked)
	if err != nil {
		return t, err
	}
	if err = json.Unmarshal([]byte(scopes), &t.Scopes); err != nil {
		return t, err
	}
	if err = json.Unmarshal([]byte(projects), &t.Projects); err != nil {
		return t, err
	}
	t.CreatedAt = parseTime(created)
	if expires.Valid {
		v := parseTime(expires.String)
		t.ExpiresAt = &v
	}
	return t, nil
}

const tokenColumns = "id,name,scopes,projects,created_at,expires_at,revoked"

func (s *Store) Authenticate(ctx context.Context, secret string) (domain.Token, error) {
	t, err := scanToken(s.DB.QueryRowContext(ctx, "SELECT "+tokenColumns+" FROM tokens WHERE hash=?", digest(secret)))
	if errors.Is(err, sql.ErrNoRows) || err == nil && (t.Revoked || t.ExpiresAt != nil && !s.Now().Before(*t.ExpiresAt)) {
		return domain.Token{}, domain.Fail("UNAUTHENTICATED", "a valid, unexpired Deployer token is required")
	}
	return t, err
}
func (s *Store) Tokens(ctx context.Context) ([]domain.Token, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+tokenColumns+" FROM tokens ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Token{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) Revoke(ctx context.Context, id, actor string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.ExecContext(ctx, "UPDATE tokens SET revoked=1 WHERE id=?", id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return domain.Fail("NOT_FOUND", "token not found")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO events(actor,kind,target,created_at) VALUES(?,?,?,?)", actor, "token.revoked", id, stamp(s.Now())); err != nil {
		return err
	}
	return tx.Commit()
}

func scanProject(row scanner) (domain.Project, error) {
	var p domain.Project
	var created string
	err := row.Scan(&p.ID, &p.Name, &p.Repository, &created)
	p.CreatedAt = parseTime(created)
	return p, err
}
func (s *Store) Project(ctx context.Context, idOrName string) (domain.Project, error) {
	p, err := scanProject(s.DB.QueryRowContext(ctx, "SELECT id,name,repository,created_at FROM projects WHERE id=? OR name=?", idOrName, idOrName))
	if errors.Is(err, sql.ErrNoRows) {
		return p, domain.Fail("NOT_FOUND", "project not found")
	}
	return p, err
}
func (s *Store) Projects(ctx context.Context) ([]domain.Project, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,name,repository,created_at FROM projects ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) CreateProject(ctx context.Context, name, repository, actor, key string) (domain.Project, error) {
	var p domain.Project
	if err := domain.ValidateName(name); err != nil {
		return p, err
	}
	if err := domain.ValidateRepository(repository); err != nil {
		return p, err
	}
	if len(key) > 200 {
		return p, domain.Fail("INVALID_INPUT", "idempotency key exceeds 200 bytes")
	}
	input, _ := json.Marshal([]string{"project.create", name, repository})
	hash := digest(string(input))
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	if key != "" {
		var oldHash, id string
		err := tx.QueryRowContext(ctx, "SELECT hash,result_id FROM requests WHERE actor=? AND key=?", actor, key).Scan(&oldHash, &id)
		if err == nil {
			if hash != oldHash {
				return p, domain.Fail("IDEMPOTENCY_CONFLICT", "this idempotency key was used with different input")
			}
			return scanProject(tx.QueryRowContext(ctx, "SELECT id,name,repository,created_at FROM projects WHERE id=?", id))
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return p, err
		}
	}
	var exists int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects WHERE name=?", name).Scan(&exists); err != nil {
		return p, err
	}
	if exists != 0 {
		return p, domain.Fail("ALREADY_EXISTS", "project name is already registered")
	}
	p = domain.Project{ID: domain.ID("prj"), Name: name, Repository: repository, CreatedAt: s.Now()}
	if _, err = tx.ExecContext(ctx, "INSERT INTO projects(id,name,repository,created_at) VALUES(?,?,?,?)", p.ID, p.Name, p.Repository, stamp(p.CreatedAt)); err != nil {
		return p, err
	}
	if key != "" {
		if _, err = tx.ExecContext(ctx, "INSERT INTO requests(actor,key,hash,result_id) VALUES(?,?,?,?)", actor, key, hash, p.ID); err != nil {
			return p, err
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO events(actor,kind,target,created_at) VALUES(?,?,?,?)", actor, "project.created", p.ID, stamp(p.CreatedAt)); err != nil {
		return p, err
	}
	return p, tx.Commit()
}
