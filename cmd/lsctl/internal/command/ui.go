package command

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/ui"
)

func (r *root) newUICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ui [prefix ...]",
		Short: "Open the interactive UI",
		Long: "Open a three-pane UI: keys on the left, values in the center, and history on the right.\n" +
			"Pass prefixes to load only matching configurations:\n" +
			"  lsctl ui prompt_group:\n\n" +
			"Every UI action has an equivalent non-interactive command shown in its confirmation dialog,\n" +
			"ready to copy into CI/CD. Press ? for all key bindings.",
		Aliases: []string{"tui"},
	}

	// The long-lived TUI applies timeout per backend call, not to its root context.
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := requireTTY(); err != nil {
			return err
		}
		be, err := r.backend()
		if err != nil {
			return err
		}

		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		m := ui.New(be, r.author, r.timeout, args)
		p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
		final, err := p.Run()
		if err != nil {
			return err
		}

		// Replay writes because the alt-screen status disappears on exit.
		if fm, ok := final.(*ui.Model); ok {
			for _, w := range fm.Writes() {
				fmt.Fprintln(cmd.ErrOrStderr(), w)
			}
		}
		return nil
	}
	return cmd
}

// requireTTY directs scripts and CI to non-interactive commands.
func requireTTY() error {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return fmt.Errorf("check terminal: %w", err)
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		return errors.New("lsctl ui requires an interactive terminal; use get / list / history / rollback in scripts")
	}
	return nil
}
