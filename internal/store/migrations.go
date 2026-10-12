package store

import (
	"fmt"

	"github.com/filstawski/deployer/internal/domain"
)

// migrations are applied in order; PRAGMA user_version records how many ran.
// Never edit a released migration: append a new one instead.
var migrations = []string{`
CREATE TABLE settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE tokens (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL,
	secret_hash TEXT NOT NULL UNIQUE,
	scopes      TEXT NOT NULL,
	projects    TEXT NOT NULL,
	created_by  TEXT NOT NULL,
	created_at  INTEGER NOT NULL,
	expires_at  INTEGER,
	revoked_at  INTEGER
);

CREATE TABLE projects (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL UNIQUE,
	repository TEXT NOT NULL,
	created_at INTEGER NOT NULL
);

CREATE TABLE deployments (
	id                TEXT PRIMARY KEY,
	project_id        TEXT NOT NULL REFERENCES projects (id),
	name              TEXT NOT NULL,
	ref               TEXT NOT NULL,
	commit_sha        TEXT NOT NULL DEFAULT '',
	state             TEXT NOT NULL,
	error_code        TEXT NOT NULL DEFAULT '',
	error_message     TEXT NOT NULL DEFAULT '',
	error_hint        TEXT NOT NULL DEFAULT '',
	image             TEXT NOT NULL DEFAULT '',
	port              INTEGER NOT NULL DEFAULT 0,
	health_path       TEXT NOT NULL DEFAULT '',
	health_timeout_ms INTEGER NOT NULL DEFAULT 0,
	env               TEXT NOT NULL DEFAULT '{}',
	ttl_seconds       INTEGER NOT NULL,
	created_by        TEXT NOT NULL,
	created_at        INTEGER NOT NULL,
	updated_at        INTEGER NOT NULL,
	ready_at          INTEGER,
	expires_at        INTEGER,
	purge_at          INTEGER,
	operation_id      TEXT NOT NULL DEFAULT ''
);
-- Names are unique among live deployments and released on destroy.
CREATE UNIQUE INDEX deployments_live_name ON deployments (name) WHERE state != 'destroyed';
CREATE INDEX deployments_project ON deployments (project_id);

CREATE TABLE operations (
	id            TEXT PRIMARY KEY,
	kind          TEXT NOT NULL,
	status        TEXT NOT NULL,
	project_id    TEXT NOT NULL,
	deployment_id TEXT NOT NULL DEFAULT '',
	actor         TEXT NOT NULL,
	attempts      INTEGER NOT NULL DEFAULT 0,
	error_code    TEXT NOT NULL DEFAULT '',
	error_message TEXT NOT NULL DEFAULT '',
	error_hint    TEXT NOT NULL DEFAULT '',
	result        TEXT,
	created_at    INTEGER NOT NULL,
	updated_at    INTEGER NOT NULL,
	started_at    INTEGER,
	finished_at   INTEGER,
	deadline      INTEGER NOT NULL
);
CREATE INDEX operations_status ON operations (status, created_at);
CREATE INDEX operations_deployment ON operations (deployment_id, status);

CREATE TABLE idempotency_keys (
	actor         TEXT NOT NULL,
	key           TEXT NOT NULL,
	request_hash  TEXT NOT NULL,
	resource_id   TEXT NOT NULL,
	created_at    INTEGER NOT NULL,
	PRIMARY KEY (actor, key)
);

CREATE TABLE events (
	seq     INTEGER PRIMARY KEY AUTOINCREMENT,
	at      INTEGER NOT NULL,
	actor   TEXT NOT NULL,
	kind    TEXT NOT NULL,
	subject TEXT NOT NULL
);
`, `
ALTER TABLE operations ADD COLUMN request TEXT;
ALTER TABLE operations ADD COLUMN checkpoint TEXT;
`}

func (s *Store) migrate() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("database schema version %d is newer than this deployerd supports (%d); upgrade deployerd", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		if _, err := tx.Exec(migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if i == 0 {
			if _, err := tx.Exec("INSERT INTO settings (key, value) VALUES ('installation_id', ?)", domain.RandomString(10)); err != nil {
				return err
			}
		}
	}
	// PRAGMA statements cannot take parameters.
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", len(migrations))); err != nil {
		return err
	}
	return tx.Commit()
}
