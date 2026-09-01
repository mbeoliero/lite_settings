package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/render"
)

func TestInferFormat(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	cases := []struct {
		name, src, value, want string
	}{
		{"yaml extension", "a.yaml", "x", "yaml"},
		{"yml extension", "a.YML", "x", "yaml"},
		{"json extension", "a.json", "x", "json"},
		{"extension takes precedence", "a.json", "not: json", "json"},
		{"json object", "", `{"a":1}`, "json"},
		{"json array", "", ` [1,2] `, "json"},
		{"multiline yaml", "", "a: 1\nb: 2", "yaml"},
		{"numeric scalar", "", "30", "raw"},
		{"duration scalar", "", "30s", "raw"},
		// Keep host:port-like scalars raw instead of misdecoding them as YAML maps.
		{"single-line colon is scalar", "", "127.0.0.1:8080", "raw"},
		{"unknown extension falls back to content", "a.txt", `{"a":1}`, "json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := inferFormat(c.src, c.value); got != c.want {
				t.Fatalf("inferFormat(%q, %q) = %q, want %q", c.src, c.value, got, c.want)
			}
		})
	}
}

func TestReadValueFromArg(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	v, src, err := readValue(&cobra.Command{}, "", []string{"k", "30s"})
	if err != nil || v != "30s" || src != "" {
		t.Fatalf("got (%q, %q, %v)", v, src, err)
	}
}

func TestReadValueFromFile(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	path := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := os.WriteFile(path, []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v, src, err := readValue(&cobra.Command{}, path, []string{"k"})
	if err != nil || v != "a: 1\n" || src != path {
		t.Fatalf("got (%q, %q, %v)", v, src, err)
	}
}

func TestReadValueFromStdin(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader("piped\n"))

	v, src, err := readValue(cmd, "-", []string{"k"})
	if err != nil || v != "piped\n" || src != "" {
		t.Fatalf("got (%q, %q, %v)", v, src, err)
	}
}

func TestReadValueRejectsTwoSources(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// Ambiguous input must not publish the wrong production value.
	if _, _, err := readValue(&cobra.Command{}, "a.yaml", []string{"k", "v"}); err == nil {
		t.Fatal("file and positional argument together should return an error")
	}
}

func TestReadValueRejectsNoSource(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	if _, _, err := readValue(&cobra.Command{}, "", []string{"k"}); err == nil {
		t.Fatal("missing value should return an error")
	}
}

func TestReadValueMissingFile(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	_, _, err := readValue(&cobra.Command{}, filepath.Join(t.TempDir(), "nope"), []string{"k"})
	if err == nil {
		t.Fatal("missing file should return an error")
	}
}

func TestValidFormatAndOutput(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	for _, f := range []string{"raw", "json", "yaml"} {
		if !validFormat(f) {
			t.Errorf("%q should be valid", f)
		}
	}
	if validFormat("toml") {
		t.Error("toml should not be accepted")
	}
	// An empty string means "use the command default" and must pass.
	for _, o := range []string{"", "table", "json", "raw"} {
		if !render.ValidOutput(o) {
			t.Errorf("%q should be valid", o)
		}
	}
	if render.ValidOutput("yaml") {
		t.Error("yaml is not an output format")
	}
}
