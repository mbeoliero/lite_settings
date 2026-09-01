package lite

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStartupDegradeStartsWithEmptySnapshot(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	boom := errors.New("config source unavailable")
	src := &scriptedSource{steps: []scriptStep{{err: boom}}}

	if _, err := New(src); err == nil {
		t.Fatal("default startup must fail fast instead of using an empty config")
	}

	src = &scriptedSource{steps: []scriptStep{{err: boom}}}
	c, err := New(src, WithStartupDegrade())
	if err != nil {
		t.Fatalf("degraded startup should succeed: %v", err)
	}
	defer c.Close()

	if got := c.Revision(); got != -1 {
		t.Errorf("degraded startup revision = %d, want -1", got)
	}
	if _, err := c.Get[string]("any:key"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get on an empty snapshot should return ErrNotFound; got %v", err)
	}
	if got := c.GetOr("any:key", "default"); got != "default" {
		t.Errorf("GetOr should return the code default; got %q", got)
	}
}

func TestStatsTracksSuccessAndFailure(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	boom := errors.New("fetch failed")
	gate := make(chan struct{})
	src := &scriptedSource{steps: []scriptStep{
		{snap: apiSnap(7, "a:x", "1")},
		{err: boom},
		{gate: gate}, // blocks, so the assertion reliably sees the previous failure
	}}

	// Synchronize the refresh callback with the assertion.
	var mu sync.Mutex
	var seen []error
	c, err := New(src, WithOnError(func(e error) {
		mu.Lock()
		seen = append(seen, e)
		mu.Unlock()
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { close(gate); c.Close() }()

	st := c.Stats()
	if st.Revision != 7 {
		t.Errorf("Revision = %d, want 7", st.Revision)
	}
	// The synchronous initial fetch must already count as a success.
	if st.ConsecutiveFail != 0 {
		t.Errorf("ConsecutiveFail = %d, want 0", st.ConsecutiveFail)
	}

	waitFor(t, func() bool { return c.Stats().ConsecutiveFail >= 1 })

	st = c.Stats()
	if !strings.Contains(st.LastErr, "fetch failed") {
		t.Errorf("LastErr = %q, should include the failure cause", st.LastErr)
	}
	mu.Lock()
	got := slices.Clone(seen)
	mu.Unlock()
	if len(got) == 0 || !errors.Is(got[0], boom) {
		t.Errorf("WithOnError should receive the error; got %v", got)
	}
}

func TestStatsClearsFailureAfterRecovery(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	src := &scriptedSource{steps: []scriptStep{
		{snap: apiSnap(1, "a:x", "1")},
		{err: errors.New("temporary failure")},
		{snap: apiSnap(2, "a:x", "2")},
	}}
	c, err := New(src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	waitFor(t, func() bool { return c.Revision() == 2 })
	waitFor(t, func() bool { return c.Stats().ConsecutiveFail == 0 })

	st := c.Stats()
	if st.LastErr != "" {
		t.Errorf("LastErr should be empty after recovery; got %q", st.LastErr)
	}
	if st.LastSuccess.IsZero() {
		t.Error("LastSuccess should be set after recovery")
	}
}

func TestJitterStaysWithinBand(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// Keep jitter within the documented ±25% bound.
	const base = time.Second
	lo, hi := base*3/4, base*5/4
	seen := map[time.Duration]bool{}
	for range 200 {
		d := jitter(base)
		if d < lo || d > hi {
			t.Fatalf("jitter(%v) = %v, outside [%v, %v]", base, d, lo, hi)
		}
		seen[d] = true
	}
	if len(seen) < 50 {
		t.Errorf("200 calls produced only %d distinct values", len(seen))
	}
	// Integer rounding must not collapse tiny backoffs to zero.
	if got := jitter(1); got != 1 {
		t.Errorf("jitter(1) = %v, want 1", got)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}
