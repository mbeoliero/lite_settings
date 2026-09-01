// Package server exposes store over HTTP with long polling.
package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mbeoliero/lite_settings/store"
)

const (
	DefaultAddr            = ":8080"
	DefaultLongPollTimeout = 30 * time.Second
	DefaultPollInterval    = time.Second
)

// Options configures New. Everything but Store may be left zero.
type Options struct {
	Addr            string
	Logger          *slog.Logger
	LongPollTimeout time.Duration
	PollInterval    time.Duration
	Store           *store.DB
}

// Server is the HTTP service.
type Server struct {
	addr            string
	log             *slog.Logger
	longPollTimeout time.Duration
	mux             *http.ServeMux
	pollInterval    time.Duration
	store           *store.DB
	watcher         *revisionWatcher
}

// New builds a stopped Server; Run or Start begins polling.
func New(opts Options) (*Server, error) {
	if opts.Store == nil {
		return nil, errors.New("server: Store must not be nil")
	}
	// cmp.Or only replaces zero; reject negatives before time.NewTicker panics.
	if opts.PollInterval < 0 {
		return nil, fmt.Errorf("server: PollInterval must not be negative; got %v", opts.PollInterval)
	}
	if opts.LongPollTimeout < 0 {
		return nil, fmt.Errorf("server: LongPollTimeout must not be negative; got %v", opts.LongPollTimeout)
	}
	s := &Server{
		addr:            cmp.Or(opts.Addr, DefaultAddr),
		log:             cmp.Or(opts.Logger, slog.New(slog.DiscardHandler)),
		longPollTimeout: cmp.Or(opts.LongPollTimeout, DefaultLongPollTimeout),
		pollInterval:    cmp.Or(opts.PollInterval, DefaultPollInterval),
		store:           opts.Store,
		watcher:         newRevisionWatcher(),
	}
	s.mux = s.routes()
	return s, nil
}

// Handler returns routes for another HTTP stack; call Start to run watches.
func (s *Server) Handler() http.Handler { return s.mux }

// Run starts polling and listening, blocking until ctx ends or the
// listener fails.
func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stopWatcher := s.Start(ctx)

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		stopWatcher()
		return fmt.Errorf("listen on %s: %w", s.addr, err)
	}

	hs := &http.Server{
		BaseContext: func(net.Listener) context.Context { return ctx },
		Handler:     s.mux,
		// WriteTimeout would cut off intentional long polls.
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("lite_settings started",
			"addr", ln.Addr().String(),
			"dialect", s.store.Dialect().Name(),
			"poll_interval", s.pollInterval,
			"long_poll_timeout", s.longPollTimeout)
		errCh <- hs.Serve(ln)
	}()

	select {
	case err := <-errCh:
		stopWatcher()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	// Stop watches first so long polls do not delay graceful shutdown.
	stopWatcher()

	sctx, scancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer scancel()
	err = hs.Shutdown(sctx)
	s.log.Info("lite_settings stopped")
	return err
}

// Start polls without listening, for serving Handler in another HTTP stack.
// Its idempotent stop releases long polls before waiting for the poller.
func (s *Server) Start(ctx context.Context) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.watcher.run(ctx, s.pollInterval, s.store.Revision, s.log)
	}()

	stopOnce := sync.OnceFunc(func() {
		// Release long polls before waiting for the poller.
		s.watcher.Close()
		cancel()
	})
	return func() {
		stopOnce()
		<-done
	}
}
