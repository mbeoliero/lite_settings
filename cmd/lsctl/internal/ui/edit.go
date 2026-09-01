package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Reuse $EDITOR rather than reimplementing multiline editing and Unicode-aware cursors.
func (m *Model) startEdit() tea.Cmd {
	if !m.hasDetail {
		m.status = "Select a config first"
		return nil
	}
	ed := editorCmd()
	if ed == nil {
		m.status = "Editor not found; set $EDITOR and try again"
		return nil
	}

	key, format, orig := m.detail.Key, m.detail.Format, m.detail.Value
	path, err := writeTemp(key, format, orig)
	if err != nil {
		m.errMsg = err.Error()
		return nil
	}

	c := exec.Command(ed[0], append(ed[1:], path)...)
	return tea.ExecProcess(c, func(runErr error) tea.Msg {
		// The temporary file holds production configuration in cleartext.
		defer os.Remove(path)

		if runErr != nil {
			return editedMsg{key: key, err: fmt.Errorf("editor exited with an error: %w", runErr)}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return editedMsg{key: key, err: fmt.Errorf("read edited value: %w", err)}
		}
		return editedMsg{key: key, format: format, value: string(data)}
	})
}

func editorCmd() []string {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		// Preserve arguments such as "code -w" so GUI editors wait for changes.
		if f := strings.Fields(os.Getenv(env)); len(f) > 0 {
			return f
		}
	}
	// Common fallbacks keep editing usable without shell configuration.
	for _, name := range []string{"vim", "vi", "nano"} {
		if p, err := exec.LookPath(name); err == nil {
			return []string{p}
		}
	}
	return nil
}

// The format extension enables the editor's syntax support.
func writeTemp(key, format, value string) (string, error) {
	f, err := os.CreateTemp("", "lsctl-"+safeName(key)+"-*"+extFor(format))
	if err != nil {
		return "", fmt.Errorf("create temporary file: %w", err)
	}
	path := f.Name()
	if _, err := f.WriteString(value); err != nil {
		f.Close()
		os.Remove(path)
		return "", fmt.Errorf("write temporary file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("close temporary file: %w", err)
	}
	return path, nil
}

func extFor(format string) string {
	switch format {
	case "yaml":
		return ".yaml"
	case "json":
		return ".json"
	default:
		return ".txt"
	}
}

// Keep the key recognizable in the editor title without allowing path syntax.
func safeName(key string) string {
	// Remove dots to prevent traversal and misleading syntax detection.
	var b strings.Builder
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			// Collapse invalid runs to keep the filename readable.
			if t := b.String(); t != "" && !strings.HasSuffix(t, "-") {
				b.WriteByte('-')
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	return s
}
