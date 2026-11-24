package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/identity"
	governancedomain "go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// An administrator of the platform is an issuer and a subject together, as the
// deployment names it: the same subject under another issuer is no
// administrator, and the caller is told so. The service here trusts one
// issuer and names its administrator under another, so the subject that is
// named is told, with a token of the issuer that is trusted, that it is none;
// behind the issuer that is named it is told that it is one, which is what the
// first answer is compared with.
func TestTheSubjectOfAnAdministratorUnderAnotherIssuerIsToldItIsNoAdministrator(t *testing.T) {
	named, trusted := provider(t), provider(t)
	database := pgtest.MigratedDatabase(t)

	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	t.Cleanup(fabric.Close)
	configured := settings{
		identity:       identity.Config{Issuer: trusted.URL(), Audience: audience, JWKSURL: trusted.JWKSURL()},
		administrators: []governancedomain.Principal{{Issuer: named.URL(), Subject: "platform-admin"}},
	}
	verifier, err := identity.NewVerifier(configured.identity)
	if err != nil {
		t.Fatalf("build the verifier: %v", err)
	}
	log := &lockedBuffer{}
	handler, err := compose(fabric, verifier, configured, slog.New(slog.NewTextHandler(log, nil)))
	if err != nil {
		t.Fatalf("compose the service: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	elsewhere := &stack{t: t, issuer: trusted, server: server, database: database, owner: pgtest.Connect(t, database.Migrator), log: log}

	if got, want := elsewhere.readCaller("platform-admin"), elsewhere.callerBody("platform-admin", false); got.status != http.StatusOK || !reflect.DeepEqual(got.body, want) {
		t.Errorf("the subject of the administrator under another issuer is told %d %v, want 200 %v", got.status, got.body, want)
	}

	// The same administrator behind the issuer that is named: there the subject
	// is the administrator.
	here := startOn(t, named, database, "platform-admin")
	if got, want := here.readCaller("platform-admin"), here.callerBody("platform-admin", true); got.status != http.StatusOK || !reflect.DeepEqual(got.body, want) {
		t.Errorf("the administrator under the issuer that is named is told %d %v, want 200 %v", got.status, got.body, want)
	}
}
