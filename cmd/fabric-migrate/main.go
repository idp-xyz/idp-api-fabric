// Command fabric-migrate applies the Fabric migration plan. An operator or a
// deployment job runs it before deploying; no application process executes
// DDL, and the role an application process connects with does not hold the
// privileges this command needs (F02 §3.2, F07 §5.3).
//
// Without -apply it prints the ordered plan and connects to nothing.
//
// With -apply it creates the schemas, applies the steps the database does not
// record yet and records each one. With -grant-to it then grants the named
// application role exactly what a Fabric process needs; it creates no role.
//
// IDP_FABRIC_MIGRATOR_DSN is the connection string of the migrating role, which
// owns the schemas. It is not the role an application process connects with:
// a process refuses to start as it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/migrate"
)

const connectTimeout = 15 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("migration failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	apply := flag.Bool("apply", false, "apply the plan to the database of IDP_FABRIC_MIGRATOR_DSN; without it the plan is only printed")
	grantTo := flag.String("grant-to", "", "with -apply: also grant this existing role what a Fabric process needs")
	flag.Parse()

	if err := printPlan(); err != nil {
		return err
	}
	if !*apply {
		if *grantTo != "" {
			return errors.New("-grant-to changes the database and needs -apply")
		}
		fmt.Println("DRY-RUN: no database connection was made and nothing was executed; pass -apply to run the plan")
		return nil
	}

	// The DSN carries credentials: it comes from the environment only and is
	// never echoed.
	dsn := os.Getenv("IDP_FABRIC_MIGRATOR_DSN")
	if dsn == "" {
		return errors.New("IDP_FABRIC_MIGRATOR_DSN is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	conn, err := pgx.Connect(connectCtx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), connectTimeout)
		defer closeCancel()
		_ = conn.Close(closeCtx)
	}()

	if err := migrate.Run(ctx, conn, logger); err != nil {
		return err
	}
	if *grantTo == "" {
		return nil
	}
	if err := migrate.Grant(ctx, conn, *grantTo); err != nil {
		return err
	}
	logger.Info("application privileges granted", "role", *grantTo)
	return nil
}

func printPlan() error {
	steps, err := migrate.Plan()
	if err != nil {
		return err
	}
	for index, step := range steps {
		version := step.FrameworkVersion
		if version == "" {
			version = "-"
		}
		fmt.Printf("%3d  %-9s  %-16s  %-36s  %-12s  %s\n",
			index+1, step.Origin, step.Schema, step.ID, version, step.Checksum)
	}
	return nil
}
