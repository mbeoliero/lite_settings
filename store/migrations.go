package store

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// migrationFS holds each dialect's schema in operator-reviewable SQL files.
//
//go:embed migrations/*/*.sql
var migrationFS embed.FS

// migrationsFor returns repeatable statements in filename order.
func migrationsFor(dialect string) ([]string, error) {
	dir := path.Join("migrations", dialect)
	names, err := fs.Glob(migrationFS, path.Join(dir, "*.sql"))
	if err != nil {
		return nil, fmt.Errorf("%w: list migrations for %s: %v", ErrUnsupportedDriver, dialect, err)
	}

	var out []string
	for _, n := range names {
		data, err := fs.ReadFile(migrationFS, n)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", n, err)
		}
		out = append(out, splitStatements(string(data))...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: migrations for %s are empty", ErrUnsupportedDriver, dialect)
	}
	return out, nil
}

// splitStatements splits this project's simple DDL on semicolons.
// It supports no procedures, DELIMITER, or semicolons in literals; use SQL
// lexing before adding those forms.
func splitStatements(src string) []string {
	var out []string
	for chunk := range strings.SplitSeq(src, ";") {
		var lines []string
		for l := range strings.SplitSeq(chunk, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "--") {
				continue
			}
			lines = append(lines, l)
		}
		if stmt := strings.TrimSpace(strings.Join(lines, "\n")); stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}
