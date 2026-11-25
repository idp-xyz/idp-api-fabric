// Command fabric-worker runs the background work of the IDP API Fabric control
// plane: durable jobs, outbox delivery and reconciliation (F02 §4.1, F07 §7).
//
// It reads a database that fabric-migrate has already migrated and never
// executes DDL. Set IDP_FABRIC_OUTBOX_URL to relay Fabric integration events
// to a trusted internal HTTP receiver.
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

	"go.idp.xyz/idp-bento-go/postgres/outbox"

	"go.idp.xyz/idp-api-fabric/internal/platform/buildinfo"
	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/workers"
)

const connectTimeout = 15 * time.Second

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.Error("fabric-worker failed", "error", err)
		os.Exit(1)
	}
	logger.Info("fabric-worker stopped")
}

func run(ctx context.Context, logger *slog.Logger) error {
	// The DSN carries credentials: it comes from the environment only and is
	// never echoed.
	dsn := os.Getenv("IDP_FABRIC_POSTGRES_DSN")
	if dsn == "" {
		return errors.New("IDP_FABRIC_POSTGRES_DSN is required")
	}

	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	database, err := db.Open(connectCtx, dsn)
	cancel()
	if err != nil {
		return err
	}
	defer database.Close()

	receiver := os.Getenv("IDP_FABRIC_OUTBOX_URL")
	if receiver == "" {
		logger.Info("fabric-worker started in standby", "build", buildinfo.Current())
		<-ctx.Done()
		return nil
	}
	publisher, err := newHTTPPublisher(receiver, os.Getenv("IDP_FABRIC_OUTBOX_TOKEN"))
	if err != nil {
		return err
	}
	defer publisher.client.CloseIdleConnections()
	store, err := outbox.NewStore(database.Framework)
	if err != nil {
		return err
	}
	logger.Info("fabric-worker started Outbox delivery", "build", buildinfo.Current())
	workers.RunOutbox(ctx, store, publisher, logger)
	return nil
}
