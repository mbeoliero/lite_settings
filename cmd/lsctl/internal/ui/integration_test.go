package ui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/backend"
	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/backendtest"
)

// Keep this suffix unique because the revision watermark is database-wide.
const dbSuffix = "lsctl_ui"

// These verify that TUI writes match non-interactive writes on real backends.
// Command parsing is tested in package command to avoid an import cycle.

func TestIntegrationUIRollbackMatchesRollbackCommand(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	backendtest.Each(t, dbSuffix, func(t *testing.T, be backend.Backend) {
		// Identical histories make the TUI and backend rollback results comparable.
		for _, k := range []string{"cfg:tui", "cfg:cli"} {
			backendtest.Seed(t, be, k, "v1\n", "yaml")
			backendtest.Seed(t, be, k, "v2\n", "yaml")
			backendtest.Seed(t, be, k, "v3\n", "yaml")
		}

		hist, err := be.History(t.Context(), "cfg:tui", -1)
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		first := hist[len(hist)-1].Version // earliest write

		m := boot(t, be)
		if got := m.selectedKey(); got != "cfg:cli" {
			t.Fatalf("initial selection = %q, want first key", got)
		}
		press(t, m, "j")
		if got := m.selectedKey(); got != "cfg:tui" {
			t.Fatalf("selection after moving down = %q, want cfg:tui", got)
		}

		press(t, m, "enter")
		for m.history[m.hcur].Version != first {
			before := m.hcur
			press(t, m, "j")
			if m.hcur == before {
				t.Fatalf("version %d not found in history pane", first)
			}
		}
		press(t, m, "enter")
		if m.pend == nil {
			t.Fatal("confirmation overlay was not shown")
		}
		equiv := m.pend.equiv
		press(t, m, "y")
		if m.errMsg != "" {
			t.Fatalf("rollback failed: %s", m.errMsg)
		}

		want := "lsctl rollback cfg:tui --to " + strconv.FormatInt(first, 10)
		if equiv != want {
			t.Fatalf("equivalent command = %q, want %q", equiv, want)
		}
		if _, err := be.Rollback(t.Context(), "cfg:cli", first,
			backend.Change{Author: backendtest.Author, Comment: "rollback"}); err != nil {
			t.Fatalf("rollback failed: %v", err)
		}

		tui, err := be.Get(t.Context(), "cfg:tui")
		if err != nil {
			t.Fatalf("get cfg:tui: %v", err)
		}
		cli, err := be.Get(t.Context(), "cfg:cli")
		if err != nil {
			t.Fatalf("get cfg:cli: %v", err)
		}
		if tui.Value != "v1\n" {
			t.Fatalf("value after TUI rollback = %q, want %q", tui.Value, "v1\n")
		}
		if tui.Value != cli.Value || tui.Format != cli.Format {
			t.Fatalf("TUI and command rollback results differ: %q/%q vs %q/%q",
				tui.Value, tui.Format, cli.Value, cli.Format)
		}
	})
}

func TestIntegrationUIEditWritesThroughBackend(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	backendtest.Each(t, dbSuffix, func(t *testing.T, be backend.Backend) {
		backendtest.Seed(t, be, "app:cfg", "timeout: 30\n", "yaml")
		m := boot(t, be)

		// ExecProcess delivers the same message after $EDITOR exits.
		_, cmd := m.Update(editedMsg{key: "app:cfg", format: "yaml", value: "timeout: 60\n"})
		dispatch(t, m, cmd)
		if m.pend == nil {
			t.Fatal("changed content must show a diff before confirmation")
		}
		press(t, m, "y")
		if m.errMsg != "" {
			t.Fatalf("write failed: %s", m.errMsg)
		}

		got, err := be.Get(t.Context(), "app:cfg")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Value != "timeout: 60\n" || got.Format != "yaml" {
			t.Fatalf("written value and format = %q/%q", got.Value, got.Format)
		}
		if got.UpdatedBy != "tester" {
			t.Fatalf("author = %q; TUI writes must be audited", got.UpdatedBy)
		}

		hist, err := be.History(t.Context(), "app:cfg", -1)
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if c := hist[0].Comment; !strings.Contains(c, "lsctl ui") {
			t.Fatalf("change comment = %q, want TUI attribution", c)
		}
	})
}

func TestIntegrationUIRefreshPicksUpExternalWrites(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	backendtest.Each(t, dbSuffix, func(t *testing.T, be backend.Backend) {
		backendtest.Seed(t, be, "a:one", "1", "raw")
		m := boot(t, be)
		if len(m.configs) != 1 {
			t.Fatalf("initial config count = %d, want 1", len(m.configs))
		}

		backendtest.Seed(t, be, "a:two", "2", "raw")
		press(t, m, "r")

		if len(m.configs) != 2 {
			t.Fatalf("config count after refresh = %d, want 2", len(m.configs))
		}
		if got := m.selectedKey(); got != "a:one" {
			t.Fatalf("refresh changed selection to %q", got)
		}
	})
}

func TestIntegrationUIShowsRealValueAndHistory(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	backendtest.Each(t, dbSuffix, func(t *testing.T, be backend.Backend) {
		backendtest.Seed(t, be, "prompt_group:main", "system: 你是一个助手\ntemperature: 0.7\n", "yaml")
		backendtest.Seed(t, be, "prompt_group:main", "system: 你是一个助手\ntemperature: 0.9\n", "yaml")
		backendtest.Seed(t, be, "feature:debug", "true", "raw")

		m := boot(t, be)
		rows := m.rows
		if len(rows) != 4 || !rows[0].header || rows[0].label != "feature:" {
			t.Fatalf("keys pane is not grouped by prefix: %+v", rows)
		}

		press(t, m, "j")
		if m.selectedKey() != "prompt_group:main" {
			t.Fatalf("selected key = %q", m.selectedKey())
		}
		if len(m.history) != 2 {
			t.Fatalf("history count = %d, want 2", len(m.history))
		}

		v := m.View()
		for _, want := range []string{"temperature: 0.9", "你是一个助手", "prompt_group:"} {
			if !strings.Contains(v, want) {
				t.Errorf("UI does not contain %q", want)
			}
		}
		// Wide characters occupy two terminal columns and must preserve borders.
		for i, l := range strings.Split(v, "\n") {
			if got := lipgloss.Width(l); got != m.w {
				t.Fatalf("line %d width = %d, want %d", i, got, m.w)
			}
		}

		press(t, m, "enter")
		press(t, m, "j")
		press(t, m, "d")
		if m.mode != modeOverlay {
			t.Fatal("d did not open the diff")
		}
		if dv := m.View(); !strings.Contains(dv, "+temperature: 0.9") {
			t.Fatalf("diff does not show the changed line:\n%s", dv)
		}
	})
}
