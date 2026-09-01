package backend

import "testing"

func TestDriverForDSN(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// Shared store.SQLDriver validation prevents the two binaries from drifting.
	cases := map[string]string{
		"postgres://lite:lite@127.0.0.1:5432/db?sslmode=disable":     "pgx",
		"postgresql://lite@127.0.0.1/db":                             "pgx",
		"host=127.0.0.1 user=lite dbname=db":                         "pgx",
		"root:lite@tcp(127.0.0.1:3306)/lite_settings?parseTime=true": "mysql",
		"user:pw@/db": "mysql",
	}
	for dsn, want := range cases {
		if got := driverForDSN(dsn); got != want {
			t.Errorf("driverForDSN(%q) = %q, want %q", dsn, got, want)
		}
	}
}
