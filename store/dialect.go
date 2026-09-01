package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

// Dialect isolates genuine SQL differences between MySQL and PostgreSQL.
// Shared statements use MySQL's ? placeholders and Rebind translates them.
type Dialect interface {
	// Name identifies diagnostics and migrations/<name>/.
	Name() string

	// Rebind rewrites ? placeholders into the target database's form.
	Rebind(query string) string

	// UpsertConfig returns the lite_config upsert for
	// (config_key, value, format, updated_by).
	UpsertConfig() string

	// PrefixCondition returns the prefix-match condition without the WHERE
	// keyword, taking a pattern produced by likePrefix.
	PrefixCondition() string

	// BumpRevision raises and returns the global watermark.
	// It is a method because PostgreSQL uses UPDATE ... RETURNING while MySQL
	// requires UPDATE followed by SELECT.
	BumpRevision(ctx context.Context, tx *sql.Tx) (int64, error)
}

// DialectFor picks a dialect from a database/sql driver name.
func DialectFor(driver string) (Dialect, error) {
	switch driver {
	case "mysql":
		return MySQL{}, nil
	case "postgres", "pgx", "pgx/v5":
		return Postgres{}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedDriver, driver)
	}
}

// rebindDollar rewrites ? into $1, $2, … for PostgreSQL.
// Package SQL has no literals or ?? escapes, so byte-wise rewriting is safe.
func rebindDollar(query string) string {
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for i := range len(query) {
		if query[i] != '?' {
			b.WriteByte(query[i])
			continue
		}
		n++
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(n))
	}
	return b.String()
}

// likePrefix escapes LIKE wildcards in a prefix.
// Without escaping, "prompt_group:%" also matches "promptXgroup:foo";
// ValidateKey reserves backslash as the escape character.
func likePrefix(prefix string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(prefix) + "%"
}
