package command

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/backend"
	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/render"
	"github.com/mbeoliero/lite_settings/server/api"
)

func (r *root) newGetCmd() *cobra.Command {
	var version int64

	cmd := &cobra.Command{
		Use:   "get <key>",
		Short: "Read a configuration",
		Long: "Read a configuration. By default, only the value is printed for easy piping:\n" +
			"  lsctl get app:timeout | xargs echo\n" +
			"Use -o json for full details, including updated_at and updated_by.",
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().Int64Var(&version, "version", 0, "read a historical version; defaults to the current value")

	return r.bind(cmd, func(ctx context.Context, cmd *cobra.Command, args []string) error {
		key := args[0]
		out := cmd.OutOrStdout()

		if version > 0 {
			h, err := r.findVersion(ctx, key, version)
			if err != nil {
				return err
			}
			switch r.pick(render.OutRaw) {
			case render.OutJSON:
				return render.EmitJSON(out, h)
			default:
				return writeValue(cmd, h.Value)
			}
		}

		c, err := r.be.Get(ctx, key)
		if err != nil {
			return err
		}
		switch r.pick(render.OutRaw) {
		case render.OutJSON:
			return render.EmitJSON(out, c)
		case render.OutTable:
			t := render.NewTable(out)
			t.Row("KEY", c.Key)
			t.Row("FORMAT", c.Format)
			t.Row("SIZE", render.HumanSize(len(c.Value)))
			t.Row("UPDATED", render.LocalTime(c.UpdatedAt))
			t.Row("BY", c.UpdatedBy)
			return t.Flush()
		default:
			return writeValue(cmd, c.Value)
		}
	})
}

func (r *root) newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [prefix ...]",
		Short: "List configurations by prefix",
		Long: "List configurations by prefix. Without a prefix, all configurations are listed.\n" +
			"Multiple prefixes are allowed; overlapping results are deduplicated:\n" +
			"  lsctl list prompt_group: feature:",
		Aliases: []string{"ls"},
	}

	return r.bind(cmd, func(ctx context.Context, cmd *cobra.Command, args []string) error {
		configs, err := r.be.List(ctx, args)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()

		switch r.pick(render.OutTable) {
		case render.OutJSON:
			// The wrapper keeps empty JSON stable for scripts.
			return render.EmitJSON(out, render.ListOutput{Configs: configs, Count: len(configs)})
		case render.OutRaw:
			for _, c := range configs {
				fmt.Fprintln(out, c.Key)
			}
			return nil
		default:
			if len(configs) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "no matching configurations")
				return nil
			}
			t := render.NewTable(out, "KEY", "FORMAT", "SIZE", "VALUE")
			for _, c := range configs {
				t.Row(c.Key, c.Format, render.HumanSize(len(c.Value)), render.OneLine(c.Value, 48))
			}
			return t.Flush()
		}
	})
}

func (r *root) newHistoryCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:     "history <key>",
		Short:   "Show configuration history",
		Args:    cobra.ExactArgs(1),
		Aliases: []string{"log"},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum entries to show; 0 means all")

	return r.bind(cmd, func(ctx context.Context, cmd *cobra.Command, args []string) error {
		// 0 means "everything" on the CLI but "use the default" to the
		// backend, so translate.
		n := limit
		if n <= 0 {
			n = -1
		}
		entries, err := r.be.History(ctx, args[0], n)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()

		switch r.pick(render.OutTable) {
		case render.OutJSON:
			return render.EmitJSON(out, render.HistoryOutput{Key: args[0], History: entries})
		case render.OutRaw:
			for _, h := range entries {
				fmt.Fprintln(out, h.Version)
			}
			return nil
		default:
			t := render.NewTable(out, "VERSION", "OP", "WHEN", "BY", "SIZE", "COMMENT")
			for _, h := range entries {
				t.Row(strconv.FormatInt(h.Version, 10), h.Op, render.LocalTime(h.CreatedAt),
					h.CreatedBy, render.HumanSize(len(h.Value)), render.OneLine(h.Comment, 40))
			}
			if err := t.Flush(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "\nRollback: lsctl rollback %s --to <VERSION>\n", args[0])
			return nil
		}
	})
}

func (r *root) newDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff <key> <v1> [v2]",
		Short: "Compare two versions",
		Long: "Compare two versions of a configuration.\n" +
			"  lsctl diff app:cfg 3 5   compare version 3 with version 5\n" +
			"  lsctl diff app:cfg 3     compare version 3 with the current value",
		Args: cobra.RangeArgs(2, 3),
	}

	return r.bind(cmd, func(ctx context.Context, cmd *cobra.Command, args []string) error {
		key := args[0]
		v1, err := parseVersion(args[1])
		if err != nil {
			return err
		}

		left, err := r.findVersion(ctx, key, v1)
		if err != nil {
			return err
		}
		leftLabel := fmt.Sprintf("%s@v%d", key, v1)

		var rightText, rightLabel string
		if len(args) == 3 {
			v2, err := parseVersion(args[2])
			if err != nil {
				return err
			}
			h, err := r.findVersion(ctx, key, v2)
			if err != nil {
				return err
			}
			rightText, rightLabel = h.Value, fmt.Sprintf("%s@v%d", key, v2)
		} else {
			c, err := r.be.Get(ctx, key)
			if err != nil {
				return err
			}
			rightText, rightLabel = c.Value, key+"@current"
		}

		out := cmd.OutOrStdout()
		d := render.UnifiedDiff(left.Value, rightText, leftLabel, rightLabel, render.DiffContext)

		if r.pick(render.OutTable) == render.OutJSON {
			return render.EmitJSON(out, render.DiffOutput{
				Key: key, From: leftLabel, To: rightLabel,
				Identical: d == "", Diff: d,
			})
		}
		if d == "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "%s and %s are identical\n", leftLabel, rightLabel)
			return nil
		}
		_, err = fmt.Fprint(out, d)
		return err
	})
}

// findVersion searches all history so explicit versions bypass the default limit.
func (r *root) findVersion(ctx context.Context, key string, version int64) (api.HistoryEntry, error) {
	entries, err := r.be.History(ctx, key, -1)
	if err != nil {
		return api.HistoryEntry{}, err
	}
	for _, h := range entries {
		if h.Version == version {
			return h, nil
		}
	}
	// The sentinel makes an absent version exit 4 like an absent key, so
	// `lsctl get k --version N || create-it` cannot fire on a server fault.
	return api.HistoryEntry{}, fmt.Errorf("%w: %s has no version %d (available: %s)",
		backend.ErrNotFound, key, version, versionRange(entries))
}

func versionRange(entries []api.HistoryEntry) string {
	if len(entries) == 0 {
		return "no history"
	}
	hi, lo := entries[0].Version, entries[len(entries)-1].Version
	if hi == lo {
		return "only " + strconv.FormatInt(hi, 10)
	}
	return fmt.Sprintf("%d..%d", lo, hi)
}

func parseVersion(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("version must be a positive integer, got %q", s)
	}
	return n, nil
}

// writeValue adds only a missing trailing newline, avoiding prompt collisions or blank lines.
func writeValue(cmd *cobra.Command, v string) error {
	out := cmd.OutOrStdout()
	if strings.HasSuffix(v, "\n") {
		_, err := fmt.Fprint(out, v)
		return err
	}
	_, err := fmt.Fprintln(out, v)
	return err
}
