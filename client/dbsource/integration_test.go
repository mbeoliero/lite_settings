package dbsource_test

import (
	"errors"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	lite "github.com/mbeoliero/lite_settings/client"
	"github.com/mbeoliero/lite_settings/client/dbsource"
	"github.com/mbeoliero/lite_settings/client/httpsource"
	"github.com/mbeoliero/lite_settings/internal/testdb"
	"github.com/mbeoliero/lite_settings/server"
	"github.com/mbeoliero/lite_settings/store"
)

// A short poll interval keeps real-database integration tests fast.
const (
	tickInterval = 50 * time.Millisecond
	waitTimeout  = 10 * time.Second
)

type pair struct {
	http *lite.Client
	db   *lite.Client
	st   *store.DB
}

// each compares HTTP and direct-database clients against the same backend,
// enforcing the schema-as-protocol contract.
func each(t *testing.T, prefixes []string, fn func(t *testing.T, p pair)) {
	t.Helper()
	for name, sqldb := range testdb.Backends(t, "client") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			st := testdb.Fresh(t, sqldb)
			seed(t, st)

			srv, err := server.New(server.Options{
				Store:           st,
				PollInterval:    tickInterval,
				LongPollTimeout: 2 * time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			stop := srv.Start(t.Context())
			t.Cleanup(stop)

			ts := httptest.NewServer(srv.Handler())
			t.Cleanup(ts.Close)

			common := []lite.Option{
				lite.WithLongPollTimeout(2 * time.Second),
				lite.WithPollInterval(tickInterval),
			}
			if len(prefixes) > 0 {
				common = append(common, lite.WithPrefixes(prefixes...))
			}

			hc, err := lite.New(httpsource.New(ts.URL), common...)
			if err != nil {
				t.Fatalf("HTTP client: %v", err)
			}
			t.Cleanup(func() { hc.Close() })

			dc, err := lite.New(dbsource.Wrap(st), common...)
			if err != nil {
				t.Fatalf("direct client: %v", err)
			}
			t.Cleanup(func() { dc.Close() })

			fn(t, pair{http: hc, db: dc, st: st})
		})
	}
}

func seed(t *testing.T, st *store.DB) {
	t.Helper()
	ctx := t.Context()
	c := store.Change{Author: "test", Comment: "seed"}

	set := func(key, value string, f store.Format) {
		if _, err := st.Set(ctx, key, value, f, c); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	set("prompt_group:main", `{"system":"you are helpful","model":"opus","temp":0.7}`, store.FormatJSON)
	set("prompt_group:summary", "system: summarize\nmodel: haiku\ntemp: 0.2\n", store.FormatYAML)
	set("prompt_group:legacy", "plain text prompt", store.FormatRaw)
	set("feature:debug", "true", store.FormatRaw)
	set("http:timeout", "1500ms", store.FormatRaw)
}

type promptCfg struct {
	System string  `json:"system" yaml:"system"`
	Model  string  `json:"model" yaml:"model"`
	Temp   float64 `json:"temp" yaml:"temp"`
}

func waitFor(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestIntegrationBothSourcesAgree(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, nil, func(t *testing.T, p pair) {
		for _, c := range []struct {
			name string
			cl   *lite.Client
		}{{"http", p.http}, {"db", p.db}} {
			g := c.cl.Group("prompt_group:")
			if got := g.Keys(); !slices.Equal(got, []string{"legacy", "main", "summary"}) {
				t.Fatalf("%s: Keys = %v", c.name, got)
			}

			main, err := c.cl.Get[promptCfg]("prompt_group:main")
			if err != nil {
				t.Fatalf("%s: decode json: %v", c.name, err)
			}
			if main.Model != "opus" || main.Temp != 0.7 {
				t.Fatalf("%s: main = %+v", c.name, main)
			}

			sum, err := g.Get[promptCfg]("summary")
			if err != nil {
				t.Fatalf("%s: decode yaml: %v", c.name, err)
			}
			if sum.Model != "haiku" || sum.Temp != 0.2 {
				t.Fatalf("%s: summary = %+v", c.name, sum)
			}

			if got := c.cl.GetOr("feature:debug", false); got != true {
				t.Fatalf("%s: bool = %v", c.name, got)
			}
			if got := c.cl.GetOr("http:timeout", time.Second); got != 1500*time.Millisecond {
				t.Fatalf("%s: duration = %v", c.name, got)
			}
		}

		if p.http.Revision() != p.db.Revision() {
			t.Fatalf("revision mismatch between modes: http=%d db=%d", p.http.Revision(), p.db.Revision())
		}
	})
}

func TestIntegrationBothSourcesFollowWrites(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, []string{"prompt_group:"}, func(t *testing.T, p pair) {
		httpSeen := make(chan string, 8)
		dbSeen := make(chan string, 8)
		p.http.Watch("prompt_group:", func(g lite.Group) { httpSeen <- g.GetOr("legacy", "") })
		p.db.Watch("prompt_group:", func(g lite.Group) { dbSeen <- g.GetOr("legacy", "") })

		<-httpSeen // the synchronous callback from registration
		<-dbSeen

		if _, err := p.st.Set(t.Context(), "prompt_group:legacy", "updated prompt",
			store.FormatRaw, store.Change{Author: "test"}); err != nil {
			t.Fatal(err)
		}

		for _, c := range []struct {
			name string
			ch   chan string
		}{{"http", httpSeen}, {"db", dbSeen}} {
			select {
			case got := <-c.ch:
				if got != "updated prompt" {
					t.Fatalf("%s callback value = %q", c.name, got)
				}
			case <-time.After(waitTimeout):
				t.Fatalf("%s mode did not receive a change callback", c.name)
			}
		}
	})
}

// Full snapshots imply deletions without tombstones.
func TestIntegrationDeletePropagates(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, nil, func(t *testing.T, p pair) {
		if _, err := p.st.Delete(t.Context(), "feature:debug",
			store.Change{Author: "test"}); err != nil {
			t.Fatal(err)
		}

		for _, c := range []struct {
			name string
			cl   *lite.Client
		}{{"http", p.http}, {"db", p.db}} {
			waitFor(t, c.name+" observe deletion", func() bool {
				_, ok := c.cl.Raw("feature:debug")
				return !ok
			})
			if _, err := c.cl.Get[bool]("feature:debug"); !errors.Is(err, lite.ErrNotFound) {
				t.Fatalf("%s: deletion should return ErrNotFound; got %v", c.name, err)
			}
		}
	})
}

func TestIntegrationPrefixScoping(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, []string{"prompt_group:"}, func(t *testing.T, p pair) {
		for _, c := range []struct {
			name string
			cl   *lite.Client
		}{{"http", p.http}, {"db", p.db}} {
			if _, ok := c.cl.Raw("feature:debug"); ok {
				t.Fatalf("%s: fetched feature:debug outside the prefix", c.name)
			}
			if got := c.cl.Group("").Len(); got != 3 {
				t.Fatalf("%s: snapshot size = %d, want 3", c.name, got)
			}
		}
	})
}

func TestIntegrationColdStartFromLocalCache(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, nil, func(t *testing.T, p pair) {
		path := t.TempDir() + "/snap.json"

		warm, err := lite.New(dbsource.Wrap(p.st), lite.WithFallbackFile(path),
			lite.WithPollInterval(tickInterval), lite.WithLongPollTimeout(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		warm.Close()

		dead, err := dbsource.Open("mysql", "user:pass@tcp(127.0.0.1:1)/nope")
		if err != nil {
			t.Fatal(err)
		}
		cold, err := lite.New(dead, lite.WithFallbackFile(path),
			lite.WithStartupTimeout(2*time.Second), lite.WithLongPollTimeout(time.Second))
		if err != nil {
			t.Fatalf("cold start should succeed with a cache when the source is unavailable: %v", err)
		}
		defer cold.Close()

		got, err := cold.Get[promptCfg]("prompt_group:main")
		if err != nil {
			t.Fatalf("read config from cache: %v", err)
		}
		if got.Model != "opus" {
			t.Fatalf("unexpected cache contents: %+v", got)
		}
	})
}
