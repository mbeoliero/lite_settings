// Package backendtest provides real-database backend fixtures.
// A separate package avoids a command/ui import cycle.
package backendtest

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/backend"
	"github.com/mbeoliero/lite_settings/internal/testdb"
	"github.com/mbeoliero/lite_settings/server"
	"github.com/mbeoliero/lite_settings/store"
)

// Timeout is the per-call fixture timeout.
const Timeout = 20 * time.Second

// Author signs fixture writes.
const Author = "tester"

// Each runs fn against every database and both backend modes.
// Side-by-side assertions enforce identical HTTP and direct behavior.
// suffix must be unique because packages run concurrently.
func Each(t *testing.T, suffix string, fn func(t *testing.T, be backend.Backend)) {
	t.Helper()
	for dbName, db := range testdb.Backends(t, suffix) {
		t.Run(dbName, func(t *testing.T) {
			for _, mode := range []string{"db", "http"} {
				t.Run(mode, func(t *testing.T) {
					fn(t, Open(t, testdb.Fresh(t, db), mode))
				})
			}
		})
	}
}

// Open wraps a store.DB as a direct or HTTP backend.
func Open(t *testing.T, st *store.DB, mode string) backend.Backend {
	t.Helper()
	if mode == "db" {
		return backend.WrapStore(st, "test")
	}

	srv, err := server.New(server.Options{Store: st})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	stop := srv.Start(t.Context())
	t.Cleanup(stop)

	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return backend.OpenHTTP(hs.URL, hs.Client())
}

// Seed writes one config or fails the test.
func Seed(t *testing.T, be backend.Backend, key, value, format string) {
	t.Helper()
	if _, err := be.Set(t.Context(), key, value, format,
		backend.Change{Author: "seed", Comment: "seed"}); err != nil {
		t.Fatalf("seed %s: %v", key, err)
	}
}
