// Package backend connects lsctl to HTTP or directly to a database.
package backend

import (
	"context"
	"errors"

	"github.com/mbeoliero/lite_settings/server/api"
)

// Backend provides matching HTTP and database operations.
// Migrate is the sole exception because HTTP cannot create tables.
type Backend interface {
	Get(ctx context.Context, key string) (api.ConfigDetail, error)
	List(ctx context.Context, prefixes []string) ([]api.Config, error)
	Set(ctx context.Context, key, value, format string, c Change) (api.WriteResult, error)
	Delete(ctx context.Context, key string, c Change) (api.WriteResult, error)
	History(ctx context.Context, key string, limit int) ([]api.HistoryEntry, error)
	Rollback(ctx context.Context, key string, version int64, c Change) (api.WriteResult, error)
	Migrate(ctx context.Context) error

	// Describe identifies the backend for error messages and prompts.
	Describe() string
	Close() error
}

// Change is the attribution attached to a write.
type Change struct {
	Author  string
	Comment string
}

var (
	// ErrNotFound keeps exit codes identical for HTTP 404 and store.ErrNotFound.
	ErrNotFound = errors.New("configuration not found")

	// ErrNotSupported means this backend cannot perform the operation.
	ErrNotSupported = errors.New("operation not supported by this backend")
)
