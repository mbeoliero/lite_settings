package backend

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestHTTPListPreservesEmptyPrefix(t *testing.T) {
	t.Parallel()

	type testBundle struct {
		backend  Backend
		prefixes <-chan []string
	}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		prefixes := make(chan []string, 1)
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			prefixes <- r.URL.Query()["prefix"]
			fmt.Fprint(w, `{"revision":0,"configs":[]}`)
		}))
		t.Cleanup(ts.Close)

		return &testBundle{
			backend:  OpenHTTP(ts.URL, ts.Client()),
			prefixes: prefixes,
		}
	}

	bundle := setup(t)
	if _, err := bundle.backend.List(t.Context(), []string{"a:", ""}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := <-bundle.prefixes; !slices.Equal(got, []string{"a:", ""}) {
		t.Fatalf("prefix query = %q, want [a: empty]", got)
	}
}
