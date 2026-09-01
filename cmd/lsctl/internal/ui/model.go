// Package ui provides lsctl's Bubble Tea interface over backend.Backend.
package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/backend"
	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/render"
	"github.com/mbeoliero/lite_settings/server/api"
)

// Limit interactive history to avoid fetching thousands of versions on cursor moves.
const uiHistoryLimit = 50

// uiMode keeps browsing, text input, and modal keybindings disjoint.
type uiMode int

const (
	modeBrowse uiMode = iota
	modeFilter
	modeOverlay
)

type pane int

const (
	paneKeys pane = iota
	paneValue
	paneHistory
	paneCount
)

type pendKind int

const (
	pendSet pendKind = iota
	pendRollback
)

// pending unifies edit and rollback under the required preview-then-confirm flow.
type pending struct {
	kind    pendKind
	key     string
	value   string // pendSet
	format  string // pendSet
	version int64  // pendRollback
	action  string
	comment string
	equiv   string
}

// Model holds the interface state.
// Pointer receivers preserve mutations made before returning a Bubble Tea Cmd.
type Model struct {
	be       backend.Backend
	author   string
	timeout  time.Duration
	prefixes []string

	w, h int

	configs []api.Config
	rows    []keyRow
	cur     int
	filter  string

	detail    api.ConfigDetail
	hasDetail bool
	history   []api.HistoryEntry
	hcur      int
	voff      int

	focus pane
	mode  uiMode

	// Sequence numbers reject out-of-order responses that could show another key's value.
	keysSeq, detailSeq         int
	detailKey                  string
	loadingKeys, loadingDetail bool

	overlayTitle string
	overlayLines []uiLine
	ooff         int

	pend *pending

	status string
	errMsg string

	writes []string
}

type keyRow struct {
	header bool
	label  string
	key    string
}

// New returns a model that can render before its asynchronous initial load completes.
func New(be backend.Backend, author string, timeout time.Duration, prefixes []string) *Model {
	return &Model{
		be:       be,
		author:   author,
		timeout:  timeout,
		prefixes: prefixes,
		w:        100, h: 30,
		status: "Loading…",
	}
}

func (m *Model) Init() tea.Cmd { return m.loadKeys() }

type keysMsg struct {
	seq     int
	configs []api.Config
	err     error
}

type detailMsg struct {
	seq     int
	key     string
	detail  api.ConfigDetail
	history []api.HistoryEntry
	err     error
	histErr error
}

type writeMsg struct {
	action string
	key    string
	res    api.WriteResult
	err    error
}

type editedMsg struct {
	key    string
	format string
	value  string
	err    error
}

func (m *Model) loadKeys() tea.Cmd {
	m.keysSeq++
	m.loadingKeys = true
	seq, be, to, prefixes := m.keysSeq, m.be, m.timeout, m.prefixes
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), to)
		defer cancel()
		cs, err := be.List(ctx, prefixes)
		return keysMsg{seq: seq, configs: cs, err: err}
	}
}

func (m *Model) loadDetail(key string) tea.Cmd {
	m.detailSeq++
	m.detailKey = key
	m.loadingDetail = true
	seq, be, to := m.detailSeq, m.be, m.timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), to)
		defer cancel()

		d, err := be.Get(ctx, key)
		if err != nil {
			return detailMsg{seq: seq, key: key, err: err}
		}
		h, herr := be.History(ctx, key, uiHistoryLimit)
		if errors.Is(herr, backend.ErrNotFound) {
			// Preserve the fetched value when an inconsistent backend lacks history.
			herr = nil
		}
		return detailMsg{seq: seq, key: key, detail: d, history: h, histErr: herr}
	}
}

func (m *Model) doWrite(p pending) tea.Cmd {
	be, to, author := m.be, m.timeout, m.author
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), to)
		defer cancel()

		c := backend.Change{Author: author, Comment: p.comment}
		var (
			res api.WriteResult
			err error
		)
		switch p.kind {
		case pendSet:
			res, err = be.Set(ctx, p.key, p.value, p.format, c)
		case pendRollback:
			res, err = be.Rollback(ctx, p.key, p.version, c)
		}
		return writeMsg{action: p.action, key: p.key, res: res, err: err}
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.clampScroll()
		return m, nil

	case keysMsg:
		if msg.seq != m.keysSeq {
			return m, nil
		}
		m.loadingKeys = false
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.configs = msg.configs
		cmd := m.rebuild()
		if m.status == "Loading…" || m.status == "Refreshing…" {
			m.status = ""
		}
		return m, cmd

	case detailMsg:
		// A post-write reload can have a fresh sequence for a key the cursor has left.
		if msg.seq != m.detailSeq || msg.key != m.detailKey {
			return m, nil
		}
		m.loadingDetail = false
		if msg.err != nil {
			m.hasDetail = false
			m.errMsg = msg.err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.detail, m.history, m.hasDetail = msg.detail, msg.history, true
		m.hcur, m.voff = 0, 0
		if msg.histErr != nil {
			m.status = "History unavailable: " + msg.histErr.Error()
		}
		return m, nil

	case writeMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
			m.status = ""
			return m, nil
		}
		m.errMsg = ""
		line := fmt.Sprintf("%s %s: version=%d revision=%d",
			msg.action, msg.key, msg.res.Version, msg.res.Revision)
		m.status = line
		m.writes = append(m.writes, line)
		// Reload detail only if still selected; otherwise it could restore stale state.
		if msg.key == m.selectedKey() {
			return m, tea.Batch(m.loadKeys(), m.loadDetail(msg.key))
		}
		return m, m.loadKeys()

	case editedMsg:
		return m, m.afterEdit(msg)

	case tea.KeyMsg:
		return m, m.onKey(msg)
	}
	return m, nil
}

func (m *Model) onKey(msg tea.KeyMsg) tea.Cmd {
	if msg.Type == tea.KeyCtrlC {
		return tea.Quit
	}
	switch m.mode {
	case modeFilter:
		return m.filterKey(msg)
	case modeOverlay:
		return m.overlayKey(msg)
	default:
		return m.browseKey(msg)
	}
}

func (m *Model) browseKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "q":
		return tea.Quit
	case "?":
		m.openOverlay("Keys", helpLines())
		return nil
	case "tab", "l", "right":
		m.focus = (m.focus + 1) % paneCount
		return nil
	case "shift+tab", "h", "left":
		m.focus = (m.focus + paneCount - 1) % paneCount
		return nil
	case "/":
		m.mode = modeFilter
		return nil
	case "esc":
		if m.filter != "" {
			m.filter = ""
			return m.rebuild()
		}
		m.status = ""
		return nil
	case "r":
		m.status = "Refreshing…"
		if k := m.selectedKey(); k != "" {
			return tea.Batch(m.loadKeys(), m.loadDetail(k))
		}
		return m.loadKeys()
	case "j", "down":
		return m.move(1)
	case "k", "up":
		return m.move(-1)
	case "g", "home":
		return m.jump(-1)
	case "G", "end":
		return m.jump(1)
	case "e":
		return m.startEdit()
	case "d":
		return m.showDiff()
	case "enter":
		// Require Enter in history so an accidental Enter elsewhere cannot start rollback.
		if m.focus != paneHistory {
			m.focus = paneHistory
			return nil
		}
		return m.askRollback()
	}
	return nil
}

func (m *Model) filterKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = modeBrowse
		if m.filter == "" {
			return nil
		}
		m.filter = ""
		return m.rebuild()
	case tea.KeyEnter:
		m.mode = modeBrowse
		return nil
	case tea.KeyBackspace:
		r := []rune(m.filter)
		if len(r) == 0 {
			return nil
		}
		m.filter = string(r[:len(r)-1])
		return m.rebuild()
	case tea.KeySpace:
		m.filter += " "
		return m.rebuild()
	case tea.KeyRunes:
		m.filter += string(msg.Runes)
		return m.rebuild()
	}
	return nil
}

func (m *Model) overlayKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y":
		if m.pend == nil {
			return nil
		}
		p := *m.pend
		m.closeOverlay()
		m.status = "Saving…"
		return m.doWrite(p)
	case "esc", "q", "n", "N":
		if m.pend != nil {
			m.status = "Canceled; no changes saved"
		}
		m.closeOverlay()
		return nil
	case "j", "down":
		m.ooff++
	case "k", "up":
		m.ooff--
	case "g", "home":
		m.ooff = 0
	case "G", "end":
		m.ooff = len(m.overlayLines)
	}
	m.clampScroll()
	return nil
}

func (m *Model) showDiff() tea.Cmd {
	if !m.hasDetail {
		m.status = "Select a config first"
		return nil
	}
	if len(m.history) == 0 {
		m.status = "No history to compare"
		return nil
	}
	h := m.history[m.hcur]
	key := m.detail.Key
	left := fmt.Sprintf("%s@v%d", key, h.Version)
	d := render.UnifiedDiff(h.Value, m.detail.Value, left, key+"@current", render.DiffContext)
	if d == "" {
		m.status = fmt.Sprintf("v%d matches the current value", h.Version)
		return nil
	}
	m.openOverlay(fmt.Sprintf("diff  ·  equivalent command: lsctl diff %s %d", key, h.Version), diffLinesStyled(d))
	return nil
}

func (m *Model) askRollback() tea.Cmd {
	if !m.hasDetail || len(m.history) == 0 {
		m.status = "No version to roll back to"
		return nil
	}
	h := m.history[m.hcur]
	key := m.detail.Key
	d := render.UnifiedDiff(m.detail.Value, h.Value,
		key+"@current", fmt.Sprintf("%s@v%d", key, h.Version), render.DiffContext)
	// Equal text can decode differently after rollback when the format changes.
	fmtChanged := string(m.detail.Format) != string(h.Format)
	if d == "" && !fmtChanged {
		m.status = fmt.Sprintf("v%d matches the current value; no rollback needed", h.Version)
		return nil
	}
	if d == "" {
		d = fmt.Sprintf("(same value; format changes only: %s → %s)\n", m.detail.Format, h.Format)
	}
	m.pend = &pending{
		kind:    pendRollback,
		key:     key,
		version: h.Version,
		action:  fmt.Sprintf("Rolled back to v%d", h.Version),
		comment: fmt.Sprintf("Roll back to v%d (lsctl ui)", h.Version),
		equiv:   fmt.Sprintf("lsctl rollback %s --to %d", key, h.Version),
	}
	m.openOverlay(fmt.Sprintf("Roll back %s to v%d", key, h.Version), diffLinesStyled(d))
	return nil
}

func (m *Model) afterEdit(msg editedMsg) tea.Cmd {
	if msg.err != nil {
		m.errMsg = msg.err.Error()
		return nil
	}
	if !m.hasDetail || msg.key != m.detail.Key {
		m.status = "Selection changed; edit discarded"
		return nil
	}
	d := render.UnifiedDiff(m.detail.Value, msg.value, msg.key+"@current", msg.key+"@edited", render.DiffContext)
	if d == "" {
		m.status = "No changes; nothing saved"
		return nil
	}
	m.pend = &pending{
		kind:    pendSet,
		key:     msg.key,
		value:   msg.value,
		format:  msg.format,
		action:  "Saved",
		comment: "Edit (lsctl ui)",
		equiv:   equivSet(msg.key, msg.value, msg.format),
	}
	m.openOverlay("Save "+msg.key, diffLinesStyled(d))
	return nil
}

func (m *Model) rebuild() tea.Cmd {
	prev := m.selectedKey()
	m.rows = buildRows(m.configs, m.filter)
	if i := m.rowOf(prev); i >= 0 {
		m.cur = i
	} else {
		m.cur = m.firstKeyRow()
	}
	return m.syncDetail(prev)
}

// Clear detail before async loading so edits and rollbacks cannot target the previous key.
func (m *Model) syncDetail(prev string) tea.Cmd {
	k := m.selectedKey()
	if k == prev && (m.hasDetail || k == "") {
		return nil
	}
	m.hcur, m.voff = 0, 0
	m.hasDetail = false
	m.detail, m.history = api.ConfigDetail{}, nil
	if k == "" {
		m.detailSeq++ // invalidate any response still in flight
		m.detailKey = ""
		m.loadingDetail = false
		return nil
	}
	return m.loadDetail(k)
}

func (m *Model) move(d int) tea.Cmd {
	switch m.focus {
	case paneKeys:
		return m.moveKeys(d)
	case paneValue:
		m.voff += d
	case paneHistory:
		m.hcur += d
	}
	m.clampScroll()
	return nil
}

func (m *Model) jump(d int) tea.Cmd {
	switch m.focus {
	case paneKeys:
		prev := m.selectedKey()
		if d < 0 {
			m.cur = m.firstKeyRow()
		} else {
			m.cur = m.lastKeyRow()
		}
		return m.syncDetail(prev)
	case paneValue:
		if d < 0 {
			m.voff = 0
		} else {
			m.voff = len(valueLines(m.detail.Value))
		}
	case paneHistory:
		if d < 0 {
			m.hcur = 0
		} else {
			m.hcur = len(m.history)
		}
	}
	m.clampScroll()
	return nil
}

func (m *Model) moveKeys(d int) tea.Cmd {
	prev := m.selectedKey()
	for i := m.cur + d; i >= 0 && i < len(m.rows); i += d {
		if !m.rows[i].header {
			m.cur = i
			return m.syncDetail(prev)
		}
	}
	return nil
}

// Centralizing bounds prevents cursor and viewport mutations from diverging.
func (m *Model) clampScroll() {
	m.hcur = clamp(m.hcur, 0, len(m.history)-1)
	m.voff = clamp(m.voff, 0, len(valueLines(m.detail.Value))-m.innerH())
	m.ooff = clamp(m.ooff, 0, len(m.overlayLines)-m.innerH())
	m.cur = clamp(m.cur, 0, len(m.rows)-1)
}

func (m *Model) selectedKey() string {
	if m.cur < 0 || m.cur >= len(m.rows) {
		return ""
	}
	return m.rows[m.cur].key
}

func (m *Model) rowOf(key string) int {
	if key == "" {
		return -1
	}
	return slices.IndexFunc(m.rows, func(r keyRow) bool { return !r.header && r.key == key })
}

func (m *Model) firstKeyRow() int {
	return max(slices.IndexFunc(m.rows, notHeader), 0)
}

func (m *Model) lastKeyRow() int {
	for i, r := range slices.Backward(m.rows) {
		if notHeader(r) {
			return i
		}
	}
	return 0
}

func notHeader(r keyRow) bool { return !r.header }

func (m *Model) openOverlay(title string, lines []uiLine) {
	m.mode = modeOverlay
	m.overlayTitle = title
	m.overlayLines = lines
	m.ooff = 0
}

func (m *Model) closeOverlay() {
	m.mode = modeBrowse
	m.overlayTitle = ""
	m.overlayLines = nil
	m.ooff = 0
	m.pend = nil
}

// Group at the first colon, matching what a prefix means everywhere else
// (lite.WithPrefixes, lsctl list): service:db:host lands under service:.
func buildRows(configs []api.Config, filter string) []keyRow {
	rows := make([]keyRow, 0, len(configs)+8)
	last := "\x00" // no real prefix can match, ensuring the first group gets a header
	for _, c := range configs {
		if filter != "" && !strings.Contains(c.Key, filter) {
			continue
		}
		g, leaf := splitKey(c.Key)
		if g != last {
			if g != "" {
				rows = append(rows, keyRow{header: true, label: g})
			}
			last = g
		}
		rows = append(rows, keyRow{label: leaf, key: c.Key})
	}
	return rows
}

func splitKey(k string) (group, leaf string) {
	before, after, ok := strings.Cut(k, ":")
	if !ok {
		return "", k
	}
	return before + ":", after
}

// Embed the value because a temporary-file command would not be reproducible after exit.
func equivSet(key, value, format string) string {
	return fmt.Sprintf("lsctl set %s %s --format %s", key, shellQuote(value), format)
}

// POSIX single quotes preserve every byte; embedded quotes require close-escape-reopen.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}

// Writes returns successful writes for display after the alt screen disappears.
func (m *Model) Writes() []string { return m.writes }
