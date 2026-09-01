package render

import (
	"slices"
	"strings"
	"testing"
)

func TestUnifiedDiffIdenticalIsEmpty(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	if d := UnifiedDiff("a\nb\n", "a\nb\n", "x", "y", 3); d != "" {
		t.Fatalf("identical content should have no diff, got:\n%s", d)
	}
}

func TestUnifiedDiffTrailingNewlineIsNotAChange(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// Ignore editors' trailing-newline differences.
	if d := UnifiedDiff("a\nb", "a\nb\n", "x", "y", 3); d != "" {
		t.Fatalf("a trailing newline alone should not produce a diff, got:\n%s", d)
	}
}

func TestUnifiedDiffModifyLine(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	d := UnifiedDiff("a\nb\nc\n", "a\nB\nc\n", "old", "new", 1)
	want := "--- old\n+++ new\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"
	if d != want {
		t.Fatalf("wrong diff\ngot:\n%s\nwant:\n%s", d, want)
	}
}

func TestUnifiedDiffFromEmpty(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	d := UnifiedDiff("", "x\ny\n", "empty", "new", 3)
	if !strings.Contains(d, "@@ -0,0 +1,2 @@") {
		t.Fatalf("wrong empty-to-nonempty header:\n%s", d)
	}
	if !strings.Contains(d, "+x\n+y\n") {
		t.Fatalf("all lines should be additions:\n%s", d)
	}
}

func TestUnifiedDiffToEmpty(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	d := UnifiedDiff("x\ny\n", "", "old", "empty", 3)
	if !strings.Contains(d, "@@ -1,2 +0,0 @@") {
		t.Fatalf("wrong nonempty-to-empty header:\n%s", d)
	}
}

func TestUnifiedDiffContextLimitsOutput(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// Context must not expand a small change to the whole file.
	a := slices.Repeat([]string{"line"}, 100)
	b := slices.Clone(a)
	b[50] = "changed"
	d := UnifiedDiff(strings.Join(a, "\n"), strings.Join(b, "\n"), "o", "n", 2)

	if n := strings.Count(d, "\n"); n > 12 {
		t.Fatalf("context was not limited; output has %d lines:\n%s", n, d)
	}
	if !strings.Contains(d, "+changed") {
		t.Fatalf("changed line is missing:\n%s", d)
	}
}

func TestUnifiedDiffSplitsIntoMultipleHunks(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	a := strings.Repeat("x\n", 40)
	b := "A\n" + strings.Repeat("x\n", 38) + "B\n"
	d := UnifiedDiff(a, b, "o", "n", 1)

	if n := strings.Count(d, "@@ -"); n != 2 {
		t.Fatalf("changes at both ends should produce 2 hunks, got %d:\n%s", n, d)
	}
}

func TestUnifiedDiffHugeInputFallsBack(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// Above the LCS limit, delete/add fallback must remain safe.
	n := 3000 // 3000*3000 = 9e6 > maxDiffCells
	a := strings.Repeat("a\n", n)
	b := strings.Repeat("b\n", n)

	d := UnifiedDiff(a, b, "o", "n", 0)
	if !strings.Contains(d, "-a") || !strings.Contains(d, "+b") {
		t.Fatalf("fallback output is incomplete")
	}
}

func TestDiffLinesTrimsCommonAffixes(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	ops := diffLines([]string{"h", "a", "t"}, []string{"h", "b", "t"})
	var got string
	for _, o := range ops {
		got += string(o.kind) + o.text + ";"
	}
	if got != " h;-a;+b; t;" {
		t.Fatalf("wrong operation sequence: %s", got)
	}
}

func TestSpanEmptyRange(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	if s := span(1, 0); s != "0,0" {
		t.Fatalf("empty range should fall back one line, got %s", s)
	}
	if s := span(5, 1); s != "5" {
		t.Fatalf("single-line range should omit the count, got %s", s)
	}
}
