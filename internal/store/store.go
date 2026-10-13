// Package store persists Deployer state in SQLite.
//
// Store methods are single-statement reads and writes. Multi-step changes
// that must be atomic, such as admitting a deployment, run inside Store.Tx,
// whose callback receives a *Tx exposing the same operations.
package store

import (
	"context"
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

	"github.com/filstawski/deployer/internal/domain"
	_ "modernc.org/sqlite"
)

// Store is the SQLite-backed state store.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open opens or creates the database at path and applies migrations.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	f.Close()
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	// SQLite takes a URI. A Windows path, used by tests of the CLI, becomes
	// file:///C:/...
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" {
		uriPath = "/" + uriPath
	}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: uriPath, RawQuery: q.Encode()}).String())
	if err != nil {
		return nil, err
	}
	// One connection serializes access, which keeps SQLite free of busy
	// errors. Transactions are short; never query s.db inside Tx callbacks.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: func() time.Time { return time.Now().UTC() }}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping checks that the database is usable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Now returns the store's current time, which tests can control.
func (s *Store) Now() time.Time { return s.now() }

// SetClock replaces the clock. It is intended for tests.
func (s *Store) SetClock(now func() time.Time) { s.now = now }

// InstallationID returns the random ID that labels this installation's
// Docker resources.
func (s *Store) InstallationID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'installation_id'").Scan(&id)
	return id, err
}

// Tx runs fn in a write transaction. If fn returns an error, nothing is
// committed. The *Tx must not be used after fn returns.
func (s *Store) Tx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(&Tx{ctx: ctx, q: tx, now: s.now()}); err != nil {
		return err
	}
	return tx.Commit()
}

// Tx is a write transaction. All methods share one consistent view and one
// timestamp, Now.
type Tx struct {
	ctx context.Context
	q   querier
	now time.Time
}

// Now returns the transaction's timestamp.
func (t *Tx) Now() time.Time { return t.now }

// querier is implemented by *sql.DB and *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Event records an audit event.
func (t *Tx) Event(actor, kind, subject string) error {
	_, err := t.q.ExecContext(t.ctx, "INSERT INTO events (at, actor, kind, subject) VALUES (?, ?, ?, ?)", ms(t.now), actor, kind, subject)
	return err
}

// Idempotent looks up an idempotency key. It returns the stored resource ID
// if the key was used before with the same request, and IDEMPOTENCY_CONFLICT
// if it was used with a different one.
func (t *Tx) Idempotent(actor, key, requestHash string) (resourceID string, found bool, err error) {
	if key == "" {
		return "", false, nil
	}
	var hash string
	err = t.q.QueryRowContext(t.ctx, "SELECT request_hash, resource_id FROM idempotency_keys WHERE actor = ? AND key = ?", actor, key).Scan(&hash, &resourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if hash != requestHash {
		return "", false, domain.Errorf(domain.CodeIdempotencyConflict, "idempotency key %q was already used for a different request", key).
			WithHint("use a new key for a new request")
	}
	return resourceID, true, nil
}

// RecordIdempotency stores the result of a request made with key.
func (t *Tx) RecordIdempotency(actor, key, requestHash, resourceID string) error {
	if key == "" {
		return nil
	}
	if len(key) > 200 {
		return domain.Errorf(domain.CodeInvalidInput, "idempotency key is longer than 200 bytes")
	}
	_, err := t.q.ExecContext(t.ctx, "INSERT INTO idempotency_keys (actor, key, request_hash, resource_id, created_at) VALUES (?, ?, ?, ?, ?)",
		actor, key, requestHash, resourceID, ms(t.now))
	return err
}

// Prune deletes idempotency keys and audit events older than the given ages.
func (s *Store) Prune(ctx context.Context, keyAge, eventAge time.Duration) error {
	now := s.now()
	if _, err := s.db.ExecContext(ctx, "DELETE FROM idempotency_keys WHERE created_at < ?", ms(now.Add(-keyAge))); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM events WHERE at < ?", ms(now.Add(-eventAge)))
	return err
}

// RequestHash returns a stable hash of a request, for idempotency checks.
func RequestHash(parts ...any) string {
	b, err := json.Marshal(parts)
	if err != nil {
		panic(err) // Callers pass plain values.
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func msOrNil(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time { return time.UnixMilli(v).UTC() }

func fromNullMS(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMS(v.Int64)
	return &t
}

type scanner interface{ Scan(dest ...any) error }

func notFound(err error, format string, args ...any) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Errorf(domain.CodeNotFound, format, args...)
	}
	return err
}
