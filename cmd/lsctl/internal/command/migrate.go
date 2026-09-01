package command

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func (r *root) newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Create tables and initialize the revision row",
		Long: "Create tables and initialize the lite_config_meta row in the target database. Safe to run repeatedly.\n\n" +
			"This command requires direct database access through --dsn because schema migration needs database privileges.\n" +
			"The server does not expose this operation, but can run it at startup with --migrate.",
	}

	return r.bind(cmd, func(ctx context.Context, cmd *cobra.Command, args []string) error {
		if err := r.be.Migrate(ctx); err != nil {
			return err
		}
		_, err := fmt.Fprintf(cmd.ErrOrStderr(), "migrated %s database\n", r.be.Describe())
		return err
	})
}
