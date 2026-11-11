package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/buildinfo"
	httptransport "go.idp.xyz/idp-api-fabric/internal/transport/http"
)

func TestServeAnswersUntilCancelledThenStopsCleanly(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	logger := slog.New(slog.DiscardHandler)
	stopped := make(chan error, 1)
	go func() {
		stopped <- serve(ctx, logger, listener, httptransport.NewHandler(logger, buildinfo.Current()))
	}()

	response, err := http.Get("http://" + listener.Addr().String() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz: status %d, want 200", response.StatusCode)
	}

	cancel()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("a cancelled server must stop cleanly, got %v", err)
		}
	case <-time.After(shutdownTimeout):
		t.Fatal("the server did not stop after its context was cancelled")
	}
}
