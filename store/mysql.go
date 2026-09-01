package store

import (
	"context"
	"database/sql"
	"fmt"
)

// MySQL implements the MySQL / MariaDB dialect.
type MySQL struct{}

// Name returns the registered driver name.
func (MySQL) Name() string { return "mysql" }

// Rebind is the identity because shared SQL uses ? placeholders.
func (MySQL) Rebind(query string) string { return query }

// UpsertConfig uses VALUES() for MySQL 5.7 and MariaDB compatibility.
// It writes updated_at explicitly because MySQL skips ON UPDATE when values
// are unchanged, although identical writes still advance audit history.
func (MySQL) UpsertConfig() string {
	return `INSERT INTO lite_config (config_key, value, format, updated_by)
VALUES (?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  value      = VALUES(value),
  format     = VALUES(format),
  updated_by = VALUES(updated_by),
  updated_at = CURRENT_TIMESTAMP`
}

// PrefixCondition uses explicit ESCAPE so NO_BACKSLASH_ESCAPES cannot alter matching.
func (MySQL) PrefixCondition() string {
	return `config_key LIKE ? ESCAPE '\\'`
}

// BumpRevision uses UPDATE then SELECT because MySQL lacks RETURNING.
// The UPDATE row lock prevents another write between them.
func (MySQL) BumpRevision(ctx context.Context, tx *sql.Tx) (int64, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE lite_config_meta SET revision = revision + 1 WHERE id = 1`)
	if err != nil {
		return 0, fmt.Errorf("bump revision: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return 0, ErrNotMigrated
	}

	var rev int64
	if err := tx.QueryRowContext(ctx,
		`SELECT revision FROM lite_config_meta WHERE id = 1`).Scan(&rev); err != nil {
		return 0, fmt.Errorf("read revision: %w", err)
	}
	return rev, nil
}
