package server_test

import (
	jsonv2 "encoding/json/v2"
	"reflect"
	"testing"

	lite "github.com/mbeoliero/lite_settings/client"
	"github.com/mbeoliero/lite_settings/server/api"
)

// Server and client duplicate snapshot types so SDK users avoid server
// dependencies; this round trip replaces their missing compile-time link.
func TestWireCompatibility(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	srv := api.Snapshot{
		Revision: 42,
		Configs: []api.Config{
			{Key: "a:x", Value: "1", Format: "raw"},
			{Key: "a:y", Value: `{"n":2}`, Format: "json"},
		},
	}

	data, err := jsonv2.Marshal(srv)
	if err != nil {
		t.Fatalf("server-side encoding: %v", err)
	}

	var cli lite.Snapshot
	if err := jsonv2.Unmarshal(data, &cli); err != nil {
		t.Fatalf("client-side decoding: %v", err)
	}

	want := lite.Snapshot{
		Revision: 42,
		Configs: []lite.Config{
			{Key: "a:x", Value: "1", Format: "raw"},
			{Key: "a:y", Value: `{"n":2}`, Format: "json"},
		},
	}
	if !reflect.DeepEqual(cli, want) {
		t.Fatalf("round trip mismatch\n got: %+v\nwant: %+v", cli, want)
	}

	// Field counts catch server-only additions ignored by decoding.
	if got, want := reflect.TypeFor[api.Config]().NumField(), reflect.TypeFor[lite.Config]().NumField(); got != want {
		t.Errorf("Config field count differs: server=%d client=%d", got, want)
	}
	if got, want := reflect.TypeFor[api.Snapshot]().NumField(), reflect.TypeFor[lite.Snapshot]().NumField(); got != want {
		t.Errorf("Snapshot field count differs: server=%d client=%d", got, want)
	}
}
