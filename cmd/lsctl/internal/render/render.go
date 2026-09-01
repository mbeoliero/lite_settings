// Package render formats lsctl results as tables, JSON, or unified diffs.
package render

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mbeoliero/lite_settings/server/api"
)

// Output formats. The empty value means "this command's own default".
const (
	OutTable = "table"
	OutJSON  = "json"
	OutRaw   = "raw"
)

// ValidOutput reports whether s is a format or the empty command default.
func ValidOutput(s string) bool {
	return slices.Contains([]string{"", OutTable, OutJSON, OutRaw}, s)
}

// EmitJSON writes indented JSON with a trailing newline.
func EmitJSON(w io.Writer, v any) error {
	if err := jsonv2.MarshalWrite(w, v, jsontext.WithIndent("  ")); err != nil {
		return err
	}
	_, err := fmt.Fprintln(w)
	return err
}

// Table is a minimal table renderer over tabwriter.
type Table struct {
	tw *tabwriter.Writer
}

// NewTable starts a table, omitting the header row when none are provided.
func NewTable(w io.Writer, headers ...string) *Table {
	t := &Table{tw: tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)}
	if len(headers) > 0 {
		fmt.Fprintln(t.tw, strings.Join(headers, "\t"))
	}
	return t
}

// Row buffers one row. Nothing reaches w until Flush.
func (t *Table) Row(cells ...string) {
	fmt.Fprintln(t.tw, strings.Join(cells, "\t"))
}

// Flush writes the buffered rows, aligning the columns.
func (t *Table) Flush() error { return t.tw.Flush() }

// OneLine makes document values recognizable without breaking table rows.
func OneLine(s string, limit int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i]) + " …"
	}
	return Truncate(s, limit)
}

// Truncate cuts on rune boundaries, so multibyte characters survive.
func Truncate(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}

// HumanSize renders a byte count for humans.
func HumanSize(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fK", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/(1024*1024))
	}
}

// LocalTime renders audit timestamps in the reader's local zone.
func LocalTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// Named JSON structs keep field order stable for scripts and golden files.
// ListOutput is the shape of `lsctl list -o json`.
type ListOutput struct {
	Configs []api.Config `json:"configs"`
	Count   int          `json:"count"`
}

// HistoryOutput is the shape of `lsctl history -o json`.
type HistoryOutput struct {
	Key     string             `json:"key"`
	History []api.HistoryEntry `json:"history"`
}

// DiffOutput is the shape of `lsctl diff -o json`.
type DiffOutput struct {
	Key       string `json:"key"`
	From      string `json:"from"`
	To        string `json:"to"`
	Identical bool   `json:"identical"`
	Diff      string `json:"diff"`
}

// DryRunOutput is the shape of a `--dry-run -o json` write.
type DryRunOutput struct {
	Key     string `json:"key"`
	Format  string `json:"format"`
	Exists  bool   `json:"exists"`
	Changed bool   `json:"changed"`
	Diff    string `json:"diff"`
	DryRun  bool   `json:"dry_run"`
}

// WriteOutput is the receipt of a write with -o json.
type WriteOutput struct {
	Key      string `json:"key"`
	Action   string `json:"action"`
	Format   string `json:"format,omitzero"`
	Version  int64  `json:"version"`
	Revision int64  `json:"revision"`
}
