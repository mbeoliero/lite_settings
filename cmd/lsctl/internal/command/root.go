// Package command assembles lsctl commands for matching HTTP and database backends.
package command

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/backend"
	"github.com/mbeoliero/lite_settings/cmd/lsctl/internal/render"
)

// Exit codes.
//
// not-found gets its own code so that `lsctl get k || create-it` fires
// on a genuine absence and not on an unreachable server.
const (
	exitError    = 1
	exitNotFound = 4
)

const defaultTimeout = 10 * time.Second

type root struct {
	server  string
	dsn     string
	driver  string
	author  string
	output  string
	timeout time.Duration

	be backend.Backend
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	cmd, r := newRootCmd()
	err := cmd.Execute()
	// The backend is cached on root and outlives each subcommand, so it
	// is closed here rather than per command.
	r.close()

	if err != nil {
		fmt.Fprintln(os.Stderr, "lsctl:", err)
		if errors.Is(err, backend.ErrNotFound) {
			return exitNotFound
		}
		return exitError
	}
	return 0
}

func newRootCmd() (*cobra.Command, *root) {
	r := &root{}

	cmd := &cobra.Command{
		Use:   "lsctl",
		Short: "Manage lite_settings configurations",
		Long: "lsctl manages lite_settings configurations.\n\n" +
			"Specify exactly one backend:\n" +
			"  --server http://127.0.0.1:8080   use the server HTTP API\n" +
			"  --dsn    'user:pw@tcp(...)/db'   connect directly to the database\n\n" +
			"Set LSCTL_SERVER or LSCTL_DSN to avoid passing it each time.",
		SilenceUsage:  true, // a runtime error should not reprint usage
		SilenceErrors: true, // Execute prints, so it can pick the exit code
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	r.addGlobalFlags(cmd)

	cmd.AddCommand(
		r.newGetCmd(),
		r.newListCmd(),
		r.newSetCmd(),
		r.newRmCmd(),
		r.newHistoryCmd(),
		r.newDiffCmd(),
		r.newRollbackCmd(),
		r.newUICmd(),
		r.newMigrateCmd(),
	)
	return cmd, r
}

// addGlobalFlags also lets tests exercise the production parsing path.
func (r *root) addGlobalFlags(cmd *cobra.Command) {
	f := cmd.PersistentFlags()
	f.StringVarP(&r.server, "server", "s", os.Getenv("LSCTL_SERVER"), "server URL, for example http://127.0.0.1:8080")
	f.StringVar(&r.dsn, "dsn", os.Getenv("LSCTL_DSN"), "database DSN for direct mode")
	f.StringVar(&r.driver, "driver", os.Getenv("LSCTL_DRIVER"), "database driver mysql|pgx; inferred from DSN when omitted")
	f.StringVar(&r.author, "author", defaultAuthor(), "author recorded in audit history")
	f.StringVarP(&r.output, "output", "o", "", "output format table|json|raw; defaults per command")
	f.DurationVar(&r.timeout, "timeout", defaultTimeout, "timeout for one operation")
}

// bind attaches backend setup and teardown to a subcommand.
//
// Not root's PersistentPreRunE: that would make `lsctl --help` dial the
// database, and help should not require a working backend.
func (r *root) bind(cmd *cobra.Command, run func(context.Context, *cobra.Command, []string) error) *cobra.Command {
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if !render.ValidOutput(r.output) {
			return fmt.Errorf("--output must be table|json|raw, got %q", r.output)
		}
		if _, err := r.backend(); err != nil {
			return err
		}

		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		ctx, cancel := context.WithTimeout(ctx, r.timeout)
		defer cancel()

		return run(ctx, cmd, args)
	}
	return cmd
}

func (r *root) backend() (backend.Backend, error) {
	if r.be != nil {
		return r.be, nil
	}
	switch {
	case r.server != "" && r.dsn != "":
		return nil, errors.New("--server and --dsn are mutually exclusive")
	case r.server != "":
		// No http.Client.Timeout: ctx owns the deadline. Two of them
		// only makes "which one fired" hard to answer.
		r.be = backend.OpenHTTP(r.server, &http.Client{Transport: http.DefaultTransport})
	case r.dsn != "":
		be, err := backend.OpenDB(r.driver, r.dsn)
		if err != nil {
			return nil, err
		}
		r.be = be
	default:
		return nil, errors.New("specify --server or --dsn (or set LSCTL_SERVER / LSCTL_DSN)")
	}
	return r.be, nil
}

func (r *root) close() {
	if r.be != nil {
		r.be.Close() //nolint:errcheck // the process is exiting anyway
		r.be = nil
	}
}

func (r *root) change(comment string) backend.Change {
	return backend.Change{Author: r.author, Comment: comment}
}

func defaultAuthor() string {
	var name string
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	return cmp.Or(os.Getenv("LSCTL_AUTHOR"), name, os.Getenv("USER"), "unknown")
}

func (r *root) pick(def string) string {
	return cmp.Or(r.output, def)
}
