package server

import (
	"cmp"
	"context"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/mbeoliero/lite_settings/server/api"
	"github.com/mbeoliero/lite_settings/store"
)

const (
	defaultHistoryLimit = 100
	defaultAuthor       = "anonymous"

	// bodyReadTimeout stops slowloris writes without limiting long polls.
	bodyReadTimeout = 30 * time.Second
)

// timeNow allows deterministic tests.
var timeNow = time.Now

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	// ValidateKey excludes '/', keeping keys within one path segment.
	mux.HandleFunc("GET /v1/configs", s.handleList)
	mux.HandleFunc("GET /v1/configs/{key}", s.handleGet)
	mux.HandleFunc("PUT /v1/configs/{key}", s.handleSet)
	mux.HandleFunc("DELETE /v1/configs/{key}", s.handleDelete)
	mux.HandleFunc("GET /v1/configs/{key}/history", s.handleHistory)
	mux.HandleFunc("POST /v1/configs/{key}/rollback", s.handleRollback)
	mux.HandleFunc("GET /v1/watch", s.handleWatch)
	mux.HandleFunc("GET /v1/revision", s.handleRevision)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	return mux
}

// handleList serves a full prefix snapshot; no prefix means everything.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	// Watermark before data; reversing this can permanently skip a change.
	rev, err := s.observedRevision(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	ps, err := prefixes(r)
	if err != nil {
		s.errorf(w, r, http.StatusBadRequest, "%v", err)
		return
	}
	snap, err := s.snapshot(r.Context(), rev, ps)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, r, http.StatusOK, snap)
}

// handleWatch long-polls until the watermark differs, then returns a full
// snapshot. No baseline returns immediately; timeout returns 304.
func (s *Server) handleWatch(w http.ResponseWriter, r *http.Request) {
	since := int64(-1)
	if v := r.URL.Query().Get("revision"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			s.errorf(w, r, http.StatusBadRequest, "revision is not a valid integer: %q", v)
			return
		}
		since = n
	}

	ps, err := prefixes(r)
	if err != nil {
		s.errorf(w, r, http.StatusBadRequest, "%v", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.longPollTimeout)
	defer cancel()

	rev, changed := s.watcher.Wait(ctx, since)
	if !changed {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	// observedRevision keeps all endpoints aligned during the pre-poll fallback.
	if rev < 0 {
		rev, err = s.observedRevision(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return
		}
	}

	// A trailing watcher labels data low, never dangerously high.
	snap, err := s.snapshot(r.Context(), rev, ps)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, r, http.StatusOK, snap)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.store.Get(r.Context(), r.PathValue("key"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, r, http.StatusOK, api.ConfigDetail{
		Format:    string(cfg.Format),
		Key:       cfg.Key,
		UpdatedAt: cfg.UpdatedAt,
		UpdatedBy: cfg.UpdatedBy,
		Value:     cfg.Value,
	})
}

// handleSet publishes an unwrapped raw value.
func (s *Server) handleSet(w http.ResponseWriter, r *http.Request) {
	format, err := parseFormat(r.URL.Query().Get("format"))
	if err != nil {
		s.errorf(w, r, http.StatusBadRequest, "%v", err)
		return
	}

	value, err := s.readBody(w, r, store.MaxValueSize)
	if err != nil {
		return // readBody already wrote the response
	}

	res, err := s.store.Set(r.Context(), r.PathValue("key"), string(value), format, change(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeResult(w, r, r.PathValue("key"), res)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	res, err := s.store.Delete(r.Context(), key, change(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeResult(w, r, key, res)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	limit := defaultHistoryLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			s.errorf(w, r, http.StatusBadRequest, "limit is not a valid integer: %q", v)
			return
		}
		limit = n
	}

	entries, err := s.store.History(r.Context(), r.PathValue("key"), limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Hard-deleted keys retain history, so only empty history is not found.
	if len(entries) == 0 {
		s.fail(w, r, store.ErrNotFound)
		return
	}

	out := make([]api.HistoryEntry, len(entries))
	for i, e := range entries {
		out[i] = api.HistoryEntry{
			Version:   e.Version,
			Value:     e.Value,
			Format:    string(e.Format),
			Op:        string(e.Op),
			Comment:   e.Comment,
			CreatedAt: e.CreatedAt,
			CreatedBy: e.CreatedBy,
		}
	}
	s.write(w, r, http.StatusOK, out)
}

func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	body, err := s.readBody(w, r, 1<<12)
	if err != nil {
		return // readBody already wrote the response
	}
	var req api.RollbackRequest
	if err := jsonv2.Unmarshal(body, &req); err != nil {
		s.errorf(w, r, http.StatusBadRequest, "request body is not valid JSON: %v", err)
		return
	}
	if req.Version <= 0 {
		s.errorf(w, r, http.StatusBadRequest, "version must be a positive integer")
		return
	}

	key := r.PathValue("key")
	res, err := s.store.Rollback(r.Context(), key, req.Version, change(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeResult(w, r, key, res)
}

func (s *Server) handleRevision(w http.ResponseWriter, r *http.Request) {
	rev, err := s.observedRevision(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.write(w, r, http.StatusOK, api.RevisionResponse{Revision: rev})
}

// handleHealth returns 503 until the watcher's first successful poll.
// Later database blips stay in the body so instances keep long polls and
// clients can serve cached configuration.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ready, rev, dbErr := s.watcher.health()
	code := http.StatusOK
	if !ready {
		code = http.StatusServiceUnavailable
	}
	s.write(w, r, code, api.Health{OK: ready, Revision: rev, DBError: dbErr})
}

// observedRevision returns the watcher watermark visible to clients.
// A direct database value could disagree with /v1/watch and force a useless
// poll. A trailing watcher labels data safely low; a high label can skip a
// change forever. Before the first poll it falls back to the database while
// /healthz remains not ready.
func (s *Server) observedRevision(ctx context.Context) (int64, error) {
	if ready, rev, _ := s.watcher.health(); ready {
		return rev, nil
	}
	return s.store.Revision(ctx)
}

// snapshot collects a consistent full-prefix snapshot in one query.
// It reads the database watermark before data and labels the snapshot with
// min(watcher, database). Normally that is the watcher value. After a restore,
// min prevents the old high watcher value from labelling rolled-back data and
// making clients skip it permanently. The extra query is a single-row read.
func (s *Server) snapshot(ctx context.Context, rev int64, ps []string) (api.Snapshot, error) {
	dbRev, err := s.store.Revision(ctx)
	if err != nil {
		return api.Snapshot{}, err
	}
	rev = min(rev, dbRev)

	cfgs, err := s.store.ListPrefixes(ctx, ps)
	if err != nil {
		return api.Snapshot{}, err
	}
	out := make([]api.Config, 0, len(cfgs))
	for _, c := range cfgs {
		out = append(out, api.Config{Key: c.Key, Value: c.Value, Format: string(c.Format)})
	}
	return api.Snapshot{Revision: rev, Configs: out}, nil
}

// maxPrefixes prevents URL length from becoming unbounded query work.
const maxPrefixes = 64

// prefixes deduplicates and bounds prefixes before they become SQL OR branches.
// An empty prefix subsumes the rest.
func prefixes(r *http.Request) ([]string, error) {
	ps := r.URL.Query()["prefix"]
	if len(ps) == 0 {
		return []string{""}, nil // no prefix means everything
	}
	if len(ps) > maxPrefixes {
		return nil, fmt.Errorf("at most %d prefixes are allowed; got %d", maxPrefixes, len(ps))
	}

	seen := make(map[string]bool, len(ps))
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		if p == "" {
			return []string{""}, nil
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out, nil
}

// readBody enforces the exact size limit so limit+1 consistently returns 413.
// A per-body deadline stops slowloris writes without cutting off long polls.
func (s *Server) readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(timeNow().Add(bodyReadTimeout)); err != nil &&
		!errors.Is(err, http.ErrNotSupported) {
		s.log.Warn("failed to set request body read deadline", "path", r.URL.Path, "err", err)
	}

	r.Body = http.MaxBytesReader(w, r.Body, limit)
	body, err := io.ReadAll(r.Body)
	if err == nil {
		return body, nil
	}
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		s.errorf(w, r, http.StatusRequestEntityTooLarge, "request body exceeds the %d-byte limit", limit)
		return nil, err
	}
	s.errorf(w, r, http.StatusBadRequest, "failed to read request body: %v", err)
	return nil, err
}

func change(r *http.Request) store.Change {
	q := r.URL.Query()
	return store.Change{Comment: q.Get("comment"), Author: cmp.Or(q.Get("author"), defaultAuthor)}
}

func parseFormat(v string) (store.Format, error) {
	switch store.Format(v) {
	case "":
		return store.FormatRaw, nil
	case store.FormatRaw, store.FormatJSON, store.FormatYAML:
		return store.Format(v), nil
	}
	return "", fmt.Errorf("format must be raw, json, or yaml; got %q", v)
}

func (s *Server) writeResult(w http.ResponseWriter, r *http.Request, key string, res store.Result) {
	s.write(w, r, http.StatusOK, api.WriteResult{Key: key, Version: res.Version, Revision: res.Revision})
}

func (s *Server) write(w http.ResponseWriter, r *http.Request, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := jsonv2.MarshalWrite(w, v); err != nil {
		// Headers are committed; only logging remains possible.
		s.log.Error("failed to encode response", "path", r.URL.Path, "err", err)
	}
}

func (s *Server) errorf(w http.ResponseWriter, r *http.Request, code int, format string, args ...any) {
	s.write(w, r, code, api.ErrorResponse{Error: fmt.Sprintf(format, args...)})
}

// fail maps store errors onto HTTP status codes.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.errorf(w, r, http.StatusNotFound, "%v", err)
	case errors.Is(err, store.ErrInvalidKey), errors.Is(err, store.ErrInvalidValue),
		errors.Is(err, store.ErrInvalidChange):
		s.errorf(w, r, http.StatusBadRequest, "%v", err)
	case errors.Is(err, store.ErrNotMigrated):
		s.errorf(w, r, http.StatusServiceUnavailable, "%v", err)
	case errors.Is(err, context.Canceled):
		// Client cancellation needs neither a server error nor a reply.
		return
	default:
		s.log.Error("request failed", "path", r.URL.Path, "err", err)
		s.errorf(w, r, http.StatusInternalServerError, "internal error")
	}
}
