package lite

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// PollRequest describes one fetch without coupling Source implementations
// to positional parameters.
type PollRequest struct {
	// Since is the client's current revision. -1 means no baseline, in
	// which case Source must return a full snapshot immediately.
	Since int64
	// Prefixes to fetch. Empty, or containing "", means everything.
	Prefixes []string
	// Interval between revision checks in direct-DB mode.
	Interval time.Duration
	// Timeout bounds how long this fetch may hang.
	Timeout time.Duration
}

// Source provides snapshots through HTTP long polling or direct database access.
// It is the polymorphic layer because Client and Group require generic methods.
type Source interface {
	// Poll fetches a snapshot. (nil, nil) means the revision did not
	// change within the deadline: normal, not an error, no backoff.
	//
	// Implementations MUST read the revision before the data, so a
	// snapshot is never labelled higher than its true version. Labelling
	// low costs one poll cycle; labelling high skips a change forever.
	Poll(ctx context.Context, req PollRequest) (*Snapshot, error)
	Close() error
}

const (
	DefaultPollInterval    = time.Second
	DefaultLongPollTimeout = 30 * time.Second
	DefaultStartupTimeout  = 10 * time.Second

	// Keep the client deadline past the server's hang budget so its 304 can
	// arrive instead of degrading long polling into periodic reconnects.
	longPollGrace = 5 * time.Second

	minBackoff = 200 * time.Millisecond
	maxBackoff = 30 * time.Second
)

type options struct {
	prefixes        []string
	pollInterval    time.Duration
	longPollTimeout time.Duration
	startupTimeout  time.Duration
	fallbackPath    string
	strict          bool
	degradeOnStart  bool
	onChange        func(prefix string, keys []string)
	onError         func(error)
	log             *slog.Logger
}

// Option adjusts client behaviour.
type Option func(*options)

// WithPrefixes limits background fetches to these prefixes. Unset means everything.
func WithPrefixes(prefixes ...string) Option {
	return func(o *options) { o.prefixes = append(o.prefixes, prefixes...) }
}

// WithPollInterval sets the revision poll interval for direct-DB mode. Unused over HTTP.
func WithPollInterval(d time.Duration) Option {
	return func(o *options) { o.pollInterval = d }
}

// WithLongPollTimeout bounds how long one long poll may hang.
func WithLongPollTimeout(d time.Duration) Option {
	return func(o *options) { o.longPollTimeout = d }
}

// WithStartupTimeout bounds the synchronous first fetch in New.
func WithStartupTimeout(d time.Duration) Option {
	return func(o *options) { o.startupTimeout = d }
}

// WithFallbackFile persists snapshots for cold starts when the source is unreachable.
// The file is read only at startup after the first fetch fails; normal reads stay in memory.
func WithFallbackFile(path string) Option {
	return func(o *options) { o.fallbackPath = path }
}

// WithStrictDecode rejects fields absent from the target struct.
// It is opt-in because new fields would otherwise break older instances during rolling deploys.
func WithStrictDecode() Option {
	return func(o *options) { o.strict = true }
}

// WithStartupDegrade lets New use an empty snapshot when the first fetch fails.
// It is opt-in because every read must tolerate ErrNotFound or use GetOr until
// the background loop succeeds.
func WithStartupDegrade() Option {
	return func(o *options) { o.degradeOnStart = true }
}

// WithOnError reports every background fetch failure, for metrics and
// alerting. It runs on the refresh goroutine, so it must not block.
func WithOnError(fn func(error)) Option {
	return func(o *options) { o.onError = fn }
}

// WithOnChange reports changes as (prefix, full keys changed under it).
func WithOnChange(fn func(prefix string, keys []string)) Option {
	return func(o *options) { o.onChange = fn }
}

// WithLogger sets the logger. Discards by default.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) { o.log = l }
}

type watcher struct {
	prefix string
	fn     func(Group)
}

// Client holds an in-memory snapshot and keeps it in step with the
// source in the background.
//
// Reads are lock-free: the snapshot is immutable and swapped by pointer.
type Client struct {
	src Source
	opt options
	log *slog.Logger

	snap atomic.Pointer[view]

	seeded atomic.Bool

	// Separate atomics avoid locking between refresh and metrics goroutines;
	// the fields need no mutually consistent snapshot.
	lastSuccess     atomic.Int64 // UnixNano; 0 means never succeeded
	consecutiveFail atomic.Int64
	lastErr         atomic.Pointer[string]

	cancel context.CancelFunc
	done   chan struct{}

	// Serializes snapshot notification with watcher registration, preventing
	// concurrent or duplicate callbacks when a refresh lands during Watch.
	applyMu sync.Mutex

	mu       sync.Mutex
	watchers []watcher
	closed   bool
}

// New creates a client after a synchronous initial fetch.
// On failure it tries WithFallbackFile, then WithStartupDegrade, then fails.
func New(src Source, opts ...Option) (*Client, error) {
	o := options{
		pollInterval:    DefaultPollInterval,
		longPollTimeout: DefaultLongPollTimeout,
		startupTimeout:  DefaultStartupTimeout,
		log:             slog.New(slog.DiscardHandler),
	}
	for _, f := range opts {
		f(&o)
	}
	o.prefixes = normalizePrefixes(o.prefixes)
	o.log = cmp.Or(o.log, slog.New(slog.DiscardHandler)) // WithLogger(nil)

	c := &Client{src: src, opt: o, log: o.log, done: make(chan struct{})}
	c.snap.Store(emptyView())

	// New has not returned a usable client handle yet, so the first fetch must
	// not fire callbacks. Watch supplies initial values after registration.
	startCtx, cancelStart := context.WithTimeout(context.Background(), o.startupTimeout)
	err := c.fetchOnce(startCtx, false)
	cancelStart()
	if err == nil && c.hasSnapshot() {
		// Record the initial arrival so a healthy client does not report that it never fetched.
		c.markSuccess()
	}

	if err != nil || !c.hasSnapshot() {
		err = cmp.Or(err, ErrNoSnapshot)
		switch {
		case c.loadFallback(err):
		case o.degradeOnStart:
			c.log.Warn("initial config fetch failed; starting with an empty snapshot", "err", err)
		default:
			src.Close()
			return nil, fmt.Errorf("initial config fetch: %w", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go c.run(ctx)
	return c, nil
}

func (c *Client) loadFallback(cause error) bool {
	if c.opt.fallbackPath == "" {
		return false
	}
	s, lerr := readFallback(c.opt.fallbackPath)
	if lerr != nil {
		c.log.Warn("fallback file unavailable; cold-start fallback disabled", "path", c.opt.fallbackPath, "err", lerr)
		return false
	}
	c.snap.Store(s)
	c.log.Warn("config source unavailable; cold-starting from fallback file",
		"path", c.opt.fallbackPath, "revision", s.revision, "keys", len(s.keys), "cause", cause)
	return true
}

func (c *Client) hasSnapshot() bool { return c.snap.Load().revision >= 0 }

// Revision reports the current snapshot's revision, or -1 if there is none yet.
func (c *Client) Revision() int64 { return c.snap.Load().revision }

// Group returns the configs under prefix, bound to the current snapshot.
func (c *Client) Group(prefix string) Group {
	return Group{prefix: prefix, snap: c.snap.Load(), strict: c.opt.strict, log: c.log}
}

// Raw returns the raw text stored under key.
func (c *Client) Raw(key string) (string, bool) { return c.Group("").Raw(key) }

// Get decodes the value at key into T.
func (c *Client) Get[T any](key string) (T, error) { return c.Group("").Get[T](key) }

// GetOr decodes the value at key into T, returning def on failure.
// T is inferred from def, so no explicit type argument is needed.
func (c *Client) GetOr[T any](key string, def T) T { return c.Group("").GetOr(key, def) }

// Watch registers a callback for a prefix. It fires once immediately
// with the current snapshot, then on every subsequent change under that
// prefix, so callers need no separate initialization path.
//
// Callbacks run serially — the initial one on the calling goroutine,
// later ones on the refresh goroutine, never two at once: do not block,
// and do not call Close or Watch from inside one.
func (c *Client) Watch(prefix string, fn func(Group)) {
	// Held across both steps: see applyMu. A callback must therefore not
	// call Watch itself, the same way it must not call Close.
	c.applyMu.Lock()
	defer c.applyMu.Unlock()

	c.mu.Lock()
	c.watchers = append(c.watchers, watcher{prefix: prefix, fn: fn})
	c.mu.Unlock()

	c.safely("Watch", prefix, func() { fn(c.Group(prefix)) })
}

// Close stops the refresh loop and closes the source. Safe to call twice.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
	return c.src.Close()
}

func (c *Client) run(ctx context.Context) {
	defer close(c.done)

	backoff := minBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		pollCtx, cancel := context.WithTimeout(ctx, c.opt.longPollTimeout+longPollGrace)
		err := c.fetchOnce(pollCtx, true)
		// Read before cancel(), which would set pollCtx.Err() itself.
		ownDeadline := errors.Is(pollCtx.Err(), context.DeadlineExceeded)
		cancel()

		if ctx.Err() != nil {
			return
		}
		if err == nil || (ownDeadline && errors.Is(err, context.DeadlineExceeded)) {
			// Our deadline is normal reconnect rhythm. Other DeadlineExceeded errors
			// are source failures; unchanged is reported as (nil, nil).
			c.markSuccess()
			backoff = minBackoff
			continue
		}

		c.markFailure(err)
		wait := jitter(backoff)
		c.log.Warn("config fetch failed; retrying", "err", err, "retry_in", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// jitter spreads backoff by ±25%.
//
// Without it, a fleet that started together reconnects in the same
// instant the source recovers and knocks it back down — and the longer
// the backoff, the tighter that fleet's lockstep becomes.
func jitter(d time.Duration) time.Duration {
	spread := int64(d) / 2
	if spread <= 0 {
		return d
	}
	return d - time.Duration(spread/2) + time.Duration(rand.N(spread))
}

func (c *Client) markSuccess() {
	c.lastSuccess.Store(time.Now().UnixNano())
	c.consecutiveFail.Store(0)
	c.lastErr.Store(nil)
}

func (c *Client) markFailure(err error) {
	c.consecutiveFail.Add(1)
	c.lastErr.Store(new(err.Error()))
	if c.opt.onError != nil {
		c.safely("OnError", "", func() { c.opt.onError(err) })
	}
}

// Stats is a point-in-time view of fetch health, for metrics and alerting.
type Stats struct {
	// Revision of the current snapshot; -1 if there is none yet.
	Revision int64
	// LastSuccess is when the last fetch succeeded; zero if never.
	LastSuccess time.Time
	// ConsecutiveFail resets to zero on any success.
	ConsecutiveFail int64
	// LastErr is the last failure's text, cleared on success.
	LastErr string
}

// Stats reports current fetch health.
//
// Reads succeeding is not enough of a signal: once the source is gone
// the client keeps serving its last snapshot silently, and nothing on
// the read path looks wrong. Export time.Since(LastSuccess) as a metric
// to catch configuration that has stopped updating.
func (c *Client) Stats() Stats {
	st := Stats{
		Revision:        c.snap.Load().revision,
		ConsecutiveFail: c.consecutiveFail.Load(),
	}
	if ns := c.lastSuccess.Load(); ns != 0 {
		st.LastSuccess = time.Unix(0, ns)
	}
	if p := c.lastErr.Load(); p != nil {
		st.LastErr = *p
	}
	return st
}

func (c *Client) fetchOnce(ctx context.Context, notify bool) error {
	s, err := c.src.Poll(ctx, PollRequest{
		Since:    c.snap.Load().revision,
		Prefixes: c.opt.prefixes,
		Interval: c.opt.pollInterval,
		Timeout:  c.opt.longPollTimeout,
	})
	if err != nil {
		return err
	}
	if s == nil {
		return nil // unchanged
	}
	c.apply(s, notify)
	return nil
}

// apply installs a new snapshot and notifies subscribers.
//
// It does not compare revisions: every fetch is a full snapshot, so a
// revision going backwards (a database restored from backup) is handled
// correctly by plain replacement.
func (c *Client) apply(s *Snapshot, notify bool) {
	c.applyMu.Lock()
	defer c.applyMu.Unlock()

	next := newView(s)
	prev := c.snap.Swap(next)

	changed := diff(prev, next)

	// Persist before returning on an empty diff so empty snapshots replace stale
	// fallback data. Mark seeded only after a successful write so failures retry.
	if c.opt.fallbackPath != "" && (len(changed) > 0 || !c.seeded.Load()) {
		if err := writeFallback(c.opt.fallbackPath, next); err != nil {
			c.log.Warn("failed to write fallback file", "path", c.opt.fallbackPath, "err", err)
		} else {
			c.seeded.Store(true)
		}
	}

	if len(changed) == 0 {
		return // revision moved, content did not
	}
	c.log.Info("config updated", "revision", next.revision, "changed", len(changed))

	if notify {
		c.notify(next, changed)
	}
}

func (c *Client) notify(s *view, changed []string) {
	c.mu.Lock()
	ws := slices.Clone(c.watchers)
	c.mu.Unlock()

	for _, w := range ws {
		sub := filterPrefix(changed, w.prefix)
		if len(sub) == 0 {
			continue
		}
		g := Group{prefix: w.prefix, snap: s, strict: c.opt.strict, log: c.log}
		c.safely("Watch", w.prefix, func() { w.fn(g) })
	}

	if c.opt.onChange == nil {
		return
	}
	for _, p := range c.notifyPrefixes() {
		sub := filterPrefix(changed, p)
		if len(sub) == 0 {
			continue
		}
		c.safely("OnChange", p, func() { c.opt.onChange(p, sub) })
	}
}

// safely contains panics from caller callbacks. They run on the single
// refresh goroutine, so an escaping panic would freeze all configuration
// updates for the process.
func (c *Client) safely(kind, prefix string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("config callback panicked; ignored", "kind", kind, "prefix", prefix, "panic", r)
		}
	}()
	fn()
}

func (c *Client) notifyPrefixes() []string {
	if len(c.opt.prefixes) == 0 {
		return []string{""}
	}
	return c.opt.prefixes
}

func filterPrefix(keys []string, prefix string) []string {
	if prefix == "" {
		return keys
	}
	var out []string
	for _, k := range keys {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out
}

// normalizePrefixes deduplicates; an empty prefix means everything, which
// makes the rest redundant.
func normalizePrefixes(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, p := range in {
		if p == "" {
			return nil
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
