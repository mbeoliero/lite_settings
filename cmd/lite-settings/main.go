// Command lite-settings is the config server binary.
//
//	lite-settings --dsn 'root:pw@tcp(127.0.0.1:3306)/lite_settings?parseTime=true'
//	lite-settings --driver postgres --dsn 'postgres://u:p@127.0.0.1:5432/lite?sslmode=disable'
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	// Keep drivers out of store and client so SDK users inherit neither.
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/mbeoliero/lite_settings/server"
	"github.com/mbeoliero/lite_settings/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "lite-settings:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		dsn      = flag.String("dsn", "", "database connection string (required)")
		driver   = flag.String("driver", "mysql", "mysql | postgres")
		addr     = flag.String("addr", server.DefaultAddr, "HTTP listen address")
		poll     = flag.Duration("poll-interval", server.DefaultPollInterval, "server revision polling interval")
		longPoll = flag.Duration("long-poll-timeout", server.DefaultLongPollTimeout, "long-poll timeout")
		migrate  = flag.Bool("migrate", true, "create or update schema at startup")
		logLevel = flag.String("log-level", "info", "debug | info | warn | error")
	)
	flag.Parse()

	if *dsn == "" {
		flag.Usage()
		return fmt.Errorf("--dsn is required")
	}

	lvl, err := parseLevel(*logLevel)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))

	drv, err := store.SQLDriver(*driver)
	if err != nil {
		return fmt.Errorf("--driver: %w", err)
	}

	db, err := sql.Open(drv, store.NormalizeDSN(drv, *dsn))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	// Connection caps bound failures; normal polling and long polls use little DB work.
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)

	// Install signals before dialing so Ctrl-C can cancel stuck startup.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
	defer pcancel()
	if err := db.PingContext(pctx); err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	st, err := store.New(db, drv)
	if err != nil {
		return err
	}
	if *migrate {
		mctx, mcancel := context.WithTimeout(ctx, 30*time.Second)
		defer mcancel()
		if err := st.Migrate(mctx); err != nil {
			return fmt.Errorf("migrate schema: %w", err)
		}
		log.Info("schema ready", "dialect", st.Dialect().Name())
	}

	srv, err := server.New(server.Options{
		Store:           st,
		Addr:            *addr,
		PollInterval:    *poll,
		LongPollTimeout: *longPoll,
		Logger:          log,
	})
	if err != nil {
		return err
	}
	return srv.Run(ctx)
}

func parseLevel(s string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return 0, fmt.Errorf("invalid --log-level: %q", s)
	}
	return level, nil
}
