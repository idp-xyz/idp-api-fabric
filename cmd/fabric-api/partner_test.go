package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/identity"
	"go.idp.xyz/idp-api-fabric/internal/platform/identity/devissuer"
)

func TestCargoPaiPartnerOriginUsesLiveGrantsAndPublicationState(t *testing.T) {
	s := publicationFor(t)
	target, _ := s.target(partnerSandbox, "cargopai-developers")
	slot := assetSlot(target, s.asset)
	revision, check := s.checkedRevision(revisionSource, "")
	released := s.prepare(revision, check, "1.0.0", "publisher")
	s.require(released, http.StatusCreated, "")
	releaseID := released.id("release_id")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, releaseID, "", "initial"),
		http.StatusConflict, "PUBLICATION_TARGET_UNAVAILABLE")

	parcel := s.register("platform-admin", "parcel", "parcel-owner")
	parcelWorkspace := s.createWorkspace(parcel, "parcel-owner", "engineering")
	s.join(parcel, parcelWorkspace, "parcel-owner", "parcel-maintainer", "workspace_maintainer")
	parcelRevisions := s.revisionAsset(parcel, parcelWorkspace, "parcel-maintainer", "parcel-api", "HTTP")
	parcelAsset := last(parcelRevisions[:len(parcelRevisions)-len("/revisions")])
	partnerIssuer := provider(t)
	const partnerAudience = "cargopai-reference"
	var revoked, providerDown atomic.Bool
	grantService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]string
		if r.Method != http.MethodPost || r.URL.Path != "/decisions" ||
			r.Header.Get("Authorization") != "Bearer local-grant-credential" ||
			json.NewDecoder(r.Body).Decode(&input) != nil ||
			input["issuer"] != partnerIssuer.URL() || input["product_id"] != s.product ||
			input["target_id"] != last(target) || input["audience"] != "partner" ||
			input["environment"] != "sandbox" || input["action"] != "contract.download" ||
			(input["api_asset_id"] != last(s.asset) && input["api_asset_id"] != parcelAsset) {
			t.Errorf("partner grant scope or request was incorrect: %#v", input)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if providerDown.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{
			"allowed": input["subject"] == "cargo-partner" && !revoked.Load(),
		})
	}))
	t.Cleanup(grantService.Close)
	origin := s.startPartnerOrigin(partnerSettings{
		productID: s.product, targetID: last(target), grantURL: grantService.URL + "/decisions",
		grantToken: "local-grant-credential",
		identity:   identity.Config{Issuer: partnerIssuer.URL(), JWKSURL: partnerIssuer.JWKSURL(), Audience: partnerAudience},
	})
	mint := func(subject, tokenAudience string) string {
		t.Helper()
		value, err := partnerIssuer.Mint(devissuer.TokenRequest{Subject: subject, Audience: []string{tokenAudience}})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	allowedToken := mint("cargo-partner", partnerAudience)
	parcelToken := mint("parcel-partner", partnerAudience)
	reference := "/v1/reference/" + last(s.asset)
	expect := func(path, token string, status int) ([]byte, http.Header) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, origin.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := origin.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("partner origin returned %d %v: %s (%v), want %d with no-store",
				response.StatusCode, response.Header, body, err, status)
		}
		return body, response.Header
	}

	expect(reference, "", http.StatusUnauthorized)
	expect(reference, s.token("author"), http.StatusUnauthorized)
	expect(reference, mint("cargo-partner", audience), http.StatusUnauthorized)
	expect("/v1/products", allowedToken, http.StatusNotFound)
	if got := s.callWithHeaders(http.MethodGet, "/v1/caller", allowedToken, "", nil); got.status != http.StatusUnauthorized {
		t.Fatalf("management accepted the partner token: %v", got)
	}
	expect(reference, allowedToken, http.StatusNotFound)

	s.require(s.change(slot, "publisher", "ACTIVATE", 0, releaseID, "", "first"), http.StatusOK, "")
	body, headers := expect(reference, allowedToken, http.StatusOK)
	if digest := fmt.Sprintf("sha256:%x", sha256.Sum256(body)); headers.Get("X-Artifact-Digest") != digest ||
		headers.Get("Content-Type") != "application/octet-stream" || len(body) == 0 {
		t.Fatalf("unexpected immutable artifact: %v", headers)
	}
	expect(reference, parcelToken, http.StatusNotFound)
	expect("/v1/reference/"+parcelAsset, allowedToken, http.StatusNotFound)
	s.require(s.call(http.MethodGet, slot+"/artifact", "author", ""), http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")

	revoked.Store(true)
	expect(reference, allowedToken, http.StatusNotFound)
	revoked.Store(false)
	providerDown.Store(true)
	expect(reference, allowedToken, http.StatusServiceUnavailable)
	providerDown.Store(false)
	expect(reference, allowedToken, http.StatusOK)

	s.require(s.securityRole("security", "security_admin", "owner"), http.StatusCreated, "")
	s.require(s.securityRole("clearer", "security_reviewer", "owner"), http.StatusCreated, "")
	blocks := target + "/access-blocks"
	block := s.call(http.MethodPost, blocks, "security", `{"reason":"isolated revocation test"}`)
	s.require(block, http.StatusCreated, "")
	expect(reference, allowedToken, http.StatusNotFound)
	s.require(s.change(slot, "publisher", "WITHDRAW", 1, "", "withdraw", "withdraw"), http.StatusOK, "")
	s.require(s.call(http.MethodPost, block.header.Get("Location")+"/clearance", "clearer", `{"reason":"resolved"}`), http.StatusOK, "")
	expect(reference, allowedToken, http.StatusNotFound)
	s.require(s.change(slot, "publisher", "RESTORE", 2, releaseID, "restore", "restore"), http.StatusOK, "")
	restored, restoredHeaders := expect(reference, allowedToken, http.StatusOK)
	if !bytes.Equal(body, restored) || headers.Get("X-Artifact-Digest") != restoredHeaders.Get("X-Artifact-Digest") {
		t.Fatal("restoring the slot changed the partner artifact")
	}

	// A release on a check that the tools have moved on from is put on the
	// partner slot, served there and returned to, as any release is: the
	// partner origin asks no more of it than the internal origin does.
	later := recordAnotherCheck(t, checkFixture{stack: s.stack}, checkRef(s, revision), check)
	before := s.preparedOn(releaseID, later, "0.9.0")
	s.require(s.change(slot, "publisher", "ACTIVATE", 3, before, "", "before"), http.StatusOK, "")
	if served, _ := expect(reference, allowedToken, http.StatusOK); !bytes.Equal(served, body) {
		t.Fatal("the release on the earlier check is served with other bytes")
	}
	s.require(s.change(slot, "publisher", "WITHDRAW", 4, "", "withdraw", "withdraw-before"), http.StatusOK, "")
	expect(reference, allowedToken, http.StatusNotFound)
	s.require(s.change(slot, "publisher", "RESTORE", 5, before, "restore", "restore-before"), http.StatusOK, "")
	expect(reference, allowedToken, http.StatusOK)
}

func (s *stack) startPartnerOrigin(partner partnerSettings) *httptest.Server {
	s.t.Helper()
	database, err := db.Open(s.t.Context(), s.database.Application)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(database.Close)
	internalIdentity := identity.Config{Issuer: s.issuer.URL(), JWKSURL: s.issuer.JWKSURL(), Audience: audience}
	verifier, err := identity.NewVerifier(internalIdentity)
	if err != nil {
		s.t.Fatal(err)
	}
	handlers, err := compose(database, verifier, settings{identity: internalIdentity, partner: &partner},
		slog.New(slog.NewTextHandler(s.log, nil)))
	if err != nil || handlers.partner == nil {
		s.t.Fatalf("wire partner origin: %v", err)
	}
	s.server = httptest.NewServer(handlers)
	s.t.Cleanup(s.server.Close)
	origin := httptest.NewServer(handlers.partner)
	s.t.Cleanup(origin.Close)
	return origin
}
