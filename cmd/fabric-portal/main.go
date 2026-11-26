// Command fabric-portal serves the Engineering Portal behind the internal
// sign-in proxy. That proxy attaches the caller's Fabric API access token
// server-side. Each API call is authorized by fabric-api as that caller.
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strings"
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
		logger.Error("fabric-portal failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	address := os.Getenv("IDP_FABRIC_PORTAL_HTTP_ADDR")
	if address == "" {
		address = "127.0.0.1:8081"
	}
	origin := os.Getenv("IDP_FABRIC_PORTAL_API_URL")
	if origin == "" {
		origin = "http://127.0.0.1:8080"
	}
	handler, err := newPortal(origin)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr: address, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second,
	}
	served := make(chan error, 1)
	go func() {
		logger.Info("fabric-portal listening", "address", address)
		served <- server.ListenAndServe()
	}()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return err
		}
		if err := <-served; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func newPortal(origin string) (http.Handler, error) {
	upstream, err := url.Parse(origin)
	if err != nil || upstream.Hostname() == "" || upstream.Scheme != "http" && upstream.Scheme != "https" ||
		upstream.User != nil || upstream.RawQuery != "" || upstream.ForceQuery || upstream.Fragment != "" ||
		upstream.Path != "" && upstream.Path != "/" {
		return nil, errors.New("IDP_FABRIC_PORTAL_API_URL must be an HTTP(S) origin")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", staticFile("index.html"))
	for _, name := range []string{"app.js", "app.css", "workbench.js", "publication.js", "access-blocks.js", "catalog.js", "dashboard.js", "checks.js", "releases.js", "compare.js"} {
		mux.HandleFunc("GET /"+name, staticFile(name))
	}
	proxy := fabricProxy(upstream)

	const product = "/api/v1/products/{product}"
	const asset = product + "/assets/{asset}"
	const revision = asset + "/revisions/{revision}"
	const target = product + "/publication-targets/{target}"
	const slot = target + "/assets/{asset}"
	const blocks = target + "/access-blocks"
	for _, path := range []string{
		"/api/v1/caller",
		"/api/v1/products",
		product + "/caller",
		product + "/workspaces",
		product + "/audience-configs",
		product + "/assets",
		asset + "/revisions",
		revision,
		revision + "/check-runs",
		revision + "/check-runs/{check}",
		revision + "/compatibility",
		revision + "/reviews",
		asset + "/releases",
		asset + "/publications",
		product + "/publication-targets",
		target + "/assets",
		slot,
		slot + "/operations",
		blocks,
	} {
		mux.Handle("GET "+path, proxy)
	}
	// Reviews are read only: since CR-0001 a release is prepared on a check.
	mux.Handle("POST "+revision+"/check-runs", proxy)
	mux.Handle("POST "+asset+"/releases", proxy)
	mux.Handle("POST "+slot+"/operations", proxy)
	mux.Handle("POST "+blocks, proxy)
	mux.Handle("POST "+blocks+"/{block}/clearance", proxy)

	protected := http.NewCrossOriginProtection().Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		protected.ServeHTTP(w, r)
	}), nil
}

func staticFile(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, web, "web/"+name)
	}
}

func fabricProxy(upstream *url.URL) http.Handler {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 25 * time.Second
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(upstream)
			request.Out.URL.Path = strings.TrimPrefix(request.In.URL.Path, "/api")
			request.Out.URL.RawPath = strings.TrimPrefix(request.In.URL.RawPath, "/api")
			request.Out.Header = make(http.Header)
			for _, header := range []string{"Authorization", "Content-Type", "Accept", "Idempotency-Key"} {
				if value := request.In.Header.Get(header); value != "" {
					request.Out.Header.Set(header, value)
				}
			}
		},
		ModifyResponse: func(response *http.Response) error {
			mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
			if response.StatusCode >= 300 && response.StatusCode < 400 ||
				mediaType != "application/json" && mediaType != "application/problem+json" {
				return errors.New("unexpected response from Fabric API")
			}
			response.Header.Del("Set-Cookie")
			response.Header.Del("Location")
			response.Header.Del("Cache-Control")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintln(w, "{\"status\":502,\"error_code\":\"FABRIC_UNAVAILABLE\"}")
		},
	}
}
