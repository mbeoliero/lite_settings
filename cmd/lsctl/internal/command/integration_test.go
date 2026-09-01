package command

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/backend"
	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/backendtest"
	"github.com/mbeoliero/lite_settings/internal/testdb"
	"github.com/mbeoliero/lite_settings/store"
)

// dbSuffix names this package's test database; it must be unique.
const dbSuffix = "lsctl_cmd"

func newTestRoot(t *testing.T, st *store.DB, mode string) *root {
	t.Helper()
	return &root{author: backendtest.Author, timeout: backendtest.Timeout,
		be: backendtest.Open(t, st, mode)}
}

func each(t *testing.T, fn func(t *testing.T, r *root)) {
	t.Helper()
	backendtest.Each(t, dbSuffix, func(t *testing.T, be backend.Backend) {
		fn(t, &root{author: backendtest.Author, timeout: backendtest.Timeout, be: be})
	})
}

// run uses a flagged parent so persistent flags follow the production path.
func run(t *testing.T, r *root, cmd *cobra.Command, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	parent := &cobra.Command{Use: "lsctl", SilenceUsage: true, SilenceErrors: true}
	// Restore test values overwritten by production flag defaults.
	author, timeout := r.author, r.timeout
	r.addGlobalFlags(parent)
	r.author, r.timeout = author, timeout

	var out, errb bytes.Buffer
	parent.AddCommand(cmd)
	parent.SetOut(&out)
	parent.SetErr(&errb)
	parent.SetArgs(append([]string{cmd.Name()}, args...))
	parent.SetContext(t.Context())
	err = parent.Execute()
	return out.String(), errb.String(), err
}

func seed(t *testing.T, r *root, key, value, format string) {
	t.Helper()
	backendtest.Seed(t, r.be, key, value, format)
}

func TestIntegrationSetGetRoundTrip(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		if _, _, err := run(t, r, r.newSetCmd(), "app:timeout", "30s"); err != nil {
			t.Fatalf("set: %v", err)
		}

		out, _, err := run(t, r, r.newGetCmd(), "app:timeout")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if out != "30s\n" {
			t.Fatalf("get output %q, want %q", out, "30s\n")
		}
	})
}

func TestIntegrationSetMultilinePreservesValueExactly(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		// Trailing newlines, indentation, and blank lines must round-trip
		// verbatim: in YAML they all carry meaning.
		const doc = "model: gpt\nprompts:\n  - a\n\n  - b\n"
		seed(t, r, "prompt_group:main", doc, "yaml")

		out, _, err := run(t, r, r.newGetCmd(), "prompt_group:main")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if out != doc {
			t.Fatalf("value changed\ngot %q\nwant %q", out, doc)
		}
	})
}

func TestIntegrationGetNotFoundIsSentinel(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		_, _, err := run(t, r, r.newGetCmd(), "nope:missing")
		if !errors.Is(err, backend.ErrNotFound) {
			t.Fatalf("both backends should return backend.ErrNotFound, got %v", err)
		}
	})
}

func TestIntegrationListPrefix(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		seed(t, r, "a:one", "1", "raw")
		seed(t, r, "a:two", "2", "raw")
		seed(t, r, "b:three", "3", "raw")

		out, _, err := run(t, r, r.newListCmd(), "-o", "raw", "a:")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if out != "a:one\na:two\n" {
			t.Fatalf("wrong prefix filter result: %q", out)
		}

		// Deliberately overlapping prefixes: the result must be deduplicated
		// and sorted.
		out, _, err = run(t, r, r.newListCmd(), "-o", "raw", "a:", "a:o", "b:")
		if err != nil {
			t.Fatalf("list multiple prefixes: %v", err)
		}
		if out != "a:one\na:two\nb:three\n" {
			t.Fatalf("wrong multiple-prefix result: %q", out)
		}
	})
}

func TestIntegrationListAllWhenNoPrefix(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		seed(t, r, "a:one", "1", "raw")
		seed(t, r, "b:two", "2", "raw")

		out, _, err := run(t, r, r.newListCmd(), "-o", "raw")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if out != "a:one\nb:two\n" {
			t.Fatalf("no prefix should list all configurations: %q", out)
		}
	})
}

func TestIntegrationHistoryAndRollback(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		seed(t, r, "app:cfg", "v1", "raw")
		seed(t, r, "app:cfg", "v2", "raw")
		seed(t, r, "app:cfg", "v3", "raw")

		out, _, err := run(t, r, r.newHistoryCmd(), "-o", "raw", "app:cfg")
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if out != "3\n2\n1\n" {
			t.Fatalf("wrong history versions: %q", out)
		}

		if _, _, err := run(t, r, r.newRollbackCmd(), "app:cfg", "--to", "1"); err != nil {
			t.Fatalf("rollback: %v", err)
		}

		got, _, err := run(t, r, r.newGetCmd(), "app:cfg")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got != "v1\n" {
			t.Fatalf("wrong value after rollback: %q", got)
		}

		// A rollback creates a new version; history is never erased.
		out, _, err = run(t, r, r.newHistoryCmd(), "-o", "raw", "app:cfg")
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if out != "4\n3\n2\n1\n" {
			t.Fatalf("wrong history after rollback: %q", out)
		}
	})
}

func TestIntegrationDeleteThenRollbackRestores(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		seed(t, r, "app:gone", "important", "raw")

		_, stderr, err := run(t, r, r.newRmCmd(), "app:gone")
		if err != nil {
			t.Fatalf("rm: %v", err)
		}
		// rm does not prompt for confirmation, so it must print how to undo.
		if !strings.Contains(stderr, "lsctl rollback app:gone --to") {
			t.Fatalf("rm did not print an undo command: %q", stderr)
		}

		if _, _, err := run(t, r, r.newGetCmd(), "app:gone"); !errors.Is(err, backend.ErrNotFound) {
			t.Fatalf("deleted key should not be found, got %v", err)
		}

		if _, _, err := run(t, r, r.newRollbackCmd(), "app:gone", "--to", "1"); err != nil {
			t.Fatalf("rollback: %v", err)
		}
		got, _, err := run(t, r, r.newGetCmd(), "app:gone")
		if err != nil || got != "important\n" {
			t.Fatalf("deleted value should be recoverable, got (%q, %v)", got, err)
		}
	})
}

func TestIntegrationDiffBetweenVersions(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		seed(t, r, "app:cfg", "a\nb\nc\n", "yaml")
		seed(t, r, "app:cfg", "a\nB\nc\n", "yaml")

		out, _, err := run(t, r, r.newDiffCmd(), "app:cfg", "1", "2")
		if err != nil {
			t.Fatalf("diff: %v", err)
		}
		if !strings.Contains(out, "-b") || !strings.Contains(out, "+B") {
			t.Fatalf("wrong diff:\n%s", out)
		}

		out, _, err = run(t, r, r.newDiffCmd(), "app:cfg", "1")
		if err != nil {
			t.Fatalf("diff with two arguments: %v", err)
		}
		if !strings.Contains(out, "+B") {
			t.Fatalf("wrong diff against current value:\n%s", out)
		}
	})
}

func TestIntegrationDiffFindsOldVersionBeyondDefaultLimit(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		// The server's history returns 100 rows by default. A version named
		// explicitly must still be found when it falls outside that window.
		for i := range 105 {
			seed(t, r, "app:churn", fmt.Sprintf("v%d", i), "raw")
		}
		out, _, err := run(t, r, r.newDiffCmd(), "app:churn", "1", "2")
		if err != nil {
			t.Fatalf("diff old version: %v", err)
		}
		if !strings.Contains(out, "-v0") || !strings.Contains(out, "+v1") {
			t.Fatalf("wrong diff:\n%s", out)
		}
	})
}

func TestIntegrationDiffUnknownVersionReportsRange(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		seed(t, r, "app:cfg", "x", "raw")

		_, _, err := run(t, r, r.newDiffCmd(), "app:cfg", "9")
		if err == nil {
			t.Fatal("missing version should return an error")
		}
		if !strings.Contains(err.Error(), "version 9") {
			t.Fatalf("error should identify the version: %v", err)
		}
		if !errors.Is(err, backend.ErrNotFound) {
			t.Fatalf("a missing version is not-found, so it must exit 4: %v", err)
		}
	})
}

func TestIntegrationSetDryRunDoesNotWrite(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		seed(t, r, "app:cfg", "old\n", "raw")

		out, _, err := run(t, r, r.newSetCmd(), "app:cfg", "new\n", "--dry-run")
		if err != nil {
			t.Fatalf("dry-run: %v", err)
		}
		if !strings.Contains(out, "-old") || !strings.Contains(out, "+new") {
			t.Fatalf("wrong preview diff:\n%s", out)
		}

		got, _, err := run(t, r, r.newGetCmd(), "app:cfg")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got != "old\n" {
			t.Fatalf("--dry-run should not write, current value is %q", got)
		}
	})
}

func TestIntegrationSetDryRunOnNewKey(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		// A preview must work against a missing target rather than report
		// not found.
		out, _, err := run(t, r, r.newSetCmd(), "app:brand-new", "hello", "--dry-run")
		if err != nil {
			t.Fatalf("dry-run new key: %v", err)
		}
		if !strings.Contains(out, "+hello") {
			t.Fatalf("wrong preview diff:\n%s", out)
		}
		if _, _, err := run(t, r, r.newGetCmd(), "app:brand-new"); !errors.Is(err, backend.ErrNotFound) {
			t.Fatalf("--dry-run should not create a key, got %v", err)
		}
	})
}

func TestIntegrationSetRecordsAuthorAndComment(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		if _, _, err := run(t, r, r.newSetCmd(), "app:cfg", "v", "-m", "调整超时"); err != nil {
			t.Fatalf("set: %v", err)
		}
		out, _, err := run(t, r, r.newHistoryCmd(), "app:cfg")
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if !strings.Contains(out, "tester") || !strings.Contains(out, "调整超时") {
			t.Fatalf("audit data was not stored:\n%s", out)
		}
	})
}

func TestIntegrationSetFromStdinInfersFormat(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		cmd := r.newSetCmd()
		cmd.SetIn(strings.NewReader("a: 1\nb: 2\n"))
		if _, _, err := run(t, r, cmd, "prompt:main", "-f", "-"); err != nil {
			t.Fatalf("set: %v", err)
		}

		out, _, err := run(t, r, r.newGetCmd(), "-o", "json", "prompt:main")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if !strings.Contains(out, `"format": "yaml"`) {
			t.Fatalf("multiline key-value input should infer yaml:\n%s", out)
		}
	})
}

func TestIntegrationWriteResultGoesToStderr(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		stdout, stderr, err := run(t, r, r.newSetCmd(), "app:cfg", "v")
		if err != nil {
			t.Fatalf("set: %v", err)
		}
		// stdout stays clean; write receipts go to stderr.
		if stdout != "" {
			t.Fatalf("set should not write to stdout: %q", stdout)
		}
		if !strings.Contains(stderr, "version=1") || !strings.Contains(stderr, "revision=") {
			t.Fatalf("incomplete write receipt: %q", stderr)
		}
	})
}

func TestIntegrationJSONOutputIsMachineReadable(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		seed(t, r, "a:one", "1", "raw")

		out, _, err := run(t, r, r.newListCmd(), "-o", "json", "a:")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, want := range []string{`"count": 1`, `"key": "a:one"`, `"value": "1"`} {
			if !strings.Contains(out, want) {
				t.Fatalf("JSON output is missing %s:\n%s", want, out)
			}
		}
	})
}

func TestIntegrationGetSpecificVersion(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		seed(t, r, "app:cfg", "v1", "raw")
		seed(t, r, "app:cfg", "v2", "raw")

		out, _, err := run(t, r, r.newGetCmd(), "app:cfg", "--version", "1")
		if err != nil {
			t.Fatalf("get --version: %v", err)
		}
		if out != "v1\n" {
			t.Fatalf("wrong historical version: %q", out)
		}

		if _, _, err := run(t, r, r.newGetCmd(), "app:cfg", "--version", "99"); !errors.Is(err, backend.ErrNotFound) {
			t.Fatalf("a missing version is not-found, so it must exit 4: %v", err)
		}
	})
}

func TestIntegrationHistoryOfMissingKeyIsNotFound(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	each(t, func(t *testing.T, r *root) {
		// The server returns 404 for empty history and the DB backend must
		// match, or the same command exits differently per backend.
		_, _, err := run(t, r, r.newHistoryCmd(), "nope:missing")
		if !errors.Is(err, backend.ErrNotFound) {
			t.Fatalf("got %v", err)
		}
	})
}

func TestIntegrationMigrateOnlyOnDBBackend(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	for dbName, db := range testdb.Backends(t, dbSuffix) {
		t.Run(dbName, func(t *testing.T) {
			t.Parallel()

			st := testdb.Fresh(t, db)

			r := newTestRoot(t, st, "db")
			if _, _, err := run(t, r, r.newMigrateCmd()); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			if _, _, err := run(t, r, r.newMigrateCmd()); err != nil {
				t.Fatalf("repeated migrate should be idempotent: %v", err)
			}

			// HTTP must explain that migration requires a direct database.
			rh := newTestRoot(t, st, "http")
			_, _, err := run(t, rh, rh.newMigrateCmd())
			if !errors.Is(err, backend.ErrNotSupported) {
				t.Fatalf("HTTP backend should reject migrate, got %v", err)
			}
			if !strings.Contains(err.Error(), "--dsn") {
				t.Fatalf("error should explain what to do: %v", err)
			}
		})
	}
}

func TestIntegrationBothBackendsAgreeOnList(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	for dbName, db := range testdb.Backends(t, dbSuffix) {
		t.Run(dbName, func(t *testing.T) {
			t.Parallel()

			st := testdb.Fresh(t, db)
			rdb := newTestRoot(t, st, "db")
			rhttp := newTestRoot(t, st, "http")

			for i := range 5 {
				seed(t, rdb, fmt.Sprintf("k:%02d", i), fmt.Sprintf("v%d", i), "raw")
			}

			// Both backends must match byte-for-byte, including order and deduplication.
			a, _, err := run(t, rdb, rdb.newListCmd(), "-o", "json", "k:")
			if err != nil {
				t.Fatalf("db list: %v", err)
			}
			b, _, err := run(t, rhttp, rhttp.newListCmd(), "-o", "json", "k:")
			if err != nil {
				t.Fatalf("http list: %v", err)
			}
			if a != b {
				t.Fatalf("backend output differs\ndb:\n%s\nhttp:\n%s", a, b)
			}
		})
	}
}

func TestIntegrationServerErrorIsNotMistakenForNotFound(t *testing.T) {
	// This package's integration tests share one database suffix and must run serially.

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// A 500 must not become not-found and trigger creation during an outage.
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"db down"}`, http.StatusInternalServerError)
	}))
	defer hs.Close()

	r := &root{author: "tester", timeout: 5 * time.Second}
	r.be = backend.OpenHTTP(hs.URL, hs.Client())

	_, _, err := run(t, r, r.newGetCmd(), "any:key")
	if err == nil {
		t.Fatal("500 should return an error")
	}
	if errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("500 should not be treated as not found: %v", err)
	}
	if !strings.Contains(err.Error(), "db down") {
		t.Fatalf("server error message should be preserved: %v", err)
	}
}
