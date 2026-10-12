package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/filstawski/deployer/internal/domain"
)

// NewToken describes a token to create.
type NewToken struct {
	Name     string
	Scopes   []domain.Scope
	Projects []string
	// TTL is the token lifetime; 0 means it never expires.
	TTL       time.Duration
	CreatedBy string
}

const tokenColumns = "id, name, scopes, projects, created_by, created_at, expires_at, revoked_at"

// CreateToken stores a new token and returns it with its secret. The secret
// is not stored and cannot be retrieved again.
func (s *Store) CreateToken(ctx context.Context, in NewToken) (domain.Token, string, error) {
	if err := domain.ValidateTokenName(in.Name); err != nil {
		return domain.Token{}, "", err
	}
	if err := domain.ValidateGrants(in.Scopes, in.Projects); err != nil {
		return domain.Token{}, "", err
	}
	if in.TTL < 0 {
		return domain.Token{}, "", domain.Errorf(domain.CodeInvalidInput, "token lifetime must not be negative")
	}
	secret := domain.NewTokenSecret()
	var tok domain.Token
	err := s.Tx(ctx, func(tx *Tx) error {
		for _, id := range in.Projects {
			if id == domain.AllProjects {
				continue
			}
			if _, err := tx.Project(id); err != nil {
				return domain.Errorf(domain.CodeInvalidInput, "project grant %q does not match a project", id)
			}
		}
		tok = domain.Token{ID: domain.NewID(domain.PrefixToken), Name: in.Name, Scopes: in.Scopes, Projects: in.Projects, CreatedBy: in.CreatedBy, CreatedAt: tx.now}
		if in.TTL > 0 {
			expires := tx.now.Add(in.TTL)
			tok.ExpiresAt = &expires
		}
		scopes, _ := json.Marshal(tok.Scopes)
		projects, _ := json.Marshal(tok.Projects)
		if _, err := tx.q.ExecContext(ctx, "INSERT INTO tokens (id, name, secret_hash, scopes, projects, created_by, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
			tok.ID, tok.Name, hashSecret(secret), string(scopes), string(projects), tok.CreatedBy, ms(tok.CreatedAt), msOrNil(tok.ExpiresAt)); err != nil {
			return err
		}
		return tx.Event(in.CreatedBy, "token.created", tok.ID)
	})
	if err != nil {
		return domain.Token{}, "", err
	}
	return tok, secret, nil
}

// Authenticate returns the active token whose secret matches.
func (s *Store) Authenticate(ctx context.Context, secret string) (domain.Token, error) {
	unauthenticated := domain.Errorf(domain.CodeUnauthenticated, "the API token is invalid, expired, or revoked")
	if !domain.ValidTokenSecret(secret) {
		return domain.Token{}, unauthenticated
	}
	tok, err := scanToken(s.db.QueryRowContext(ctx, "SELECT "+tokenColumns+" FROM tokens WHERE secret_hash = ?", hashSecret(secret)))
	if errors.Is(err, sql.ErrNoRows) || err == nil && !tok.Active(s.now()) {
		return domain.Token{}, unauthenticated
	}
	return tok, err
}

// GetToken returns a token by ID.
func (s *Store) GetToken(ctx context.Context, id string) (domain.Token, error) {
	tok, err := scanToken(s.db.QueryRowContext(ctx, "SELECT "+tokenColumns+" FROM tokens WHERE id = ?", id))
	return tok, notFound(err, "token %q not found", id)
}

// ListTokens returns all tokens, oldest first.
func (s *Store) ListTokens(ctx context.Context) ([]domain.Token, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+tokenColumns+" FROM tokens ORDER BY created_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Token{}
	for rows.Next() {
		tok, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tok)
	}
	return out, rows.Err()
}

// CountActiveTokens returns the number of tokens that can still authenticate.
func (s *Store) CountActiveTokens(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tokens WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", ms(s.now())).Scan(&n)
	return n, err
}

// RevokeToken revokes a token. Revoking an already revoked token succeeds.
func (s *Store) RevokeToken(ctx context.Context, id, actor string) error {
	return s.Tx(ctx, func(tx *Tx) error {
		res, err := tx.q.ExecContext(ctx, "UPDATE tokens SET revoked_at = COALESCE(revoked_at, ?) WHERE id = ?", ms(tx.now), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return domain.Errorf(domain.CodeNotFound, "token %q not found", id)
		}
		return tx.Event(actor, "token.revoked", id)
	})
}

func scanToken(row scanner) (domain.Token, error) {
	var tok domain.Token
	var scopes, projects string
	var created int64
	var expires, revoked sql.NullInt64
	if err := row.Scan(&tok.ID, &tok.Name, &scopes, &projects, &tok.CreatedBy, &created, &expires, &revoked); err != nil {
		return tok, err
	}
	if err := json.Unmarshal([]byte(scopes), &tok.Scopes); err != nil {
		return tok, err
	}
	if err := json.Unmarshal([]byte(projects), &tok.Projects); err != nil {
		return tok, err
	}
	tok.CreatedAt = fromMS(created)
	tok.ExpiresAt = fromNullMS(expires)
	tok.RevokedAt = fromNullMS(revoked)
	return tok, nil
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
