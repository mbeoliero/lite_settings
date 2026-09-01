package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/render"
)

// Draw titled borders here without cutting Lip Gloss ANSI sequences.
// Lip Gloss still handles color capability detection.
var (
	uiDim   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	uiFocus = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	uiSel   = lipgloss.NewStyle().Reverse(true)
	uiMark  = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	uiErrSt = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	uiAdd   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	uiDel   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	uiWarn  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
)

// Keep text plain until after padding so ANSI escapes do not distort display width.
type uiLine struct {
	text  string
	style lipgloss.Style
}

func plain(s string) uiLine { return uiLine{text: s} }

// Below this size the three panes cannot render coherently.
const (
	uiMinWidth  = 60
	uiMinHeight = 12
)

func (m *Model) innerH() int {
	return max(m.h-5, 1)
}

func (m *Model) View() string {
	if m.w < uiMinWidth || m.h < uiMinHeight {
		return fmt.Sprintf("Terminal too small: need at least %d×%d, current size %d×%d\nPress q to quit.",
			uiMinWidth, uiMinHeight, m.w, m.h)
	}

	ih := m.innerH()
	var body []string
	if m.mode == modeOverlay {
		body = box(m.overlayTitle, true, m.w, ih+2, m.overlayLines, m.ooff)
	} else {
		kw, hw := m.columnWidths()
		vw := m.w - kw - hw
		body = joinCols(
			box("keys"+m.filterTag(), m.focus == paneKeys, kw, ih+2, m.keysContent(ih), 0),
			box(m.valueTitle(), m.focus == paneValue, vw, ih+2, m.valueContent(), m.voff),
			box("history", m.focus == paneHistory, hw, ih+2, m.historyContent(ih), 0),
		)
	}

	out := make([]string, 0, len(body)+3)
	out = append(out, m.header())
	out = append(out, body...)
	out = append(out, m.statusLine(), m.hintLine())
	return strings.Join(out, "\n")
}

// Reserve most width for values; keys and versions are shorter.
func (m *Model) columnWidths() (keys, history int) {
	keys = clamp(m.w*28/100, 18, 34)
	history = clamp(m.w*26/100, 18, 30)
	return keys, history
}

func (m *Model) header() string {
	left := "lsctl ui"
	if m.be != nil {
		left += "  " + m.be.Describe()
	}
	right := fmt.Sprintf("%d items", len(m.configs))
	if len(m.prefixes) > 0 {
		right = strings.Join(m.prefixes, " ") + "  " + right
	}
	if m.loadingKeys || m.loadingDetail {
		right = "Loading  " + right
	}
	gap := m.w - runewidth.StringWidth(left) - runewidth.StringWidth(right)
	if gap < 1 {
		return uiDim.Render(fit(left, m.w))
	}
	return uiDim.Render(left + strings.Repeat(" ", gap) + right)
}

func (m *Model) filterTag() string {
	switch {
	case m.mode == modeFilter:
		return "  /" + m.filter + "▏"
	case m.filter != "":
		return "  /" + m.filter
	}
	return ""
}

func (m *Model) valueTitle() string {
	if !m.hasDetail {
		return "value"
	}
	return fmt.Sprintf("value  %s  %s", m.detail.Format, render.HumanSize(len(m.detail.Value)))
}

func (m *Model) statusLine() string {
	switch {
	case m.errMsg != "":
		return uiErrSt.Render(fit("✗ "+render.OneLine(m.errMsg, m.w-2), m.w))
	case m.pend != nil:
		// Keep multiline values in the overlay so the status row cannot break layout.
		return uiWarn.Render(fit("Equivalent command: "+render.OneLine(m.pend.equiv, m.w), m.w))
	case m.status != "":
		return fit(render.OneLine(m.status, m.w), m.w)
	case m.hasDetail:
		return uiDim.Render(fit(fmt.Sprintf("%s  updated by %s at %s",
			m.detail.Key, m.detail.UpdatedBy, render.LocalTime(m.detail.UpdatedAt)), m.w))
	}
	return strings.Repeat(" ", m.w)
}

func (m *Model) hintLine() string {
	var h string
	switch {
	case m.mode == modeFilter:
		h = "type to filter   enter apply   esc clear"
	case m.pend != nil:
		h = "y save   n/esc cancel   j/k scroll diff"
	case m.mode == modeOverlay:
		h = "j/k scroll   q/esc close"
	case m.focus == paneHistory:
		h = "enter rollback   d diff   e edit   / filter   r refresh   ? help   q quit"
	default:
		h = "tab switch pane   e edit   d diff   enter history   / filter   r refresh   ? help   q quit"
	}
	return uiDim.Render(fit(h, m.w))
}

func (m *Model) keysContent(h int) []uiLine {
	if len(m.rows) == 0 {
		switch {
		case m.loadingKeys:
			return []uiLine{{text: "Loading…", style: uiDim}}
		case m.filter != "":
			return []uiLine{{text: "No matches for " + m.filter, style: uiDim}}
		default:
			return []uiLine{{text: "No configs yet", style: uiDim}}
		}
	}

	start := window(m.cur, len(m.rows), h)
	lines := make([]uiLine, 0, h)
	for i := start; i < len(m.rows) && len(lines) < h; i++ {
		r := m.rows[i]
		switch {
		case r.header:
			lines = append(lines, uiLine{text: r.label, style: uiDim})
		case i == m.cur && m.focus == paneKeys:
			lines = append(lines, uiLine{text: "> " + r.label, style: uiSel})
		case i == m.cur:
			lines = append(lines, uiLine{text: "> " + r.label, style: uiMark})
		default:
			lines = append(lines, plain("  "+r.label))
		}
	}
	return lines
}

func (m *Model) valueContent() []uiLine {
	if !m.hasDetail {
		if m.loadingDetail {
			return []uiLine{{text: "Loading…", style: uiDim}}
		}
		return []uiLine{{text: "← Select a config with j/k", style: uiDim}}
	}
	src := valueLines(m.detail.Value)
	lines := make([]uiLine, len(src))
	for i, s := range src {
		lines[i] = plain(s)
	}
	return lines
}

func (m *Model) historyContent(h int) []uiLine {
	if !m.hasDetail {
		return nil
	}
	if len(m.history) == 0 {
		return []uiLine{{text: "No history", style: uiDim}}
	}

	start := window(m.hcur, len(m.history), h)
	lines := make([]uiLine, 0, h)
	for i := start; i < len(m.history) && len(lines) < h; i++ {
		e := m.history[i]
		cur := " "
		if i == 0 {
			cur = "•"
		}
		text := fmt.Sprintf("%sv%-4d %-9s %s", cur, e.Version, relTime(e.CreatedAt), e.Op)
		switch {
		case i == m.hcur && m.focus == paneHistory:
			lines = append(lines, uiLine{text: text, style: uiSel})
		case i == m.hcur:
			lines = append(lines, uiLine{text: text, style: uiMark})
		default:
			lines = append(lines, plain(text))
		}
	}
	return lines
}

func helpLines() []uiLine {
	rows := [][2]string{
		{"j / k / ↑ ↓", "Move or scroll in the current pane"},
		{"g / G", "Jump to the start / end of the current pane"},
		{"tab / h / l", "Switch panes"},
		{"enter", "Keys/value: open history; history: roll back"},
		{"e", "Edit with $EDITOR, then review and confirm the diff"},
		{"d", "Compare the selected version with the current value"},
		{"/", "Filter by key; esc clears the filter"},
		{"r", "Reload"},
		{"y / n", "Confirm / cancel"},
		{"q / ctrl-c", "Quit"},
	}
	out := []uiLine{
		{text: "Every TUI action has an equivalent non-interactive command shown before confirmation.", style: uiDim},
		plain(""),
	}
	for _, r := range rows {
		out = append(out, plain(fmt.Sprintf("  %-14s  %s", r[0], r[1])))
	}
	out = append(out,
		plain(""),
		uiLine{text: "  Commands: lsctl list / get / history / diff / set / rollback", style: uiDim},
	)
	return out
}

func diffLinesStyled(d string) []uiLine {
	raw := strings.Split(strings.TrimRight(d, "\n"), "\n")
	out := make([]uiLine, len(raw))
	for i, l := range raw {
		switch {
		case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"), strings.HasPrefix(l, "@@"):
			out[i] = uiLine{text: l, style: uiDim}
		case strings.HasPrefix(l, "+"):
			out[i] = uiLine{text: l, style: uiAdd}
		case strings.HasPrefix(l, "-"):
			out[i] = uiLine{text: l, style: uiDel}
		default:
			out[i] = plain(l)
		}
	}
	return out
}

func box(title string, focused bool, w, h int, content []uiLine, off int) []string {
	if w < 4 || h < 2 {
		return nil
	}
	inner := w - 2
	bs := uiDim
	if focused {
		bs = uiFocus
	}

	lab := " " + title + " "
	if runewidth.StringWidth(lab) > inner {
		lab = runewidth.Truncate(lab, inner, "")
	}
	top := lab + strings.Repeat("─", inner-runewidth.StringWidth(lab))

	out := make([]string, 0, h)
	out = append(out, bs.Render("┌"+top+"┐"))
	left, right := bs.Render("│"), bs.Render("│")
	for i := range h - 2 {
		var l uiLine
		if j := i + off; j >= 0 && j < len(content) {
			l = content[j]
		}
		out = append(out, left+l.style.Render(fit(" "+l.text, inner))+right)
	}
	out = append(out, bs.Render("└"+strings.Repeat("─", inner)+"┘"))
	return out
}

func joinCols(cols ...[]string) []string {
	h := 0
	for _, c := range cols {
		h = max(h, len(c))
	}
	out := make([]string, h)
	var sb strings.Builder
	for i := range h {
		sb.Reset()
		for _, c := range cols {
			if i < len(c) {
				sb.WriteString(c[i])
			}
		}
		out[i] = sb.String()
	}
	return out
}

func window(cur, n, h int) int {
	if h <= 0 || n <= h {
		return 0
	}
	return clamp(cur-h/2, 0, n-h)
}

// Use terminal column width so Unicode and emoji cannot shift borders.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = strings.ReplaceAll(s, "\t", "    ")
	if runewidth.StringWidth(s) > w {
		s = runewidth.Truncate(s, w, "…")
	}
	// Truncating before a wide rune can leave one column that still needs padding.
	if n := runewidth.StringWidth(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func valueLines(v string) []string {
	if v == "" {
		return nil
	}
	v = strings.ReplaceAll(v, "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(v, "\n"), "\n")
	for i, l := range lines {
		lines[i] = stripControl(l)
	}
	return lines
}

// Remove terminal controls from untrusted values while preserving tabs.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\t' && (r < 0x20 || r == 0x7f) {
			return -1
		}
		return r
	}, s)
}

// Use coarse time because the history pane is narrow; lsctl history remains exact.
func relTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	switch d := time.Since(t); {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Local().Format("01-02")
	}
}
