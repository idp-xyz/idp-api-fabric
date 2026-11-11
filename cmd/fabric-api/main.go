// Command fabric-api serves the IDP API Fabric control plane over HTTP.
//
// It reads a database that fabric-migrate has already migrated and never
// executes DDL: the process refuses to start against a schema it does not
// recognise. No bounded context is mounted yet, so beyond that check it
// answers /healthz and /version only.
//
// IDP_FABRIC_POSTGRES_DSN is the connection string of the application role:
// the one fabric-migrate grants to, not the one it migrates as. The process
// refuses to start as a role that could change the schema.
//
// IDP_FABRIC_HTTP_ADDR sets the listen address. The default is loopback only:
// the control plane is private (F02 §4.1), so reaching it from another host is
// a deployment decision, not a default.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/buildinfo"
	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	httptransport "go.idp.xyz/idp-api-fabric/internal/transport/http"
)

const (
	defaultAddress  = "127.0.0.1:8080"
	connectTimeout  = 15 * time.Second
	shutdownTimeout = 10 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fabric-api failed", "error", err)
		os.Exit(1)
	}
	logger.Info("fabric-api stopped")
}

func run(logger *slog.Logger) error {
	address := os.Getenv("IDP_FABRIC_HTTP_ADDR")
	if address == "" {
		address = defaultAddress
	}

	// The DSN carries credentials: it comes from the environment only and is
	// never echoed. It is required rather than optional: a control plane that
	// cannot reach its database must fail to start, not answer /healthz with
	// nothing behind it.
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

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
	}
	return serve(ctx, logger, listener, httptransport.NewHandler(buildinfo.Current()))
}

// serve answers on listener until ctx is done, then lets in-flight requests
// finish within shutdownTimeout. It returns nil only for that clean stop.
func serve(ctx context.Context, logger *slog.Logger, listener net.Listener, handler http.Handler) error {
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveResult := make(chan error, 1)
	go func() {
		logger.Info("fabric-api listening", "address", listener.Addr().String(), "build", buildinfo.Current())
		serveResult <- server.Serve(listener)
	}()

	select {
	case err := <-serveResult:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down: %w", err)
	}
	if err := <-serveResult; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
