package lite

import "testing"

type payload struct {
	N int `json:"n"`
}

func TestDecodeCacheReturnsSameResultWithinSnapshot(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	v := newView(&Snapshot{Revision: 1, Configs: []Config{
		{Key: "a:x", Value: `{"n":1}`, Format: FormatJSON},
	}})
	g := Group{prefix: "a:", snap: v}

	first, err := g.Get[*payload]("x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	second, err := g.Get[*payload]("x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Pointer results are shared within a snapshot and therefore read-only.
	if first != second {
		t.Error("two Get calls on the same snapshot should hit the cache")
	}
}

func TestDecodeCacheIsKeyedByType(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	v := newView(&Snapshot{Revision: 1, Configs: []Config{
		{Key: "a:x", Value: "42", Format: FormatRaw},
	}})
	g := Group{prefix: "a:", snap: v}

	if got, err := g.Get[int]("x"); err != nil || got != 42 {
		t.Fatalf("Get[int] = (%v, %v)", got, err)
	}
	// Cache entries are isolated by target type.
	if got, err := g.Get[string]("x"); err != nil || got != "42" {
		t.Fatalf("Get[string] = (%q, %v)", got, err)
	}
}

func TestDecodeCacheCachesFailures(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	v := newView(&Snapshot{Revision: 1, Configs: []Config{
		{Key: "a:x", Value: "not a number", Format: FormatRaw},
	}})
	g := Group{prefix: "a:", snap: v}

	for range 2 {
		got, err := g.Get[int]("x")
		if err == nil {
			t.Fatal("expected decode failure")
		}
		if got != 0 {
			t.Errorf("decode failure should return the zero value; got %d", got)
		}
	}
}

func TestNewSnapshotDropsDecodeCache(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	src := &scriptedSource{steps: []scriptStep{
		{snap: &Snapshot{Revision: 1, Configs: []Config{
			{Key: "a:x", Value: `{"n":1}`, Format: FormatJSON}}}},
		{snap: &Snapshot{Revision: 2, Configs: []Config{
			{Key: "a:x", Value: `{"n":2}`, Format: FormatJSON}}}},
	}}
	c, err := New(src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	if got, err := c.Get[payload]("a:x"); err != nil || got.N != 1 {
		t.Fatalf("initial snapshot = (%+v, %v)", got, err)
	}
	waitFor(t, func() bool { return c.Revision() == 2 })

	got, err := c.Get[payload]("a:x")
	if err != nil || got.N != 2 {
		t.Fatalf("new snapshot should decode the new value; got (%+v, %v)", got, err)
	}
}
