// Package httpsource reads configuration by long polling the config server,
// keeping application instances away from the database.
package httpsource

import (
	"context"
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	lite "github.com/mbeoliero/lite_settings/client"
)

// maxBodySize caps one snapshot response, so a misbehaving server cannot
// exhaust client memory.
const maxBodySize = 64 << 20

// pollGrace keeps the fallback deadline past the server's hang budget.
const pollGrace = 5 * time.Second

type source struct {
	base string
	hc   *http.Client
	own  bool
}

// Option adjusts the HTTP source.
type Option func(*source)

// WithHTTPClient supplies a custom http.Client.
// Its Timeout must remain unset because long-poll bounds come from
// lite.WithLongPollTimeout through the request context.
func WithHTTPClient(hc *http.Client) Option {
	return func(s *source) { s.hc, s.own = hc, false }
}

// New creates a long-polling source. baseURL looks like http://cfg:8080.
func New(baseURL string, opts ...Option) lite.Source {
	s := &source{base: strings.TrimRight(baseURL, "/"), hc: newPollClient(), own: true}
	for _, f := range opts {
		f(s)
	}
	return s
}

// newPollClient leaves client and response-header timeouts unset because
// long-poll deadlines come from the context. Keep-alive probes surface
// half-open connections before that deadline.
func newPollClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConnsPerHost = 4
	t.DialContext = (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 15 * time.Second,
	}).DialContext
	return &http.Client{Transport: t}
}

// withPollDeadline bounds a poll that would otherwise hang forever: this
// client sets no timeout of its own, and a third party calling Poll directly
// may pass no deadline. It only fills that gap — shortening a caller's own
// deadline would degrade long polling into periodic reconnects.
func withPollDeadline(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok || timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout+pollGrace)
}

func (s *source) Poll(ctx context.Context, req lite.PollRequest) (*lite.Snapshot, error) {
	ctx, cancel := withPollDeadline(ctx, req.Timeout)
	defer cancel()

	q := url.Values{}
	// A negative Since means no baseline: omit the parameter and the
	// server returns a full snapshot immediately.
	if req.Since >= 0 {
		q.Set("revision", strconv.FormatInt(req.Since, 10))
	}
	for _, p := range req.Prefixes {
		q.Add("prefix", p)
	}

	u := s.base + "/v1/watch"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	hreq.Header.Set("Accept", "application/json")

	resp, err := s.hc.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", u, err)
	}
	defer func() {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		var snap lite.Snapshot
		if err := jsonv2.UnmarshalRead(io.LimitReader(resp.Body, maxBodySize), &snap); err != nil {
			return nil, fmt.Errorf("decode snapshot: %w", err)
		}
		return &snap, nil

	case http.StatusNotModified:
		return nil, nil // hung to the deadline, unchanged

	default:
		return nil, fmt.Errorf("config service returned %s: %s", resp.Status, errorBody(resp.Body))
	}
}

func (s *source) Close() error {
	if s.own {
		s.hc.CloseIdleConnections()
	}
	return nil
}

func errorBody(r io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(r, 4<<10))
	if err != nil || len(data) == 0 {
		return "(empty response body)"
	}
	var e struct {
		Error string `json:"error"`
	}
	if jsonv2.Unmarshal(data, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(data))
}
