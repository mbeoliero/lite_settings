package store

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateKey(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	valid := []string{
		"a",
		"prompt_group:main",
		"feature:debug",
		"http.timeout",
		"svc-a:db@primary",
		strings.Repeat("k", MaxKeyLen),
	}
	for _, k := range valid {
		if err := ValidateKey(k); err != nil {
			t.Errorf("ValidateKey(%q) = %v, want nil", k, err)
		}
	}

	invalid := map[string]string{
		"empty":              "",
		"too long":           strings.Repeat("k", MaxKeyLen+1),
		"contains backslash": `a\b`, // likePrefix's escaping is only complete because this is excluded
		"contains slash":     "a/b", // would break /v1/configs/{key} routing
		"contains percent":   "a%b",
		"contains space":     "a b",
		"contains Chinese":   "配置",
		"contains newline":   "a\nb",
	}
	for name, k := range invalid {
		if err := ValidateKey(k); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("%s: ValidateKey(%q) = %v, want ErrInvalidKey", name, k, err)
		}
	}
}

func TestValidateValue(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	cases := []struct {
		name   string
		value  string
		format Format
		ok     bool
	}{
		{"raw scalar", "30s", FormatRaw, true},
		{"raw empty value is valid", "", FormatRaw, true},
		{"raw content is not validated", "{[(", FormatRaw, true},

		{"valid json", `{"temperature":0.7}`, FormatJSON, true},
		{"invalid json syntax", `{"temperature":}`, FormatJSON, false},
		{"empty json is invalid", "", FormatJSON, false},
		{"duplicate json key is rejected", `{"a":1,"a":2}`, FormatJSON, false},

		{"valid yaml", "system: hi\ntemperature: 0.7", FormatYAML, true},
		{"invalid yaml indentation", "a:\n  b: 1\n   c: 2", FormatYAML, false},
		{"empty yaml is valid", "", FormatYAML, true},

		{"too long", strings.Repeat("x", MaxValueSize+1), FormatRaw, false},
		{"unknown format", "x", Format("toml"), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateValue(c.value, c.format)
			if c.ok && err != nil {
				t.Fatalf("got %v, want nil", err)
			}
			if !c.ok {
				if err == nil {
					t.Fatal("got nil, want error")
				}
				if !errors.Is(err, ErrInvalidValue) {
					t.Fatalf("got %v, want ErrInvalidValue", err)
				}
			}
		})
	}
}

// Syntax errors need positions actionable to lsctl users.
func TestValidateJSONErrorDetail(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	err := ValidateValue(`{"temperature":}`, FormatJSON)
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "temperature") {
		t.Errorf("error does not include the failure position: %v", err)
	}
}

// Escaping must stop prompt_group: from matching promptXgroup:.
func TestLikePrefixEscapesWildcards(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	cases := map[string]string{
		"prompt_group:": `prompt\_group:%`,
		"feature:":      `feature:%`,
		"a%b":           `a\%b%`,
		`a\b`:           `a\\b%`,
		"":              "%",
	}
	for in, want := range cases {
		if got := likePrefix(in); got != want {
			t.Errorf("likePrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDialectFor(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	for driver, want := range map[string]string{
		"mysql":    "mysql",
		"postgres": "postgres",
		"pgx":      "postgres",
		"pgx/v5":   "postgres",
	} {
		d, err := DialectFor(driver)
		if err != nil {
			t.Fatalf("DialectFor(%q): %v", driver, err)
		}
		if d.Name() != want {
			t.Errorf("DialectFor(%q).Name() = %q, want %q", driver, d.Name(), want)
		}
	}

	if _, err := DialectFor("sqlite3"); !errors.Is(err, ErrUnsupportedDriver) {
		t.Errorf("DialectFor(sqlite3) = %v, want ErrUnsupportedDriver", err)
	}
}

// Shape checks catch common cross-dialect SQL mistakes without a database.
func TestDialectSQLShape(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	if got := (MySQL{}).PrefixCondition(); !strings.Contains(got, `ESCAPE '\\'`) {
		t.Errorf("MySQL requires a double-backslash escape, got %q", got)
	}
	if got := (Postgres{}).PrefixCondition(); !strings.Contains(got, `ESCAPE '\'`) ||
		strings.Contains(got, `ESCAPE '\\'`) {
		t.Errorf("PostgreSQL requires a single-backslash escape, got %q", got)
	}

	for _, d := range []Dialect{MySQL{}, Postgres{}} {
		if n := strings.Count(d.UpsertConfig(), "?"); n != 4 {
			t.Errorf("%s: upsert has %d placeholders, want 4 (key,value,format,updated_by)", d.Name(), n)
		}
		stmts, err := migrationsFor(d.Name())
		if err != nil {
			t.Errorf("%s: %v", d.Name(), err)
			continue
		}
		if len(stmts) == 0 {
			t.Errorf("%s: migration is empty", d.Name())
		}
		for _, stmt := range stmts {
			if strings.Contains(stmt, "CREATE TABLE") && !strings.Contains(stmt, "IF NOT EXISTS") {
				t.Errorf("%s: schema statement must be idempotent: %s", d.Name(), firstLine(stmt))
			}
		}
	}
}

// Reject inputs a token-stream decoder otherwise permits.
func TestValidateJSONRejectsNonDocument(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	for _, v := range []string{"", "   ", "\n\t", `{"a":1}{"b":2}`, `{"a":1} trailing`} {
		if err := ValidateValue(v, FormatJSON); err == nil {
			t.Errorf("ValidateValue(%q, json) = nil, want error", v)
		}
	}
}

func TestValidateChange(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// Validation must prevent database errors or lenient MySQL truncation.
	tests := []struct {
		name string
		c    Change
		ok   bool
	}{
		{"AtAuthorLimit", Change{Author: strings.Repeat("a", MaxAuthorLen)}, true},
		{"AtCommentLimit", Change{Comment: strings.Repeat("c", MaxCommentLen)}, true},
		{"Empty", Change{}, true},
		{"OverAuthorLimit", Change{Author: strings.Repeat("a", MaxAuthorLen+1)}, false},
		{"OverCommentLimit", Change{Comment: strings.Repeat("c", MaxCommentLen+1)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateChange(tt.c)
			if tt.ok && err != nil {
				t.Fatalf("expected success, got %v", err)
			}
			if !tt.ok && !errors.Is(err, ErrInvalidChange) {
				t.Fatalf("expected ErrInvalidChange, got %v", err)
			}
		})
	}
}

func TestMySQLMigrationPinsCaseSensitiveCollation(t *testing.T) {
	t.Parallel()

	type testBundle struct{}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		return &testBundle{}
	}

	bundle := setup(t)

	_ = bundle

	// MySQL's default collation would merge case-distinct valid keys.
	stmts, err := migrationsFor("mysql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range stmts {
		if !strings.Contains(stmt, "config_key") {
			continue
		}
		if !strings.Contains(stmt, "COLLATE utf8mb4_bin") {
			t.Fatalf("schema statement containing config_key must specify a case-sensitive collation:\n%s", stmt)
		}
	}
}
