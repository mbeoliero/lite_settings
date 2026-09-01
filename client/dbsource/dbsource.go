// Package dbsource reads configuration directly from the database using
// the schema as its protocol. It suits small deployments at the cost of one
// database connection per application instance.
package dbsource

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	lite "github.com/mbeoliero/lite_settings/client"
	"github.com/mbeoliero/lite_settings/store"
)

const defaultInterval = time.Second

// Source reads configuration directly from a database.
type Source struct {
	st *store.DB
	db *sql.DB // non-nil only when Open created the connection
}

var _ lite.Source = (*Source)(nil)

// New builds a source on an existing *sql.DB without taking ownership.
// driver is the name passed to sql.Open.
func New(db *sql.DB, driver string) (*Source, error) {
	st, err := store.New(db, driver)
	if err != nil {
		return nil, err
	}
	return &Source{st: st}, nil
}

// Wrap builds a source on an existing *store.DB.
func Wrap(st *store.DB) *Source { return &Source{st: st} }

// Open creates a connection owned by the source.
// driver accepts the same names as the binaries' --driver flag.
func Open(driver, dsn string) (*Source, error) {
	driver, err := store.SQLDriver(driver)
	if err != nil {
		return nil, err
	}
	// MySQL must receive the normalized parseTime setting because reads scan
	// updated_at into time.Time.
	db, err := sql.Open(driver, store.NormalizeDSN(driver, dsn))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	st, err := store.New(db, driver)
	if err != nil {
		db.Close()
		return nil, err
	}
	// One pool per application instance, so keep it small.
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(30 * time.Minute)
	return &Source{st: st, db: db}, nil
}

// Poll watches the revision and returns a full snapshot when it moves.
//
// The revision is always read before the data, so a snapshot is never
// labelled higher than its true version: labelling low self-heals within
// one cycle, labelling high skips a change forever.
func (s *Source) Poll(ctx context.Context, req lite.PollRequest) (*lite.Snapshot, error) {
	interval := req.Interval
	if interval <= 0 {
		interval = defaultInterval
	}

	// Hang until Timeout before reporting "unchanged", so the caller's
	// loop keeps the same rhythm as in long-polling mode.
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	for {
		rev, err := s.st.Revision(ctx)
		if err != nil {
			return nil, fmt.Errorf("read revision: %w", err)
		}
		// != rather than >: a database restored from backup moves the
		// revision backwards, and that must trigger a refetch too.
		if rev != req.Since {
			return s.fetch(ctx, rev, req.Prefixes)
		}

		select {
		case <-ctx.Done():
			return nil, nil
		case <-time.After(interval):
		}
	}
}

// One ListPrefixes call prevents concurrent writes from mixing multiple
// points in time in the snapshot.
func (s *Source) fetch(ctx context.Context, rev int64, prefixes []string) (*lite.Snapshot, error) {
	list, err := s.st.ListPrefixes(ctx, prefixes)
	if err != nil {
		return nil, fmt.Errorf("read prefixes %v: %w", prefixes, err)
	}

	out := make([]lite.Config, 0, len(list))
	for _, c := range list {
		out = append(out, lite.Config{Key: c.Key, Value: c.Value, Format: string(c.Format)})
	}
	return &lite.Snapshot{Revision: rev, Configs: out}, nil
}

// Close closes only a connection this package opened.
func (s *Source) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}
