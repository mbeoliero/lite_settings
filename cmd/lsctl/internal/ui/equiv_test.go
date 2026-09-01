package ui

import (
	"os/exec"
	"strings"
	"testing"
)

// A real shell must recover the exact value from the copyable confirmation command.
func TestEquivSetSurvivesTheShell(t *testing.T) {
	t.Parallel()

	type testBundle struct {
		values map[string]string
	}

	setup := func(t *testing.T) *testBundle {
		t.Helper()

		if _, err := exec.LookPath("sh"); err != nil {
			t.Skipf("sh is unavailable: %v", err)
		}
		return &testBundle{values: map[string]string{
			"Multiline":     "a: 1\nb: 2\n",
			"NoTrailingNL":  "plain",
			"SingleQuote":   "it's \"quoted\"",
			"ShellMetachar": "$HOME `id` $(id) \\ ; | &",
		}}
	}

	for name, value := range setup(t).values {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cmd := equivSet("cfg:x", value, "yaml")
			args, ok := strings.CutPrefix(cmd, "lsctl set cfg:x ")
			if !ok {
				t.Fatalf("unexpected command shape: %q", cmd)
			}
			quoted := strings.TrimSuffix(args, " --format yaml")

			// printf exposes the shell-parsed argument without adding bytes.
			out, err := exec.Command("sh", "-c", "printf '%s' "+quoted).Output()
			if err != nil {
				t.Fatalf("sh failed to run %s: %v", cmd, err)
			}
			if string(out) != value {
				t.Errorf("shell round trip = %q, want %q (command: %s)", out, value, cmd)
			}
		})
	}
}
