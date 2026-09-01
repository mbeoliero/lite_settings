package store_test

import (
	"errors"
	"testing"

	"github.com/mbeoliero/lite_settings/store"
)

func TestNormalizeDSN(t *testing.T) {
	t.Parallel()

	type testBundle struct {
		cases []struct{ name, driver, in, want string }
	}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{cases: []struct{ name, driver, in, want string }{
			{"AppendsToBareDSN", "mysql",
				"root:pw@tcp(127.0.0.1:3306)/lite",
				"root:pw@tcp(127.0.0.1:3306)/lite?parseTime=true"},
			{"AppendsToExistingParams", "mysql",
				"root:pw@tcp(127.0.0.1:3306)/lite?charset=utf8mb4",
				"root:pw@tcp(127.0.0.1:3306)/lite?charset=utf8mb4&parseTime=true"},
			{"KeepsIdempotent", "mysql",
				"root:pw@tcp(h:3306)/lite?parseTime=true",
				"root:pw@tcp(h:3306)/lite?parseTime=true"},
			{"LeavesPostgresAlone", "pgx",
				"postgres://u:p@h:5432/lite?sslmode=disable",
				"postgres://u:p@h:5432/lite?sslmode=disable"},
			{"OverridesExplicitFalse", "mysql",
				"root:pw@tcp(h:3306)/lite?parseTime=false&loc=UTC",
				"root:pw@tcp(h:3306)/lite?loc=UTC&parseTime=true"},
			{"PasswordWithSlashUnharmed", "mysql",
				"root:a/b?c@tcp(h:3306)/lite",
				"root:a/b?c@tcp(h:3306)/lite?parseTime=true"},
		}}
	}

	for _, tc := range setup(t).cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := store.NormalizeDSN(tc.driver, tc.in); got != tc.want {
				t.Errorf("NormalizeDSN(%q, %q) = %q, want %q", tc.driver, tc.in, got, tc.want)
			}
		})
	}
}

func TestSQLDriver(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	t.Run("Aliases", func(t *testing.T) {
		t.Parallel()

		for in, want := range map[string]string{
			"mysql":      "mysql",
			"pgx":        "pgx",
			"pgx/v5":     "pgx",
			"postgres":   "pgx",
			"postgresql": "pgx",
		} {
			got, err := store.SQLDriver(in)
			if err != nil || got != want {
				t.Errorf("SQLDriver(%q) = %q, %v, want %q, nil", in, got, err, want)
			}
		}
	})

	t.Run("UnknownIsRejected", func(t *testing.T) {
		t.Parallel()

		if _, err := store.SQLDriver("sqlite"); !errors.Is(err, store.ErrUnsupportedDriver) {
			t.Errorf("expected ErrUnsupportedDriver, got %v", err)
		}
	})
}
