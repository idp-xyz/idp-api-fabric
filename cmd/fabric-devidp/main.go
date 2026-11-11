// Command fabric-devidp is the token issuer of a development machine. It
// publishes a key set on a loopback address and signs access tokens on
// request, so that fabric-api verifies real signatures against a real key set
// where no identity provider is bound yet. Binding the real provider changes
// the three IDP_FABRIC_OIDC_* settings of fabric-api, not its code.
//
// It is not a product capability and is never deployed: it has no accounts,
// no login and no password, and it signs whatever subject it is asked to. It
// listens on a loopback address only.
//
//	fabric-devidp serve [-addr 127.0.0.1:9450] [-key <file>]
//	fabric-devidp mint -sub <subject> [-aud fabric-api] [-ttl 10m]
//	                   [-issuer http://127.0.0.1:9450] [-key <file>]
//
// serve publishes the key set and logs the settings fabric-api needs to trust
// it. mint prints one token and nothing else, so that a shell can capture it:
//
//	TOKEN="$(fabric-devidp mint -sub alice)"
//
// Both use the same key. By default it lives in the user's configuration
// directory, outside any repository, and is generated on the first run. The
// key identifier follows the key, so restarting the issuer does not
// invalidate the keys a verifier has cached.
package main

import (
	"context"
	"crypto/rsa"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/identity/devissuer"
)

const (
	defaultAddress = "127.0.0.1:9450"
	defaultIssuer  = "http://" + defaultAddress

	// defaultAudience is how a local fabric-api is addressed. It has to equal
	// the IDP_FABRIC_OIDC_AUDIENCE that fabric-api runs with.
	defaultAudience = "fabric-api"

	// maxTTL bounds the lifetime of a minted token. A development token is
	// meant to be minted again, not kept.
	maxTTL = 24 * time.Hour

	shutdownTimeout = 5 * time.Second
)

var errUsage = errors.New("usage: fabric-devidp serve|mint [options]; see -h of either")

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, logger); err != nil {
		logger.Error("fabric-devidp failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer, logger *slog.Logger) error {
	if len(args) == 0 {
		return errUsage
	}
	switch args[0] {
	case "serve":
		return serve(ctx, args[1:], logger)
	case "mint":
		return mint(args[1:], stdout)
	default:
		return fmt.Errorf("unknown command %q; %w", args[0], errUsage)
	}
}

func serve(ctx context.Context, args []string, logger *slog.Logger) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	address := flags.String("addr", defaultAddress, "loopback address to listen on")
	keyPath := flags.String("key", defaultKeyPath(), "PKCS #8 PEM file of the signing key; generated if it does not exist")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("serve takes no argument, got %q", flags.Arg(0))
	}
	if err := requireLoopback(*address); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", *address, err)
	}
	return serveOn(ctx, listener, *keyPath, logger)
}

// serveOn publishes the key set on listener until ctx is done. The issuer
// names itself after the address it actually listens on.
func serveOn(ctx context.Context, listener net.Listener, keyPath string, logger *slog.Logger) error {
	defer func() { _ = listener.Close() }()

	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return err
	}
	issuer, err := devissuer.New("http://"+listener.Addr().String(), key)
	if err != nil {
		return err
	}

	server := &http.Server{Handler: issuer.Handler(), ReadHeaderTimeout: 5 * time.Second}
	served := make(chan error, 1)
	go func() {
		logger.Info("fabric-devidp listening: a development issuer, never to be deployed",
			"key_file", keyPath, "key_id", issuer.KeyID(),
			"IDP_FABRIC_OIDC_ISSUER", issuer.URL(),
			"IDP_FABRIC_OIDC_AUDIENCE", defaultAudience,
			"IDP_FABRIC_OIDC_JWKS_URL", issuer.JWKSURL())
		served <- server.Serve(listener)
	}()

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down: %w", err)
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func mint(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("mint", flag.ContinueOnError)
	subject := flags.String("sub", "", "subject the token is about (required)")
	audiences := flags.String("aud", defaultAudience, "audience the token is addressed to; several are separated by commas")
	ttl := flags.Duration("ttl", devissuer.DefaultTTL, "lifetime of the token, at most "+maxTTL.String())
	issuerURL := flags.String("issuer", defaultIssuer, "issuer written into the token: http:// and the address serve listens on")
	keyPath := flags.String("key", defaultKeyPath(), "PKCS #8 PEM file of the signing key; generated if it does not exist")
	if err := flags.Parse(args); err != nil {
		return err
	}
	switch {
	case flags.NArg() > 0:
		return fmt.Errorf("mint takes no argument, got %q", flags.Arg(0))
	case *subject == "":
		return errors.New("mint needs a subject: -sub <subject>")
	case *ttl <= 0 || *ttl > maxTTL:
		return fmt.Errorf("the lifetime must be positive and at most %s", maxTTL)
	}
	var audience []string
	for _, name := range strings.Split(*audiences, ",") {
		if name = strings.TrimSpace(name); name != "" {
			audience = append(audience, name)
		}
	}

	key, err := loadOrCreateKey(*keyPath)
	if err != nil {
		return err
	}
	issuer, err := devissuer.New(*issuerURL, key)
	if err != nil {
		return err
	}
	token, err := issuer.Mint(devissuer.TokenRequest{Subject: *subject, Audience: audience, TTL: *ttl})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, token)
	return err
}

// requireLoopback refuses an address other machines can reach. The issuer
// signs any subject it is asked to, so the verifier of fabric-api fetches a
// key set over plain http from a loopback IP address only; anything else
// would be an issuer nothing can use.
func requireLoopback(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("the address %q is not host:port", address)
	}
	if ip, err := netip.ParseAddr(host); err != nil || !ip.IsLoopback() {
		return fmt.Errorf("the address %q is not a loopback IP address; fabric-devidp listens on one only, as in %s", address, defaultAddress)
	}
	return nil
}

// defaultKeyPath is the key file in the user's configuration directory, for
// example ~/.config/idp-api-fabric/devidp-key.pem. It is empty when that
// directory is not known.
func defaultKeyPath() string {
	directory, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(directory, "idp-api-fabric", "devidp-key.pem")
}

// loadOrCreateKey reads the signing key at path. If there is none, it
// generates one and writes it there, readable by its owner only. A file that
// exists and cannot be used is an error: the key is never silently replaced,
// because every token signed with it would stop verifying.
func loadOrCreateKey(path string) (*rsa.PrivateKey, error) {
	if path == "" {
		return nil, errors.New("the user's configuration directory is unknown; name the key file with -key")
	}
	data, err := os.ReadFile(path)
	if err == nil {
		key, err := devissuer.DecodeKeyPEM(data)
		if err != nil {
			return nil, fmt.Errorf("the key file %s cannot be used: %w", path, err)
		}
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read the key file: %w", err)
	}

	key, err := devissuer.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("generate the signing key: %w", err)
	}
	encoded, err := devissuer.EncodeKeyPEM(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create the directory of the key file: %w", err)
	}
	// Created exclusively: if another run wrote the file in the meantime,
	// that key is the one to use.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return loadOrCreateKey(path)
	}
	if err != nil {
		return nil, fmt.Errorf("create the key file: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write the key file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("write the key file: %w", err)
	}
	return key, nil
}
