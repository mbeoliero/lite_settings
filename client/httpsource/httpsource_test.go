package httpsource

import (
	"context"
	jsonv2 "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	lite "github.com/mbeoliero/lite_settings/client"
)

func httpSrcTest(t *testing.T, h http.HandlerFunc) (lite.Source, *httptest.Server) {
	t.Helper()
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	src := New(ts.URL)
	t.Cleanup(func() { src.Close() })
	return src, ts
}

// Omitting the revision on cold start requests an immediate full snapshot.
func TestHTTPSourceColdStartOmitsRevision(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	var got url.Values
	src, _ := httpSrcTest(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		jsonv2.MarshalWrite(w, lite.Snapshot{Revision: 5})
	})

	if _, err := src.Poll(t.Context(), lite.PollRequest{Since: -1}); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["revision"]; ok {
		t.Fatalf("cold start should not include a revision; got %v", got)
	}
}

func TestHTTPSourceSendsRevisionAndPrefixes(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	var got url.Values
	var path string
	src, _ := httpSrcTest(t, func(w http.ResponseWriter, r *http.Request) {
		got, path = r.URL.Query(), r.URL.Path
		jsonv2.MarshalWrite(w, lite.Snapshot{Revision: 8})
	})

	snap, err := src.Poll(t.Context(), lite.PollRequest{
		Since: 7, Prefixes: []string{"prompt_group:", "feature:"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/v1/watch" {
		t.Fatalf("path = %q", path)
	}
	if got.Get("revision") != "7" {
		t.Fatalf("revision = %q", got.Get("revision"))
	}
	if p := got["prefix"]; !slices.Equal(p, []string{"prompt_group:", "feature:"}) {
		t.Fatalf("prefix = %v", p)
	}
	if snap == nil || snap.Revision != 8 {
		t.Fatalf("snapshot = %+v", snap)
	}
}

// A normal 304 must return (nil, nil) to avoid unnecessary backoff.
func TestHTTPSource304MeansNoChange(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	src, _ := httpSrcTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})

	snap, err := src.Poll(t.Context(), lite.PollRequest{Since: 3})
	if err != nil {
		t.Fatalf("304 should not return an error: %v", err)
	}
	if snap != nil {
		t.Fatalf("304 should return a nil snapshot; got %+v", snap)
	}
}

func TestHTTPSourceSurfacesServerError(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	src, _ := httpSrcTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		jsonv2.MarshalWrite(w, struct {
			Error string `json:"error"`
		}{Error: "database is not initialized"})
	})

	_, err := src.Poll(t.Context(), lite.PollRequest{Since: 1})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "database is not initialized") {
		t.Fatalf("error should include the server message; got %v", err)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("error should include the status code; got %v", err)
	}
}

func TestHTTPSourceNonJSONError(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	src, _ := httpSrcTest(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "502 Bad Gateway from nginx", http.StatusBadGateway)
	})

	_, err := src.Poll(t.Context(), lite.PollRequest{Since: 1})
	if err == nil || !strings.Contains(err.Error(), "nginx") {
		t.Fatalf("plain-text proxy error should be preserved; got %v", err)
	}
}

func TestHTTPSourceMalformedBody(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	src, _ := httpSrcTest(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"revision": `))
	})

	if _, err := src.Poll(t.Context(), lite.PollRequest{Since: 1}); err == nil {
		t.Fatal("truncated response body should fail, not pass as an empty snapshot")
	}
}

// Context cancellation must interrupt a hanging poll so shutdown can finish.
func TestHTTPSourceRespectsContextCancel(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	src, _ := httpSrcTest(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := src.Poll(ctx, lite.PollRequest{Since: 1})
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation should return an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Poll did not return promptly after context cancellation")
	}
}

func TestHTTPSourcePollDeadline(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	t.Run("adds one when the caller has none", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := withPollDeadline(context.Background(), 30*time.Second)
		defer cancel()

		dl, ok := ctx.Deadline()
		if !ok {
			t.Fatal("a deadline-less caller must not be able to hang forever")
		}
		if left := time.Until(dl); left <= 30*time.Second {
			t.Fatalf("deadline must outlast the poll timeout; %v left", left)
		}
	})

	t.Run("keeps the caller's own deadline", func(t *testing.T) {
		t.Parallel()

		want := time.Now().Add(time.Hour)
		outer, cancelOuter := context.WithDeadline(context.Background(), want)
		defer cancelOuter()

		ctx, cancel := withPollDeadline(outer, time.Second)
		defer cancel()

		if got, _ := ctx.Deadline(); !got.Equal(want) {
			t.Fatalf("caller deadline changed: got %v, want %v", got, want)
		}
	})

	t.Run("stays unbounded when no timeout is given", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := withPollDeadline(context.Background(), 0)
		defer cancel()

		if _, ok := ctx.Deadline(); ok {
			t.Fatal("an unset timeout must not invent a bound")
		}
	})
}

func TestHTTPSourceTrimsTrailingSlash(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	var path string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		jsonv2.MarshalWrite(w, lite.Snapshot{Revision: 1})
	}))
	defer ts.Close()

	src := New(ts.URL + "/")
	defer src.Close()

	if _, err := src.Poll(t.Context(), lite.PollRequest{Since: -1}); err != nil {
		t.Fatal(err)
	}
	if path != "/v1/watch" {
		t.Fatalf("incorrect path join: %q", path)
	}
}

func TestHTTPSourceUnreachable(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	src := New("http://127.0.0.1:1")
	defer src.Close()

	if _, err := src.Poll(t.Context(), lite.PollRequest{Since: -1}); err == nil {
		t.Fatal("connection failure should return an error")
	}
}
