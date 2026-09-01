package server

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func discardLog() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// blocked asserts that Wait is currently hanging.
func blocked(t *testing.T, done <-chan int64) {
	t.Helper()
	select {
	case rev := <-done:
		t.Fatalf("Wait returned unexpectedly with rev=%d", rev)
	case <-time.After(80 * time.Millisecond):
	}
}

func awaited(t *testing.T, done <-chan int64) int64 {
	t.Helper()
	select {
	case rev := <-done:
		return rev
	case <-time.After(3 * time.Second):
		t.Fatal("Wait did not return before timeout")
		return 0
	}
}

func TestRevisionWatcherWaitReturnsWhenRevisionDiffers(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	b := newRevisionWatcher()
	b.set(7)

	rev, changed := b.Wait(t.Context(), 5)
	if !changed || rev != 7 {
		t.Errorf("Wait = (%d, %v), want (7, true)", rev, changed)
	}
}

// Missing baselines must return immediately, even at cold start.
func TestRevisionWatcherWaitColdStart(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	b := newRevisionWatcher()
	b.set(0) // empty database, watermark 0

	rev, changed := b.Wait(t.Context(), -1)
	if !changed || rev != 0 {
		t.Errorf("cold-start Wait = (%d, %v), want (0, true)", rev, changed)
	}
}

func TestRevisionWatcherWaitBlocksThenWakes(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	b := newRevisionWatcher()
	b.set(5)

	done := make(chan int64, 1)
	go func() {
		rev, changed := b.Wait(t.Context(), 5)
		if !changed {
			t.Error("changed = false after wake-up by set, want true")
		}
		done <- rev
	}()

	blocked(t, done)
	b.set(6)
	if rev := awaited(t, done); rev != 6 {
		t.Errorf("rev after wake-up = %d, want 6", rev)
	}
}

// Unchanged polls must not force client refetches.
func TestRevisionWatcherSetSameRevisionDoesNotWake(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	b := newRevisionWatcher()
	b.set(5)

	done := make(chan int64, 1)
	go func() {
		rev, _ := b.Wait(t.Context(), 5)
		done <- rev
	}()

	blocked(t, done)
	b.set(5)
	b.set(5)
	blocked(t, done)

	b.Close()
	awaited(t, done)
}

// Restores move revision backwards, so Wait must use != rather than >.
func TestRevisionWatcherWaitWakesOnRevisionGoingBackward(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	b := newRevisionWatcher()
	b.set(100)

	done := make(chan int64, 1)
	go func() {
		rev, changed := b.Wait(t.Context(), 100)
		if !changed {
			t.Error("changed = false after revision rollback, want true")
		}
		done <- rev
	}()

	blocked(t, done)
	b.set(3)
	if rev := awaited(t, done); rev != 3 {
		t.Errorf("rev after rollback = %d, want 3", rev)
	}
}

func TestRevisionWatcherWaitTimeout(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	b := newRevisionWatcher()
	b.set(5)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	rev, changed := b.Wait(ctx, 5)
	if changed {
		t.Error("changed = true after timeout, want false")
	}
	if rev != 5 {
		t.Errorf("rev after timeout = %d, want 5", rev)
	}
}

// Close must release waiters so shutdown does not await long-poll timeouts.
func TestRevisionWatcherCloseReleasesWaiters(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	b := newRevisionWatcher()
	b.set(5)

	const n = 20
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			if _, changed := b.Wait(t.Context(), 5); changed {
				t.Error("changed = true after Close, want false")
			}
		})
	}

	time.Sleep(50 * time.Millisecond)
	b.Close()
	b.Close() // calling twice must be safe

	waitAll(t, &wg)
}

// Stress the Wait/set race: rev and changed must share a lock or a close can
// be lost between unlock and select. Run with -race.
func TestRevisionWatcherNoLostWakeup(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	b := newRevisionWatcher()
	b.set(0)

	const waiters = 50
	var wg sync.WaitGroup
	for range waiters {
		wg.Go(func() {

			var since int64
			for since < 100 {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				rev, changed := b.Wait(ctx, since)
				cancel()
				if !changed {
					t.Errorf("lost wake-up at since=%d", since)
					return
				}
				since = rev
			}
		})
	}

	go func() {
		for i := int64(1); i <= 100; i++ {
			b.set(i)
		}
	}()

	waitAll(t, &wg)
}

// waitReady cannot use Wait(ctx, -1), which immediately means no baseline.
func waitReady(t *testing.T, w *revisionWatcher) int64 {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ok, rev, _ := w.health(); ok {
			return rev
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("watcher did not complete its first poll before the deadline")
	return 0
}

func waitAll(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("not all waiters returned before the deadline")
	}
}

func TestRevisionWatcherRunPolls(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	var cur atomic.Int64
	cur.Store(42)

	b := newRevisionWatcher()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go b.run(ctx, 10*time.Millisecond, func(context.Context) (int64, error) {
		return cur.Load(), nil
	}, discardLog())

	// The first poll must run before the initial ticker delay.
	if rev := waitReady(t, b); rev != 42 {
		t.Fatalf("revision after first poll = %d, want 42", rev)
	}

	cur.Store(43)
	if rev, changed := b.Wait(ctx, 42); !changed || rev != 43 {
		t.Errorf("Wait after revision change = (%d, %v), want (43, true)", rev, changed)
	}
}

// Database outages must not make clients retry the same unavailable database.
func TestRevisionWatcherRunSurvivesQueryErrors(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	var calls atomic.Int64
	rev := func(context.Context) (int64, error) {
		if calls.Add(1) <= 3 {
			return 0, errors.New("database failed")
		}
		return 9, nil
	}

	b := newRevisionWatcher()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go b.run(ctx, 10*time.Millisecond, rev, discardLog())

	if got := waitReady(t, b); got != 9 {
		t.Errorf("revision after recovery = %d, want 9", got)
	}
	if ok, _, _ := b.health(); !ok {
		t.Error("watcher is not ready after a successful poll")
	}
}

// Readiness must wait for the first successful poll.
func TestRevisionWatcherHealthNotReadyBeforeFirstPoll(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	b := newRevisionWatcher()
	if ok, _, _ := b.health(); ok {
		t.Error("watcher is ready before its first poll")
	}

	b.setErr(errors.New("connection failed"))
	ok, _, dbErr := b.health()
	if ok {
		t.Error("watcher is ready after a failed first poll")
	}
	if dbErr != "connection failed" {
		t.Errorf("db_error = %q, want connection failed", dbErr)
	}

	b.set(1)
	if ok, rev, dbErr := b.health(); !ok || rev != 1 || dbErr != "" {
		t.Errorf("health after successful poll = (%v, %d, %q), want (true, 1, \"\")", ok, rev, dbErr)
	}
}

// Poller exit must release hanging waiters.
func TestRevisionWatcherRunClosesOnContextCancel(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	b := newRevisionWatcher()
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan struct{})
	go func() {
		defer close(done)
		b.run(ctx, 10*time.Millisecond, func(context.Context) (int64, error) { return 1, nil }, discardLog())
	}()

	// Establish a real watcher baseline before waiting.
	if rev := waitReady(t, b); rev != 1 {
		t.Fatalf("revision after first poll = %d, want 1", rev)
	}

	waiter := make(chan int64, 1)
	go func() {
		rev, _ := b.Wait(t.Context(), 1)
		waiter <- rev
	}()

	blocked(t, waiter)
	cancel()
	awaited(t, waiter)
	<-done
}
