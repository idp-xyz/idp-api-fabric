package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/identity"
	"go.idp.xyz/idp-api-fabric/internal/platform/identity/devissuer"
)

func TestTheKeyIsGeneratedOnceAndKeptPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "devidp-key.pem")

	first, err := loadOrCreateKey(path)
	if err != nil {
		t.Fatalf("create the key: %v", err)
	}
	again, err := loadOrCreateKey(path)
	if err != nil {
		t.Fatalf("load the key: %v", err)
	}
	if !first.Equal(again) {
		t.Error("the second run got another key than the first")
	}

	// Windows has no owner-only mode to check.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("the key file has mode %o, want 600", mode)
		}
	}
}

func TestAKeyFileThatCannotBeUsedIsNotReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devidp-key.pem")
	if err := os.WriteFile(path, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadOrCreateKey(path); err == nil {
		t.Fatal("a key was returned for a file that holds none")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "not a key" {
		t.Error("the unusable key file was overwritten")
	}

	if _, err := loadOrCreateKey(""); err == nil {
		t.Error("a key was returned without a key file")
	}
}

func TestOnlyALoopbackAddressIsListenedOn(t *testing.T) {
	for _, address := range []string{"127.0.0.1:9450", "127.0.0.1:0", "[::1]:9450"} {
		if err := requireLoopback(address); err != nil {
			t.Errorf("%s was refused: %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:9450", ":9450", "[::]:9450", "192.168.1.5:9450", "localhost:9450", "127.0.0.1", "nonsense"} {
		if err := requireLoopback(address); err == nil {
			t.Errorf("%s was accepted, want a refusal", address)
		}
	}

	err := run(context.Background(), []string{"serve", "-addr", "0.0.0.0:9450", "-key", filepath.Join(t.TempDir(), "key.pem")},
		io.Discard, slog.New(slog.DiscardHandler))
	if err == nil {
		t.Error("serve accepted an address other machines can reach")
	}
}

// What mint prints is a token the verifier of fabric-api accepts, when that
// verifier is configured with the settings serve logs.
func TestAMintedTokenIsAcceptedByAVerifierBoundToTheServedKeySet(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "devidp-key.pem")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	issuerURL := "http://" + listener.Addr().String()

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)
	go func() { stopped <- serveOn(ctx, listener, keyPath, slog.New(slog.DiscardHandler)) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-stopped:
			if err != nil {
				t.Errorf("a cancelled issuer must stop cleanly, got %v", err)
			}
		case <-time.After(2 * shutdownTimeout):
			t.Error("the issuer did not stop after its context was cancelled")
		}
	})

	var printed bytes.Buffer
	err = run(ctx, []string{"mint", "-sub", "alice", "-issuer", issuerURL, "-key", keyPath}, &printed, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	token, newline := strings.CutSuffix(printed.String(), "\n")
	if !newline || strings.ContainsAny(token, " \n") || strings.Count(token, ".") != 2 {
		t.Fatalf("mint printed %q, want one token and a newline", printed.String())
	}

	verifier, err := identity.NewVerifier(identity.Config{
		Issuer: issuerURL, Audience: defaultAudience, JWKSURL: issuerURL + devissuer.JWKSPath,
	})
	if err != nil {
		t.Fatalf("build the verifier: %v", err)
	}
	principal, err := verifier.Verify(ctx, token)
	if err != nil {
		t.Fatalf("verify the minted token: %v", err)
	}
	if want := (identity.Principal{Issuer: issuerURL, Subject: "alice"}); principal != want {
		t.Errorf("principal %+v, want %+v", principal, want)
	}

	// A token addressed to another service is not accepted here.
	printed.Reset()
	err = run(ctx, []string{"mint", "-sub", "alice", "-aud", "another-service", "-issuer", issuerURL, "-key", keyPath},
		&printed, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("mint for another audience: %v", err)
	}
	if _, err := verifier.Verify(ctx, strings.TrimSpace(printed.String())); err == nil {
		t.Error("a token addressed to another service was accepted")
	}
}

func TestRequestsThatSayTooLittleOrTooMuchAreRefused(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "devidp-key.pem")
	cases := map[string][]string{
		"no command":             {},
		"an unknown command":     {"sign"},
		"mint without subject":   {"mint", "-key", keyPath},
		"mint with no lifetime":  {"mint", "-sub", "alice", "-ttl", "0s", "-key", keyPath},
		"mint for too long":      {"mint", "-sub", "alice", "-ttl", "25h", "-key", keyPath},
		"mint with an argument":  {"mint", "-sub", "alice", "-key", keyPath, "extra"},
		"mint with no audience":  {"mint", "-sub", "alice", "-aud", " , ", "-key", keyPath},
		"mint with a bad issuer": {"mint", "-sub", "alice", "-issuer", "127.0.0.1:9450", "-key", keyPath},
		"serve with an argument": {"serve", "-key", keyPath, "extra"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var printed bytes.Buffer
			if err := run(context.Background(), args, &printed, slog.New(slog.DiscardHandler)); err == nil {
				t.Fatalf("the request was accepted and printed %q", printed.String())
			}
			if printed.Len() != 0 {
				t.Errorf("a refused request printed %q", printed.String())
			}
		})
	}
}
