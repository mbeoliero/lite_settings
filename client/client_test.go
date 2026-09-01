package lite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// scriptedSource returns snapshots from a prepared script; gate holds a
// step until the test wants it. Once the script runs out it blocks until
// ctx ends, standing in for "long poll hanging with no change".
type scriptedSource struct {
	mu    sync.Mutex
	steps []scriptStep
	idx   int

	calls    atomic.Int64
	closes   atomic.Int64
	lastReq  atomic.Pointer[PollRequest]
	firstReq atomic.Pointer[PollRequest]
}

type scriptStep struct {
	snap *Snapshot
	err  error
	gate chan struct{}
}

func (s *scriptedSource) Poll(ctx context.Context, req PollRequest) (*Snapshot, error) {
	s.calls.Add(1)
	s.lastReq.Store(&req)
	s.firstReq.CompareAndSwap(nil, &req)

	s.mu.Lock()
	var st scriptStep
	ok := s.idx < len(s.steps)
	if ok {
		st = s.steps[s.idx]
		s.idx++
	}
	s.mu.Unlock()

	if !ok {
		<-ctx.Done()
		return nil, nil
	}
	if st.gate != nil {
		select {
		case <-st.gate:
		case <-ctx.Done():
			return nil, nil
		}
	}
	return st.snap, st.err
}

func (s *scriptedSource) Close() error {
	s.closes.Add(1)
	return nil
}

func apiSnap(rev int64, kv ...string) *Snapshot {
	s := &Snapshot{Revision: rev}
	for pair := range slices.Chunk(kv, 2) {
		s.Configs = append(s.Configs, Config{Key: pair[0], Value: pair[1], Format: FormatRaw})
	}
	return s
}

func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

func mustNotRecv[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	select {
	case v := <-ch:
		t.Fatalf("unexpectedly received %s: %v", what, v)
	case <-time.After(150 * time.Millisecond):
	}
}

// New must complete the first fetch synchronously, or a Get right after
// it would read an empty snapshot.
func TestNewFetchesBeforeReturning(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	src := &scriptedSource{steps: []scriptStep{{snap: apiSnap(7, "http:timeout", "1500ms")}}}
	c, err := New(src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	got, err := c.Get[time.Duration]("http:timeout")
	if err != nil || got != 1500*time.Millisecond {
		t.Fatalf("config should be readable when New returns; got %v, err %v", got, err)
	}
	if c.Revision() != 7 {
		t.Fatalf("revision = %d, want 7", c.Revision())
	}
}

// A cold start has no baseline and must ask with -1, so the source
// returns everything at once instead of hanging the client.
func TestColdStartAsksWithoutBaseline(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	src := &scriptedSource{steps: []scriptStep{{snap: apiSnap(1, "a", "1")}}}
	c, err := New(src, WithPrefixes("a", "b", "a"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	req := src.firstReq.Load()
	if req == nil || req.Since != -1 {
		t.Fatalf("initial fetch Since = %v, want -1", req)
	}
	if !slices.Equal(req.Prefixes, []string{"a", "b"}) {
		t.Fatalf("prefixes not deduplicated: %v", req.Prefixes)
	}
}

func TestNewFailsFastWithoutCache(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	boom := errors.New("config service unavailable")
	src := &scriptedSource{steps: []scriptStep{{err: boom}}}

	c, err := New(src, WithStartupTimeout(time.Second))
	if err == nil {
		c.Close()
		t.Fatal("New must fail when the initial fetch fails without a local cache")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("error should wrap the cause; got %v", err)
	}
	if src.closes.Load() != 1 {
		t.Fatalf("New must close Source on failure; Close called %d times", src.closes.Load())
	}
}

func TestNewFallsBackToLocalCache(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	path := filepath.Join(t.TempDir(), "snap.json")
	if err := writeFallback(path, snapOf(99, "prompt_group:main", "cached")); err != nil {
		t.Fatal(err)
	}

	src := &scriptedSource{steps: []scriptStep{{err: errors.New("database unavailable")}}}
	c, err := New(src, WithFallbackFile(path), WithStartupTimeout(time.Second))
	if err != nil {
		t.Fatalf("cold-start fallback should succeed with a local cache: %v", err)
	}
	defer c.Close()

	if got, _ := c.Raw("prompt_group:main"); got != "cached" {
		t.Fatalf("fallback cache not used; Raw = %q", got)
	}
	if c.Revision() != 99 {
		t.Fatalf("revision = %d, want 99", c.Revision())
	}
}

func TestNewFailsWhenCacheAlsoMissing(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	src := &scriptedSource{steps: []scriptStep{{err: errors.New("source unavailable")}}}
	c, err := New(src,
		WithFallbackFile(filepath.Join(t.TempDir(), "nope.json")),
		WithStartupTimeout(time.Second))
	if err == nil {
		c.Close()
		t.Fatal("must fail when both source and cache are unavailable")
	}
}

func TestWatchFiresImmediatelyThenOnChange(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	gate := make(chan struct{})
	src := &scriptedSource{steps: []scriptStep{
		{snap: apiSnap(1, "prompt_group:main", "v1")},
		{gate: gate, snap: apiSnap(2, "prompt_group:main", "v2")},
	}}
	c, err := New(src)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	seen := make(chan string, 4)
	c.Watch("prompt_group:", func(g Group) {
		seen <- g.GetOr("main", "")
	})

	if got := recv(t, seen, "initial callback"); got != "v1" {
		t.Fatalf("initial callback = %q, want v1", got)
	}

	close(gate)
	if got := recv(t, seen, "change callback"); got != "v2" {
		t.Fatalf("change callback = %q, want v2", got)
	}
}

func TestWatchSkipsUnrelatedPrefix(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	gate := make(chan struct{})
	src := &scriptedSource{steps: []scriptStep{
		{snap: apiSnap(1, "prompt_group:main", "v1", "feature:debug", "false")},
		{gate: gate, snap: apiSnap(2, "prompt_group:main", "v1", "feature:debug", "true")},
	}}
	c, err := New(src)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	prompts := make(chan int64, 4)
	c.Watch("prompt_group:", func(g Group) { prompts <- g.Revision() })
	features := make(chan int64, 4)
	c.Watch("feature:", func(g Group) { features <- g.Revision() })

	recv(t, prompts, "initial prompt callback")
	recv(t, features, "initial feature callback")

	close(gate)
	if got := recv(t, features, "feature change callback"); got != 2 {
		t.Fatalf("feature revision = %d", got)
	}
	mustNotRecv(t, prompts, "prompt_group callback")
}

func TestOnChangeReportsPrefixAndKeys(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	type ev struct {
		prefix string
		keys   []string
	}
	events := make(chan ev, 8)

	gate := make(chan struct{})
	src := &scriptedSource{steps: []scriptStep{
		{snap: apiSnap(1, "a:1", "x", "b:1", "x")},
		{gate: gate, snap: apiSnap(2, "a:1", "CHANGED", "b:1", "x")},
	}}
	c, err := New(src,
		WithPrefixes("a:", "b:"),
		WithOnChange(func(prefix string, keys []string) { events <- ev{prefix, keys} }))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	close(gate)
	got := recv(t, events, "OnChange")
	if got.prefix != "a:" || !slices.Equal(got.keys, []string{"a:1"}) {
		t.Fatalf("OnChange = %+v, want prefix a: / keys [a:1]", got)
	}
	mustNotRecv(t, events, "OnChange for b:")
}

// A panic in a user callback must not freeze the client's updates.
func TestCallbackPanicDoesNotFreezeClient(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	g1, g2 := make(chan struct{}), make(chan struct{})
	src := &scriptedSource{steps: []scriptStep{
		{snap: apiSnap(1, "k", "v1")},
		{gate: g1, snap: apiSnap(2, "k", "v2")},
		{gate: g2, snap: apiSnap(3, "k", "v3")},
	}}
	c, err := New(src)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var fired atomic.Int64
	c.Watch("", func(g Group) {
		if fired.Add(1) > 1 { // skip the synchronous callback from registration
			panic("application callback failed")
		}
	})

	good := make(chan string, 4)
	c.Watch("", func(g Group) { good <- g.GetOr("k", "") })
	recv(t, good, "second watcher's initial callback")

	close(g1)
	if got := recv(t, good, "first change"); got != "v2" {
		t.Fatalf("= %q", got)
	}
	close(g2)
	if got := recv(t, good, "change after panic"); got != "v3" {
		t.Fatalf("client stopped updating after panic; got %q", got)
	}
}

// Database restore can move the revision backwards; full snapshots must still
// replace the current one rather than be rejected by a rev > cur check.
func TestRevisionRegressionAppliesSnapshot(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	gate := make(chan struct{})
	src := &scriptedSource{steps: []scriptStep{
		{snap: apiSnap(500, "k", "new")},
		{gate: gate, snap: apiSnap(3, "k", "restored-from-backup")},
	}}
	c, err := New(src)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	seen := make(chan string, 4)
	c.Watch("", func(g Group) { seen <- g.GetOr("k", "") })
	recv(t, seen, "initial callback")

	close(gate)
	if got := recv(t, seen, "callback after revision rollback"); got != "restored-from-backup" {
		t.Fatalf("revision rollback should replace the entire snapshot; got %q", got)
	}
	if c.Revision() != 3 {
		t.Fatalf("revision = %d, want 3", c.Revision())
	}
}

func TestUnchangedContentDoesNotNotify(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	gate := make(chan struct{})
	src := &scriptedSource{steps: []scriptStep{
		{snap: apiSnap(1, "k", "same")},
		{gate: gate, snap: apiSnap(2, "k", "same")},
	}}
	c, err := New(src)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	seen := make(chan int64, 4)
	c.Watch("", func(g Group) { seen <- g.Revision() })
	recv(t, seen, "initial callback")

	close(gate)
	mustNotRecv(t, seen, "callback for unchanged content")

	// The baseline must still advance, or the next long poll returns
	// immediately with nothing.
	deadline := time.Now().Add(2 * time.Second)
	for c.Revision() != 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if c.Revision() != 2 {
		t.Fatalf("revision did not advance: %d", c.Revision())
	}
}

func TestErrorBacksOffThenRecovers(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	src := &scriptedSource{steps: []scriptStep{
		{snap: apiSnap(1, "k", "v1")},
		{err: errors.New("temporary failure")},
		{snap: apiSnap(2, "k", "v2")},
	}}
	c, err := New(src)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	seen := make(chan string, 4)
	c.Watch("", func(g Group) { seen <- g.GetOr("k", "") })
	recv(t, seen, "initial callback")

	if got := recv(t, seen, "callback after recovery"); got != "v2" {
		t.Fatalf("= %q", got)
	}
}

func TestCacheWrittenOnChange(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	path := filepath.Join(t.TempDir(), "snap.json")
	gate := make(chan struct{})
	src := &scriptedSource{steps: []scriptStep{
		{snap: apiSnap(1, "k", "v1")},
		{gate: gate, snap: apiSnap(2, "k", "v2")},
	}}
	c, err := New(src, WithFallbackFile(path))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	seen := make(chan string, 4)
	c.Watch("", func(g Group) { seen <- g.GetOr("k", "") })
	recv(t, seen, "initial callback")
	close(gate)
	recv(t, seen, "change callback")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s, err := readFallback(path); err == nil && s.revision == 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("cache not updated to revision 2; file contents: %s", data)
}

func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	src := &scriptedSource{steps: []scriptStep{{snap: apiSnap(1, "k", "v")}}}
	c, err := New(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if got := src.closes.Load(); got != 1 {
		t.Fatalf("Source.Close called %d times, want 1", got)
	}
}

func TestGetErrors(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	src := &scriptedSource{steps: []scriptStep{{snap: apiSnap(1, "n", "notanumber")}}}
	c, err := New(src)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if _, err := c.Get[string]("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key should return ErrNotFound; got %v", err)
	}
	if _, err := c.Get[int]("n"); !errors.Is(err, ErrDecode) {
		t.Fatalf("decode failure should return ErrDecode; got %v", err)
	}
	if got := c.GetOr("missing", 5); got != 5 {
		t.Fatalf("GetOr missing = %d", got)
	}
	if got := c.GetOr("n", 5); got != 5 {
		t.Fatalf("GetOr decode failure = %d", got)
	}
}

// GetOr infers its type parameter from the default, so call sites never
// write [T].
func TestGetOrInfersTypeFromDefault(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	src := &scriptedSource{steps: []scriptStep{{snap: apiSnap(1,
		"feature:debug", "true", "http:timeout", "2s", "n:workers", "8")}}}
	c, err := New(src)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if got := c.GetOr("feature:debug", false); got != true {
		t.Fatalf("bool = %v", got)
	}
	if got := c.GetOr("http:timeout", time.Second); got != 2*time.Second {
		t.Fatalf("duration = %v", got)
	}
	if got := c.GetOr("n:workers", 1); got != 8 {
		t.Fatalf("int = %v", got)
	}
	if got := c.GetOr("n:missing", 3); got != 3 {
		t.Fatalf("default = %v", got)
	}
}

func TestGroupRelativeKeys(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	src := &scriptedSource{steps: []scriptStep{{snap: apiSnap(1,
		"prompt_group:main", "m", "prompt_group:sub", "s", "feature:debug", "true")}}}
	c, err := New(src)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	g := c.Group("prompt_group:")
	if got := g.Keys(); !slices.Equal(got, []string{"main", "sub"}) {
		t.Fatalf("Keys should be relative; got %v", got)
	}
	if g.Len() != 2 {
		t.Fatalf("Len = %d", g.Len())
	}
	// What Keys returns must feed straight back into Get.
	for _, k := range g.Keys() {
		if _, err := g.Get[string](k); err != nil {
			t.Fatalf("Get(%q): %v", k, err)
		}
	}
	if v, ok := g.Raw("main"); !ok || v != "m" {
		t.Fatalf("Raw = %q %v", v, ok)
	}
}

// A pinned Group must remain self-consistent after the client refreshes.
func TestGroupIsSnapshotPinned(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	gate := make(chan struct{})
	src := &scriptedSource{steps: []scriptStep{
		{snap: apiSnap(1, "k", "v1")},
		{gate: gate, snap: apiSnap(2, "k", "v2")},
	}}
	c, err := New(src)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	pinned := c.Group("")
	seen := make(chan string, 4)
	c.Watch("", func(g Group) { seen <- g.GetOr("k", "") })
	recv(t, seen, "initial callback")
	close(gate)
	recv(t, seen, "change callback")

	if got := pinned.GetOr("k", ""); got != "v1" {
		t.Fatalf("old Group was mutated; got %q", got)
	}
	if got := c.Group("").GetOr("k", ""); got != "v2" {
		t.Fatalf("new Group should see the new value; got %q", got)
	}
}

// OnChange must stay silent until New returns a usable client handle.
// Watch supplies the initial value.
func TestOnChangeSilentOnColdStart(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	events := make(chan string, 8)
	src := &scriptedSource{steps: []scriptStep{{snap: apiSnap(1, "a:1", "x", "a:2", "y")}}}
	c, err := New(src,
		WithPrefixes("a:"),
		WithOnChange(func(prefix string, keys []string) { events <- prefix }))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	mustNotRecv(t, events, "OnChange during cold start")
}

func TestCacheSeededByEmptyFirstSnapshot(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// An empty initial snapshot must still seed or replace the fallback file;
	// otherwise the next outage can resurrect stale configuration.
	path := filepath.Join(t.TempDir(), "snap.json")
	src := &scriptedSource{steps: []scriptStep{{snap: apiSnap(7)}}}

	c, err := New(src, WithFallbackFile(path))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	s, err := readFallback(path)
	if err != nil {
		t.Fatalf("empty snapshot must also be persisted; readFallback: %v", err)
	}
	if s.revision != 7 {
		t.Fatalf("cache revision = %d, want 7", s.revision)
	}
	if len(s.byKey) != 0 {
		t.Fatalf("cache should contain an empty snapshot; got %d keys", len(s.byKey))
	}
}

func TestStaleCacheOverwrittenByEmptySnapshot(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// Once every config is deleted, the empty snapshot must overwrite the
	// old file on disk, or the next cold start revives them.
	path := filepath.Join(t.TempDir(), "snap.json")
	if err := writeFallback(path, newView(apiSnap(1, "gone", "old"))); err != nil {
		t.Fatal(err)
	}

	src := &scriptedSource{steps: []scriptStep{{snap: apiSnap(2)}}}
	c, err := New(src, WithFallbackFile(path))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	s, err := readFallback(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.byKey["gone"]; ok {
		t.Fatal("deleted key remains in cache and would be restored on cold start")
	}
	if s.revision != 2 {
		t.Fatalf("cache revision = %d, want 2", s.revision)
	}
}
