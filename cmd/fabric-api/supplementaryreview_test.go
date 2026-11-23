package main

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

var internalProduction = [2]string{"internal", "production"}

// checkOf answers the check of the revision under the policy and the tools
// of today: the one that is recorded, or a new one.
func (s *reviewFixture) checkOf(revision string) string {
	s.t.Helper()
	got := s.call(http.MethodPost, revision+"/check-runs", "author", "")
	if got.status != http.StatusOK && got.status != http.StatusCreated {
		s.t.Fatalf("check the revision: %d %v", got.status, got.body)
	}
	return got.body["check_run"].(map[string]any)["check_run_id"].(string)
}

// where answers where the registry has the revision: its status and the
// version of that status.
func (s *reviewFixture) where(revision string) string {
	s.t.Helper()
	var at string
	if err := s.owner.QueryRow(s.t.Context(),
		`SELECT status || ' at ' || status_version FROM contractregistry.contract_revision WHERE revision_id = $1`, last(revision),
	).Scan(&at); err != nil {
		s.t.Fatal(err)
	}
	return at
}

// said counts the events of the type that say whether their review is
// supplementary, or that do not say.
func (s *reviewFixture) said(eventType string, supplementary bool) int {
	s.t.Helper()
	var events int
	if err := s.owner.QueryRow(s.t.Context(), `SELECT count(*) FROM bento.outbox
WHERE event_type = $1 AND scope = $2 AND coalesce(payload->>'supplementary', 'not said') = $3`,
		eventType, s.product, map[bool]string{true: "true", false: "not said"}[supplementary]).Scan(&events); err != nil {
		s.t.Fatal(err)
	}
	return events
}

// A revision that is approved is reviewed again: an author asks, on a check
// of today and for a scope, and a reviewer who had no part in it decides.
// The review is supplementary. The revision stays approved while it is open
// and however it ends, what was approved before is what it was, and an
// approval is one more approval of the same content: a release is prepared
// under it, and published where its scope reaches.
func TestAnApprovedRevisionIsReviewedAgainAndStaysApproved(t *testing.T) {
	s := publicationFor(t)
	s.join(s.product, s.workspace, "owner", "second", "internal_reviewer")
	id, first := s.approve(revisionSource, "", internalSandbox)
	revision := s.revisions + "/" + id
	reviews := revision + "/reviews"
	check := s.checkOf(revision)
	approved := s.call(http.MethodGet, revision, "author", "").body
	firstReview := s.call(http.MethodGet, reviews+"/"+first, "author", "").body
	if approved["status"] != "APPROVED" || s.where(revision) != "APPROVED at 3" || firstReview["supplementary"] != false {
		t.Fatalf("the approved revision is %v, %s, under a review that says supplementary %v", approved["status"], s.where(revision), firstReview["supplementary"])
	}
	// unmoved fails the test unless the revision is what its approval left.
	unmoved := func(after string) {
		t.Helper()
		if got := s.call(http.MethodGet, revision, "author", "").body; !reflect.DeepEqual(got, approved) || s.where(revision) != "APPROVED at 3" {
			t.Fatalf("after %s the revision reads %v and is %s, want it as its approval left it", after, got, s.where(revision))
		}
		if got := s.call(http.MethodGet, reviews+"/"+first, "reviewer", "").body; !reflect.DeepEqual(got, firstReview) {
			t.Fatalf("after %s the first review reads %v, want it as it was decided", after, got)
		}
	}

	// Only an author asks, as for a draft.
	for _, caller := range []string{"reviewer", "second", "maintainer", "owner", "publisher"} {
		s.require(s.call(http.MethodPost, reviews, caller, asking(check, internalProduction)), http.StatusForbidden, "FORBIDDEN")
	}
	s.require(s.call(http.MethodPost, reviews, "stranger", asking(check, internalProduction)), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	stale := recordAnotherCheck(t, checkFixture{stack: s.stack}, domain.RevisionRef{
		ProductID: s.product, WorkspaceID: s.workspace, AssetID: last(s.asset), RevisionID: id,
	}, check)
	s.expect("another review rests on a check of today, for a scope", "coauthor", []exchange{
		{http.MethodPost, reviews, asking(stale, internalProduction), http.StatusConflict, "POLICY_STALE"},
		{http.MethodPost, reviews, asking("00000000-0000-4000-8000-000000000000", internalProduction), http.StatusNotFound, "CHECK_RUN_NOT_FOUND"},
		{http.MethodPost, reviews, asking(check), http.StatusBadRequest, "REQUEST_INVALID"},
	})
	if got := s.count("policyreview.review_request"); got != 1 {
		t.Fatalf("the refused requests left %d reviews, want the one that approved the revision", got)
	}
	unmoved("the refused requests")

	// The first approval already serves the revision, on a slot of its own
	// beside the one the rest of the test publishes on. The review asked
	// next has no conclusion yet, and what is published stays published.
	sandbox, sandboxConfig := s.target(internalSandbox, "engineering")
	during := s.call(http.MethodPost, "/v1/products/"+s.product+"/publication-targets", "owner", jsonBody(map[string]string{
		"audience_config_id": last(sandboxConfig), "portal_key": "during",
	}))
	s.require(during, http.StatusCreated, "")
	duringSlot := assetSlot(during.header.Get("Location"), s.asset)
	serving := s.prepare(id, first, "0.9.0", "publisher")
	s.require(serving, http.StatusCreated, "")
	s.require(s.change(duringSlot, "publisher", "ACTIVATE", 0, serving.id("release_id"), "", "during"), http.StatusOK, "")
	s.requireArtifact(duringSlot, serving.id("artifact_digest"), revisionSource)

	// Another author asks for production. The review is open, and the
	// revision is approved as before.
	asked := s.call(http.MethodPost, reviews, "coauthor", asking(check, internalProduction))
	s.require(asked, http.StatusCreated, "")
	withdrawn := reviews + "/" + asked.id("review_id")
	if asked.body["supplementary"] != true || asked.body["status"] != "IN_REVIEW" || asked.body["check_run_id"] != check ||
		asked.body["conclusion"] != nil || asked.body["requested_by"].(map[string]any)["subject"] != "coauthor" ||
		!reflect.DeepEqual(asked.body["scope"], []any{map[string]any{"audience": "internal", "environment": "production"}}) ||
		asked.header.Get("Location") != withdrawn || asked.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the supplementary review: %v %v", asked.body, asked.header)
	}
	unmoved("the request")
	// The review is open and has no conclusion. The slot keeps serving the
	// release prepared before the review: the same bytes and the same digest.
	s.requireArtifact(duringSlot, serving.id("artifact_digest"), revisionSource)
	// A revision is under one review at a time.
	for _, caller := range []string{"author", "coauthor"} {
		s.require(s.call(http.MethodPost, reviews, caller, asking(check, internalSandbox)), http.StatusConflict, "REVIEW_IN_PROGRESS")
	}
	// While it is open, the revision is released under the approval it has.
	released := s.prepare(id, first, "1.0.0", "publisher")
	s.require(released, http.StatusCreated, "")
	s.require(s.prepare(id, asked.id("review_id"), "1.0.1", "publisher"), http.StatusConflict, "APPROVAL_REQUIRED")

	// Whoever asked takes it back, and the revision is approved as before.
	s.require(s.call(http.MethodPost, withdrawn+"/withdrawal", "author", `{}`), http.StatusForbidden, "FORBIDDEN")
	back := s.call(http.MethodPost, withdrawn+"/withdrawal", "coauthor", `{"reason":"the wrong check"}`)
	s.require(back, http.StatusOK, "")
	if back.body["status"] != "WITHDRAWN" || back.body["supplementary"] != true {
		t.Fatalf("the withdrawn review: %v", back.body)
	}
	unmoved("the withdrawal")

	// The next one is rejected. That takes nothing back: the revision is
	// approved, and its first approval releases it as before.
	asked = s.call(http.MethodPost, reviews, "coauthor", asking(check, internalProduction))
	s.require(asked, http.StatusCreated, "")
	rejected := reviews + "/" + asked.id("review_id")
	s.require(s.call(http.MethodPost, rejected+"/decision", "coauthor", deciding("REJECTED", "not mine to say", time.Time{})), http.StatusForbidden, "FORBIDDEN")
	refusal := s.call(http.MethodPost, rejected+"/decision", "second", deciding("REJECTED", "production waits for the audit", time.Time{}))
	s.require(refusal, http.StatusOK, "")
	if refusal.body["status"] != "REJECTED" || refusal.body["supplementary"] != true ||
		refusal.body["conclusion"].(map[string]any)["reason"] != "production waits for the audit" {
		t.Fatalf("the rejected review: %v", refusal.body)
	}
	unmoved("the rejection")
	s.require(s.prepare(id, asked.id("review_id"), "1.0.1", "publisher"), http.StatusConflict, "APPROVAL_REQUIRED")
	s.require(s.prepare(id, first, "1.0.1", "publisher"), http.StatusCreated, "")
	s.expect("a review that ended is not ended again", "second", []exchange{
		{http.MethodPost, rejected + "/decision", deciding("APPROVED", "", time.Now().Add(time.Hour)), http.StatusConflict, "REVIEW_CONCLUDED"},
		{http.MethodPost, withdrawn + "/decision", deciding("APPROVED", "", time.Now().Add(time.Hour)), http.StatusConflict, "REVIEW_CONCLUDED"},
	})

	// The third is approved, by a reviewer who had no part in it. Whoever
	// recorded the revision, ran its check or asked for this review does
	// not decide, reviewer or not. Whoever approved the revision before
	// had no part in this, and decides.
	asked = s.call(http.MethodPost, reviews, "coauthor", asking(check, internalProduction, internalSandbox))
	s.require(asked, http.StatusCreated, "")
	granted := reviews + "/" + asked.id("review_id")
	until := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Second)
	s.join(s.product, s.workspace, "owner", "author", "internal_reviewer")
	s.join(s.product, s.workspace, "owner", "coauthor", "internal_reviewer")
	for _, caller := range []string{"author", "coauthor"} {
		s.require(s.call(http.MethodPost, granted+"/decision", caller, deciding("APPROVED", "", until)), http.StatusForbidden, "REVIEWER_NOT_INDEPENDENT")
	}
	approval := s.call(http.MethodPost, granted+"/decision", "reviewer", deciding("APPROVED", "the audit is done", until))
	s.require(approval, http.StatusOK, "")
	conclusion, _ := approval.body["conclusion"].(map[string]any)
	if approval.body["status"] != "APPROVED" || approval.body["supplementary"] != true || conclusion["valid_until"] != until.Format(time.RFC3339) ||
		conclusion["concluded_by"].(map[string]any)["subject"] != "reviewer" {
		t.Fatalf("the approved review: %v", approval.body)
	}
	unmoved("the approval")

	// It is one more approval of the same content: a release under it is
	// another release of the same artifact.
	wider := s.prepare(id, asked.id("review_id"), "1.1.0", "publisher")
	s.require(wider, http.StatusCreated, "")
	if wider.body["artifact_digest"] != released.body["artifact_digest"] || wider.body["review_id"] != asked.id("review_id") ||
		s.count("contractregistry.release_artifact") != 1 || s.count("contractregistry.contract_release") != 4 {
		t.Fatalf("the release under the other approval: %v; %d artifacts, %d releases", wider.body,
			s.count("contractregistry.release_artifact"), s.count("contractregistry.contract_release"))
	}
	// Each approval reaches as far as its scope. The first one was for the
	// sandbox; production is published under the one that names it.
	live, _ := s.target(internalProduction, "engineering")
	sandboxSlot, liveSlot := assetSlot(sandbox, s.asset), assetSlot(live, s.asset)
	narrow, wide := released.id("release_id"), wider.id("release_id")
	s.require(s.change(liveSlot, "publisher", "ACTIVATE", 0, narrow, "", "narrow"), http.StatusConflict, "APPROVAL_SCOPE_MISMATCH")
	s.require(s.change(liveSlot, "publisher", "ACTIVATE", 0, wide, "", "wide"), http.StatusOK, "")
	s.requireArtifact(liveSlot, wider.id("artifact_digest"), revisionSource)
	s.require(s.change(sandboxSlot, "publisher", "ACTIVATE", 0, narrow, "", "narrow"), http.StatusOK, "")
	// The same revision under another release takes its place without a
	// comparison: there is nothing to compare it with but itself.
	s.require(s.change(sandboxSlot, "publisher", "ACTIVATE", 1, wide, "", "wide"), http.StatusOK, "")
	s.requireArtifact(sandboxSlot, wider.id("artifact_digest"), revisionSource)

	// The history of the revision: its reviews in the order they were
	// asked for, each as it ended.
	listed, next := s.page(reviews, "reviewer")
	var history []string
	for _, item := range listed {
		history = append(history, fmt.Sprintf("%v supplementary=%v by %v", item["status"], item["supplementary"], item["requested_by"].(map[string]any)["subject"]))
	}
	if want := []string{
		"APPROVED supplementary=false by author", "WITHDRAWN supplementary=true by coauthor",
		"REJECTED supplementary=true by coauthor", "APPROVED supplementary=true by coauthor",
	}; !reflect.DeepEqual(history, want) || next != "" || !reflect.DeepEqual(listed[3], approval.body) {
		t.Errorf("the reviews of the revision are %v, want %v", history, want)
	}

	// What is on record. Each request, withdrawal and decision under its
	// rule, each refusal for who the caller is, and an event for each that
	// says the review is supplementary. The events of the first review do
	// not say so, as before.
	for _, c := range []struct {
		subject, action, rule string
		done                  bool
		want                  int
	}{
		{"author", "review.request", "AC-50", true, 1},
		{"coauthor", "review.request", "AC-50", true, 3},
		{"coauthor", "review.withdraw", "AC-51", true, 1},
		{"second", "review.reject", "AC-03", true, 1},
		{"reviewer", "review.approve", "AC-03", true, 2},
		{"reviewer", "review.request", "DENY-03", false, 1},
		{"second", "review.request", "DENY-03", false, 1},
		{"author", "review.withdraw", "DENY-03", false, 1},
		{"coauthor", "review.reject", "DENY-03", false, 1},
		{"author", "review.approve", "DENY-06", false, 1},
		{"coauthor", "review.approve", "DENY-06", false, 1},
	} {
		if got := s.on(c.subject, c.action, c.rule, c.done); got != c.want {
			t.Errorf("%d records of %s by %s under %s, want %d", got, c.action, c.subject, c.rule, c.want)
		}
	}
	for _, c := range []struct {
		event         string
		supplementary bool
		want          int
	}{
		{"fabric.policyreview.review.requested", false, 1}, {"fabric.policyreview.review.requested", true, 3},
		{"fabric.policyreview.review.withdrawn", true, 1}, {"fabric.policyreview.review.withdrawn", false, 0},
		{"fabric.policyreview.review.decided", false, 1}, {"fabric.policyreview.review.decided", true, 2},
	} {
		if got := s.said(c.event, c.supplementary); got != c.want {
			t.Errorf("%d events %s that say supplementary %t, want %d", got, c.event, c.supplementary, c.want)
		}
	}
	if requests, conclusions := s.count("policyreview.review_request"), s.count("policyreview.review_conclusion"); requests != 4 || conclusions != 4 {
		t.Errorf("%d requests and %d conclusions are stored, want 4 of each", requests, conclusions)
	}
}

// What is published under an approval that ran out is served no more, and
// is published again once the revision is approved again: the same content,
// released under the other approval, takes the place of the release whose
// approval is over. Nothing of the content changes, and nothing of what was
// recorded before.
func TestWhatAnExpiredApprovalPublishedIsPublishedAgainUnderAnother(t *testing.T) {
	s := publicationFor(t)
	target, _ := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	id, first := s.approve(revisionSource, "", internalSandbox)
	revision := s.revisions + "/" + id
	released := s.prepare(id, first, "1.0.0", "publisher")
	s.require(released, http.StatusCreated, "")
	old := released.id("release_id")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, old, "", "first"), http.StatusOK, "")
	s.requireArtifact(slot, released.id("artifact_digest"), revisionSource)

	// The approval runs out: it held for a moment that is over.
	if _, err := s.owner.Exec(t.Context(), `UPDATE policyreview.review_conclusion
SET valid_until = concluded_at + interval '1 millisecond' WHERE review_id = $1`, first); err != nil {
		t.Fatal(err)
	}
	s.require(s.call(http.MethodGet, slot+"/artifact", "author", ""), http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	s.require(s.prepare(id, first, "1.0.1", "publisher"), http.StatusConflict, "APPROVAL_EXPIRED")
	s.require(s.change(slot, "publisher", "WITHDRAW", 1, "", "the approval ran out", "down"), http.StatusOK, "")
	s.require(s.change(slot, "publisher", "RESTORE", 2, old, "try again", "up"), http.StatusConflict, "APPROVAL_EXPIRED")
	// The content is what it was: importing it again answers the revision
	// that is there, approved.
	if again := s.call(http.MethodPost, s.revisions, "author", uploadRevision("openapi.yaml", revisionSource)); again.status != http.StatusOK ||
		again.id("revision_id") != id || again.body["status"] != "APPROVED" {
		t.Fatalf("the same content imported again: %d %v, want the approved revision", again.status, again.body)
	}

	// The revision is reviewed again and approved again.
	asked := s.call(http.MethodPost, revision+"/reviews", "author", asking(s.checkOf(revision), internalSandbox))
	s.require(asked, http.StatusCreated, "")
	s.require(s.call(http.MethodPost, asked.header.Get("Location")+"/decision", "reviewer",
		deciding("APPROVED", "", time.Now().Add(time.Hour))), http.StatusOK, "")
	// The release of before is what it was, under the approval it had.
	s.require(s.change(slot, "publisher", "RESTORE", 2, old, "try again", "up"), http.StatusConflict, "APPROVAL_EXPIRED")
	if got := s.call(http.MethodGet, s.asset+"/releases/"+old, "publisher", ""); got.status != http.StatusOK || !reflect.DeepEqual(got.body, released.body) {
		t.Errorf("the release of before reads %d %v, want it as it was prepared", got.status, got.body)
	}
	// Another release of the same content under the other approval is
	// published in its place, and what the slot serves is byte for byte
	// what it served before.
	renewed := s.prepare(id, asked.id("review_id"), "1.0.0-r2", "publisher")
	s.require(renewed, http.StatusCreated, "")
	if renewed.body["artifact_digest"] != released.body["artifact_digest"] || s.count("contractregistry.release_artifact") != 1 {
		t.Fatalf("the release under the other approval: %v; %d artifacts", renewed.body, s.count("contractregistry.release_artifact"))
	}
	s.require(s.change(slot, "publisher", "ACTIVATE", 2, renewed.id("release_id"), "", "renewed"), http.StatusOK, "")
	s.requireArtifact(slot, released.id("artifact_digest"), revisionSource)
	if got := s.where(revision); got != "APPROVED at 3" {
		t.Errorf("the revision is %s, want it where its first approval left it", got)
	}
}

// A supplementary review is asked for in a product that takes new work and
// recorded wholly or not at all, and of several requests that arrive
// together one is made.
func TestASupplementaryReviewIsRecordedWhollyOrNotAtAllAndOneAtATime(t *testing.T) {
	s := publicationFor(t)
	id, _ := s.approve(revisionSource, "", internalSandbox)
	revision := s.revisions + "/" + id
	reviews := revision + "/reviews"
	check := s.checkOf(revision)
	// A revision that was rejected is not reviewed again as it is.
	lost := s.inReview(revisionSource+"# another draft\n", "author", "author", "author")
	s.require(s.call(http.MethodPost, lost.review+"/decision", "reviewer", deciding("REJECTED", "not this one", time.Time{})), http.StatusOK, "")
	s.require(s.call(http.MethodPost, lost.revision+"/reviews", "author", asking(lost.check, internalSandbox)), http.StatusConflict, "REVISION_STATUS_CONFLICT")

	suspend := func(status string) {
		t.Helper()
		if _, err := s.owner.Exec(t.Context(), `UPDATE productgovernance.product SET status = $2 WHERE product_id = $1`, s.product, status); err != nil {
			t.Fatal(err)
		}
	}
	reject := func(events string) func() {
		t.Helper()
		// The events of before stay: the constraint holds what is written from now on.
		if _, err := s.owner.Exec(t.Context(), fmt.Sprintf(`ALTER TABLE bento.outbox
ADD CONSTRAINT reject_review_events CHECK (event_type <> '%s') NOT VALID`, events)); err != nil {
			t.Fatal(err)
		}
		return func() {
			t.Helper()
			if _, err := s.owner.Exec(t.Context(), `ALTER TABLE bento.outbox DROP CONSTRAINT reject_review_events`); err != nil {
				t.Fatal(err)
			}
		}
	}
	stored := func() [4]int {
		return [4]int{
			s.count("policyreview.review_request"), s.count("policyreview.review_scope"), s.count("policyreview.review_conclusion"),
			s.count("platformoperations.audit_record"),
		}
	}

	suspend("SUSPENDED")
	s.require(s.call(http.MethodPost, reviews, "author", asking(check, internalProduction)), http.StatusConflict, "PRODUCT_NOT_ACTIVE")
	suspend("ACTIVE")
	before := stored()
	allow := reject("fabric.policyreview.review.requested")
	s.require(s.call(http.MethodPost, reviews, "author", asking(check, internalProduction)), http.StatusInternalServerError, "INTERNAL_ERROR")
	allow()
	if got := stored(); got != before {
		t.Fatalf("the request that failed left %v requests, targets, conclusions and audit records, want %v", got, before)
	}

	// arrived returns the next result, and stops after twenty seconds. These
	// requests are decided one at a time because each holds the revision.
	// When that hold does nothing, several succeed, one answers 500, or a
	// result never arrives. The test then fails here instead of waiting out
	// the timeout of the package.
	arrived := func(results <-chan answer, what string) answer {
		t.Helper()
		timer := time.NewTimer(20 * time.Second)
		defer timer.Stop()
		select {
		case got := <-results:
			return got
		case <-timer.C:
			t.Fatalf("%s did not finish within 20 seconds", what)
			return answer{}
		}
	}

	// Requests that arrive together are decided one after the other: one
	// is made, and the others find its review open.
	const submitters = 6
	results := make(chan answer, submitters)
	for i := range submitters {
		token := s.token([]string{"author", "coauthor"}[i%2])
		go func() { results <- s.callWith(http.MethodPost, reviews, token, asking(check, internalProduction)) }()
	}
	var made []answer
	for range submitters {
		switch got := arrived(results, "a request among several"); {
		case got.status == http.StatusCreated:
			made = append(made, got)
		case got.status != http.StatusConflict || got.code() != "REVIEW_IN_PROGRESS":
			t.Errorf("a request among several: %d %v, want it made or told that a review is open", got.status, got.body)
		}
	}
	// With the review that approved the revision and the one of the
	// revision that was rejected, three reviews are stored.
	if len(made) != 1 || s.count("policyreview.review_request") != 3 || s.where(revision) != "APPROVED at 3" {
		t.Fatalf("%d of %d requests were made, %d reviews are stored and the revision is %s",
			len(made), submitters, s.count("policyreview.review_request"), s.where(revision))
	}
	review := made[0].header.Get("Location")

	// A decision whose event cannot be written leaves the review open.
	before = stored()
	allow = reject("fabric.policyreview.review.decided")
	s.require(s.call(http.MethodPost, review+"/decision", "reviewer", deciding("APPROVED", "", time.Now().Add(time.Hour))), http.StatusInternalServerError, "INTERNAL_ERROR")
	allow()
	if got := stored(); got != before || s.call(http.MethodGet, review, "author", "").body["status"] != "IN_REVIEW" {
		t.Fatalf("the decision that failed left %v requests, targets, conclusions and audit records, want %v and the review open", got, before)
	}
	// A suspended product takes no decision; a request is still taken back.
	suspend("SUSPENDED")
	s.require(s.call(http.MethodPost, review+"/decision", "reviewer", deciding("APPROVED", "", time.Now().Add(time.Hour))), http.StatusConflict, "PRODUCT_NOT_ACTIVE")
	requester := made[0].body["requested_by"].(map[string]any)["subject"].(string)
	s.require(s.call(http.MethodPost, review+"/withdrawal", requester, `{}`), http.StatusOK, "")
	suspend("ACTIVE")

	// Decisions that arrive together on the next one make one conclusion.
	asked := s.call(http.MethodPost, reviews, "author", asking(check, internalProduction))
	s.require(asked, http.StatusCreated, "")
	s.join(s.product, s.workspace, "owner", "second", "internal_reviewer")
	decisions := make(chan answer, submitters)
	for i := range submitters {
		token := s.token([]string{"reviewer", "second"}[i%2])
		body := deciding([]string{"APPROVED", "REJECTED"}[i%2], []string{"", "not yet"}[i%2], []time.Time{time.Now().Add(time.Hour), {}}[i%2])
		go func() {
			decisions <- s.callWith(http.MethodPost, asked.header.Get("Location")+"/decision", token, body)
		}()
	}
	decided := 0
	for range submitters {
		switch got := arrived(decisions, "a decision among several"); {
		case got.status == http.StatusOK:
			decided++
		case got.status != http.StatusConflict || got.code() != "REVIEW_CONCLUDED":
			t.Errorf("a decision among several: %d %v, want it made or told that the review is concluded", got.status, got.body)
		}
	}
	if decided != 1 || s.count("policyreview.review_conclusion") != 4 || s.where(revision) != "APPROVED at 3" {
		t.Errorf("%d of %d decisions were made, %d conclusions are stored and the revision is %s",
			decided, submitters, s.count("policyreview.review_conclusion"), s.where(revision))
	}
}
