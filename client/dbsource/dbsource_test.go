package dbsource_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	lite "github.com/mbeoliero/lite_settings/client"
	"github.com/mbeoliero/lite_settings/client/dbsource"
)

func TestPollReportsCanceledQuery(t *testing.T) {
	t.Parallel()

	type testBundle struct {
		source *dbsource.Source
	}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		db, err := sql.Open("mysql", "root@tcp(127.0.0.1:1)/lite_settings")
		if err != nil {
			t.Fatalf("sql.Open: %v", err)
		}
		t.Cleanup(func() { db.Close() })

		source, err := dbsource.New(db, "mysql")
		if err != nil {
			t.Fatalf("dbsource.New: %v", err)
		}
		return &testBundle{source: source}
	}

	bundle := setup(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := bundle.source.Poll(ctx, lite.PollRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Poll error = %v, want context.Canceled", err)
	}
}
