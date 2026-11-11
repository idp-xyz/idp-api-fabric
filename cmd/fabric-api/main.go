// Command fabric-api serves the IDP API Fabric control plane over HTTP.
//
// This is the process skeleton: it answers /healthz and /version and nothing
// else. No bounded context is mounted yet, so the process opens no database,
// identity provider or object store connection.
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
	httptransport "go.idp.xyz/idp-api-fabric/internal/transport/http"
)

const (
	defaultAddress  = "127.0.0.1:8080"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
