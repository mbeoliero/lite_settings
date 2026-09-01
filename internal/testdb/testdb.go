// Package testdb gives each integration-test package an isolated database.
// Packages run concurrently, and sharing lite_config_meta would create apparent
// revision gaps that violate the store invariant.
package testdb

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql" // also registers the "mysql" driver

	// Central registration avoids blank imports in every test package.
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/mbeoliero/lite_settings/store"
)

const (
	envMySQL    = "LITE_TEST_MYSQL_DSN"
	envPostgres = "LITE_TEST_POSTGRES_DSN"
)

var safeSuffix = regexp.MustCompile(`^[a-z0-9_]+$`)

// Conn carries a test connection and its driver name, which database/sql
// does not expose for store.New.
type Conn struct {
	DB     *sql.DB
	Driver string
}

// Backends returns configured mysql and postgres backends, skipping if none.
func Backends(t *testing.T, suffix string) map[string]Conn {
	t.Helper()
	if !safeSuffix.MatchString(suffix) {
		t.Fatalf("suffix must contain only lowercase letters, digits, and underscores; got %q", suffix)
	}

	out := map[string]Conn{}
	if dsn := os.Getenv(envMySQL); dsn != "" {
		out["mysql"] = openMySQL(t, dsn, suffix)
	}
	if dsn := os.Getenv(envPostgres); dsn != "" {
		out["postgres"] = openPostgres(t, dsn, suffix)
	}
	if len(out) == 0 {
		t.Skipf("%s / %s are not set; skipping integration tests", envMySQL, envPostgres)
	}
	return out
}

// Fresh migrates the connection and empties it, returning a ready store.
func Fresh(t *testing.T, c Conn) *store.DB {
	t.Helper()
	ctx := t.Context()

	s, err := store.New(c.DB, c.Driver)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Preserve lite_config_meta: resetting monotonic revision would hide bugs.
	for _, tbl := range []string{"lite_config", "lite_config_history"} {
		if _, err := c.DB.ExecContext(ctx, "DELETE FROM "+tbl); err != nil {
			t.Fatalf("truncate table %s: %v", tbl, err)
		}
	}
	return s
}

func openMySQL(t *testing.T, dsn, suffix string) Conn {
	t.Helper()
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", envMySQL, err)
	}

	target := cfg.DBName + "_" + suffix
	admin := *cfg
	admin.DBName = "" // no default database, so CREATE DATABASE works
	exec(t, "mysql", admin.FormatDSN(),
		fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`", target))

	cfg.DBName = target
	return connect(t, "mysql", cfg.FormatDSN())
}

func openPostgres(t *testing.T, dsn, suffix string) Conn {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", envPostgres, err)
	}

	target := strings.TrimPrefix(u.Path, "/") + "_" + suffix
	admin := u.Clone()
	admin.Path = "/postgres" // cannot create a database from within itself
	// PostgreSQL lacks CREATE DATABASE IF NOT EXISTS; execIgnore handles the race.
	if !existsPG(t, admin.String(), target) {
		execIgnore(t, "pgx", admin.String(), fmt.Sprintf(`CREATE DATABASE "%s"`, target))
	}

	u2 := u.Clone()
	u2.Path = "/" + target
	return connect(t, "pgx", u2.String())
}

func existsPG(t *testing.T, dsn, name string) bool {
	t.Helper()
	db := dial(t, "pgx", dsn)
	defer db.Close()

	var n int
	err := db.QueryRow(`SELECT 1 FROM pg_database WHERE datname = $1`, name).Scan(&n)
	return err == nil && n == 1
}

// connect opens a connection and closes it when the test ends.
func connect(t *testing.T, driver, dsn string) Conn {
	t.Helper()
	db := dial(t, driver, dsn)
	t.Cleanup(func() { db.Close() })
	return Conn{DB: db, Driver: driver}
}

// dial opens a short-lived connection without test cleanup.
func dial(t *testing.T, driver, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatalf("open %s: %v", driver, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("ping %s: %v", driver, err)
	}
	return db
}

func exec(t *testing.T, driver, dsn, q string) {
	t.Helper()
	db := dial(t, driver, dsn)
	defer db.Close()
	if _, err := db.Exec(q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// execIgnore tolerates another package winning the database-creation race.
func execIgnore(t *testing.T, driver, dsn, q string) {
	t.Helper()
	db := dial(t, driver, dsn)
	defer db.Close()
	db.Exec(q) //nolint:errcheck // duplicate creation under a race is expected
}
