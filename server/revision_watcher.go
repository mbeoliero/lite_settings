package server

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// revisionWatcher broadcasts database watermark changes to long polls.
// Fixed polling keeps query volume independent of client count and requires
// no coordination between instances.
type revisionWatcher struct {
	mu      sync.Mutex
	rev     int64
	changed chan struct{} // closed and replaced when the watermark moves

	ready  bool   // at least one poll has succeeded
	dbErr  string // last poll error, empty when healthy
	closed bool

	stop chan struct{} // closed by Close, releasing all waiters at once
}

func newRevisionWatcher() *revisionWatcher {
	return &revisionWatcher{
		rev:     -1,
		changed: make(chan struct{}),
		stop:    make(chan struct{}),
	}
}

// Wait blocks until the watermark differs, ctx ends, or the watcher closes.
// It tests != rather than > because restores move revision backwards. A
// negative baseline always returns immediately, including before the first
// poll, so cold starts receive a full snapshot.
func (w *revisionWatcher) Wait(ctx context.Context, since int64) (int64, bool) {
	w.mu.Lock()
	if since < 0 || w.rev != since {
		rev := w.rev
		w.mu.Unlock()
		return rev, true
	}
	// Read rev and changed under one lock to avoid missing a close between them.
	ch := w.changed
	w.mu.Unlock()

	select {
	case <-ch:
		return w.current(), true
	case <-w.stop:
		return w.current(), false
	case <-ctx.Done():
		return w.current(), false
	}
}

func (w *revisionWatcher) current() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rev
}

// set records a new watermark and wakes every waiter.
func (w *revisionWatcher) set(rev int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ready, w.dbErr = true, ""
	if w.rev == rev {
		return
	}
	w.rev = rev
	close(w.changed)
	w.changed = make(chan struct{})
}

func (w *revisionWatcher) setErr(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dbErr = err.Error()
}

// health reports (ready, current watermark, last database error).
func (w *revisionWatcher) health() (bool, int64, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ready, w.rev, w.dbErr
}

// Close releases every waiter. Safe to call more than once.
func (w *revisionWatcher) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	close(w.stop)
}

// revisionFunc allows tests to replace the heartbeat query.
type revisionFunc func(context.Context) (int64, error)

// run polls until ctx ends.
// Query failures keep clients waiting instead of retrying an unavailable database.
func (w *revisionWatcher) run(ctx context.Context, interval time.Duration, rev revisionFunc, log *slog.Logger) {
	defer w.Close()

	poll := func() {
		// One stuck heartbeat must not stall later polls.
		qctx, cancel := context.WithTimeout(ctx, interval+5*time.Second)
		defer cancel()

		r, err := rev(qctx)
		if err != nil {
			if ctx.Err() == nil {
				w.setErr(err)
				log.Warn("failed to poll revision", "err", err)
			}
			return
		}
		w.set(r)
	}

	poll() // once up front, so startup does not wait for the first tick

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			poll()
		}
	}
}
