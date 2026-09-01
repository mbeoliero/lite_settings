package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/backend"
	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/render"
	"github.com/mbeoliero/lite_settings/server/api"
)

func (r *root) newSetCmd() *cobra.Command {
	var (
		file    string
		format  string
		comment string
		dryRun  bool
	)

	cmd := &cobra.Command{
		Use:   "set <key> [value]",
		Short: "Write a configuration",
		Long: "Write a configuration from an argument, file, or standard input:\n" +
			"  lsctl set app:timeout 30s\n" +
			"  lsctl set prompt_group:main -f main.yaml -m \"adjust timeout\"\n" +
			"  cat main.yaml | lsctl set prompt_group:main -f -\n\n" +
			"Without --format, .yaml/.yml files use yaml, .json files use json, and all others use raw.",
		Args: cobra.RangeArgs(1, 2),
	}
	f := cmd.Flags()
	f.StringVarP(&file, "file", "f", "", "read value from a file; - means standard input")
	f.StringVar(&format, "format", "", "value format raw|json|yaml; inferred when omitted")
	f.StringVarP(&comment, "message", "m", "", "change description recorded in audit history")
	f.BoolVar(&dryRun, "dry-run", false, "show the diff without writing")

	return r.bind(cmd, func(ctx context.Context, cmd *cobra.Command, args []string) error {
		key := args[0]
		value, src, err := readValue(cmd, file, args)
		if err != nil {
			return err
		}
		if format == "" {
			format = inferFormat(src, value)
		}
		if !validFormat(format) {
			return fmt.Errorf("--format must be raw|json|yaml, got %q", format)
		}

		if dryRun {
			return r.previewSet(ctx, cmd, key, value, format)
		}

		res, err := r.be.Set(ctx, key, value, format, r.change(comment))
		if err != nil {
			return err
		}
		return r.reportWrite(cmd, "wrote", key, format, res)
	})
}

// previewSet provides a non-interactive equivalent of the TUI preview.
func (r *root) previewSet(ctx context.Context, cmd *cobra.Command, key, value, format string) error {
	var (
		cur       string
		curFormat string
		exists    bool
	)
	c, err := r.be.Get(ctx, key)
	switch {
	case err == nil:
		cur, curFormat, exists = c.Value, c.Format, true
	case errors.Is(err, backend.ErrNotFound):
	default:
		return err
	}

	label := key + "@current"
	if !exists {
		label = key + "@(missing)"
	}
	d := render.UnifiedDiff(cur, value, label, key+"@new", render.DiffContext)
	// Equal text in different formats can decode differently.
	fmtChanged := exists && curFormat != format

	out := cmd.OutOrStdout()
	if r.pick(render.OutTable) == render.OutJSON {
		return render.EmitJSON(out, render.DryRunOutput{
			Key: key, Format: format, Exists: exists,
			Changed: d != "" || fmtChanged, Diff: d, DryRun: true,
		})
	}
	if d == "" {
		// Set creates a version even for unchanged values, so dry-run must say so.
		if fmtChanged {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"%s value is unchanged but format changes: %s → %s; set will still create a new version\n", key, curFormat, format)
			return nil
		}
		fmt.Fprintf(cmd.ErrOrStderr(),
			"%s value and format are unchanged; set will still create a new version and increment the revision\n", key)
		return nil
	}
	fmt.Fprint(out, d)
	fmt.Fprintf(cmd.ErrOrStderr(), "\n(--dry-run; not written; remove the flag to apply, format=%s)\n", format)
	return nil
}

func (r *root) newRmCmd() *cobra.Command {
	var comment string

	cmd := &cobra.Command{
		Use:     "rm <key>",
		Short:   "Delete a configuration",
		Long:    "Delete a configuration. History retains the deleted value so it can be restored with rollback.",
		Args:    cobra.ExactArgs(1),
		Aliases: []string{"delete"},
	}
	cmd.Flags().StringVarP(&comment, "message", "m", "", "change description recorded in audit history")

	return r.bind(cmd, func(ctx context.Context, cmd *cobra.Command, args []string) error {
		key := args[0]
		res, err := r.be.Delete(ctx, key, r.change(comment))
		if err != nil {
			return err
		}
		if err := r.reportWrite(cmd, "deleted", key, "", res); err != nil {
			return err
		}
		// Avoid bulk-unfriendly prompts; recoverable deletes print the undo command.
		if r.pick(render.OutTable) != render.OutJSON {
			fmt.Fprintf(cmd.ErrOrStderr(), "Undo: lsctl rollback %s --to %d\n", key, res.Version)
		}
		return nil
	})
}

func (r *root) newRollbackCmd() *cobra.Command {
	var (
		to      int64
		comment string
	)

	cmd := &cobra.Command{
		Use:   "rollback <key>",
		Short: "Roll back a configuration",
		Long: "Roll back a configuration to a specified version. Rollback creates a new version,\n" +
			"so it can itself be rolled back without erasing history.",
		Args: cobra.ExactArgs(1),
	}
	f := cmd.Flags()
	f.Int64Var(&to, "to", 0, "target version (required; see lsctl history)")
	f.StringVarP(&comment, "message", "m", "", "change description recorded in audit history")
	cmd.MarkFlagRequired("to") //nolint:errcheck // the flag is declared above

	return r.bind(cmd, func(ctx context.Context, cmd *cobra.Command, args []string) error {
		if to <= 0 {
			return fmt.Errorf("--to must be a positive integer, got %d", to)
		}
		key := args[0]
		res, err := r.be.Rollback(ctx, key, to, r.change(comment))
		if err != nil {
			return err
		}
		return r.reportWrite(cmd, fmt.Sprintf("rolled back to v%d", to), key, "", res)
	})
}

// reportWrite always uses stderr so write pipelines have stable empty stdout.
func (r *root) reportWrite(cmd *cobra.Command, action, key, format string, res api.WriteResult) error {
	errOut := cmd.ErrOrStderr()
	if r.pick(render.OutTable) == render.OutJSON {
		return render.EmitJSON(errOut, render.WriteOutput{
			Key: key, Action: action, Format: format,
			Version: res.Version, Revision: res.Revision,
		})
	}
	_, err := fmt.Fprintf(errOut, "%s %s: version=%d revision=%d\n",
		action, key, res.Version, res.Revision)
	return err
}

func readValue(cmd *cobra.Command, file string, args []string) (value, src string, err error) {
	hasArg := len(args) == 2
	switch {
	case file != "" && hasArg:
		return "", "", errors.New("value must have one source: use either --file or a positional argument")
	case file == "-":
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", "", fmt.Errorf("read standard input: %w", err)
		}
		return string(data), "", nil
	case file != "":
		data, err := os.ReadFile(file)
		if err != nil {
			return "", "", fmt.Errorf("read %s: %w", file, err)
		}
		return string(data), file, nil
	case hasArg:
		return args[1], "", nil
	default:
		return "", "", errors.New("missing value: use a positional argument, -f <file>, or -f - for standard input")
	}
}

// inferFormat prefers clear file or content signals, otherwise safe raw.
func inferFormat(src, value string) string {
	switch strings.ToLower(filepath.Ext(src)) {
	case ".yaml", ".yml":
		return "yaml"
	case ".json":
		return "json"
	}
	switch t := strings.TrimSpace(value); {
	case strings.HasPrefix(t, "{"), strings.HasPrefix(t, "["):
		return "json"
	case strings.Contains(t, "\n") && strings.Contains(t, ":"):
		// Require multiple lines so scalar values such as host:port stay raw.
		return "yaml"
	default:
		return "raw"
	}
}

func validFormat(s string) bool {
	return slices.Contains([]string{"raw", "json", "yaml"}, s)
}
