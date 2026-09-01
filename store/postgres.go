package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Postgres implements the PostgreSQL dialect.
type Postgres struct{}

// Name returns the registered driver name.
func (Postgres) Name() string { return "postgres" }

// Rebind rewrites ? into $1, $2, …
func (Postgres) Rebind(query string) string { return rebindDollar(query) }

// UpsertConfig writes updated_at because PostgreSQL has no column-level ON UPDATE.
func (Postgres) UpsertConfig() string {
	return `INSERT INTO lite_config (config_key, value, format, updated_by)
VALUES (?, ?, ?, ?)
ON CONFLICT (config_key) DO UPDATE SET
  value      = EXCLUDED.value,
  format     = EXCLUDED.format,
  updated_by = EXCLUDED.updated_by,
  updated_at = now()`
}

// PrefixCondition matches an escaped LIKE prefix.
func (Postgres) PrefixCondition() string {
	return `config_key LIKE ? ESCAPE '\'`
}

// BumpRevision uses UPDATE ... RETURNING to raise and read the watermark.
func (Postgres) BumpRevision(ctx context.Context, tx *sql.Tx) (int64, error) {
	var rev int64
	err := tx.QueryRowContext(ctx,
		`UPDATE lite_config_meta SET revision = revision + 1 WHERE id = 1 RETURNING revision`).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotMigrated
	}
	if err != nil {
		return 0, fmt.Errorf("bump revision: %w", err)
	}
	return rev, nil
}
