package backend

import (
	"bytes"
	"context"
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mbeoliero/lite_settings/server/api"
)

// maxErrBody limits unexpectedly large proxy error pages.
const maxErrBody = 4 << 10

type httpBackend struct {
	base string
	hc   *http.Client
}

func OpenHTTP(baseURL string, hc *http.Client) Backend {
	return &httpBackend{base: strings.TrimRight(baseURL, "/"), hc: hc}
}

func (b *httpBackend) Describe() string { return b.base }
func (b *httpBackend) Close() error     { b.hc.CloseIdleConnections(); return nil }

// Migrate is unavailable over HTTP: creating tables needs the database.
func (b *httpBackend) Migrate(context.Context) error {
	return fmt.Errorf("%w: schema migration requires a direct database connection; use lsctl migrate --dsn ... "+
		"or start the server with --migrate", ErrNotSupported)
}

func (b *httpBackend) Get(ctx context.Context, key string) (api.ConfigDetail, error) {
	var out api.ConfigDetail
	err := b.do(ctx, http.MethodGet, "/v1/configs/"+url.PathEscape(key), nil, "", &out)
	return out, err
}

func (b *httpBackend) List(ctx context.Context, prefixes []string) ([]api.Config, error) {
	q := url.Values{}
	for _, p := range prefixes {
		q.Add("prefix", p)
	}
	path := "/v1/configs"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	var snap api.Snapshot
	if err := b.do(ctx, http.MethodGet, path, nil, "", &snap); err != nil {
		return nil, err
	}
	return snap.Configs, nil
}

func (b *httpBackend) Set(ctx context.Context, key, value, format string, c Change) (api.WriteResult, error) {
	q := url.Values{}
	if format != "" {
		q.Set("format", format)
	}
	addChange(q, c)

	var out api.WriteResult
	// The API expects the raw value rather than a JSON wrapper.
	err := b.do(ctx, http.MethodPut,
		"/v1/configs/"+url.PathEscape(key)+"?"+q.Encode(),
		strings.NewReader(value), "text/plain; charset=utf-8", &out)
	return out, err
}

func (b *httpBackend) Delete(ctx context.Context, key string, c Change) (api.WriteResult, error) {
	q := url.Values{}
	addChange(q, c)

	var out api.WriteResult
	err := b.do(ctx, http.MethodDelete,
		"/v1/configs/"+url.PathEscape(key)+"?"+q.Encode(), nil, "", &out)
	return out, err
}

func (b *httpBackend) History(ctx context.Context, key string, limit int) ([]api.HistoryEntry, error) {
	path := "/v1/configs/" + url.PathEscape(key) + "/history"
	// Negative means unlimited; zero preserves the server default.
	switch {
	case limit > 0:
		path += "?limit=" + strconv.Itoa(limit)
	case limit < 0:
		path += "?limit=0"
	}
	var out []api.HistoryEntry
	err := b.do(ctx, http.MethodGet, path, nil, "", &out)
	return out, err
}

func (b *httpBackend) Rollback(ctx context.Context, key string, version int64, c Change) (api.WriteResult, error) {
	body, err := jsonv2.Marshal(api.RollbackRequest{Version: version})
	if err != nil {
		return api.WriteResult{}, err
	}
	q := url.Values{}
	addChange(q, c)

	var out api.WriteResult
	err = b.do(ctx, http.MethodPost,
		"/v1/configs/"+url.PathEscape(key)+"/rollback?"+q.Encode(),
		bytes.NewReader(body), "application/json", &out)
	return out, err
}

func addChange(q url.Values, c Change) {
	if c.Author != "" {
		q.Set("author", c.Author)
	}
	if c.Comment != "" {
		q.Set("comment", c.Comment)
	}
}

// do sends one request and decodes a 2xx body into out.
func (b *httpBackend) do(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, b.base+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := b.hc.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", b.base+path, err)
	}
	defer func() {
		io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrBody))
		resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("%s returned %s: %s", b.base, resp.Status, errorBody(resp.Body))
	}
	if out == nil {
		return nil
	}
	if err := jsonv2.UnmarshalRead(resp.Body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// errorBody handles both server JSON and proxy plain text.
func errorBody(r io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(r, maxErrBody))
	if err != nil || len(data) == 0 {
		return "(empty response body)"
	}
	var e api.ErrorResponse
	if jsonv2.Unmarshal(data, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(data))
}
