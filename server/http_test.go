package server

import (
	"bytes"
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mbeoliero/lite_settings/internal/testdb"
	"github.com/mbeoliero/lite_settings/server/api"
	"github.com/mbeoliero/lite_settings/store"
)

// HTTP integration tests skip when the docker-compose DSNs are unset.

type harness struct {
	t   *testing.T
	ts  *httptest.Server
	st  *store.DB
	srv *Server
}

func eachBackend(t *testing.T, fn func(h *harness)) {
	t.Helper()
	for name, db := range testdb.Backends(t, "server") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			st := testdb.Fresh(t, db)

			srv, err := New(Options{
				Store:           st,
				PollInterval:    20 * time.Millisecond,
				LongPollTimeout: 700 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("server.New: %v", err)
			}

			// Separate watcher control permits fixed-watermark tests.
			go srv.watcher.run(t.Context(), srv.pollInterval, st.Revision, discardLog())

			ts := httptest.NewServer(srv.Handler())
			t.Cleanup(ts.Close)

			fn(&harness{t: t, ts: ts, st: st, srv: srv})
		})
	}
}

// eachBackendNoWatcher holds chosen watermarks without racing the poller.
func eachBackendNoWatcher(t *testing.T, fn func(h *harness)) {
	t.Helper()
	for name, db := range testdb.Backends(t, "server") {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			st := testdb.Fresh(t, db)

			srv, err := New(Options{
				Store:           st,
				PollInterval:    20 * time.Millisecond,
				LongPollTimeout: 700 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("server.New: %v", err)
			}

			ts := httptest.NewServer(srv.Handler())
			t.Cleanup(ts.Close)
			t.Cleanup(srv.watcher.Close)

			fn(&harness{t: t, ts: ts, st: st, srv: srv})
		})
	}
}

func (h *harness) do(method, path string, body io.Reader) *http.Response {
	h.t.Helper()
	req, err := http.NewRequestWithContext(h.t.Context(), method, h.ts.URL+path, body)
	if err != nil {
		h.t.Fatalf("build %s %s: %v", method, path, err)
	}
	resp, err := h.ts.Client().Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// putSync returns the write watermark after the watcher catches up.
// Long-poll tests need that baseline rather than the watcher's reported value.
func (h *harness) putSync(key, value, format string) int64 {
	h.t.Helper()
	res := h.decodeJSON[api.WriteResult](h.put(key, value, format), 200)
	waitWatcherRev(h.t, h.srv, res.Revision)
	return res.Revision
}

func (h *harness) put(key, value, format string) *http.Response {
	h.t.Helper()
	q := url.Values{"format": {format}, "author": {"tester"}}
	return h.do(http.MethodPut, "/v1/configs/"+key+"?"+q.Encode(), strings.NewReader(value))
}

// decodeJSON reads the body and asserts the status code.
func (h *harness) decodeJSON[T any](resp *http.Response, wantCode int) T {
	h.t.Helper()
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantCode {
		h.t.Fatalf("status code = %d, want %d; body=%s", resp.StatusCode, wantCode, raw)
	}
	var v T
	if len(raw) > 0 {
		if err := jsonv2.Unmarshal(raw, &v); err != nil {
			h.t.Fatalf("failed to decode response: %v; body=%s", err, raw)
		}
	}
	return v
}

func expectCode(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Errorf("status code = %d, want %d; body=%s", resp.StatusCode, want, raw)
	}
}

func TestHTTPSetGetRoundtrip(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {
		res := h.decodeJSON[api.WriteResult](h.put("prompt_group:main", `{"temp":0.7}`, "json"), 200)
		if res.Version != 1 || res.Key != "prompt_group:main" {
			t.Errorf("write result = %+v", res)
		}

		got := h.decodeJSON[api.ConfigDetail](h.do("GET", "/v1/configs/prompt_group:main", nil), 200)
		if got.Value != `{"temp":0.7}` || got.Format != "json" {
			t.Errorf("read-back value = %+v", got)
		}
		if got.UpdatedBy != "tester" {
			t.Errorf("updated_by = %q, want tester", got.UpdatedBy)
		}

		// Client-visible revision comes from the trailing watcher.
		waitWatcherRev(t, h.srv, res.Revision)
		rev := h.decodeJSON[api.RevisionResponse](h.do("GET", "/v1/revision", nil), 200)
		if rev.Revision != res.Revision {
			t.Errorf("/v1/revision = %d, want %d", rev.Revision, res.Revision)
		}
	})
}

// ValidateKey excludes '/' so colon-bearing keys remain one path segment.
func TestHTTPKeyWithColonInPath(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {
		expectCode(t, h.put("a.b:c@d-e_f", "1", "raw"), 200)
		got := h.decodeJSON[api.ConfigDetail](h.do("GET", "/v1/configs/a.b:c@d-e_f", nil), 200)
		if got.Key != "a.b:c@d-e_f" {
			t.Errorf("key = %q", got.Key)
		}
	})
}

func TestHTTPListPrefix(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {
		for _, k := range []string{"prompt_group:a", "prompt_group:b", "promptXgroup:c", "feature:d"} {
			expectCode(t, h.put(k, "v", "raw"), 200)
		}

		snap := h.decodeJSON[api.Snapshot](h.do("GET", "/v1/configs?prefix=prompt_group:", nil), 200)
		if got := keysOf(snap); !slices.Equal(got, []string{"prompt_group:a", "prompt_group:b"}) {
			t.Errorf("prefix result = %v", got)
		}

		snap = h.decodeJSON[api.Snapshot](h.do("GET", "/v1/configs?prefix=prompt_group:&prefix=feature:", nil), 200)
		if got := keysOf(snap); !slices.Equal(got, []string{"feature:d", "prompt_group:a", "prompt_group:b"}) {
			t.Errorf("multiple-prefix result = %v", got)
		}

		snap = h.decodeJSON[api.Snapshot](h.do("GET", "/v1/configs", nil), 200)
		if len(snap.Configs) != 4 {
			t.Errorf("full snapshot contains %d configs, want 4", len(snap.Configs))
		}
	})
}

// Overlapping prefixes must produce one row each.
func TestHTTPOverlappingPrefixesDedup(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {
		expectCode(t, h.put("a:b:c", "v", "raw"), 200)
		snap := h.decodeJSON[api.Snapshot](h.do("GET", "/v1/configs?prefix=a:&prefix=a:b:", nil), 200)
		if len(snap.Configs) != 1 {
			t.Errorf("overlapping prefixes returned duplicates: %v", keysOf(snap))
		}
	})
}

func TestHTTPDeleteThenHistoryAndRollback(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {
		expectCode(t, h.put("feature:debug", "true", "raw"), 200)
		expectCode(t, h.put("feature:debug", "false", "raw"), 200)
		expectCode(t, h.do("DELETE", "/v1/configs/feature:debug?author=tester", nil), 200)

		expectCode(t, h.do("GET", "/v1/configs/feature:debug", nil), 404)

		// History must remain readable after a hard delete.
		hist := h.decodeJSON[[]api.HistoryEntry](h.do("GET", "/v1/configs/feature:debug/history", nil), 200)
		if len(hist) != 3 || hist[0].Op != "delete" {
			t.Fatalf("history length = %d, latest op = %q", len(hist), hist[0].Op)
		}
		if hist[0].Value != "false" {
			t.Errorf("delete entry preserved value %q, want deleted value", hist[0].Value)
		}

		body := strings.NewReader(`{"version":3}`)
		expectCode(t, h.do("POST", "/v1/configs/feature:debug/rollback?author=tester", body), 200)

		got := h.decodeJSON[api.ConfigDetail](h.do("GET", "/v1/configs/feature:debug", nil), 200)
		if got.Value != "false" {
			t.Errorf("value after rollback = %q, want false", got.Value)
		}
	})
}

func TestHTTPErrorMapping(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {
		cases := []struct {
			name string
			resp func() *http.Response
			want int
		}{
			{"missing key", func() *http.Response {
				return h.do("GET", "/v1/configs/nope:missing", nil)
			}, 404},
			{"invalid key character", func() *http.Response {
				return h.put("bad%20key", "v", "raw")
			}, 400},
			{"invalid format", func() *http.Response {
				return h.put("k:a", "v", "jsonn")
			}, 400},
			{"invalid JSON syntax", func() *http.Response {
				return h.put("k:b", `{"a":}`, "json")
			}, 400},
			{"invalid YAML syntax", func() *http.Response {
				return h.put("k:c", "a:\n  - b\n c: d", "yaml")
			}, 400},
			{"rollback to missing version", func() *http.Response {
				return h.do("POST", "/v1/configs/k:d/rollback", strings.NewReader(`{"version":99}`))
			}, 404},
			{"invalid rollback request body", func() *http.Response {
				return h.do("POST", "/v1/configs/k:d/rollback", strings.NewReader(`{`))
			}, 400},
			{"non-positive rollback version", func() *http.Response {
				return h.do("POST", "/v1/configs/k:d/rollback", strings.NewReader(`{"version":0}`))
			}, 400},
			{"oversized value", func() *http.Response {
				return h.put("k:big", strings.Repeat("x", store.MaxValueSize+10), "raw")
			}, 413},
		}
		for _, c := range cases {
			h.t.Run(c.name, func(t *testing.T) {
				t.Parallel()

				resp := c.resp()
				defer resp.Body.Close()
				raw, _ := io.ReadAll(resp.Body)
				if resp.StatusCode != c.want {
					t.Errorf("status code = %d, want %d; body=%s", resp.StatusCode, c.want, raw)
				}
				if c.want != 413 && !bytes.Contains(raw, []byte(`"error"`)) {
					t.Errorf("error response is missing the error field: %s", raw)
				}
			})
		}
	})
}

// Rejected writes must not trigger client refetches.
func TestHTTPRejectedWriteDoesNotBumpRevision(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {
		waitWatcherReady(t, h.srv)
		before := h.decodeJSON[api.RevisionResponse](h.do("GET", "/v1/revision", nil), 200)
		expectCode(t, h.put("k:bad", `{"a":}`, "json"), 400)
		after := h.decodeJSON[api.RevisionResponse](h.do("GET", "/v1/revision", nil), 200)
		if before.Revision != after.Revision {
			t.Errorf("invalid write advanced revision: %d -> %d", before.Revision, after.Revision)
		}
	})
}

func TestHTTPWatchImmediateWhenStale(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {
		expectCode(t, h.put("prompt_group:a", "v", "raw"), 200)

		start := time.Now()
		snap := h.decodeJSON[api.Snapshot](h.do("GET", "/v1/watch?prefix=prompt_group:", nil), 200)
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Errorf("cold-start watch blocked for %v, want immediate return", elapsed)
		}
		if len(snap.Configs) != 1 || snap.Revision <= 0 {
			t.Errorf("snapshot = %+v", snap)
		}
	})
}

func TestHTTPWatchNotModifiedOnTimeout(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {
		rev := h.putSync("prompt_group:a", "v", "raw")

		start := time.Now()
		resp := h.do("GET", fmt.Sprintf("/v1/watch?revision=%d&prefix=prompt_group:", rev), nil)
		expectCode(t, resp, http.StatusNotModified)
		if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
			t.Errorf("long poll blocked for only %v", elapsed)
		}
	})
}

// A write must wake long polls with a new snapshot.
func TestHTTPWatchWakesOnWrite(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {
		rev := h.putSync("prompt_group:a", "old", "raw")

		type result struct {
			snap api.Snapshot
			code int
		}
		done := make(chan result, 1)
		go func() {
			resp := h.do("GET", fmt.Sprintf("/v1/watch?revision=%d&prefix=prompt_group:", rev), nil)
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			var s api.Snapshot
			jsonv2.Unmarshal(raw, &s)
			done <- result{s, resp.StatusCode}
		}()

		time.Sleep(100 * time.Millisecond)
		expectCode(t, h.put("prompt_group:a", "new", "raw"), 200)

		select {
		case r := <-done:
			if r.code != 200 {
				t.Fatalf("watch status code = %d, want 200", r.code)
			}
			if len(r.snap.Configs) != 1 || r.snap.Configs[0].Value != "new" {
				t.Errorf("snapshot after wake-up = %+v", r.snap.Configs)
			}
			if r.snap.Revision <= rev {
				t.Errorf("snapshot revision = %d, want greater than %d", r.snap.Revision, rev)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("watch was not woken after write")
		}
	})
}

// List and watch must share the watcher watermark or cold starts waste a poll.
func TestHTTPListRevisionMatchesWatchBaseline(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {
		rev := h.putSync("prompt_group:a", "v", "raw")

		snap := h.decodeJSON[api.Snapshot](h.do("GET", "/v1/configs?prefix=prompt_group:", nil), 200)
		if snap.Revision != rev {
			t.Fatalf("/v1/configs revision = %d, want %d", snap.Revision, rev)
		}

		start := time.Now()
		resp := h.do("GET", fmt.Sprintf("/v1/watch?revision=%d&prefix=prompt_group:", snap.Revision), nil)
		expectCode(t, resp, http.StatusNotModified)
		if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
			t.Errorf("watch using /v1/configs revision blocked for only %v; endpoint revisions differ", elapsed)
		}
	})
}

func TestHTTPHealthz(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {
		waitWatcherReady(t, h.srv)
		got := h.decodeJSON[api.Health](h.do("GET", "/healthz", nil), 200)
		if !got.OK || got.DBError != "" {
			t.Errorf("healthz = %+v", got)
		}
	})
}

// Readiness must wait for the first successful poll.
func TestHTTPHealthzNotReadyBeforeFirstPoll(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {

		srv, err := New(Options{Store: h.st})
		if err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewServer(srv.Handler())
		defer ts.Close()

		resp, err := ts.Client().Get(ts.URL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		got := h.decodeJSON[api.Health](resp, http.StatusServiceUnavailable)
		if got.OK {
			t.Error("revisionWatcher is ready before its first poll")
		}
	})
}

func keysOf(s api.Snapshot) []string {
	out := make([]string, len(s.Configs))
	for i, c := range s.Configs {
		out[i] = c.Key
	}
	return out
}

func waitWatcherRev(t *testing.T, s *Server, want int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, rev, _ := s.watcher.health(); rev == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("revisionWatcher did not reach revision %d before the deadline", want)
}

func waitWatcherReady(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok, _, _ := s.watcher.health(); ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("revisionWatcher was not ready before the deadline")
}

// Size-limit and prefix regressions.

func TestHTTPValueAtLimitBoundary(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {

		expectCode(t, h.put("boundary:at", strings.Repeat("x", store.MaxValueSize), "raw"), 200)

		// Enforce the API contract: over-limit bodies always return 413.
		expectCode(t, h.put("boundary:over", strings.Repeat("x", store.MaxValueSize+1), "raw"),
			http.StatusRequestEntityTooLarge)
	})
}

func TestHTTPRejectsOversizedAuditFields(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {
		// Invalid audit lengths must not become 500s or silent truncation.
		q := url.Values{"author": {strings.Repeat("a", store.MaxAuthorLen+1)}}
		expectCode(t, h.do(http.MethodPut, "/v1/configs/audit:author?"+q.Encode(),
			strings.NewReader("v")), http.StatusBadRequest)

		q = url.Values{"comment": {strings.Repeat("c", store.MaxCommentLen+1)}}
		expectCode(t, h.do(http.MethodPut, "/v1/configs/audit:comment?"+q.Encode(),
			strings.NewReader("v")), http.StatusBadRequest)
	})
}

func TestHTTPTooManyPrefixesRejected(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackend(t, func(h *harness) {
		q := url.Values{}
		for i := range maxPrefixes + 1 {
			q.Add("prefix", fmt.Sprintf("p%d:", i))
		}
		expectCode(t, h.do(http.MethodGet, "/v1/configs?"+q.Encode(), nil),
			http.StatusBadRequest)
	})
}

func TestPrefixesDedupAndCollapse(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	req := func(qs string) []string {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/v1/configs?"+qs, nil)
		ps, err := prefixes(r)
		if err != nil {
			t.Fatalf("prefixes(%q): %v", qs, err)
		}
		return ps
	}

	t.Run("Collapse", func(t *testing.T) {
		t.Parallel()

		if got := req("prefix=a:&prefix=&prefix=b:"); !slices.Equal(got, []string{""}) {
			t.Fatalf("prefixes containing an empty prefix normalized to %q, want [\"\"]", got)
		}
	})

	t.Run("Dedup", func(t *testing.T) {
		t.Parallel()

		// Deduplicate before generating SQL branches.
		if got := req("prefix=a:&prefix=a:&prefix=b:&prefix=a:"); !slices.Equal(got, []string{"a:", "b:"}) {
			t.Fatalf("duplicate prefixes normalized to %q, want duplicates removed", got)
		}
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()

		if got := req(""); !slices.Equal(got, []string{""}) {
			t.Fatalf("request without prefix normalized to %q, want all configs", got)
		}
	})
}

func TestNewRejectsNegativeDurations(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// Reject negatives before they panic time.NewTicker in the poller.
	if _, err := New(Options{Store: &store.DB{}, PollInterval: -time.Second}); err == nil {
		t.Fatal("negative PollInterval was accepted")
	}
	if _, err := New(Options{Store: &store.DB{}, LongPollTimeout: -time.Second}); err == nil {
		t.Fatal("negative LongPollTimeout was accepted")
	}
}

// Before the first poll, sentinel -1 must not make a missing baseline hang.
func TestHTTPWatchWithoutBaselineDoesNotWaitForFirstPoll(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackendNoWatcher(t, func(h *harness) {
		if _, err := h.st.Set(t.Context(), "a:x", "1", store.FormatRaw, store.Change{Author: "t"}); err != nil {
			t.Fatalf("write: %v", err)
		}
		if ok, _, _ := h.srv.watcher.health(); ok {
			t.Fatal("test requires a watcher that has not polled yet")
		}

		start := time.Now()
		snap := h.decodeJSON[api.Snapshot](h.do("GET", "/v1/watch", nil), 200)

		if elapsed := time.Since(start); elapsed > h.srv.longPollTimeout/2 {
			t.Errorf("watch without a baseline took %v, want immediate return", elapsed)
		}
		if snap.Revision < 0 {
			t.Errorf("snapshot revision = %d, want the database revision", snap.Revision)
		}
		if len(snap.Configs) != 1 || snap.Configs[0].Key != "a:x" {
			t.Errorf("snapshot = %+v, want all configs", snap.Configs)
		}
	})
}

// After a restore, the stale high watcher watermark must not label rolled-back
// data or clients will treat it as already seen.
func TestHTTPSnapshotRevisionNeverExceedsDatabase(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	eachBackendNoWatcher(t, func(h *harness) {
		ctx := t.Context()
		if _, err := h.st.Set(ctx, "a:x", "1", store.FormatRaw, store.Change{Author: "t"}); err != nil {
			t.Fatalf("write: %v", err)
		}
		dbRev, err := h.st.Revision(ctx)
		if err != nil {
			t.Fatalf("read revision: %v", err)
		}

		// Simulate a watcher retaining the pre-restore watermark.
		h.srv.watcher.set(dbRev + 100)

		snap := h.decodeJSON[api.Snapshot](h.do("GET", "/v1/configs?prefix=a:", nil), 200)
		if snap.Revision > dbRev {
			t.Errorf("snapshot revision = %d, exceeds database revision %d", snap.Revision, dbRev)
		}
	})
}
