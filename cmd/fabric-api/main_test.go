package main

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	governancedomain "go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

func TestServeAnswersUntilCancelledThenStopsCleanly(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	stopped := make(chan error, 1)
	go func() { stopped <- serve(ctx, listener, handler) }()

	response, err := http.Get("http://" + listener.Addr().String() + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Errorf("GET /: status %d, want 204", response.StatusCode)
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

// environment answers for the variables a test sets and for no other.
func environment(variables map[string]string) func(string) string {
	return func(name string) string { return variables[name] }
}

func complete() map[string]string {
	return map[string]string{
		"IDP_FABRIC_POSTGRES_DSN":  "postgres://fabric_app:secret@db.internal/fabric",
		"IDP_FABRIC_OIDC_ISSUER":   "https://issuer.example",
		"IDP_FABRIC_OIDC_AUDIENCE": "fabric-api",
		"IDP_FABRIC_OIDC_JWKS_URL": "https://issuer.example/.well-known/jwks.json",
	}
}

func TestTheSettingsAreReadFromTheEnvironment(t *testing.T) {
	variables := complete()
	variables["IDP_FABRIC_HTTP_ADDR"] = "10.0.0.5:8443"
	variables["IDP_FABRIC_PLATFORM_ADMIN_SUBJECTS"] = `["alice", "ops, the bot"]`

	got, err := readSettings(environment(variables))
	if err != nil {
		t.Fatalf("read the settings: %v", err)
	}
	if got.address != "10.0.0.5:8443" || got.dsn != variables["IDP_FABRIC_POSTGRES_DSN"] {
		t.Errorf("address %q and DSN %q are not what was set", got.address, got.dsn)
	}
	if got.identity.Issuer != "https://issuer.example" || got.identity.Audience != "fabric-api" ||
		got.identity.JWKSURL != "https://issuer.example/.well-known/jwks.json" {
		t.Errorf("identity %+v is not what was set", got.identity)
	}
	// An administrator is a subject of the one issuer the process trusts.
	want := []governancedomain.Principal{
		{Issuer: "https://issuer.example", Subject: "alice"},
		{Issuer: "https://issuer.example", Subject: "ops, the bot"},
	}
	if len(got.administrators) != 2 || got.administrators[0] != want[0] || got.administrators[1] != want[1] {
		t.Errorf("administrators %+v, want %+v", got.administrators, want)
	}
}

func TestTheDefaultsDecideNothingAboutTrust(t *testing.T) {
	got, err := readSettings(environment(complete()))
	if err != nil {
		t.Fatalf("read the settings: %v", err)
	}
	// Loopback only, and nobody administers the platform.
	if got.address != "127.0.0.1:8080" {
		t.Errorf("the default address is %q, want loopback only", got.address)
	}
	if len(got.administrators) != 0 {
		t.Errorf("without a list, %+v administer the platform", got.administrators)
	}
}

func TestSettingsTheProcessCannotRunWithAreRefused(t *testing.T) {
	for _, required := range []string{
		"IDP_FABRIC_POSTGRES_DSN", "IDP_FABRIC_OIDC_ISSUER", "IDP_FABRIC_OIDC_AUDIENCE", "IDP_FABRIC_OIDC_JWKS_URL",
	} {
		variables := complete()
		delete(variables, required)
		_, err := readSettings(environment(variables))
		if err == nil || !strings.Contains(err.Error(), required) {
			t.Errorf("without %s: got %v, want an error that names it", required, err)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("the error repeats the connection string: %v", err)
		}
	}

	for name, listed := range map[string]string{
		"not JSON":              `alice,bob`,
		"an object":             `{"alice":true}`,
		"a list of non-strings": `["alice", 7]`,
	} {
		variables := complete()
		variables["IDP_FABRIC_PLATFORM_ADMIN_SUBJECTS"] = listed
		if _, err := readSettings(environment(variables)); err == nil {
			t.Errorf("administrators given as %s were accepted", name)
		}
	}
}
