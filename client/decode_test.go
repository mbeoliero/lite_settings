package lite

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDecodeScalars(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// Scalar reads intentionally ignore the declared format.
	if got, err := decode[string](`{"a":1}`, FormatJSON, false); err != nil || got != `{"a":1}` {
		t.Fatalf("string: got %q err %v", got, err)
	}
	if got, err := decode[bool]("true", FormatRaw, false); err != nil || !got {
		t.Fatalf("bool: got %v err %v", got, err)
	}
	if got, err := decode[int]("42", FormatRaw, false); err != nil || got != 42 {
		t.Fatalf("int: got %v err %v", got, err)
	}
	if got, err := decode[int64]("-9007199254740993", FormatRaw, false); err != nil || got != -9007199254740993 {
		t.Fatalf("int64: got %v err %v", got, err)
	}
	if got, err := decode[uint64]("18446744073709551615", FormatRaw, false); err != nil || got != 18446744073709551615 {
		t.Fatalf("uint64: got %v err %v", got, err)
	}
	if got, err := decode[float64]("1.5", FormatRaw, false); err != nil || got != 1.5 {
		t.Fatalf("float64: got %v err %v", got, err)
	}
	if got, err := decode[time.Duration]("1500ms", FormatRaw, false); err != nil || got != 1500*time.Millisecond {
		t.Fatalf("duration: got %v err %v", got, err)
	}
	if got, err := decode[[]byte]("abc", FormatRaw, false); err != nil || string(got) != "abc" {
		t.Fatalf("bytes: got %q err %v", got, err)
	}
}

func TestDecodeScalarErrors(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	for _, tc := range []struct{ name, raw string }{
		{"bool", "yepp"},
		{"int", "1.5"},
		{"duration", "30"},
	} {
		var err error
		switch tc.name {
		case "bool":
			_, err = decode[bool](tc.raw, FormatRaw, false)
		case "int":
			_, err = decode[int](tc.raw, FormatRaw, false)
		case "duration":
			_, err = decode[time.Duration](tc.raw, FormatRaw, false)
		}
		if !errors.Is(err, ErrDecode) {
			t.Fatalf("%s: want ErrDecode, got %v", tc.name, err)
		}
	}
}

func TestDecodeTruncatesOversizedValueInError(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	_, err := decode[int](strings.Repeat("x", 500), FormatRaw, false)
	if err == nil {
		t.Fatal("expected an error")
	}
	// The error must not smear the whole config into the log.
	if len(err.Error()) > 200 {
		t.Fatalf("error message too long (%d bytes): %s", len(err.Error()), err)
	}
	if !strings.Contains(err.Error(), "…") {
		t.Fatalf("expected truncation; got %s", err)
	}
}

type promptCfg struct {
	System string  `json:"system" yaml:"system"`
	Model  string  `json:"model" yaml:"model"`
	Temp   float64 `json:"temp" yaml:"temp"`
}

func TestDecodeJSONAndYAML(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	j, err := decode[promptCfg](`{"system":"you are","model":"opus","temp":0.7}`, FormatJSON, false)
	if err != nil || j.System != "you are" || j.Model != "opus" || j.Temp != 0.7 {
		t.Fatalf("json: %+v err %v", j, err)
	}

	y, err := decode[promptCfg]("system: you are\nmodel: opus\ntemp: 0.7\n", FormatYAML, false)
	if err != nil || y != j {
		t.Fatalf("yaml: %+v err %v", y, err)
	}
}

// Strict decoding stays opt-in so new fields do not break older instances.
func TestDecodeStrictIsOptIn(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	const withUnknown = `{"system":"s","model":"m","temp":0.1,"top_p":0.9}`
	if _, err := decode[promptCfg](withUnknown, FormatJSON, false); err != nil {
		t.Fatalf("default mode must allow unknown fields: %v", err)
	}
	if _, err := decode[promptCfg](withUnknown, FormatJSON, true); !errors.Is(err, ErrDecode) {
		t.Fatalf("strict mode should reject unknown fields; got %v", err)
	}

	const yamlUnknown = "system: s\nmodel: m\ntop_p: 0.9\n"
	if _, err := decode[promptCfg](yamlUnknown, FormatYAML, false); err != nil {
		t.Fatalf("default yaml mode must allow unknown fields: %v", err)
	}
	if _, err := decode[promptCfg](yamlUnknown, FormatYAML, true); !errors.Is(err, ErrDecode) {
		t.Fatalf("strict yaml mode should reject unknown fields; got %v", err)
	}
}

func TestDecodeRawToStructIsAnError(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	_, err := decode[promptCfg]("just text", FormatRaw, false)
	if !errors.Is(err, ErrDecode) {
		t.Fatalf("want ErrDecode, got %v", err)
	}
	if !strings.Contains(err.Error(), "json or yaml") {
		t.Fatalf("error should explain the remedy; got %s", err)
	}
}

func TestDecodeMalformedJSON(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	if _, err := decode[promptCfg](`{"system":`, FormatJSON, false); !errors.Is(err, ErrDecode) {
		t.Fatalf("want ErrDecode, got %v", err)
	}
}
