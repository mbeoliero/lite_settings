package backend

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql" // registers "mysql"
	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx"

	"github.com/mbeoliero/lite_settings/server/api"
	"github.com/mbeoliero/lite_settings/store"
)

type dbBackend struct {
	st   *store.DB
	db   *sql.DB
	desc string
}

// OpenDB connects directly. An empty driver is inferred from the DSN.
func OpenDB(driver, dsn string) (Backend, error) {
	driver, err := store.SQLDriver(cmp.Or(driver, driverForDSN(dsn)))
	if err != nil {
		return nil, fmt.Errorf("--driver: %w", err)
	}
	db, err := sql.Open(driver, store.NormalizeDSN(driver, dsn))
	if err != nil {
		return nil, fmt.Errorf("open %s connection: %w", driver, err)
	}
	// One-shot CLI calls need only a small pool.
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)

	st, err := store.New(db, driver)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &dbBackend{st: st, db: db, desc: driver}, nil
}

// driverForDSN infers one of the two supported drivers; Ping catches a wrong guess.
func driverForDSN(dsn string) string {
	switch {
	case strings.HasPrefix(dsn, "postgres://"),
		strings.HasPrefix(dsn, "postgresql://"),
		strings.Contains(dsn, "host=") && strings.Contains(dsn, "user="):
		return "pgx"
	default:
		return "mysql"
	}
}

func (b *dbBackend) Describe() string { return b.desc }

// Close closes only pools opened by OpenDB.
func (b *dbBackend) Close() error {
	if b.db == nil {
		return nil
	}
	return b.db.Close()
}

func (b *dbBackend) Migrate(ctx context.Context) error { return b.st.Migrate(ctx) }

func (b *dbBackend) Get(ctx context.Context, key string) (api.ConfigDetail, error) {
	c, err := b.st.Get(ctx, key)
	if err != nil {
		return api.ConfigDetail{}, mapErr(err)
	}
	return api.ConfigDetail{
		Config:    api.Config{Key: c.Key, Value: c.Value, Format: string(c.Format)},
		UpdatedAt: c.UpdatedAt,
		UpdatedBy: c.UpdatedBy,
	}, nil
}

// List uses one query to keep HTTP and DB snapshots consistent.
// ListPrefixes deduplicates overlaps and treats no prefixes as everything.
func (b *dbBackend) List(ctx context.Context, prefixes []string) ([]api.Config, error) {
	rows, err := b.st.ListPrefixes(ctx, prefixes)
	if err != nil {
		return nil, mapErr(err)
	}

	out := make([]api.Config, 0, len(rows))
	for _, c := range rows {
		out = append(out, api.Config{Key: c.Key, Value: c.Value, Format: string(c.Format)})
	}
	return out, nil
}

func (b *dbBackend) Set(ctx context.Context, key, value, format string, c Change) (api.WriteResult, error) {
	r, err := b.st.Set(ctx, key, value, store.Format(format), store.Change{Author: c.Author, Comment: c.Comment})
	return writeResult(key, r), mapErr(err)
}

func (b *dbBackend) Delete(ctx context.Context, key string, c Change) (api.WriteResult, error) {
	r, err := b.st.Delete(ctx, key, store.Change{Author: c.Author, Comment: c.Comment})
	return writeResult(key, r), mapErr(err)
}

func (b *dbBackend) Rollback(ctx context.Context, key string, version int64, c Change) (api.WriteResult, error) {
	r, err := b.st.Rollback(ctx, key, version, store.Change{Author: c.Author, Comment: c.Comment})
	return writeResult(key, r), mapErr(err)
}

func (b *dbBackend) History(ctx context.Context, key string, limit int) ([]api.HistoryEntry, error) {
	rows, err := b.st.History(ctx, key, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	// Match HTTP's 404 so both backends use the same exit code.
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	out := make([]api.HistoryEntry, len(rows))
	for i, h := range rows {
		out[i] = api.HistoryEntry{
			Version:   h.Version,
			Value:     h.Value,
			Format:    string(h.Format),
			Op:        string(h.Op),
			Comment:   h.Comment,
			CreatedAt: h.CreatedAt,
			CreatedBy: h.CreatedBy,
		}
	}
	return out, nil
}

func writeResult(key string, r store.Result) api.WriteResult {
	return api.WriteResult{Key: key, Version: r.Version, Revision: r.Revision}
}

// mapErr normalizes store sentinels so backend exit codes match.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, store.ErrNotMigrated):
		return fmt.Errorf("%w (run lsctl migrate first)", err)
	default:
		return err
	}
}

// WrapStore adapts a caller-owned store.DB into a Backend.
func WrapStore(st *store.DB, desc string) Backend {
	return &dbBackend{st: st, desc: desc}
}
