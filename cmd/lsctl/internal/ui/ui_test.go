package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/backend"
	"github.com/mbeoliero/lite_settings/server/api"
)

// fakeBackend isolates state-machine tests; integration_test.go covers persistence.
type fakeBackend struct {
	details map[string]api.ConfigDetail
	hist    map[string][]api.HistoryEntry

	listErr, getErr, histErr, writeErr error

	writes []string
	ver    int64
}

func newFake() *fakeBackend {
	return &fakeBackend{
		details: map[string]api.ConfigDetail{},
		hist:    map[string][]api.HistoryEntry{},
		ver:     10,
	}
}

func (f *fakeBackend) put(key, value, format string, older ...string) *fakeBackend {
	f.ver++
	f.details[key] = api.ConfigDetail{
		Config:    api.Config{Key: key, Value: value, Format: format},
		UpdatedAt: time.Now().Add(-time.Minute),
		UpdatedBy: "seed",
	}
	h := []api.HistoryEntry{{Version: f.ver, Value: value, Format: format, Op: "set",
		CreatedAt: time.Now().Add(-time.Minute), CreatedBy: "seed"}}
	for i, v := range older {
		h = append(h, api.HistoryEntry{
			Version: f.ver - int64(i) - 1, Value: v, Format: format, Op: "set",
			CreatedAt: time.Now().Add(-time.Hour), CreatedBy: "seed",
		})
	}
	f.hist[key] = h
	return f
}

func (f *fakeBackend) Get(_ context.Context, key string) (api.ConfigDetail, error) {
	if f.getErr != nil {
		return api.ConfigDetail{}, f.getErr
	}
	d, ok := f.details[key]
	if !ok {
		return api.ConfigDetail{}, backend.ErrNotFound
	}
	return d, nil
}

func (f *fakeBackend) List(context.Context, []string) ([]api.Config, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]api.Config, 0, len(f.details))
	for _, d := range f.details {
		out = append(out, d.Config)
	}
	slices.SortFunc(out, func(a, b api.Config) int { return strings.Compare(a.Key, b.Key) })
	return out, nil
}

func (f *fakeBackend) History(_ context.Context, key string, _ int) ([]api.HistoryEntry, error) {
	if f.histErr != nil {
		return nil, f.histErr
	}
	h, ok := f.hist[key]
	if !ok {
		return nil, backend.ErrNotFound
	}
	return h, nil
}

func (f *fakeBackend) Set(_ context.Context, key, value, format string, _ backend.Change) (api.WriteResult, error) {
	if f.writeErr != nil {
		return api.WriteResult{}, f.writeErr
	}
	f.writes = append(f.writes, "set "+key)
	f.put(key, value, format)
	return api.WriteResult{Key: key, Version: f.ver, Revision: f.ver}, nil
}

func (f *fakeBackend) Rollback(_ context.Context, key string, v int64, _ backend.Change) (api.WriteResult, error) {
	if f.writeErr != nil {
		return api.WriteResult{}, f.writeErr
	}
	f.writes = append(f.writes, fmt.Sprintf("rollback %s@%d", key, v))
	f.ver++
	return api.WriteResult{Key: key, Version: f.ver, Revision: f.ver}, nil
}

func (f *fakeBackend) Delete(context.Context, string, backend.Change) (api.WriteResult, error) {
	return api.WriteResult{}, backend.ErrNotSupported
}
func (f *fakeBackend) Migrate(context.Context) error { return nil }
func (f *fakeBackend) Describe() string              { return "fake" }
func (f *fakeBackend) Close() error                  { return nil }

// Run Bubble Tea commands synchronously so state transitions are testable without a terminal.
func dispatch(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	dispatchDepth(t, m, cmd, 0)
}

func dispatchDepth(t *testing.T, m *Model, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil {
		return
	}
	if depth > 20 {
		t.Fatal("message chain did not settle; possible feedback loop")
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		for _, c := range b {
			dispatchDepth(t, m, c, depth+1)
		}
		return
	}
	if msg == nil {
		return
	}
	_, next := m.Update(msg)
	dispatchDepth(t, m, next, depth+1)
}

func press(t *testing.T, m *Model, k string) {
	t.Helper()
	_, cmd := m.Update(keyMsg(k))
	dispatch(t, m, cmd)
}

// Match Bubble Tea's String spellings so tests cannot send impossible keystrokes.
func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func boot(t *testing.T, be backend.Backend) *Model {
	t.Helper()
	m := New(be, "tester", 20*time.Second, nil)
	m.w, m.h = 120, 30
	dispatch(t, m, m.Init())
	return m
}

func TestKeyMsgMatchesBubbletea(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	for _, s := range []string{"tab", "shift+tab", "enter", "esc", "up", "down",
		"backspace", "ctrl+c", "j", "k", "e", "d", "q", "y", "n", "/", "?", "G"} {
		if got := keyMsg(s).String(); got != s {
			t.Errorf("keyMsg(%q).String() = %q", s, got)
		}
	}
}

func TestSplitKey(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	cases := []struct{ in, group, leaf string }{
		{"prompt_group:main", "prompt_group:", "main"},
		{"a:b:c", "a:", "b:c"},
		{"plain", "", "plain"},
		{"trailing:", "trailing:", ""},
	}
	for _, c := range cases {
		g, l := splitKey(c.in)
		if g != c.group || l != c.leaf {
			t.Errorf("splitKey(%q) = %q,%q, want %q,%q", c.in, g, l, c.group, c.leaf)
		}
	}
}

func TestBuildRowsGroupsByPrefix(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	cs := []api.Config{
		{Key: "feature:debug"},
		{Key: "prompt_group:fallback"},
		{Key: "prompt_group:main"},
		// Nested keys join their top-level group, not one header each.
		{Key: "service:cache:host"},
		{Key: "service:db:host"},
		{Key: "standalone"},
	}
	rows := buildRows(cs, "")

	var got []string
	for _, r := range rows {
		if r.header {
			got = append(got, "# "+r.label)
		} else {
			got = append(got, r.label)
		}
	}
	want := []string{
		"# feature:", "debug",
		"# prompt_group:", "fallback", "main",
		"# service:", "cache:host", "db:host",
		"standalone",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("row order = %v\nwant %v", got, want)
	}
}

func TestBuildRowsFilterDropsEmptyGroups(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	cs := []api.Config{{Key: "a:one"}, {Key: "b:two"}}
	rows := buildRows(cs, "two")
	if len(rows) != 2 || !rows[0].header || rows[0].label != "b:" || rows[1].key != "b:two" {
		t.Fatalf("filtered rows = %+v, want only group b:", rows)
	}
}

func TestCursorSkipsGroupHeaders(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	m := boot(t, newFake().put("a:one", "1", "raw").put("b:two", "2", "raw"))

	if got := m.selectedKey(); got != "a:one" {
		t.Fatalf("initial selection = %q, want first key", got)
	}
	press(t, m, "j")
	if got := m.selectedKey(); got != "b:two" {
		t.Fatalf("selection after moving down = %q, want b:two after skipping header", got)
	}
	press(t, m, "k")
	if got := m.selectedKey(); got != "a:one" {
		t.Fatalf("selection after moving up = %q, want a:one", got)
	}
}

func TestCursorStopsAtEndsWithoutWrapping(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	m := boot(t, newFake().put("a:one", "1", "raw").put("a:two", "2", "raw"))

	press(t, m, "k")
	if got := m.selectedKey(); got != "a:one" {
		t.Fatalf("moving up from first row wrapped to %q", got)
	}
	press(t, m, "j")
	press(t, m, "j")
	if got := m.selectedKey(); got != "a:two" {
		t.Fatalf("moving down from last row wrapped to %q", got)
	}
}

func TestFilterKeepsCursorOnSameKey(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	m := boot(t, newFake().put("a:one", "1", "raw").put("a:two", "2", "raw"))
	press(t, m, "j")
	if m.selectedKey() != "a:two" {
		t.Fatal("setup: a:two is not selected")
	}

	press(t, m, "/")
	press(t, m, "t")
	press(t, m, "w")
	if m.mode != modeFilter {
		t.Fatal("filter input mode is not active")
	}
	if got := m.selectedKey(); got != "a:two" {
		t.Fatalf("selection after filtering = %q, want a:two", got)
	}

	press(t, m, "esc")
	if m.filter != "" || m.mode != modeBrowse {
		t.Fatalf("esc left filter=%q mode=%v; want empty filter in browse mode", m.filter, m.mode)
	}
	if got := m.selectedKey(); got != "a:two" {
		t.Fatalf("selection after clearing filter = %q, want a:two", got)
	}
}

func TestFilterBackspace(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	m := boot(t, newFake().put("a:one", "1", "raw"))
	press(t, m, "/")
	press(t, m, "o")
	press(t, m, "x")
	press(t, m, "backspace")
	if m.filter != "o" {
		t.Fatalf("filter after backspace = %q, want %q", m.filter, "o")
	}
}

func TestStaleDetailResponseIsDiscarded(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	m := boot(t, newFake().put("a", "A", "raw").put("b", "B", "raw"))
	m.hasDetail = false

	_ = m.loadDetail("a")
	stale := m.detailSeq
	_ = m.loadDetail("b")

	m.Update(detailMsg{seq: stale, key: "a",
		detail: api.ConfigDetail{Config: api.Config{Key: "a", Value: "A"}}})
	if m.hasDetail {
		t.Fatal("stale detail response was accepted for the wrong selected key")
	}

	m.Update(detailMsg{seq: m.detailSeq, key: "b",
		detail: api.ConfigDetail{Config: api.Config{Key: "b", Value: "B"}}})
	if !m.hasDetail || m.detail.Key != "b" {
		t.Fatalf("latest response was not accepted; detail key = %q", m.detail.Key)
	}
}

func TestStaleKeysResponseIsDiscarded(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	m := boot(t, newFake().put("a", "A", "raw"))
	before := len(m.configs)

	_ = m.loadKeys()
	stale := m.keysSeq
	_ = m.loadKeys()

	m.Update(keysMsg{seq: stale, configs: []api.Config{{Key: "x"}, {Key: "y"}, {Key: "z"}}})
	if len(m.configs) != before {
		t.Fatalf("stale key list was accepted with %d configs", len(m.configs))
	}
}

func TestEnterOnKeysPaneFocusesHistoryInsteadOfRollingBack(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	f := newFake().put("a", "new", "raw", "old")
	m := boot(t, f)

	press(t, m, "enter")
	if m.focus != paneHistory {
		t.Fatalf("enter in keys pane set focus=%v, want history pane", m.focus)
	}
	if m.pend != nil || len(f.writes) != 0 {
		t.Fatal("enter in keys pane triggered a rollback")
	}
}

func TestRollbackAsksForConfirmationAndShowsEquivalentCommand(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	f := newFake().put("a", "new", "raw", "old")
	m := boot(t, f)

	press(t, m, "enter")
	press(t, m, "j")
	press(t, m, "enter")

	if m.pend == nil {
		t.Fatal("pending confirmation state was not entered")
	}
	if m.mode != modeOverlay {
		t.Fatalf("mode = %v, want diff preview", m.mode)
	}
	if len(f.writes) != 0 {
		t.Fatalf("writes before confirmation = %v", f.writes)
	}
	want := fmt.Sprintf("lsctl rollback a --to %d", m.pend.version)
	if m.pend.equiv != want {
		t.Fatalf("equivalent command = %q, want %q", m.pend.equiv, want)
	}
	// The copyable command must be visible, not merely stored in model state.
	if !strings.Contains(m.View(), "lsctl rollback a --to") {
		t.Fatal("equivalent command is not rendered")
	}
}

func TestRollbackCancelDoesNotWrite(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	f := newFake().put("a", "new", "raw", "old")
	m := boot(t, f)
	press(t, m, "enter")
	press(t, m, "j")
	press(t, m, "enter")

	press(t, m, "n")
	if m.pend != nil || m.mode != modeBrowse {
		t.Fatal("n did not cancel and close the preview")
	}
	if len(f.writes) != 0 {
		t.Fatalf("writes after cancellation = %v", f.writes)
	}
}

func TestRollbackConfirmWritesAndReloads(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	f := newFake().put("a", "new", "raw", "old")
	m := boot(t, f)
	press(t, m, "enter")
	press(t, m, "j")
	target := m.history[m.hcur].Version
	press(t, m, "enter")
	press(t, m, "y")

	want := fmt.Sprintf("rollback a@%d", target)
	if !slices.Contains(f.writes, want) {
		t.Fatalf("write records = %v, want entry containing %q", f.writes, want)
	}
	if m.pend != nil || m.mode != modeBrowse {
		t.Fatal("preview remained open after saving")
	}
	if len(m.writes) != 1 || !strings.Contains(m.writes[0], "a: version=") {
		t.Fatalf("session write records = %v; saved change is missing", m.writes)
	}
}

func TestRollbackToIdenticalVersionIsRefused(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	f := newFake().put("a", "same", "raw")
	m := boot(t, f)
	press(t, m, "enter")
	press(t, m, "enter")

	if m.pend != nil {
		t.Fatal("identical version opened a confirmation overlay")
	}
	if !strings.Contains(m.status, "no rollback needed") {
		t.Fatalf("status = %q, want no rollback needed", m.status)
	}
}

func TestEditWithoutChangeDoesNotWrite(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	f := newFake().put("a", "same", "raw")
	m := boot(t, f)

	_, cmd := m.Update(editedMsg{key: "a", format: "raw", value: "same"})
	dispatch(t, m, cmd)

	if m.pend != nil || len(f.writes) != 0 {
		t.Fatal("unchanged content requested confirmation or a write")
	}
	if !strings.Contains(m.status, "No changes") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestEditOfStaleSelectionIsDiscarded(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	f := newFake().put("a", "A", "raw").put("b", "B", "raw")
	m := boot(t, f)
	_, cmd := m.Update(editedMsg{key: "b", format: "raw", value: "B2"})
	dispatch(t, m, cmd)

	if m.pend != nil || len(f.writes) != 0 {
		t.Fatal("edit for a non-selected key was not discarded")
	}
}

func TestEditShowsDiffThenWritesOnConfirm(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	f := newFake().put("a", "one\ntwo\n", "yaml")
	m := boot(t, f)

	_, cmd := m.Update(editedMsg{key: "a", format: "yaml", value: "one\nTWO\n"})
	dispatch(t, m, cmd)

	if m.pend == nil || m.pend.kind != pendSet {
		t.Fatal("pending write confirmation state was not entered")
	}
	if len(f.writes) != 0 {
		t.Fatal("write occurred before confirmation")
	}
	view := m.View()
	if !strings.Contains(view, "-two") || !strings.Contains(view, "+TWO") {
		t.Fatalf("preview does not show added and removed lines:\n%s", view)
	}

	press(t, m, "y")
	if !slices.Contains(f.writes, "set a") {
		t.Fatalf("writes after confirmation = %v", f.writes)
	}
	if f.details["a"].Value != "one\nTWO\n" {
		t.Fatalf("written value = %q", f.details["a"].Value)
	}
}

func TestEditorErrorSurfacesWithoutWriting(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	f := newFake().put("a", "A", "raw")
	m := boot(t, f)
	_, cmd := m.Update(editedMsg{key: "a", err: errors.New("editor failed")})
	dispatch(t, m, cmd)

	if m.errMsg != "editor failed" {
		t.Fatalf("errMsg = %q", m.errMsg)
	}
	if len(f.writes) != 0 {
		t.Fatal("failed edit produced a write")
	}
}

func TestEditorCmdPrefersVisualAndKeepsArgs(t *testing.T) {
	// t.Setenv mutates the process environment, so this test must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	t.Setenv("VISUAL", "code -w")
	t.Setenv("EDITOR", "vi")
	if got := editorCmd(); !slices.Equal(got, []string{"code", "-w"}) {
		t.Fatalf("editorCmd() = %v, want $VISUAL with arguments", got)
	}
	t.Setenv("VISUAL", "")
	if got := editorCmd(); !slices.Equal(got, []string{"vi"}) {
		t.Fatalf("editorCmd() = %v, want $EDITOR when $VISUAL is empty", got)
	}
}

func TestWriteTempUsesFormatExtension(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	for format, ext := range map[string]string{"yaml": ".yaml", "json": ".json", "raw": ".txt"} {
		p, err := writeTemp("prompt_group:main", format, "x")
		if err != nil {
			t.Fatalf("writeTemp: %v", err)
		}
		if !strings.HasSuffix(p, ext) {
			t.Errorf("temporary file %q for format %s does not end in %s", p, format, ext)
		}
		if !strings.Contains(p, "prompt_group-main") {
			t.Errorf("temporary filename %q does not identify the key", p)
		}
	}
}

func TestSafeNameStripsPathSeparators(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	for _, in := range []string{"../../etc/passwd:x", "a:://b", "prompt_group:main"} {
		got := safeName(in)
		if strings.ContainsAny(got, `/\.:`) {
			t.Errorf("safeName(%q) = %q, contains a path separator or dot", in, got)
		}
		if strings.Contains(got, "--") || strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
			t.Errorf("safeName(%q) = %q, dashes are not collapsed and trimmed", in, got)
		}
	}
	if got := safeName("prompt_group:main"); got != "prompt_group-main" {
		t.Fatalf("safeName lost readability: %q", got)
	}
}

func TestHistoryErrorStillShowsValue(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	f := newFake().put("a", "A", "raw")
	f.histErr = errors.New("history unavailable")
	m := boot(t, f)

	if !m.hasDetail || m.detail.Value != "A" {
		t.Fatal("history failure discarded the value")
	}
	if !strings.Contains(m.status, "History unavailable") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestWriteErrorSurfaces(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	f := newFake().put("a", "new", "raw", "old")
	f.writeErr = errors.New("database is read-only")
	m := boot(t, f)
	press(t, m, "enter")
	press(t, m, "j")
	press(t, m, "enter")
	press(t, m, "y")

	if m.errMsg != "database is read-only" {
		t.Fatalf("errMsg = %q", m.errMsg)
	}
	if len(m.writes) != 0 {
		t.Fatal("failed write was added to session records")
	}
}

func TestListErrorSurfaces(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	f := newFake()
	f.listErr = errors.New("connection failed")
	m := boot(t, f)
	if m.errMsg != "connection failed" {
		t.Fatalf("errMsg = %q", m.errMsg)
	}
	if !strings.Contains(m.View(), "connection failed") {
		t.Fatal("error is not shown in the UI")
	}
}

func TestViewFillsExactlyTheTerminal(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	m := boot(t, newFake().put("prompt_group:main", "系统提示词：你好\n温度: 0.7\n", "yaml"))

	for _, size := range [][2]int{{80, 24}, {120, 40}, {61, 13}, {200, 60}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(m.View(), "\n")
		if len(lines) != size[1] {
			t.Errorf("%dx%d rendered %d lines", size[0], size[1], len(lines))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w != size[0] {
				t.Errorf("%dx%d line %d width = %d", size[0], size[1], i, w)
			}
		}
	}
}

func TestViewFitsWithWideRunes(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// Wide runes occupy two columns and must not shift the right border.
	m := boot(t, newFake().put("配置组:主提示词", strings.Repeat("中文", 80), "yaml"))
	// Odd widths exercise truncation before a rune that cannot straddle the edge.
	for _, w := range []int{80, 81, 99, 137} {
		m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		for i, l := range strings.Split(m.View(), "\n") {
			if got := lipgloss.Width(l); got != w {
				t.Fatalf("width %d: line %d width = %d", w, i, got)
			}
		}
	}
}

func TestViewRefusesTinyTerminal(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	m := boot(t, newFake())
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 8})
	if !strings.Contains(m.View(), "Terminal too small") {
		t.Fatal("tiny terminal did not show a size warning")
	}
}

func TestViewStripsEscapeSequencesFromValues(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// Untrusted escape sequences can move the cursor or trigger terminal responses.
	m := boot(t, newFake().put("a", "before\x1b[6nafter", "raw"))
	if strings.Contains(m.View(), "\x1b[6n") {
		t.Fatal("escape sequence was not stripped from the value")
	}
	// Remove only ESC; the visible suffix is user content.
	if !strings.Contains(m.View(), "before[6nafter") {
		t.Fatal("visible text changed after stripping ESC")
	}
}

func TestHelpOverlayListsEquivalentCommands(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	m := boot(t, newFake().put("a", "A", "raw"))
	press(t, m, "?")
	if m.mode != modeOverlay {
		t.Fatal("? did not open help")
	}
	v := m.View()
	for _, want := range []string{"equivalent", "lsctl list", "rollback"} {
		if !strings.Contains(v, want) {
			t.Errorf("help does not mention %q", want)
		}
	}
	press(t, m, "q")
	if m.mode != modeBrowse {
		t.Fatal("q quit the program instead of closing help")
	}
}

func TestDiffOverlayShowsEquivalentCommand(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	m := boot(t, newFake().put("a", "new", "raw", "old"))
	press(t, m, "enter")
	press(t, m, "j")
	press(t, m, "d")
	if m.mode != modeOverlay {
		t.Fatal("d did not open the diff")
	}
	if !strings.Contains(m.View(), "lsctl diff a") {
		t.Fatal("diff preview does not show the equivalent command")
	}
	if m.pend != nil {
		t.Fatal("viewing a diff created a pending write")
	}
}

func TestFitPadsAndTruncates(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	if got := fit("ab", 5); got != "ab   " {
		t.Errorf("fit(\"ab\",5) = %q", got)
	}
	if got := fit("abcdef", 4); lipgloss.Width(got) != 4 {
		t.Errorf("fit(\"abcdef\",4) = %q, width %d", got, lipgloss.Width(got))
	}
	if got := fit("中文字", 4); lipgloss.Width(got) != 4 {
		t.Errorf("fit(\"中文字\",4) = %q, width %d", got, lipgloss.Width(got))
	}
	if got := fit("x", 0); got != "" {
		t.Errorf("fit with width 0 = %q, want empty string", got)
	}
}

func TestWindowKeepsCursorVisible(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	cases := []struct{ cur, n, h, want int }{
		{0, 3, 10, 0},
		{0, 100, 10, 0},
		{50, 100, 10, 45},
		{99, 100, 10, 90},
		{5, 100, 0, 0},
	}
	for _, c := range cases {
		if got := window(c.cur, c.n, c.h); got != c.want {
			t.Errorf("window(%d,%d,%d) = %d, want %d", c.cur, c.n, c.h, got, c.want)
		}
		if c.h > 0 && c.n > c.h {
			if got := window(c.cur, c.n, c.h); c.cur < got || c.cur >= got+c.h {
				t.Errorf("window(%d,%d,%d) = %d: cursor is outside window", c.cur, c.n, c.h, got)
			}
		}
	}
}

func TestValueLinesDropsTrailingBlank(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	if got := valueLines("a\nb\n"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("valueLines = %#v", got)
	}
	if got := valueLines("a\r\nb"); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("CRLF was not normalized: %#v", got)
	}
	if got := valueLines(""); got != nil {
		t.Errorf("empty value returned %#v, want nil", got)
	}
}

func TestRelTime(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	now := time.Now()
	cases := []struct {
		in   time.Time
		want string
	}{
		{time.Time{}, "-"},
		{now.Add(-10 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-3 * time.Hour), "3h ago"},
		{now.Add(-50 * time.Hour), "2d ago"},
		{now.Add(time.Minute), "just now"}, // tolerate clock skew
	}
	for _, c := range cases {
		if got := relTime(c.in); got != c.want {
			t.Errorf("relTime(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestClamp(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	cases := []struct{ v, lo, hi, want int }{
		{5, 0, 10, 5}, {-1, 0, 10, 0}, {11, 0, 10, 10},
		{3, 0, -1, 0},
	}
	for _, c := range cases {
		if got := clamp(c.v, c.lo, c.hi); got != c.want {
			t.Errorf("clamp(%d,%d,%d) = %d, want %d", c.v, c.lo, c.hi, got, c.want)
		}
	}
}

func TestMovingSelectionClearsStaleDetail(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	m := boot(t, newFake().put("a", "A", "raw").put("b", "B", "raw"))

	if m.selectedKey() != "a" || !m.hasDetail || m.detail.Key != "a" {
		t.Fatalf("precondition failed: key=%q hasDetail=%v", m.selectedKey(), m.hasDetail)
	}

	// Leave b's asynchronous detail response in flight.
	m.Update(keyMsg("j"))
	if m.selectedKey() != "b" {
		t.Fatalf("selection = %q, want b", m.selectedKey())
	}
	// Stale detail would make edit or rollback target a.
	if m.hasDetail {
		t.Fatal("selection change did not invalidate stale detail")
	}
	if m.detail.Key != "" || len(m.history) != 0 {
		t.Fatalf("stale detail remains: key=%q history=%d", m.detail.Key, len(m.history))
	}
}

func TestRollbackRefusedWhileDetailPending(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	m := boot(t, newFake().put("a", "A", "raw", "A0").put("b", "B", "raw"))

	m.Update(keyMsg("j")) // keep b's detail response pending
	m.Update(keyMsg("l"))
	m.Update(keyMsg("l"))
	m.Update(keyMsg("enter"))

	if m.pend != nil {
		t.Fatalf("rollback started before detail loaded: pend.key=%q", m.pend.key)
	}
}

func TestWriteReloadDoesNotResurrectStaleDetail(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	m := boot(t, newFake().put("a", "A", "raw").put("b", "B", "raw"))

	// Move to b before its detail arrives while a's write completes.
	m.Update(keyMsg("j"))
	m.Update(writeMsg{action: "Saved", key: "a", res: api.WriteResult{Version: 2, Revision: 9}})

	// A fresh sequence alone must not let a's post-write reload replace b's detail.
	m.Update(detailMsg{seq: m.detailSeq, key: "a",
		detail: api.ConfigDetail{Config: api.Config{Key: "a", Value: "A"}}})
	if m.hasDetail && m.detail.Key == "a" {
		t.Fatal("post-write reload restored detail for a key no longer selected")
	}
}

func TestRollbackOnFormatOnlyChange(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	f := newFake().put("k", "1", "json")
	// Equal text can decode differently after rollback when the format changes.
	f.hist["k"] = append(f.hist["k"], api.HistoryEntry{
		Version: f.ver - 1, Value: "1", Format: "raw", Op: "set",
		CreatedAt: time.Now().Add(-time.Hour), CreatedBy: "seed",
	})
	m := boot(t, f)

	press(t, m, "l")
	press(t, m, "l")
	press(t, m, "j")
	press(t, m, "enter")

	if m.pend == nil {
		t.Fatalf("format-only change did not allow rollback; status=%q", m.status)
	}
	if m.pend.kind != pendRollback {
		t.Fatalf("pend.kind = %v, want pendRollback", m.pend.kind)
	}
}
