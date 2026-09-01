package lite

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func snapOf(rev int64, kv ...string) *view {
	if len(kv)%2 != 0 {
		panic("snapOf requires key/value pairs")
	}
	return newView(apiSnap(rev, kv...))
}

func TestSnapshotKeysSorted(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	s := snapOf(1, "b:2", "x", "a:1", "x", "c:3", "x")
	if got := s.keys; !slices.Equal(got, []string{"a:1", "b:2", "c:3"}) {
		t.Fatalf("keys not sorted: %v", got)
	}
}

func TestSnapshotPrefixRange(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// "prompt_group:" is itself a key and "zz:last" sorts after the run, so
	// both bounds have a neighbour to get wrong.
	s := snapOf(1, "prompt_group:main", "x", "prompt_group:sub", "x",
		"prompt_group:", "x", "feature:debug", "x", "zz:last", "x")

	lo, hi := s.prefixRange("prompt_group:")
	if got := s.keys[lo:hi]; !slices.Equal(got, []string{"prompt_group:", "prompt_group:main", "prompt_group:sub"}) {
		t.Fatalf("incorrect prefix range: %v", got)
	}

	if lo, hi := s.prefixRange(""); lo != 0 || hi != len(s.keys) {
		t.Fatalf("empty prefix should span every entry; got [%d,%d)", lo, hi)
	}
	if lo, hi := s.prefixRange("nothing:"); lo != hi {
		t.Fatalf("absent prefix should be empty; got [%d,%d)", lo, hi)
	}
}

func TestDiffDetectsAddModifyDelete(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	old := snapOf(1, "a", "1", "b", "2", "c", "3")
	new := snapOf(2, "a", "1", "b", "CHANGED", "d", "4") // c removed, d added

	got := diff(old, new)
	want := []string{"b", "c", "d"}
	if !slices.Equal(got, want) {
		t.Fatalf("diff = %v, want %v", got, want)
	}
}

// Format changes matter because identical text can decode differently.
func TestDiffDetectsFormatChange(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	old := newView(&Snapshot{Revision: 1, Configs: []Config{{Key: "a", Value: "1", Format: FormatRaw}}})
	new := newView(&Snapshot{Revision: 2, Configs: []Config{{Key: "a", Value: "1", Format: FormatJSON}}})

	if got := diff(old, new); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("format change should be detected; diff = %v", got)
	}
}

func TestDiffEmptyWhenIdentical(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	if got := diff(snapOf(1, "a", "1"), snapOf(99, "a", "1")); len(got) != 0 {
		t.Fatalf("diff should be empty for identical content; got %v", got)
	}
}

func TestCacheRoundtrip(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	path := filepath.Join(t.TempDir(), "nested", "snap.json")
	want := snapOf(42, "prompt_group:main", "hello", "feature:debug", "true")

	if err := writeFallback(path, want); err != nil {
		t.Fatalf("writeFallback: %v", err)
	}
	got, err := readFallback(path)
	if err != nil {
		t.Fatalf("readFallback: %v", err)
	}
	if got.revision != 42 || len(got.byKey) != 2 || got.byKey["feature:debug"].Value != "true" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

// Interrupted fallback writes must leave the previous complete file intact.
func TestCacheSaveIsAtomic(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")

	if err := writeFallback(path, snapOf(1, "a", "old")); err != nil {
		t.Fatal(err)
	}
	if err := writeFallback(path, snapOf(2, "a", "new")); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temporary file remains: %s", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Fatalf("expected one file; got %d", len(entries))
	}

	got, err := readFallback(path)
	if err != nil || got.revision != 2 || got.byKey["a"].Value != "new" {
		t.Fatalf("unexpected contents after overwrite: %+v err %v", got, err)
	}
}

func TestLoadCacheRejectsGarbage(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	path := filepath.Join(t.TempDir(), "snap.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readFallback(path); err == nil {
		t.Fatal("corrupt cache should fail, not pass as an empty config")
	}
}

func TestNormalizePrefixes(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	if got := normalizePrefixes([]string{"a:", "b:", "a:"}); !slices.Equal(got, []string{"a:", "b:"}) {
		t.Fatalf("deduplication failed: %v", got)
	}
	// An empty prefix subsumes all others.
	if got := normalizePrefixes([]string{"a:", ""}); got != nil {
		t.Fatalf("an empty prefix should collapse to all entries (nil); got %v", got)
	}
	if got := normalizePrefixes(nil); got != nil {
		t.Fatalf("nil should remain nil; got %v", got)
	}
}

func TestFilterPrefix(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	keys := []string{"a:1", "a:2", "b:1"}
	if got := filterPrefix(keys, "a:"); !slices.Equal(got, []string{"a:1", "a:2"}) {
		t.Fatalf("filterPrefix = %v", got)
	}
	if got := filterPrefix(keys, ""); !slices.Equal(got, keys) {
		t.Fatalf("empty prefix should be returned unchanged; got %v", got)
	}
}
