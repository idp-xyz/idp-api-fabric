// Command fabric-worker runs the background work of the IDP API Fabric control
// plane: durable jobs, outbox delivery and reconciliation (F02 §4.1, F07 §7).
//
// It reads a database that fabric-migrate has already migrated and never
// executes DDL: the process refuses to start against a schema it does not
// recognise. No worker is registered yet, so beyond that check it waits for a
// termination signal and exits. It exists so that the API and the worker are
// separate processes from the start (ADR-0002).
//
// IDP_FABRIC_POSTGRES_DSN is the connection string of the application role:
// the one fabric-migrate grants to, not the one it migrates as. The process
// refuses to start as a role that could change the schema.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/buildinfo"
	"go.idp.xyz/idp-api-fabric/internal/platform/db"
)

const connectTimeout = 15 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fabric-worker failed", "error", err)
		os.Exit(1)
	}
	logger.Info("fabric-worker stopped")
}

func run(logger *slog.Logger) error {
	// The DSN carries credentials: it comes from the environment only and is
	// never echoed.
	dsn := os.Getenv("IDP_FABRIC_POSTGRES_DSN")
	if dsn == "" {
		return errors.New("IDP_FABRIC_POSTGRES_DSN is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	database, err := db.Open(connectCtx, dsn)
	if err != nil {
		return err
	}
	defer database.Close()

	logger.Info("fabric-worker started; no worker is registered yet", "build", buildinfo.Current())
	<-ctx.Done()
	return nil
}
