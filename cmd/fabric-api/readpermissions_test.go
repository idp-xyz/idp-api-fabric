package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestReadsFollowTheCurrentWorkspaceAndProductGrants(t *testing.T) {
	s := publicationFor(t)
	target, _ := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	revision, check := s.checkedRevision(revisionSource, "")
	// A review of the revision from before, which whoever reads the
	// revision reads.
	review := last(s.asked(s.pathOf(revision), check, "author", internalSandbox))
	release := s.prepare(revision, check, "1.0.0", "publisher")
	s.require(release, http.StatusCreated, "")
	releaseID := release.id("release_id")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, releaseID, "", "first"), http.StatusOK, "")
	workspace := "/v1/products/" + s.product + "/workspaces/" + s.workspace
	revisionPath := s.revisions + "/" + revision
	releasePath := release.header.Get("Location")
	releaseList := strings.TrimSuffix(releasePath, "/"+releaseID)
	other := s.createWorkspace(s.product, "owner", "operations")
	s.join(s.product, other, "owner", "other-maintainer", "workspace_maintainer")
	otherAsset := strings.TrimSuffix(s.revisionAsset(s.product, other, "other-maintainer", "tracking-api", "HTTP"), "/revisions")
	parcel := s.register("platform-admin", "parcel", "parcel-owner")
	membership := s.join(s.product, s.workspace, "owner", "reader", "internal_reader")
	tokens := map[string]string{}
	for _, caller := range []string{"maintainer", "reader", "security", "clearer"} {
		tokens[caller] = s.token(caller)
	}
	read := func(caller, path string, status int, code string) answer {
		t.Helper()
		got := s.callWith(http.MethodGet, path, tokens[caller], "")
		s.require(got, status, code)
		return got
	}
	for _, path := range []string{s.asset, revisionPath, revisionPath + "/check-runs",
		revisionPath + "/reviews/" + review, workspace + "/reviews", releasePath, slot, slot + "/operations", slot + "/artifact"} {
		read("maintainer", path, http.StatusOK, "")
	}
	read("maintainer", otherAsset, http.StatusNotFound, "API_ASSET_NOT_FOUND")
	s.require(s.change(slot, "maintainer", "WITHDRAW", 1, "", "not a publisher", "maintainer"), http.StatusForbidden, "FORBIDDEN")

	if got := read("reader", s.asset, http.StatusOK, ""); got.body["overview"] != nil {
		t.Fatal("internal reader received a draft-derived overview")
	}
	read("reader", otherAsset, http.StatusNotFound, "API_ASSET_NOT_FOUND")
	read("reader", otherAsset+"/releases", http.StatusNotFound, "API_ASSET_NOT_FOUND")
	for _, path := range []string{releaseList, releasePath, slot, slot + "/operations", slot + "/artifact", target + "/assets"} {
		read("reader", path, http.StatusOK, "")
	}
	for _, path := range []string{revisionPath, workspace + "/reviews", releasePath + "/provenance", releasePath + "/artifact"} {
		read("reader", path, http.StatusForbidden, "FORBIDDEN")
	}
	s.require(s.prepare(revision, check, "1.0.1", "reader"), http.StatusForbidden, "FORBIDDEN")
	s.require(s.change(slot, "reader", "WITHDRAW", 1, "", "not a publisher", "reader"), http.StatusForbidden, "FORBIDDEN")

	security := s.securityRole("security", "security_admin", "owner")
	s.require(security, http.StatusCreated, "")
	s.require(s.securityRole("clearer", "security_reviewer", "owner"), http.StatusCreated, "")
	for _, actor := range []string{"security", "clearer"} {
		for _, path := range []string{s.asset, otherAsset, target + "/assets", slot, slot + "/operations"} {
			read(actor, path, http.StatusOK, "")
		}
		for _, path := range []string{revisionPath, releasePath, slot + "/artifact"} {
			read(actor, path, http.StatusForbidden, "FORBIDDEN")
		}
		read(actor, "/v1/products/"+parcel+"/assets", http.StatusNotFound, "PRODUCT_NOT_FOUND")
		s.require(s.change(slot, actor, "WITHDRAW", 1, "", "not a publisher", actor), http.StatusForbidden, "FORBIDDEN")
	}
	s.require(s.call(http.MethodPost, target+"/access-blocks", "security", `{"reason":"verify deny priority"}`), http.StatusCreated, "")
	read("reader", slot+"/artifact", http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")

	s.revoke(membership, "owner")
	read("reader", releasePath, http.StatusNotFound, "PRODUCT_NOT_FOUND")
	read("reader", slot, http.StatusNotFound, "PRODUCT_NOT_FOUND")
	s.require(s.call(http.MethodDelete, security.header.Get("Location"), "owner", ""), http.StatusNoContent, "")
	read("security", target+"/assets", http.StatusNotFound, "PRODUCT_NOT_FOUND")
}
