// Command fabric-developer-portal serves CargoPai's reference behind its
// partner sign-in proxy. The publication origin authorizes every request.
package main

import (
	"context"
	"embed"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed web
var web embed.FS

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, logger); err != nil {
		logger.Error("fabric-developer-portal failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	address := os.Getenv("IDP_FABRIC_DEVELOPER_PORTAL_HTTP_ADDR")
	if address == "" {
		address = "127.0.0.1:8083"
	}
	handler, err := newPortal(os.Getenv("IDP_FABRIC_DEVELOPER_PORTAL_ORIGIN"), os.Getenv("IDP_FABRIC_DEVELOPER_PORTAL_ASSET_ID"))
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr: address, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second,
	}
	result := make(chan error, 1)
	go func() {
		logger.Info("CargoPai developer portal listening", "address", address)
		result <- server.ListenAndServe()
	}()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return err
		}
		if err := <-result; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
