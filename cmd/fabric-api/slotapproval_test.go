package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	checkdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// reviewedAgain asks for another review of the approved revision as the
// author, has the reviewer that is named approve it, prepares a release of
// the revision under that approval and returns the release.
func (s *releaseFixture) reviewedAgain(revision, reviewer, version string) answer {
	s.t.Helper()
	path := s.revisions + "/" + revision
	asked := s.call(http.MethodPost, path+"/reviews", "author", asking(s.checkOf(path), internalSandbox))
	s.require(asked, http.StatusCreated, "")
	s.require(s.call(http.MethodPost, asked.header.Get("Location")+"/decision", reviewer,
		deciding("APPROVED", "", time.Now().Add(time.Hour))), http.StatusOK, "")
	released := s.prepare(revision, asked.id("review_id"), version, "publisher")
	s.require(released, http.StatusCreated, "")
	return released
}

// A slot serves the release it points at and says what became of the
// approval that put it there. An approval that does not hold any more takes
// nothing down: the artifact is served as it was, and the slot names the
// reason to whoever may read it, in the code an activation of that release
// is refused with. It says that the revision is due another review. Once
// the revision is approved again and released under that approval, the slot
// that takes the release says nothing of the kind. What keeps an artifact
// from being served is a block or a withdrawal, and the slot says those
// apart from the approval.
func TestASlotServesItsReleaseAndSaysWhenItsApprovalDoesNotHold(t *testing.T) {
	s := publicationFor(t)
	s.join(s.product, s.workspace, "owner", "second", "internal_reviewer")
	target, _ := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	id, first := s.approve(revisionSource, "", internalSandbox)
	released := s.prepare(id, first, "1.0.0", "publisher")
	s.require(released, http.StatusCreated, "")
	digest := released.id("artifact_digest")

	// read answers the slot as the publisher reads it, and fails the test
	// unless the authors of the workspace, who read it too, read the same.
	read := func() map[string]any {
		t.Helper()
		got := s.call(http.MethodGet, slot, "publisher", "")
		s.require(got, http.StatusOK, "")
		for _, reader := range []string{"author", "coauthor"} {
			if other := s.call(http.MethodGet, slot, reader, ""); other.status != http.StatusOK || !reflect.DeepEqual(other.body, got.body) {
				t.Errorf("%s reads the slot as %d %v, the publisher as %v", reader, other.status, other.body, got.body)
			}
		}
		return got.body
	}
	// serving fails the test unless the slot points at the release at the
	// generation, unblocked, names the problem (or none) and serves the
	// artifact, whichever it names.
	serving := func(when, release string, generation float64, problem any) {
		t.Helper()
		got := read()
		if _, said := got["approval_problem"]; got["active_release_id"] != release || got["generation"] != generation ||
			got["visibility"] != "ACTIVE" || got["blocked"] != false || got["approval_problem"] != problem || said != (problem != nil) {
			t.Fatalf("%s the slot reads %v, want it to point at %s at generation %v, unblocked, with the approval problem %v", when, got, release, generation, problem)
		}
		s.requireArtifact(slot, digest, revisionSource)
	}
	unserved := func(when string) {
		t.Helper()
		if got := s.call(http.MethodGet, slot+"/artifact", "author", ""); got.status != http.StatusNotFound || got.code() != "PUBLISHED_ARTIFACT_NOT_FOUND" {
			t.Errorf("%s the artifact answers %d %v, want 404 PUBLISHED_ARTIFACT_NOT_FOUND", when, got.status, got.body)
		}
	}

	// A slot that points at nothing has no approval to speak of, and
	// nothing to serve.
	if got := read(); got["visibility"] != "EMPTY" || got["approval_problem"] != nil {
		t.Fatalf("the empty slot reads %v", got)
	}
	unserved("before anything is published")
	old := released.id("release_id")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, old, "", "first"), http.StatusOK, "")
	serving("after the first activation", old, 1, nil)

	// The approval runs out. The slot points where it pointed and serves
	// what it served, and says what an activation of the release would be
	// told.
	records, events := s.count("platformoperations.audit_record"), s.count("bento.outbox")
	if _, err := s.owner.Exec(t.Context(), `UPDATE policyreview.review_conclusion
SET valid_until = concluded_at + interval '1 millisecond' WHERE review_id = $1`, first); err != nil {
		t.Fatal(err)
	}
	serving("once the approval ran out", old, 1, "APPROVAL_EXPIRED")
	// Neither read leaves a record: nothing was changed, and nothing was
	// refused.
	if after, sent := s.count("platformoperations.audit_record"), s.count("bento.outbox"); after != records || sent != events ||
		s.on("author", "publication.download", "DENY-03", false) != 1 {
		t.Errorf("the reads left %d audit records and %d events, want none besides the one of the empty slot", after-records, sent-events)
	}
	s.require(s.prepare(id, first, "1.0.1", "publisher"), http.StatusConflict, "APPROVAL_EXPIRED")
	// Whoever does not read the slot is not told either.
	s.require(s.call(http.MethodGet, slot, "platform-admin", ""), http.StatusNotFound, "API_ASSET_NOT_FOUND")
	owner := s.call(http.MethodGet, slot, "owner", "")
	if owner.status != http.StatusOK || owner.body["approval_problem"] != "APPROVAL_EXPIRED" {
		t.Errorf("the owner reads the expired approval as %d %v", owner.status, owner.body)
	}

	// The revision is approved again and released under that approval.
	// The release takes the place of the one whose approval is over, and
	// the slot has nothing to say.
	renewed := s.reviewedAgain(id, "reviewer", "1.0.0-r2")
	serving("with the other release prepared and not yet active", old, 1, "APPROVAL_EXPIRED")
	s.require(s.change(slot, "publisher", "ACTIVATE", 1, renewed.id("release_id"), "", "renewed"), http.StatusOK, "")
	serving("after the release under the other approval took its place", renewed.id("release_id"), 2, nil)

	// The reviewer of that approval loses the grant, and gets it back:
	// the approval was given under the grant that is gone.
	var membership string
	if err := s.owner.QueryRow(t.Context(), `SELECT membership_id FROM productgovernance.workspace_membership
WHERE product_id = $1 AND workspace_id = $2 AND principal_subject = 'reviewer'
  AND role = 'internal_reviewer' AND revoked_at IS NULL`, s.product, s.workspace).Scan(&membership); err != nil {
		t.Fatal(err)
	}
	s.revoke("/v1/products/"+s.product+"/workspaces/"+s.workspace+"/memberships/"+membership, "owner")
	serving("once the reviewer lost the grant", renewed.id("release_id"), 2, "REVIEWER_AUTH_REVOKED")
	s.join(s.product, s.workspace, "owner", "reviewer", "internal_reviewer")
	serving("once the reviewer was named again", renewed.id("release_id"), 2, "REVIEWER_AUTH_REVOKED")

	// A block and an approval that does not hold are two things, and the
	// slot says both. It is the block that takes the artifact down.
	s.require(s.securityRole("security", "security_admin", "owner"), http.StatusCreated, "")
	s.require(s.securityRole("clearer", "security_reviewer", "owner"), http.StatusCreated, "")
	block := s.call(http.MethodPost, target+"/access-blocks", "security", `{"reason":"investigate"}`)
	s.require(block, http.StatusCreated, "")
	if got := read(); got["blocked"] != true || got["approval_problem"] != "REVIEWER_AUTH_REVOKED" {
		t.Errorf("the blocked slot reads %v, want it blocked and its approval problem named", got)
	}
	unserved("under a block")

	// A slot that was withdrawn points at nothing, serves nothing and says
	// nothing of an approval.
	s.require(s.change(slot, "publisher", "WITHDRAW", 2, "", "under investigation", "down"), http.StatusOK, "")
	if got := read(); got["visibility"] != "WITHDRAWN" || got["approval_problem"] != nil || got["blocked"] != true {
		t.Errorf("the withdrawn slot reads %v", got)
	}
	s.require(s.call(http.MethodPost, block.header.Get("Location")+"/clearance", "clearer", `{"reason":"nothing found"}`), http.StatusOK, "")
	unserved("once it is withdrawn and the block is cleared")

	// Another reviewer approves the revision again, and what is released
	// under that approval is served.
	again := s.reviewedAgain(id, "second", "1.0.0-r3")
	s.require(s.change(slot, "publisher", "ACTIVATE", 3, again.id("release_id"), "", "again"), http.StatusOK, "")
	serving("after the release under the third approval was activated", again.id("release_id"), 4, nil)
	if got := s.where(s.revisions + "/" + id); got != "APPROVED at 3" {
		t.Errorf("the revision is %s, want it where its first approval left it", got)
	}
}

// unapproved is what a slot says of an approval that does not hold, and of
// nothing else. Only those six reasons: the approval is missing, it ran
// out, a waiver it depends on ran out, it does not reach the target, its
// reviewer lost the grant, or the policy and the tools moved on from its
// check. Each counts as itself, wrapped, or joined with some other error.
// The verification fails in other ways too, and those stay failures rather
// than reasons the slot would announce.
func TestUnapprovedIsWhatASlotSaysOfAnApprovalThatDoesNotHold(t *testing.T) {
	other := errors.New("the read failed for another reason")
	for _, reason := range []error{
		checks.ErrApprovalExpired,
		checks.ErrApprovalRequired,
		checks.ErrApprovalScope,
		checks.ErrWaiverExpired,
		checks.ErrReviewerGrantRevoked,
		checkdomain.ErrCheckStale,
	} {
		for _, err := range []error{reason, fmt.Errorf("the release: %w", reason), errors.Join(other, reason)} {
			if !unapproved(err) {
				t.Errorf("unapproved(%v) = false, want true", err)
			}
		}
	}
	for _, err := range []error{
		nil,
		checks.ErrPublisherInvolved,
		checks.ErrBaselineMismatch,
		checks.ErrReleaseCheckMissing,
		checkdomain.ErrReviewDamaged,
		registry.ErrReleaseNotFound,
		context.DeadlineExceeded,
		errors.New("a failure of the read"),
	} {
		if unapproved(err) {
			t.Errorf("unapproved(%v) = true, want false", err)
		}
	}
}

// A release can rest on an approval that waives a rule. When that waiver
// runs out the approval no longer holds, though the revision stays approved
// and the slot still points at the release: the slot says the waiver ran
// out and serves the artifact as it did, and another release under that
// approval is refused. A supplementary review that waives the rule again
// publishes the same content in its place, and the slot then has nothing to
// say.
func TestASlotSaysWhenTheWaiverOfItsReleaseHasRunOut(t *testing.T) {
	s := publicationFor(t)
	s.join(s.product, s.workspace, "owner", "second", "internal_reviewer")
	target, _ := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	// The contract's one operation has no operationId, so the check fails a
	// rule a reviewer may waive. The review is for the sandbox the slot
	// publishes to: a partner target has no origin configured, and nothing
	// can be activated there.
	imported := s.call(http.MethodPost, s.revisions, "author", uploadRevision("openapi.yaml", unnamedSource))
	s.require(imported, http.StatusCreated, "")
	id := imported.id("revision_id")
	revision := s.revisions + "/" + id
	checked := s.call(http.MethodPost, revision+"/check-runs", "author", "")
	s.require(checked, http.StatusCreated, "")
	checkID := checked.body["check_run"].(map[string]any)["check_run_id"].(string)
	asked := s.call(http.MethodPost, revision+"/reviews", "author", asking(checkID, internalSandbox))
	s.require(asked, http.StatusCreated, "")
	reviewID := asked.id("review_id")
	until := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	s.require(s.call(http.MethodPost, asked.header.Get("Location")+"/decision", "reviewer",
		deciding("APPROVED", "", until, waiving("F04-GOV-003", until.Add(-time.Hour)))), http.StatusOK, "")
	released := s.prepare(id, reviewID, "1.0.0", "publisher")
	s.require(released, http.StatusCreated, "")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, released.id("release_id"), "", "waived"), http.StatusOK, "")
	s.requireArtifact(slot, released.id("artifact_digest"), unnamedSource)

	// The waiver ends a moment after it was given, which is already past,
	// and still inside the approval: a waiver has to outlive the decision
	// and not the approval, or the stored review is not one the model
	// recorded. The approval's valid_until is left where the decision put
	// it, which is still ahead.
	tag, err := s.owner.Exec(t.Context(), `UPDATE policyreview.review_waiver AS w
SET expires_at = c.concluded_at + interval '1 millisecond'
FROM policyreview.review_conclusion c
WHERE w.review_id = c.review_id AND w.review_id = $1`, reviewID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("expire the waiver: %v, %d rows", err, tag.RowsAffected())
	}
	got := s.call(http.MethodGet, slot, "publisher", "")
	s.require(got, http.StatusOK, "")
	if got.body["active_release_id"] != released.id("release_id") || got.body["approval_problem"] != "WAIVER_EXPIRED" {
		t.Fatalf("once the waiver ran out the slot reads %v", got.body)
	}
	s.requireArtifact(slot, released.id("artifact_digest"), unnamedSource)
	s.require(s.prepare(id, reviewID, "1.0.1", "publisher"), http.StatusConflict, "WAIVER_EXPIRED")

	// The revision is approved again, under a waiver that has not run out,
	// and that release takes the place of the one whose waiver has.
	asked = s.call(http.MethodPost, revision+"/reviews", "author", asking(s.checkOf(revision), internalSandbox))
	s.require(asked, http.StatusCreated, "")
	later := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Second)
	s.require(s.call(http.MethodPost, asked.header.Get("Location")+"/decision", "second",
		deciding("APPROVED", "", later, waiving("F04-GOV-003", later.Add(-time.Hour)))), http.StatusOK, "")
	renewed := s.prepare(id, asked.id("review_id"), "1.0.0-r2", "publisher")
	s.require(renewed, http.StatusCreated, "")
	s.require(s.change(slot, "publisher", "ACTIVATE", 1, renewed.id("release_id"), "", "renewed"), http.StatusOK, "")
	after := s.call(http.MethodGet, slot, "publisher", "")
	s.require(after, http.StatusOK, "")
	if _, said := after.body["approval_problem"]; after.body["active_release_id"] != renewed.id("release_id") || said {
		t.Fatalf("after the release under the new waiver the slot reads %v", after.body)
	}
	s.requireArtifact(slot, renewed.id("artifact_digest"), unnamedSource)
}
