package store

import (
	"fmt"
	"slices"
	"strings"
)

// SQLDriver maps a dialect name to its database/sql driver.
// Keeping aliases here ensures lite-settings, lsctl, and client/dbsource agree.
func SQLDriver(name string) (string, error) {
	switch name {
	case "mysql":
		return "mysql", nil
	case "pgx", "pgx/v5", "postgres", "postgresql":
		return "pgx", nil
	}
	return "", fmt.Errorf("%w: %q; must be mysql or postgres", ErrUnsupportedDriver, name)
}

// NormalizeDSN adds connection options required by the store.
// MySQL needs parseTime=true because timestamp columns are scanned into
// time.Time; this schema requirement overrides even an explicit false.
// PostgreSQL needs no changes.
func NormalizeDSN(driver, dsn string) string {
	if driver != "mysql" || dsn == "" {
		return dsn
	}
	// Match the driver's last-slash split so '/' or '?' in credentials is safe.
	head, tail, ok := strings.CutLast(dsn, "/")
	if !ok {
		return dsn // not a shape the driver accepts; let it say so
	}
	dbname, params, hasParams := strings.Cut(tail, "?")
	if !hasParams {
		return dsn + "?parseTime=true"
	}

	kept := slices.DeleteFunc(strings.Split(params, "&"), func(p string) bool {
		return p == "" || strings.HasPrefix(p, "parseTime=")
	})
	kept = append(kept, "parseTime=true")
	return head + "/" + dbname + "?" + strings.Join(kept, "&")
}
