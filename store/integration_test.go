package store_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mbeoliero/lite_settings/internal/testdb"
	"github.com/mbeoliero/lite_settings/store"
)

// Integration tests cover real SQL, LIKE escaping, upserts, and revision
// serialization. They skip when the docker-compose DSNs are unset.
func eachBackend(t *testing.T, fn func(t *testing.T, s *store.DB)) {
	t.Helper()
	for name, db := range testdb.Backends(t, "store") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			fn(t, testdb.Fresh(t, db))
		})
	}
}

func TestIntegrationSetGetVersion(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(t *testing.T, s *store.DB) {
		ctx := t.Context()
		c := store.Change{Comment: "initial", Author: "jaken"}

		r1, err := s.Set(ctx, "prompt_group:main", `{"temperature":0.7}`, store.FormatJSON, c)
		if err != nil {
			t.Fatalf("set: %v", err)
		}
		if r1.Version != 1 {
			t.Errorf("first write version = %d, want 1", r1.Version)
		}

		got, err := s.Get(ctx, "prompt_group:main")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Value != `{"temperature":0.7}` || got.Format != store.FormatJSON {
			t.Errorf("get = %+v", got)
		}
		if got.UpdatedBy != "jaken" {
			t.Errorf("updated_by = %q, want jaken", got.UpdatedBy)
		}

		r2, err := s.Set(ctx, "prompt_group:main", `{"temperature":0.9}`, store.FormatJSON, c)
		if err != nil {
			t.Fatalf("overwrite with Set: %v", err)
		}
		if r2.Version != 2 {
			t.Errorf("overwrite version = %d, want 2", r2.Version)
		}
		if r2.Revision <= r1.Revision {
			t.Errorf("revision did not increase: %d -> %d", r1.Revision, r2.Revision)
		}

		got, _ = s.Get(ctx, "prompt_group:main")
		if got.Value != `{"temperature":0.9}` {
			t.Errorf("upsert did not take effect: %q", got.Value)
		}
	})
}

// The database verifies LIKE escaping beyond generated-pattern unit tests.
func TestIntegrationPrefixUnderscore(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(t *testing.T, s *store.DB) {
		ctx := t.Context()
		c := store.Change{Author: "t"}

		for _, k := range []string{
			"prompt_group:a",
			"prompt_group:b",
			"promptXgroup:c", // unescaped, the '_' wildcard would pull this in too
			"other:d",
		} {
			if _, err := s.Set(ctx, k, "v", store.FormatRaw, c); err != nil {
				t.Fatalf("set %s: %v", k, err)
			}
		}

		got, err := s.ListPrefix(ctx, "prompt_group:")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var keys []string
		for _, cfg := range got {
			keys = append(keys, cfg.Key)
		}
		want := []string{"prompt_group:a", "prompt_group:b"}
		if !slices.Equal(keys, want) {
			t.Errorf("ListPrefix = %v, want %v", keys, want)
		}

		all, err := s.ListPrefix(ctx, "")
		if err != nil {
			t.Fatalf("list all: %v", err)
		}
		if len(all) != 4 {
			t.Errorf("empty prefix returned %d configs, want 4", len(all))
		}
	})
}

// Hard deletes must retain rollbackable history.
func TestIntegrationDeleteAndRestore(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(t *testing.T, s *store.DB) {
		ctx := t.Context()
		c := store.Change{Author: "t"}

		s.Set(ctx, "feature:debug", "true", store.FormatRaw, c)
		s.Set(ctx, "feature:debug", "false", store.FormatRaw, c)
		if _, err := s.Delete(ctx, "feature:debug", store.Change{Comment: "deleted by mistake", Author: "t"}); err != nil {
			t.Fatalf("delete: %v", err)
		}

		if _, err := s.Get(ctx, "feature:debug"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Get after delete = %v, want ErrNotFound", err)
		}
		if got, _ := s.ListPrefix(ctx, "feature:"); len(got) != 0 {
			t.Errorf("ListPrefix after delete = %v, want empty", got)
		}

		h, err := s.History(ctx, "feature:debug", 0)
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if len(h) != 3 {
			t.Fatalf("history length = %d, want 3", len(h))
		}
		if h[0].Op != store.OpDelete || h[0].Version != 3 {
			t.Errorf("latest history entry = v%d %s, want v3 delete", h[0].Version, h[0].Op)
		}

		if h[0].Value != "false" {
			t.Errorf("delete entry did not preserve the deleted value: %q", h[0].Value)
		}

		if _, err := s.Rollback(ctx, "feature:debug", 3, store.Change{Author: "t"}); err != nil {
			t.Fatalf("rollback: %v", err)
		}
		got, err := s.Get(ctx, "feature:debug")
		if err != nil {
			t.Fatalf("Get after rollback: %v", err)
		}
		if got.Value != "false" {
			t.Errorf("value after rollback = %q, want false", got.Value)
		}

		// Rollback appends rather than rewriting history.
		h, _ = s.History(ctx, "feature:debug", 0)
		if len(h) != 4 || h[0].Op != store.OpRollback {
			t.Errorf("rollback history length = %d, latest op = %s; want 4 entries ending in rollback", len(h), h[0].Op)
		}
	})
}

// Concurrent writes must produce strictly increasing, gapless revisions;
// otherwise clients can permanently skip changes.
func TestIntegrationRevisionNoGaps(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(t *testing.T, s *store.DB) {
		ctx := t.Context()
		const n = 30

		base, err := s.Revision(ctx)
		if err != nil {
			t.Fatalf("revision: %v", err)
		}

		var (
			mu   sync.Mutex
			revs = map[int64]bool{}
			wg   sync.WaitGroup
		)
		for i := range n {
			wg.Go(func() {
				r, err := s.Set(ctx, fmt.Sprintf("k%d", i), "v", store.FormatRaw, store.Change{Author: "t"})
				if err != nil {
					t.Errorf("concurrent Set %d: %v", i, err)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if revs[r.Revision] {
					t.Errorf("revision %d was assigned more than once", r.Revision)
				}
				revs[r.Revision] = true
			})
		}
		wg.Wait()

		for i := int64(1); i <= n; i++ {
			if !revs[base+i] {
				t.Errorf("revision %d is missing (gap)", base+i)
			}
		}

		final, _ := s.Revision(ctx)
		if final != base+n {
			t.Errorf("final revision = %d, want %d", final, base+n)
		}
	})
}

func TestIntegrationValidationRejectsBadFormat(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(t *testing.T, s *store.DB) {
		ctx := t.Context()
		c := store.Change{Author: "t"}

		if _, err := s.Set(ctx, "bad:json", `{"a":}`, store.FormatJSON, c); err == nil {
			t.Error("invalid JSON was accepted")
		}
		// Failed writes must not create a revision gap.
		if _, err := s.Get(ctx, "bad:json"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("rejected write was persisted: %v", err)
		}
		if h, _ := s.History(ctx, "bad:json", 0); len(h) != 0 {
			t.Errorf("rejected write left %d history entries", len(h))
		}
	})
}

func TestIntegrationRollbackMissingVersion(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(t *testing.T, s *store.DB) {
		ctx := t.Context()
		s.Set(ctx, "k:a", "v", store.FormatRaw, store.Change{Author: "t"})
		if _, err := s.Rollback(ctx, "k:a", 99, store.Change{Author: "t"}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("rollback to missing version = %v, want ErrNotFound", err)
		}
	})
}

// Migrate must be repeatable because the server runs it on every start.
func TestIntegrationMigrateIdempotent(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(t *testing.T, s *store.DB) {
		ctx := t.Context()
		for range 3 {
			if err := s.Migrate(ctx); err != nil {
				t.Fatalf("repeat Migrate: %v", err)
			}
		}
		if _, err := s.Revision(ctx); err != nil {
			t.Errorf("Revision after repeat Migrate: %v", err)
		}
	})
}

// Both databases must treat key case identically; MySQL therefore pins
// utf8mb4_bin instead of its case-insensitive default.
func TestKeysAreCaseSensitive(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	ctx := t.Context()

	for name, db := range testdb.Backends(t, "store") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := testdb.Fresh(t, db)
			c := store.Change{Author: "t"}

			if _, err := s.Set(ctx, "Feature:X", "upper", store.FormatRaw, c); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Set(ctx, "feature:x", "lower", store.FormatRaw, c); err != nil {
				t.Fatal(err)
			}

			upper, err := s.Get(ctx, "Feature:X")
			if err != nil {
				t.Fatal(err)
			}
			if upper.Value != "upper" {
				t.Fatalf("Feature:X was overwritten by feature:x; value = %q", upper.Value)
			}
			lower, err := s.Get(ctx, "feature:x")
			if err != nil {
				t.Fatal(err)
			}
			if lower.Value != "lower" {
				t.Fatalf("feature:x = %q, want \"lower\"", lower.Value)
			}

			got, err := s.ListPrefix(ctx, "feature:")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].Key != "feature:x" {
				t.Fatalf("prefix \"feature:\" matched %v, want only the lowercase key", got)
			}
		})
	}
}

// One query must union prefixes so concurrent writes cannot create a mixed snapshot.
func TestListPrefixesUnionAndDedup(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	ctx := t.Context()

	for name, db := range testdb.Backends(t, "store") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := testdb.Fresh(t, db)
			c := store.Change{Author: "t"}
			for _, k := range []string{"a:one", "a:b:two", "z:three"} {
				if _, err := s.Set(ctx, k, "v", store.FormatRaw, c); err != nil {
					t.Fatal(err)
				}
			}

			keys := func(cs []store.Config) []string {
				out := make([]string, len(cs))
				for i, c := range cs {
					out[i] = c.Key
				}
				return out
			}

			t.Run("EmptyPrefixMeansAll", func(t *testing.T) {
				t.Parallel()

				got, err := s.ListPrefixes(ctx, []string{"a:", ""})
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 3 {
					t.Fatalf("prefixes containing an empty prefix returned %v, want all 3 configs", keys(got))
				}
			})

			t.Run("NilMeansAll", func(t *testing.T) {
				t.Parallel()

				got, err := s.ListPrefixes(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 3 {
					t.Fatalf("empty prefix list returned %v, want all 3 configs", keys(got))
				}
			})

			t.Run("OverlappingPrefixesDedup", func(t *testing.T) {
				t.Parallel()

				got, err := s.ListPrefixes(ctx, []string{"a:", "a:b:"})
				if err != nil {
					t.Fatal(err)
				}
				if want := []string{"a:b:two", "a:one"}; !slices.Equal(keys(got), want) {
					t.Fatalf("= %v, want %v", keys(got), want)
				}
			})

			t.Run("Union", func(t *testing.T) {
				t.Parallel()

				got, err := s.ListPrefixes(ctx, []string{"z:", "a:one"})
				if err != nil {
					t.Fatal(err)
				}
				if want := []string{"a:one", "z:three"}; !slices.Equal(keys(got), want) {
					t.Fatalf("= %v, want %v", keys(got), want)
				}
			})
		})
	}
}

// Audit fields must fail before opening a transaction.
func TestOversizedAuditFieldsRejectedBeforeTx(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	ctx := t.Context()

	for name, db := range testdb.Backends(t, "store") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := testdb.Fresh(t, db)
			before, err := s.Revision(ctx)
			if err != nil {
				t.Fatal(err)
			}

			long := store.Change{Author: strings.Repeat("a", store.MaxAuthorLen+1)}
			if _, err := s.Set(ctx, "audit:k", "v", store.FormatRaw, long); !errors.Is(err, store.ErrInvalidChange) {
				t.Fatalf("oversized author returned %v, want ErrInvalidChange", err)
			}

			after, err := s.Revision(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("rejected write advanced revision: %d -> %d", before, after)
			}
		})
	}
}
