// Package store persists configuration, history, and rollbacks.
// Server and direct-DB modes share its schema.
package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Change carries write audit metadata without ambiguous string parameters.
type Change struct {
	Author  string
	Comment string
}

// Config is the current state of one config entry.
type Config struct {
	Format    Format
	Key       string
	UpdatedAt time.Time
	UpdatedBy string
	Value     string
}

var _ Store = (*DB)(nil)

// DB implements Store on MySQL or PostgreSQL.
type DB struct {
	d  Dialect
	db *sql.DB
}

// New selects the dialect from the sql.Open driver name.
// database/sql does not expose that name reliably. Binaries import drivers
// so SDK users do not inherit both.
func New(db *sql.DB, driver string) (*DB, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	return &DB{d: d, db: db}, nil
}

// Delete hard-deletes a config entry.
// Full-snapshot diffing needs no tombstone; history retains the value for rollback.
func (s *DB) Delete(ctx context.Context, key string, c Change) (Result, error) {
	return s.write(ctx, key, OpDelete, c,
		func(_ *sql.Tx, cur *Config) (string, Format, error) {
			if cur == nil {
				return "", "", fmt.Errorf("%q: %w", key, ErrNotFound)
			}
			return cur.Value, cur.Format, nil
		})
}

// Dialect returns the active dialect, for diagnostics.
func (s *DB) Dialect() Dialect { return s.d }

// Get reads one config entry, or ErrNotFound if the key does not exist.
func (s *DB) Get(ctx context.Context, key string) (*Config, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	cfg, err := getConfig(ctx, s.db, s.d, key)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, fmt.Errorf("%q: %w", key, ErrNotFound)
	}
	return cfg, nil
}

// History lists a key's versions newest first; limit <= 0 means no limit.
// It reads history alone because hard-deleted keys remain rollbackable.
func (s *DB) History(ctx context.Context, key string, limit int) ([]HistoryEntry, error) {
	if err := ValidateKey(key); err != nil {
		return nil, err
	}
	q := `SELECT id, config_key, value, format, version, op, comment, created_at, created_by
	        FROM lite_config_history
	       WHERE config_key = ?
	       ORDER BY version DESC`
	args := []any{key}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, s.d.Rebind(q), args...)
	if err != nil {
		return nil, fmt.Errorf("history %q: %w", key, err)
	}
	defer rows.Close()

	out := []HistoryEntry{}
	for rows.Next() {
		var e HistoryEntry
		if err := rows.Scan(&e.ID, &e.Key, &e.Value, &e.Format, &e.Version,
			&e.Op, &e.Comment, &e.CreatedAt, &e.CreatedBy); err != nil {
			return nil, fmt.Errorf("history %q: %w", key, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("history %q: %w", key, err)
	}
	return out, nil
}

// ListPrefix returns configs under prefix sorted by key; empty means all.
func (s *DB) ListPrefix(ctx context.Context, prefix string) ([]Config, error) {
	return s.ListPrefixes(ctx, []string{prefix})
}

// ListPrefixes returns a sorted, deduplicated prefix union; empty means all.
// One statement preserves a consistent snapshot if a write occurs between
// prefixes, without requiring a read-only transaction.
func (s *DB) ListPrefixes(ctx context.Context, prefixes []string) ([]Config, error) {
	q := `SELECT config_key, value, format, updated_at, updated_by
	        FROM lite_config`

	conds, args := make([]string, 0, len(prefixes)), make([]any, 0, len(prefixes))
	all := len(prefixes) == 0
	for _, p := range prefixes {
		if p == "" {
			// Empty subsumes prior prefixes; discard their bound arguments too.
			all = true
			conds, args = conds[:0], args[:0]
			break
		}
		conds = append(conds, s.d.PrefixCondition())
		args = append(args, likePrefix(p))
	}
	if !all {
		q += ` WHERE ` + strings.Join(conds, " OR ")
	}
	q += ` ORDER BY config_key`

	out, err := s.queryConfigs(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list prefixes %v: %w", prefixes, err)
	}
	// SQL OR deduplicates rows from nested prefixes.
	return out, nil
}

// Migrate idempotently applies embedded migrations and seeds metadata.
func (s *DB) Migrate(ctx context.Context) error {
	stmts, err := migrationsFor(s.d.Name())
	if err != nil {
		return err
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate (%s): %w", firstLine(stmt), err)
		}
	}
	return nil
}

// Revision returns the global watermark via the server's cheap heartbeat query.
func (s *DB) Revision(ctx context.Context) (int64, error) {
	var rev int64
	err := s.db.QueryRowContext(ctx,
		`SELECT revision FROM lite_config_meta WHERE id = 1`).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotMigrated
	}
	if err != nil {
		return 0, fmt.Errorf("read revision: %w", err)
	}
	return rev, nil
}

// Rollback restores key to toVersion through the ordinary append-only write path.
// It reads history directly because a hard-deleted key has no current row.
func (s *DB) Rollback(ctx context.Context, key string, toVersion int64, c Change) (Result, error) {
	return s.write(ctx, key, OpRollback, c,
		func(tx *sql.Tx, _ *Config) (string, Format, error) {
			var value string
			var format Format
			err := tx.QueryRowContext(ctx, s.d.Rebind(
				`SELECT value, format FROM lite_config_history
				  WHERE config_key = ? AND version = ?`), key, toVersion).
				Scan(&value, &format)
			if errors.Is(err, sql.ErrNoRows) {
				return "", "", fmt.Errorf("%q v%d: %w", key, toVersion, ErrNotFound)
			}
			if err != nil {
				return "", "", err
			}
			return value, format, nil
		})
}

// Set publishes a config entry, creating it if absent.
func (s *DB) Set(ctx context.Context, key, value string, format Format, c Change) (Result, error) {
	format = cmp.Or(format, FormatRaw)
	return s.write(ctx, key, OpSet, c,
		func(_ *sql.Tx, _ *Config) (string, Format, error) {
			return value, format, nil
		})
}

// queryConfigs expects (config_key, value, format, updated_at, updated_by).
func (s *DB) queryConfigs(ctx context.Context, q string, args ...any) ([]Config, error) {
	rows, err := s.db.QueryContext(ctx, s.d.Rebind(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Config{}
	for rows.Next() {
		var c Config
		if err := rows.Scan(&c.Key, &c.Value, &c.Format, &c.UpdatedAt, &c.UpdatedBy); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *DB) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // returns ErrTxDone after a successful commit

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// write is the transaction template shared by set, delete, and rollback.
//
// resolve decides the value and format to write, inside the transaction;
// cur is the key's current value, or nil if it does not exist.
func (s *DB) write(
	ctx context.Context,
	key string,
	op Op,
	c Change,
	resolve func(tx *sql.Tx, cur *Config) (string, Format, error),
) (Result, error) {
	if err := ValidateKey(key); err != nil {
		return Result{}, err
	}
	// Validate before the transaction to avoid a 500 or lenient MySQL truncation.
	if err := ValidateChange(c); err != nil {
		return Result{}, err
	}

	var res Result
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		// This must be the transaction's first statement. Its id=1 row lock
		// serializes writes until commit, so revisions follow commit order with
		// no gaps. Auto-increment or MAX() can become visible out of order and
		// make clients skip a change permanently.
		rev, err := s.d.BumpRevision(ctx, tx)
		if err != nil {
			return err
		}

		cur, err := getConfig(ctx, tx, s.d, key)
		if err != nil {
			return err
		}

		value, format, err := resolve(tx, cur)
		if err != nil {
			return err
		}
		if err := ValidateValue(value, format); err != nil {
			return err
		}

		if op == OpDelete {
			_, err = tx.ExecContext(ctx,
				s.d.Rebind(`DELETE FROM lite_config WHERE config_key = ?`), key)
		} else {
			_, err = tx.ExecContext(ctx,
				s.d.Rebind(s.d.UpsertConfig()), key, value, string(format), c.Author)
		}
		if err != nil {
			return fmt.Errorf("%s %q: %w", op, key, err)
		}

		// History is append-only; versions start at 1 per key.
		var prev int64
		if err := tx.QueryRowContext(ctx, s.d.Rebind(
			`SELECT COALESCE(MAX(version), 0) FROM lite_config_history WHERE config_key = ?`,
		), key).Scan(&prev); err != nil {
			return fmt.Errorf("next version %q: %w", key, err)
		}
		version := prev + 1

		if _, err := tx.ExecContext(ctx, s.d.Rebind(
			`INSERT INTO lite_config_history
			   (config_key, value, format, version, op, comment, created_by)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		), key, value, string(format), version, string(op), c.Comment, c.Author); err != nil {
			return fmt.Errorf("append history %q: %w", key, err)
		}

		res = Result{Revision: rev, Version: version}
		return nil
	})
	return res, err
}

// Format tells the client how to parse Value.
type Format string

const (
	FormatRaw  Format = "raw"  // scalar: bool, int, string, duration, …
	FormatJSON Format = "json" // one JSON document
	FormatYAML Format = "yaml" // one YAML document
)

// HistoryEntry is one complete config version.
// OpDelete retains the removed value so that version remains rollbackable.
type HistoryEntry struct {
	ID        int64
	Comment   string
	CreatedAt time.Time
	CreatedBy string
	Format    Format
	Key       string
	Op        Op
	Value     string
	Version   int64
}

// Op records which operation produced a history entry.
type Op string

const (
	OpSet      Op = "set"
	OpDelete   Op = "delete"
	OpRollback Op = "rollback"
)

// Result is what a write produces.
type Result struct {
	Revision int64 // the global revision after the write
	Version  int64 // this key's new version number
}

// Store is the persistence contract. *DB is the only implementation.
type Store interface {
	Delete(ctx context.Context, key string, c Change) (Result, error)
	Get(ctx context.Context, key string) (*Config, error)
	History(ctx context.Context, key string, limit int) ([]HistoryEntry, error)
	ListPrefix(ctx context.Context, prefix string) ([]Config, error)
	ListPrefixes(ctx context.Context, prefixes []string) ([]Config, error)
	Revision(ctx context.Context) (int64, error)
	Rollback(ctx context.Context, key string, toVersion int64, c Change) (Result, error)
	Set(ctx context.Context, key, value string, format Format, c Change) (Result, error)
}

// queryer shares reads between *sql.DB and *sql.Tx.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// firstLine identifies failed DDL without logging the whole statement.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if line, _, ok := strings.Cut(s, "\n"); ok {
		return strings.TrimSpace(line) + " …"
	}
	return s
}

// getConfig reads one config entry, returning (nil, nil) if absent.
func getConfig(ctx context.Context, q queryer, d Dialect, key string) (*Config, error) {
	var cfg Config
	err := q.QueryRowContext(ctx, d.Rebind(
		`SELECT config_key, value, format, updated_at, updated_by
		   FROM lite_config WHERE config_key = ?`), key).
		Scan(&cfg.Key, &cfg.Value, &cfg.Format, &cfg.UpdatedAt, &cfg.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get %q: %w", key, err)
	}
	return &cfg, nil
}
