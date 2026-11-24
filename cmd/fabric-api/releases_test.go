package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

// A publisher of the workspace makes an approved revision a release: the
// files as they were recorded, under a version of the asset, bound to the
// review that approved them and the check that review rests on. The release
// is written once. Asking again for the same version of the same revision
// and review answers the release that is there, and the version names no
// other one. Whoever wrote, checked, submitted or approved the revision
// does not release it.
func TestAPublisherPreparesAnImmutableReleaseOfAnApprovedRevision(t *testing.T) {
	s := publicationFor(t)
	releases := s.asset + "/releases"
	revision, review := s.approve(revisionSource, "", internalSandbox)
	other, otherReview := s.approve(revisionSource+"# next\n", "", internalSandbox)
	draft := s.call(http.MethodPost, s.revisions, "author", uploadRevision("openapi.yaml", revisionSource+"# a draft\n"))
	s.require(draft, http.StatusCreated, "")
	asking := func(revision, review, version string) string {
		return jsonBody(map[string]string{"revision_id": revision, "review_id": review, "version": version})
	}

	// Reading the revision is not releasing it, and neither is owning the
	// product or maintaining the workspace.
	for _, caller := range []string{"author", "reviewer", "owner", "maintainer"} {
		s.require(s.prepare(revision, review, "1.0.0", caller), http.StatusForbidden, "FORBIDDEN")
	}
	// Whoever took part in the revision does not release it, publisher or not.
	s.join(s.product, s.workspace, "owner", "author", "internal_publisher")
	s.join(s.product, s.workspace, "owner", "reviewer", "internal_publisher")
	for _, caller := range []string{"author", "reviewer"} {
		s.require(s.prepare(revision, review, "1.0.0", caller), http.StatusForbidden, "PUBLISHER_NOT_INDEPENDENT")
	}
	if rows := s.count("contractregistry.contract_release") + s.count("contractregistry.release_artifact"); rows != 0 {
		t.Fatalf("the refusals left %d rows of releases and artifacts", rows)
	}

	first := s.prepare(revision, review, "1.0.0", "publisher")
	s.require(first, http.StatusCreated, "")
	id := first.id("release_id")
	imported := s.call(http.MethodGet, s.revisions+"/"+revision, "author", "")
	if first.body["status"] != "READY" || first.body["version"] != "1.0.0" || first.body["revision_id"] != revision ||
		first.body["review_id"] != review || first.body["artifact_format"] != "fabric-openapi-source/v1" ||
		first.body["ref_closure_digest"] != imported.body["ref_closure_digest"] ||
		first.body["prepared_by"].(map[string]any)["subject"] != "publisher" ||
		first.header.Get("Location") != releases+"/"+id || first.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the release: %v %v", first.body, first.header)
	}
	again := s.prepare(revision, review, "1.0.0", "publisher")
	if again.status != http.StatusOK || !reflect.DeepEqual(again.body, first.body) || again.header.Get("Location") != "" {
		t.Fatalf("the same release asked again: %d %v, want 200 and the release as it was answered", again.status, again.body)
	}
	s.expect("a version names one release", "publisher", []exchange{
		{http.MethodPost, releases, asking(other, otherReview, "1.0.0"), http.StatusConflict, "RELEASE_VERSION_CONFLICT"},
		{http.MethodPost, releases, asking(revision, otherReview, "1.0.0"), http.StatusConflict, "RELEASE_VERSION_CONFLICT"},
		{http.MethodPost, releases, asking(other, review, "1.0.0"), http.StatusConflict, "RELEASE_VERSION_CONFLICT"},
	})
	s.expect("a release is of an approved revision, under the review that approved it", "publisher", []exchange{
		{http.MethodPost, releases, asking(draft.id("revision_id"), review, "2.0.0"), http.StatusConflict, "REVISION_NOT_APPROVED"},
		{http.MethodPost, releases, asking("00000000-0000-4000-8000-000000000000", review, "2.0.0"), http.StatusNotFound, "REVISION_NOT_FOUND"},
		{http.MethodPost, releases, asking(other, review, "2.0.0"), http.StatusConflict, "APPROVAL_REQUIRED"},
		{http.MethodPost, releases, asking(revision, review, "two point zero"), http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, releases, asking(revision, review, ""), http.StatusBadRequest, "REQUEST_INVALID"},
	})

	// The same files under another version are another release of the same
	// bytes: the artifact is kept once.
	second := s.prepare(revision, review, "1.0.1", "publisher")
	s.require(second, http.StatusCreated, "")
	if second.id("release_id") == id || second.body["artifact_digest"] != first.body["artifact_digest"] ||
		s.count("contractregistry.contract_release") != 2 || s.count("contractregistry.release_artifact") != 1 {
		t.Fatalf("a second version of the same revision: %v; %d releases, %d artifacts", second.body,
			s.count("contractregistry.contract_release"), s.count("contractregistry.release_artifact"))
	}

	// Whoever reads the revisions of the workspace reads its releases.
	for _, caller := range []string{"author", "reviewer", "publisher"} {
		if read := s.call(http.MethodGet, releases+"/"+id, caller, ""); read.status != http.StatusOK || !reflect.DeepEqual(read.body, first.body) {
			t.Errorf("%s reads the release: %d %v, want it as it was answered", caller, read.status, read.body)
		}
	}
	page := s.call(http.MethodGet, releases+"?page_size=1", "publisher", "")
	s.require(page, http.StatusOK, "")
	rest := s.call(http.MethodGet, releases+"?page_size=1&page_token="+page.id("next_page_token"), "publisher", "")
	if items := page.body["items"].([]any); len(items) != 1 || !reflect.DeepEqual(items[0], first.body) || rest.status != http.StatusOK ||
		!reflect.DeepEqual(rest.body["items"], []any{second.body}) || rest.body["next_page_token"] != nil {
		t.Errorf("the releases, a page at a time: %v then %v", page.body, rest.body)
	}
	tracking := strings.TrimSuffix(s.revisionAsset(s.product, s.workspace, "maintainer", "tracking-api", "HTTP"), "/revisions")
	s.expect("a release is read under its asset", "publisher", []exchange{
		{http.MethodGet, tracking + "/releases/" + id, "", http.StatusNotFound, "RELEASE_NOT_FOUND"},
		{http.MethodGet, releases + "/00000000-0000-4000-8000-000000000000", "", http.StatusNotFound, "RELEASE_NOT_FOUND"},
		{http.MethodGet, releases + "/latest", "", http.StatusBadRequest, "REQUEST_INVALID"},
	})
	s.expect("owning the product authorizes reading its releases", "owner", []exchange{
		{http.MethodGet, releases + "/" + id, "", http.StatusOK, ""},
		{http.MethodGet, releases, "", http.StatusOK, ""},
	})

	// The artifact is the files as they were recorded, under its digest.
	var payload []byte
	digest, _ := first.body["artifact_digest"].(string)
	if err := s.owner.QueryRow(t.Context(), `SELECT payload FROM contractregistry.release_artifact WHERE artifact_digest = $1`, digest).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		Format     string `json:"format"`
		Entrypoint string `json:"entrypoint"`
		Closure    string `json:"ref_closure_digest"`
		Files      []struct {
			Path    string `json:"path"`
			Content []byte `json:"content_base64"`
		} `json:"files"`
	}
	if err := json.Unmarshal(payload, &bundle); err != nil || fmt.Sprintf("sha256:%x", sha256.Sum256(payload)) != digest ||
		bundle.Format != "fabric-openapi-source/v1" || bundle.Entrypoint != "openapi.yaml" || bundle.Closure != first.body["ref_closure_digest"] ||
		len(bundle.Files) != 1 || bundle.Files[0].Path != "openapi.yaml" || string(bundle.Files[0].Content) != revisionSource {
		t.Fatalf("the artifact is %s (%v), want the recorded files under %s", payload, err, digest)
	}

	// The process can add a release and read it. It cannot change or remove one.
	app := pgtest.Connect(t, s.database.Application)
	for _, statement := range []string{
		`UPDATE contractregistry.contract_release SET release_version = 'other'`,
		`DELETE FROM contractregistry.contract_release`,
		`UPDATE contractregistry.release_artifact SET payload = 'x'`,
		`DELETE FROM contractregistry.release_artifact`,
	} {
		if _, err := app.Exec(t.Context(), statement); err == nil || !strings.Contains(err.Error(), "42501") {
			t.Errorf("%s as the application role: %v, want it refused for lack of privilege", statement, err)
		}
	}

	// Each release that was recorded left one audit record and one event;
	// the one that was answered again left neither, and every refusal of
	// the right to release is on record as one.
	recorded := occurrences(s.recorded(func(row audited) bool {
		return row.Action == "release.prepare" && row.Outcome == "SUCCEEDED" && row.Rule == "AC-04" &&
			row.Subject == "publisher" && row.ResourceType == "ContractRelease" && row.ResourceID != ""
	}))
	refused := occurrences(s.recorded(func(row audited) bool {
		return row.Action == "release.prepare" && row.Outcome != "SUCCEEDED" && row.Rule == "DENY-03"
	}))
	var events int
	if err := s.owner.QueryRow(t.Context(), `SELECT count(*) FROM bento.outbox
WHERE event_type = 'fabric.contractregistry.release.prepared' AND scope = $1`, s.product).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if recorded != 2 || refused != 6 || events != 2 {
		t.Errorf("%d releases and %d refusals are on record and %d events were written, want 2, 6 and 2: %v", recorded, refused, events,
			s.recorded(func(row audited) bool { return strings.HasPrefix(row.Action, "release.") }))
	}
}

// A release is prepared under an approval that still holds, in a product
// that takes new work, and wholly or not at all.
func TestAReleaseNeedsAnApprovalThatStillHoldsAndIsRecordedWhollyOrNotAtAll(t *testing.T) {
	s := publicationFor(t)
	releases := s.asset + "/releases"
	// The reviewer maintains the workspace besides, from before it approves.
	s.join(s.product, s.workspace, "owner", "reviewer", "workspace_maintainer")
	revision, review := s.approve(revisionSource, "", internalSandbox)

	// Nothing is left of a release whose event could not be written.
	if _, err := s.owner.Exec(t.Context(), `ALTER TABLE bento.outbox ADD CONSTRAINT test_reject_release
CHECK (event_type <> 'fabric.contractregistry.release.prepared')`); err != nil {
		t.Fatal(err)
	}
	records := s.count("platformoperations.audit_record")
	s.require(s.prepare(revision, review, "1.0.0", "publisher"), http.StatusInternalServerError, "INTERNAL_ERROR")
	if s.count("contractregistry.contract_release") != 0 || s.count("contractregistry.release_artifact") != 0 ||
		s.count("platformoperations.audit_record") != records {
		t.Fatal("the release that failed left a release, an artifact or an audit record")
	}
	if _, err := s.owner.Exec(t.Context(), `ALTER TABLE bento.outbox DROP CONSTRAINT test_reject_release`); err != nil {
		t.Fatal(err)
	}

	// A suspended product takes no release and still shows the ones it has.
	first := s.prepare(revision, review, "1.0.0", "publisher")
	s.require(first, http.StatusCreated, "")
	if _, err := s.owner.Exec(t.Context(), `UPDATE productgovernance.product SET status = 'SUSPENDED' WHERE product_id = $1`, s.product); err != nil {
		t.Fatal(err)
	}
	s.require(s.prepare(revision, review, "1.0.1", "publisher"), http.StatusConflict, "PRODUCT_NOT_ACTIVE")
	s.require(s.call(http.MethodGet, releases+"/"+first.id("release_id"), "author", ""), http.StatusOK, "")
	if _, err := s.owner.Exec(t.Context(), `UPDATE productgovernance.product SET status = 'ACTIVE' WHERE product_id = $1`, s.product); err != nil {
		t.Fatal(err)
	}

	// The approval counts while the grant it was given under is in force:
	// the grant of a reviewer, not whatever else its holder is in the
	// workspace. A reviewer who is named again approves again; what was
	// approved before is not approved by that.
	var membership string
	if err := s.owner.QueryRow(t.Context(), `SELECT membership_id FROM productgovernance.workspace_membership
WHERE product_id = $1 AND workspace_id = $2 AND principal_subject = 'reviewer'
  AND role = 'internal_reviewer' AND revoked_at IS NULL`, s.product, s.workspace).Scan(&membership); err != nil {
		t.Fatal(err)
	}
	s.revoke("/v1/products/"+s.product+"/workspaces/"+s.workspace+"/memberships/"+membership, "owner")
	s.require(s.prepare(revision, review, "1.0.1", "publisher"), http.StatusConflict, "REVIEWER_AUTH_REVOKED")
	s.join(s.product, s.workspace, "owner", "reviewer", "internal_reviewer")
	s.require(s.prepare(revision, review, "1.0.1", "publisher"), http.StatusConflict, "REVIEWER_AUTH_REVOKED")
	// The release that was prepared before stays what it is.
	s.require(s.call(http.MethodGet, releases+"/"+first.id("release_id"), "publisher", ""), http.StatusOK, "")
	s.require(s.prepare(revision, review, "1.0.0", "publisher"), http.StatusOK, "")
	if s.count("contractregistry.contract_release") != 1 {
		t.Fatalf("%d releases are recorded, want the one", s.count("contractregistry.contract_release"))
	}
}
