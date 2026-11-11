// Command fabric-worker runs the background work of the IDP API Fabric control
// plane: durable jobs, outbox delivery and reconciliation (F02 §4.1, F07 §7).
//
// This is the process skeleton: no worker is registered yet, so the process
// starts, waits for a termination signal and exits. It exists so that the API
// and the worker are separate processes from the start (ADR-0002).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"go.idp.xyz/idp-api-fabric/internal/platform/buildinfo"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("fabric-worker started; no worker is registered yet", "build", buildinfo.Current())
	<-ctx.Done()
	logger.Info("fabric-worker stopped")
}
