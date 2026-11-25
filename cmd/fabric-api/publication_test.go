package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
)

var internalSandbox = [2]string{"internal", "sandbox"}

type releaseFixture struct {
	*reviewFixture
	asset string
}

func publicationFor(t *testing.T) *releaseFixture {
	t.Helper()
	s := reviewsFor(t)
	s.join(s.product, s.workspace, "owner", "publisher", "internal_publisher")
	return &releaseFixture{reviewFixture: s, asset: strings.TrimSuffix(s.revisions, "/revisions")}
}

func (s *releaseFixture) target(scope [2]string, portal string) (path, configPath string) {
	s.t.Helper()
	base := "/v1/products/" + s.product
	config := s.call(http.MethodPost, base+"/audience-configs", "owner", jsonBody(map[string]string{
		"audience": scope[0], "environment": scope[1],
	}))
	s.require(config, http.StatusCreated, "")
	target := s.call(http.MethodPost, base+"/publication-targets", "owner", jsonBody(map[string]string{
		"audience_config_id": config.id("audience_config_id"), "portal_key": portal,
	}))
	s.require(target, http.StatusCreated, "")
	return target.header.Get("Location"), base + "/audience-configs/" + config.id("audience_config_id")
}

func (s *releaseFixture) approve(source, baselineID string, scopes ...[2]string) (revisionID, reviewID string) {
	s.t.Helper()
	imported := s.call(http.MethodPost, s.revisions, "author", uploadRevision("openapi.yaml", source))
	s.require(imported, http.StatusCreated, "")
	revisionID = imported.id("revision_id")
	checkPath := s.revisions + "/" + revisionID + "/check-runs"
	if baselineID != "" {
		checkPath += "?baseline_revision_id=" + baselineID
	}
	checked := s.call(http.MethodPost, checkPath, "author", "")
	s.require(checked, http.StatusCreated, "")
	checkID := checked.body["check_run"].(map[string]any)["check_run_id"].(string)
	review := s.call(http.MethodPost, s.revisions+"/"+revisionID+"/reviews", "author", asking(checkID, scopes...))
	s.require(review, http.StatusCreated, "")
	reviewID = review.id("review_id")
	approved := s.call(http.MethodPost, review.header.Get("Location")+"/decision", "reviewer",
		deciding("APPROVED", "", time.Now().Add(time.Hour)))
	s.require(approved, http.StatusOK, "")
	return revisionID, reviewID
}

func (s *releaseFixture) prepare(revision, review, version, caller string) answer {
	s.t.Helper()
	return s.call(http.MethodPost, s.asset+"/releases", caller, jsonBody(map[string]string{
		"revision_id": revision, "review_id": review, "version": version,
	}))
}

func (s *releaseFixture) change(slot, actor, action string, generation int64, releaseID, reason, key string) answer {
	s.t.Helper()
	body := map[string]any{"action": action, "expected_generation": generation}
	if releaseID != "" {
		body["release_id"] = releaseID
	}
	if reason != "" {
		body["reason"] = reason
	}
	return s.callWithHeaders(http.MethodPost, slot+"/operations", s.token(actor), jsonBody(body), http.Header{"Idempotency-Key": {key}})
}

func (s *releaseFixture) securityRole(subject, role, grantor string) answer {
	s.t.Helper()
	return s.call(http.MethodPost, "/v1/products/"+s.product+"/role-assignments", grantor, jsonBody(map[string]any{
		"role": role, "principal": map[string]string{"issuer": s.issuer.URL(), "subject": subject},
	}))
}

func (s *releaseFixture) require(got answer, status int, code string) {
	s.t.Helper()
	if got.status != status || got.code() != code {
		s.t.Fatalf("got %d %v, want status %d error code %q\n%s", got.status, got.body, status, code, s.log.String())
	}
}

func jsonBody(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func assetSlot(target, asset string) string {
	return target + asset[strings.LastIndex(asset, "/assets/"):]
}

func (s *releaseFixture) requireArtifact(slot, digest, source string) {
	s.t.Helper()
	request, err := http.NewRequestWithContext(s.t.Context(), http.MethodGet, s.server.URL+slot+"/artifact", nil)
	if err != nil {
		s.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+s.token("author"))
	response, err := s.server.Client().Do(request)
	if err != nil {
		s.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	if actual := fmt.Sprintf("sha256:%x", sha256.Sum256(data)); response.StatusCode != http.StatusOK ||
		response.Header.Get("X-Artifact-Digest") != digest || actual != digest ||
		response.Header.Get("Content-Type") != "application/octet-stream" || response.Header.Get("Cache-Control") != "no-store" {
		s.t.Fatalf("artifact responded %d %v, calculated %s and expected %s", response.StatusCode, response.Header, actual, digest)
	}
	var bundle struct {
		Format string `json:"format"`
		Files  []struct {
			Path    string `json:"path"`
			Content []byte `json:"content_base64"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &bundle); err != nil || bundle.Format != "fabric-openapi-source/v1" ||
		len(bundle.Files) != 1 || bundle.Files[0].Path != "openapi.yaml" || string(bundle.Files[0].Content) != source {
		s.t.Fatalf("unexpected release artifact %s (%v)", data, err)
	}
}

func TestPublicationStageKeepsImmutableReleasesAndRecoversSlots(t *testing.T) {
	s := publicationFor(t)
	target, _ := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	revision, review := s.approve(revisionSource, "", internalSandbox)
	s.require(s.prepare(revision, review, "1.0.0", "author"), http.StatusForbidden, "FORBIDDEN")
	s.join(s.product, s.workspace, "owner", "author", "internal_publisher")
	s.require(s.prepare(revision, review, "1.0.0", "author"), http.StatusForbidden, "PUBLISHER_NOT_INDEPENDENT")
	first := s.prepare(revision, review, "1.0.0", "publisher")
	s.require(first, http.StatusCreated, "")
	firstID := first.id("release_id")
	if again := s.prepare(revision, review, "1.0.0", "publisher"); again.status != http.StatusOK || again.id("release_id") != firstID {
		t.Fatalf("retry did not return the same release: %d %v", again.status, again.body)
	}
	s.require(s.prepare(revision, ids.NewUUID(), "1.0.0", "publisher"), http.StatusConflict, "RELEASE_VERSION_CONFLICT")
	s.require(s.call(http.MethodGet, first.header.Get("Location"), "reviewer", ""), http.StatusOK, "")
	if initial := s.call(http.MethodGet, slot, "author", ""); initial.status != http.StatusOK || initial.body["generation"] != float64(0) || initial.body["visibility"] != "EMPTY" {
		t.Fatalf("unexpected empty slot: %d %v", initial.status, initial.body)
	}

	var changes [2]answer
	start := make(chan struct{})
	var calls sync.WaitGroup
	for i := range changes {
		calls.Go(func() {
			<-start
			changes[i] = s.change(slot, "publisher", "ACTIVATE", 0, firstID, "", fmt.Sprintf("first-%d", i))
		})
	}
	close(start)
	calls.Wait()
	winner := -1
	for i, change := range changes {
		if change.status == http.StatusOK {
			if winner != -1 {
				t.Fatal("two commands replaced the same empty generation")
			}
			winner = i
			continue
		}
		s.require(change, http.StatusPreconditionFailed, "PRECONDITION_FAILED")
	}
	if winner < 0 {
		t.Fatal("no command activated the slot")
	}
	key := fmt.Sprintf("first-%d", winner)
	if retry := s.change(slot, "publisher", "ACTIVATE", 0, firstID, "", key); retry.status != http.StatusOK || retry.id("operation_id") != changes[winner].id("operation_id") {
		t.Fatalf("retry did not replay the original operation: %d %v", retry.status, retry.body)
	}
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, firstID, "different", key), http.StatusConflict, "IDEMPOTENCY_CONFLICT")
	s.requireArtifact(slot, first.id("artifact_digest"), revisionSource)

	secondSource := strings.ReplaceAll(revisionSource, "v1", "v2")
	secondRevision, secondReview := s.approve(secondSource, revision, internalSandbox)
	second := s.prepare(secondRevision, secondReview, "2.0.0", "publisher")
	s.require(second, http.StatusCreated, "")
	releases := s.call(http.MethodGet, s.asset+"/releases?page_size=1", "publisher", "")
	s.require(releases, http.StatusOK, "")
	if len(releases.body["items"].([]any)) != 1 || releases.id("next_page_token") == "" {
		t.Fatalf("unexpected release history page %v", releases.body)
	}
	s.require(s.change(slot, "publisher", "ACTIVATE", 1, second.id("release_id"), "", "second"), http.StatusOK, "")
	s.requireArtifact(slot, second.id("artifact_digest"), secondSource)
	s.require(s.change(slot, "publisher", "RESTORE", 2, firstID, "return to prior version", "rollback"), http.StatusOK, "")
	s.requireArtifact(slot, first.id("artifact_digest"), revisionSource)

	other := *s
	otherReview := *s.reviewFixture
	other.reviewFixture = &otherReview
	other.revisions = s.revisionAsset(s.product, s.workspace, "maintainer", "tracking-api", "HTTP")
	other.asset = strings.TrimSuffix(other.revisions, "/revisions")
	otherRevision, otherDecision := other.approve(revisionSource, "", internalSandbox)
	otherRelease := other.prepare(otherRevision, otherDecision, "1.0.0", "publisher")
	s.require(otherRelease, http.StatusCreated, "")
	otherSlot := assetSlot(target, other.asset)
	s.require(s.change(otherSlot, "publisher", "ACTIVATE", 0, otherRelease.id("release_id"), "", key), http.StatusOK, "")
	if got := s.count("contractregistry.release_artifact"); got != 2 {
		t.Fatalf("the same bytes should share an artifact, got %d artifacts", got)
	}

	s.require(s.securityRole("security", "security_admin", "platform-admin"), http.StatusForbidden, "FORBIDDEN")
	s.require(s.securityRole("security", "security_admin", "owner"), http.StatusCreated, "")
	s.require(s.securityRole("security", "security_reviewer", "owner"), http.StatusCreated, "")
	s.require(s.securityRole("security-reviewer", "security_reviewer", "owner"), http.StatusCreated, "")
	blocks := target + "/access-blocks"
	block := s.call(http.MethodPost, blocks, "security", `{"reason":"investigate publication"}`)
	s.require(block, http.StatusCreated, "")
	blockPath := block.header.Get("Location")
	s.require(s.call(http.MethodGet, blockPath, "security-reviewer", ""), http.StatusOK, "")
	s.require(s.call(http.MethodGet, slot+"/artifact", "author", ""), http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	s.require(s.call(http.MethodGet, otherSlot+"/artifact", "author", ""), http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	if blocked := s.call(http.MethodGet, slot, "publisher", ""); blocked.body["generation"] != float64(3) ||
		blocked.body["active_release_id"] != firstID || blocked.body["blocked"] != true || blocked.body["delivery_status"] != "UNKNOWN" {
		t.Fatalf("target block altered the slot pointer or claimed delivery: %v", blocked.body)
	}
	clear := `{"reason":"verified safe to restore"}`
	s.require(s.call(http.MethodPost, blockPath+"/clearance", "security", clear), http.StatusForbidden, "SECURITY_REVIEWER_NOT_INDEPENDENT")
	s.require(s.call(http.MethodPost, blockPath+"/clearance", "security-reviewer", clear), http.StatusConflict, "WITHDRAW_BEFORE_CLEARING")
	s.require(s.change(slot, "publisher", "WITHDRAW", 3, "", "withdraw during investigation", "withdraw"), http.StatusOK, "")
	s.require(s.change(otherSlot, "publisher", "WITHDRAW", 1, "", "withdraw during investigation", "withdraw"), http.StatusOK, "")
	s.require(s.change(slot, "publisher", "RESTORE", 4, firstID, "resume", "restore"), http.StatusConflict, "PUBLICATION_BLOCKED")
	cleared := s.call(http.MethodPost, blockPath+"/clearance", "security-reviewer", clear)
	s.require(cleared, http.StatusOK, "")
	if cleared.body["clear_generation"] != float64(2) || cleared.body["edge_status"] != "UNKNOWN" {
		t.Fatalf("unexpected clearance %v", cleared.body)
	}
	s.require(s.change(slot, "publisher", "RESTORE", 4, firstID, "resume", "restore"), http.StatusOK, "")
	s.requireArtifact(slot, first.id("artifact_digest"), revisionSource)
	s.require(s.call(http.MethodGet, otherSlot+"/artifact", "author", ""), http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	page := s.call(http.MethodGet, slot+"/operations?after_generation=2&page_size=2", "reviewer", "")
	s.require(page, http.StatusOK, "")
	if items := page.body["items"].([]any); len(items) != 2 || page.body["next_after_generation"] != float64(4) {
		t.Fatalf("unexpected slot history page %v", page.body)
	}
	if operations := s.count("publication.operation"); operations != 7 {
		t.Fatalf("%d operations, want 7", operations)
	}
	conn := pgtest.Connect(t, s.database.Application)
	if _, err := conn.Exec(t.Context(), `UPDATE contractregistry.contract_release SET release_version = 'other' WHERE release_id = $1`, firstID); err == nil {
		t.Fatal("the application role was able to rewrite a release")
	}
	if _, err := conn.Exec(t.Context(), `DELETE FROM contractregistry.release_artifact WHERE artifact_digest = $1`, first.id("artifact_digest")); err == nil {
		t.Fatal("the application role was able to remove a release artifact")
	}
}

func TestPublicationStageRejectsMissingEvidenceAndRollsBackAnOutboxFailure(t *testing.T) {
	s := publicationFor(t)
	target, configPath := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	production, _ := s.target([2]string{"internal", "production"}, "engineering")
	external, _ := s.target(partnerSandbox, "developers")
	targets := s.call(http.MethodGet, "/v1/products/"+s.product+"/publication-targets?page_size=2", "publisher", "")
	s.require(targets, http.StatusOK, "")
	if len(targets.body["items"].([]any)) != 2 || targets.id("next_page_token") == "" {
		t.Fatalf("unexpected target page %v", targets.body)
	}
	revision, review := s.approve(revisionSource, "", internalSandbox, partnerSandbox)
	release := s.prepare(revision, review, "1.0.0", "publisher")
	s.require(release, http.StatusCreated, "")
	id := release.id("release_id")
	s.require(s.change(assetSlot(production, s.asset), "publisher", "ACTIVATE", 0, id, "", "first"), http.StatusConflict, "APPROVAL_SCOPE_MISMATCH")
	s.require(s.change(assetSlot(external, s.asset), "publisher", "ACTIVATE", 0, id, "", "first"), http.StatusConflict, "PUBLICATION_TARGET_UNAVAILABLE")
	s.require(s.call(http.MethodGet, slot+"/artifact", "parcel", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	s.require(s.change(slot, "publisher", "RESTORE", 0, id, "nothing published yet", "before-first"), http.StatusConflict, "RELEASE_NOT_IN_SLOT_HISTORY")

	if _, err := s.owner.Exec(t.Context(), `ALTER TABLE bento.outbox ADD CONSTRAINT test_reject_activation
CHECK (event_type <> 'fabric.publication.slot.activated')`); err != nil {
		t.Fatal(err)
	}
	auditCount, eventCount := s.count("platformoperations.audit_record"), s.count("bento.outbox")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, id, "", "first"), http.StatusInternalServerError, "INTERNAL_ERROR")
	if s.count("publication.slot") != 0 || s.count("publication.operation") != 0 ||
		s.count("platformoperations.audit_record") != auditCount || s.count("bento.outbox") != eventCount {
		t.Fatal("the outbox failure left a partial slot, operation, audit or event")
	}
	if _, err := s.owner.Exec(t.Context(), `ALTER TABLE bento.outbox DROP CONSTRAINT test_reject_activation`); err != nil {
		t.Fatal(err)
	}
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, id, "", "first"), http.StatusOK, "")
	if s.count("publication.operation") != 1 || s.count("platformoperations.audit_record") != auditCount+1 || s.count("bento.outbox") != eventCount+1 {
		t.Fatal("the successful retry did not commit one operation, audit record and event")
	}
	thirdRevision, thirdReview := s.approve(strings.ReplaceAll(revisionSource, "v1", "v3"), "", internalSandbox)
	third := s.prepare(thirdRevision, thirdReview, "3.0.0", "publisher")
	s.require(third, http.StatusCreated, "")
	s.require(s.change(slot, "publisher", "ACTIVATE", 1, third.id("release_id"), "", "no-diff"), http.StatusConflict, "COMPATIBILITY_BASELINE_MISMATCH")
	s.require(s.call(http.MethodDelete, configPath, "owner", ""), http.StatusNoContent, "")
	s.require(s.call(http.MethodGet, slot+"/artifact", "author", ""), http.StatusNotFound, "AUDIENCE_CONFIG_NOT_FOUND")
	s.require(s.change(slot, "publisher", "WITHDRAW", 1, "", "configuration removed", "after-removal"), http.StatusOK, "")
}

func TestPublicationStageHonorsScopeAndReviewerRevocation(t *testing.T) {
	s := publicationFor(t)
	target, _ := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	revision, review := s.approve(revisionSource, "", internalSandbox)
	release := s.prepare(revision, review, "1.0.0", "publisher")
	s.require(release, http.StatusCreated, "")
	id := release.id("release_id")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, id, "", "publish"), http.StatusOK, "")

	parcel := s.register("platform-admin", "parcel", "parcel-owner")
	parcelWorkspace := s.createWorkspace(parcel, "parcel-owner", "engineering")
	s.join(parcel, parcelWorkspace, "parcel-owner", "publisher", "workspace_maintainer")
	s.join(parcel, parcelWorkspace, "parcel-owner", "publisher", "internal_publisher")
	parcelAsset := strings.TrimSuffix(s.revisionAsset(parcel, parcelWorkspace, "publisher", "parcel-api", "HTTP"), "/revisions")
	foreignTarget := strings.Replace(target, s.product, parcel, 1)
	s.require(s.change(assetSlot(foreignTarget, parcelAsset), "publisher", "ACTIVATE", 0, id, "", "foreign"), http.StatusNotFound, "PUBLICATION_TARGET_NOT_FOUND")
	s.require(s.change(assetSlot(target, parcelAsset), "publisher", "ACTIVATE", 0, id, "", "foreign"), http.StatusNotFound, "API_ASSET_NOT_FOUND")

	var membership string
	if err := s.owner.QueryRow(t.Context(), `SELECT membership_id FROM productgovernance.workspace_membership
WHERE product_id = $1 AND workspace_id = $2 AND principal_subject = 'reviewer'
  AND role = 'internal_reviewer' AND revoked_at IS NULL`, s.product, s.workspace).Scan(&membership); err != nil {
		t.Fatal(err)
	}
	path := "/v1/products/" + s.product + "/workspaces/" + s.workspace + "/memberships/" + membership
	s.require(s.call(http.MethodDelete, path, "owner", ""), http.StatusNoContent, "")
	// The revocation stops the next release under that approval. What is
	// published stays published.
	s.requireArtifact(slot, release.id("artifact_digest"), revisionSource)
	s.require(s.prepare(revision, review, "1.0.1", "publisher"), http.StatusConflict, "REVIEWER_AUTH_REVOKED")
	s.join(s.product, s.workspace, "owner", "reviewer", "internal_reviewer")
	s.require(s.prepare(revision, review, "1.0.1", "publisher"), http.StatusConflict, "REVIEWER_AUTH_REVOKED")
	s.require(s.change(slot, "publisher", "WITHDRAW", 1, "", "reviewer grant revoked", "withdraw"), http.StatusOK, "")
	// The slot does not take the release as a new one, and returns to it
	// as the one it served.
	s.require(s.change(slot, "publisher", "ACTIVATE", 2, id, "", "activate-again"), http.StatusConflict, "REVIEWER_AUTH_REVOKED")
	s.require(s.change(slot, "publisher", "RESTORE", 2, id, "restore", "restore"), http.StatusOK, "")
	s.requireArtifact(slot, release.id("artifact_digest"), revisionSource)
}
