package render

import (
	"strings"
	"testing"
	"time"

	"github.com/mbeoliero/lite_settings/server/api"
)

func TestOneLineCollapsesMultiline(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	got := OneLine("first\nsecond\nthird", 40)
	if got != "first …" {
		t.Fatalf("got %q", got)
	}
}

func TestOneLineTruncates(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	got := OneLine(strings.Repeat("x", 100), 10)
	if got != strings.Repeat("x", 10)+"…" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateIsRuneSafe(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// Rune boundaries prevent invalid multibyte output.
	got := Truncate("中文配置内容", 3)
	if got != "中文配…" {
		t.Fatalf("got %q", got)
	}
}

func TestHumanSize(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	cases := map[int]string{0: "0B", 512: "512B", 2048: "2.0K", 3 << 20: "3.0M"}
	for n, want := range cases {
		if got := HumanSize(n); got != want {
			t.Errorf("HumanSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestLocalTimeZeroIsDash(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	if got := LocalTime(time.Time{}); got != "-" {
		t.Fatalf("zero time should render as -, got %q", got)
	}
}

func TestTableAligns(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	var sb strings.Builder
	tb := NewTable(&sb, "KEY", "N")
	tb.Row("a", "1")
	tb.Row("longer", "2")
	if err := tb.Flush(); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimRight(sb.String(), "\n"), "\n") {
		if !strings.Contains(line, "  ") {
			t.Fatalf("columns are not aligned: %q", line)
		}
	}
}

func TestEmitJSONIndentsAndEndsWithNewline(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	var sb strings.Builder
	if err := EmitJSON(&sb, map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(sb.String(), "\n") {
		t.Fatalf("output should end with a newline: %q", sb.String())
	}
	if !strings.Contains(sb.String(), "\n  \"a\"") {
		t.Fatalf("output should use two-space indentation: %q", sb.String())
	}
}

func TestEmitJSONIsByteStable(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// Named structs keep JSON byte order stable for scripts and golden files.
	v := ListOutput{
		Configs: []api.Config{
			{Key: "a", Value: "1", Format: "raw"},
			{Key: "b", Value: "2", Format: "json"},
		},
		Count: 2,
	}

	var first strings.Builder
	if err := EmitJSON(&first, v); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		var sb strings.Builder
		if err := EmitJSON(&sb, v); err != nil {
			t.Fatal(err)
		}
		if sb.String() != first.String() {
			t.Fatalf("unstable output\nfirst:\n%s\ncurrent:\n%s", first.String(), sb.String())
		}
	}
	// Pin configs before count.
	if i, j := strings.Index(first.String(), `"configs"`), strings.Index(first.String(), `"count"`); i > j {
		t.Fatalf("wrong field order:\n%s", first.String())
	}
}

func TestWriteOutputOmitsEmptyFormat(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	var sb strings.Builder
	if err := EmitJSON(&sb, WriteOutput{Key: "k", Action: "deleted", Version: 2, Revision: 9}); err != nil {
		t.Fatal(err)
	}
	// Omit format for rm and rollback rather than mislead scripts.
	if strings.Contains(sb.String(), "format") {
		t.Fatalf("empty format should be omitted:\n%s", sb.String())
	}
}
