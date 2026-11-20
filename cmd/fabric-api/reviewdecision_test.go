package main

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	checkdb "go.idp.xyz/idp-api-fabric/internal/policyreview/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// unnamedSource is a contract whose one operation has no operationId: a
// rule a reviewer may waive fails, and no rule that nothing waives does.
const unnamedSource = `openapi: 3.1.2
info: {title: Shipments, version: v1}
security: [{token: []}]
paths:
  /shipments:
    get:
      responses:
        '200': {description: OK}
components:
  securitySchemes:
    token: {type: http, scheme: bearer}
`

// submitted is a revision that is in review: where it and its review are
// read, the check the review rests on and what the import answered.
type submitted struct {
	revision, review, check string
	imported                answer
}

// inReview imports a revision of the source, checks it and asks for its
// review, each step as the member that is named.
func (s *reviewFixture) inReview(source, recorder, checker, requester string) submitted {
	s.t.Helper()
	imported := s.call(http.MethodPost, s.revisions, recorder, uploadRevision("openapi.yaml", source))
	if imported.status != http.StatusCreated {
		s.t.Fatalf("import a revision as %s: %d %v", recorder, imported.status, imported.body)
	}
	revision := s.revisions + "/" + imported.id("revision_id")
	check := s.call(http.MethodPost, revision+"/check-runs", checker, "")
	if check.status != http.StatusCreated {
		s.t.Fatalf("check the revision as %s: %d %v", checker, check.status, check.body)
	}
	id := check.body["check_run"].(map[string]any)["check_run_id"].(string)
	review := s.call(http.MethodPost, revision+"/reviews", requester, asking(id, partnerSandbox))
	if review.status != http.StatusCreated {
		s.t.Fatalf("ask for the review as %s: %d %v", requester, review.status, review.body)
	}
	return submitted{revision: revision, review: revision + "/reviews/" + review.id("review_id"), check: id, imported: imported}
}

// deciding is the body a review is decided with. An empty reason and a zero
// time are left out.
func deciding(outcome, reason string, validUntil time.Time, waivers ...map[string]any) string {
	body := map[string]any{"outcome": outcome}
	if reason != "" {
		body["reason"] = reason
	}
	if !validUntil.IsZero() {
		body["valid_until"] = validUntil.UTC().Format(time.RFC3339)
	}
	if len(waivers) > 0 {
		body["waivers"] = waivers
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func waiving(rule string, until time.Time) map[string]any {
	return map[string]any{"rule_id": rule, "reason": "generated clients are not used yet", "expires_at": until.UTC().Format(time.RFC3339)}
}

// A reviewer approves a revision that is in review, or rejects it. An
// approval holds until a time and waives by name what did not pass in the
// check; a rejection says why. Either is where the review and the revision
// end, and leaves its record, its audit entry and its event.
func TestAReviewerApprovesOrRejectsARevisionAndWaivesWhatDidNotPass(t *testing.T) {
	s := reviewsFor(t)
	s.join(s.product, s.workspace, "owner", "second", "internal_reviewer")
	until := time.Now().UTC().Add(90 * 24 * time.Hour).Truncate(time.Second)
	sooner := until.Add(-30 * 24 * time.Hour)
	by := func(subject string) map[string]any {
		return map[string]any{"issuer": s.issuer.URL(), "subject": subject}
	}

	// A revision whose check found nothing is approved as it is.
	clean := s.inReview(revisionSource, "author", "author", "author")
	want := s.call(http.MethodGet, clean.review, "author", "").body
	approved := s.call(http.MethodPost, clean.review+"/decision", "reviewer", deciding("APPROVED", "reviewed with the partner team", until))
	conclusion, _ := approved.body["conclusion"].(map[string]any)
	want["status"], want["conclusion"] = "APPROVED", map[string]any{
		"outcome": "APPROVED", "reason": "reviewed with the partner team", "concluded_by": by("reviewer"),
		"concluded_at": conclusion["concluded_at"], "valid_until": until.Format(time.RFC3339),
	}
	if approved.status != http.StatusOK || !reflect.DeepEqual(approved.body, want) || conclusion["concluded_at"] == nil ||
		approved.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the approval: %d %v\nwant\n %v", approved.status, approved.body, want)
	}
	if got := s.statusOf(clean.revision); got != "APPROVED" {
		t.Errorf("an approved revision is %v", got)
	}
	// Its content is what it was: only its status moved.
	moved := s.call(http.MethodGet, clean.revision, "author", "").body
	moved["status"] = "DRAFT"
	if !reflect.DeepEqual(moved, clean.imported.body) {
		t.Errorf("the approved revision reads %v, want what was imported but for its status %v", moved, clean.imported.body)
	}
	for _, reader := range []string{"author", "coauthor", "reviewer", "second"} {
		if got := s.call(http.MethodGet, clean.review, reader, ""); got.status != http.StatusOK || !reflect.DeepEqual(got.body, approved.body) {
			t.Errorf("%s reads the review: %d %v, want it as it was answered", reader, got.status, got.body)
		}
	}
	s.expect("an approval is where a review ends", "second", []exchange{
		{http.MethodPost, clean.review + "/decision", deciding("REJECTED", "on second thought", time.Time{}), http.StatusConflict, "REVIEW_CONCLUDED"},
		{http.MethodPost, clean.review + "/decision", deciding("APPROVED", "", until), http.StatusConflict, "REVIEW_CONCLUDED"},
	})
	s.expect("an approved revision is not withdrawn or submitted again", "author", []exchange{
		{http.MethodPost, clean.review + "/withdrawal", `{}`, http.StatusConflict, "REVIEW_CONCLUDED"},
		{http.MethodPost, clean.revision + "/reviews", asking(clean.check, partnerSandbox), http.StatusConflict, "REVISION_STATUS_CONFLICT"},
	})
	if again := s.call(http.MethodPost, s.revisions, "coauthor", uploadRevision("openapi.yaml", revisionSource)); again.status != http.StatusOK ||
		again.id("revision_id") != clean.imported.id("revision_id") || again.body["status"] != "APPROVED" {
		t.Errorf("the same content imported again: %d %v, want the revision that was approved", again.status, again.body)
	}

	// A revision in whose check a rule failed that a reviewer may waive.
	flawed := s.inReview(unnamedSource, "author", "author", "author")
	var failed []string
	for _, raw := range s.call(http.MethodGet, flawed.revision+"/check-runs/"+flawed.check, "reviewer", "").body["outcomes"].([]any) {
		if outcome := raw.(map[string]any); outcome["status"] == "FAIL" || outcome["status"] == "UNKNOWN" {
			failed = append(failed, outcome["rule_id"].(string))
		}
	}
	if !reflect.DeepEqual(failed, []string{"F04-GOV-003"}) {
		t.Fatalf("in the check of the contract without an operationId the rules %v did not pass, want F04-GOV-003 alone", failed)
	}
	decision := flawed.review + "/decision"
	waived := waiving("F04-GOV-003", sooner)
	past, far := time.Now().Add(-time.Hour), time.Now().Add(366*24*time.Hour)
	s.expect("an approval waives what did not pass, for a time, and nothing else", "reviewer", []exchange{
		{http.MethodPost, decision, deciding("APPROVED", "", until), http.StatusConflict, "WAIVER_REQUIRED"},
		{http.MethodPost, decision, deciding("APPROVED", "", until, waiving("F04-GOV-006", sooner)), http.StatusUnprocessableEntity, "WAIVER_FORBIDDEN"},
		{http.MethodPost, decision, deciding("APPROVED", "", until, waived, waiving("F04-GOV-006", sooner)), http.StatusUnprocessableEntity, "WAIVER_FORBIDDEN"},
		// A rule that passed, a rule there is not, a rule twice.
		{http.MethodPost, decision, deciding("APPROVED", "", until, waived, waiving("F04-GOV-002", sooner)), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{http.MethodPost, decision, deciding("APPROVED", "", until, waived, waiving("F04-GOV-999", sooner)), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{http.MethodPost, decision, deciding("APPROVED", "", until, waived, waived), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		// A waiver that outlives the approval, and one that is over.
		{http.MethodPost, decision, deciding("APPROVED", "", until, waiving("F04-GOV-003", until.Add(time.Second))), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{http.MethodPost, decision, deciding("APPROVED", "", until, waiving("F04-GOV-003", past)), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		// An approval that is over, one for more than a year, one for no time.
		{http.MethodPost, decision, deciding("APPROVED", "", past, waived), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{http.MethodPost, decision, deciding("APPROVED", "", far, waived), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{http.MethodPost, decision, deciding("APPROVED", "", time.Time{}, waived), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		// A rejection says why and nothing else.
		{http.MethodPost, decision, deciding("REJECTED", "", time.Time{}), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{http.MethodPost, decision, deciding("REJECTED", "not yet", until), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{http.MethodPost, decision, deciding("REJECTED", "not yet", time.Time{}, waived), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		// What the contract does not state.
		{http.MethodPost, decision, `{"outcome":"WITHDRAWN"}`, http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, decision, `{"outcome":"APPROVED","valid_until":"next year"}`, http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, flawed.revision + "/reviews/latest/decision", deciding("APPROVED", "", until, waived), http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPut, decision, deciding("APPROVED", "", until, waived), http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED"},
		// A review belongs to the revision in the path.
		{http.MethodPost, flawed.revision + "/reviews/" + last(clean.review) + "/decision", deciding("APPROVED", "", until), http.StatusNotFound, "REVIEW_NOT_FOUND"},
		{http.MethodPost, flawed.revision + "/reviews/00000000-0000-4000-8000-000000000000/decision", deciding("APPROVED", "", until), http.StatusNotFound, "REVIEW_NOT_FOUND"},
	})
	if got := s.statusOf(flawed.revision); got != "IN_REVIEW" || s.count("policyreview.review_waiver") != 0 || s.count("policyreview.review_conclusion") != 1 {
		t.Fatalf("after the refused decisions the revision is %v, with %d waivers and %d conclusions on record",
			got, s.count("policyreview.review_waiver"), s.count("policyreview.review_conclusion"))
	}
	waived["compensating_control"] = "the gateway names every route"
	granted := s.call(http.MethodPost, decision, "second", deciding("APPROVED", "", until, waived))
	conclusion, _ = granted.body["conclusion"].(map[string]any)
	wantConclusion := map[string]any{
		"outcome": "APPROVED", "concluded_by": by("second"), "concluded_at": conclusion["concluded_at"], "valid_until": until.Format(time.RFC3339),
		"waivers": []any{map[string]any{
			"rule_id": "F04-GOV-003", "reason": "generated clients are not used yet",
			"compensating_control": "the gateway names every route", "expires_at": sooner.Format(time.RFC3339),
		}},
	}
	if granted.status != http.StatusOK || granted.body["status"] != "APPROVED" || !reflect.DeepEqual(conclusion, wantConclusion) ||
		s.statusOf(flawed.revision) != "APPROVED" {
		t.Fatalf("the approval with a waiver: %d %v\nwant the conclusion\n %v", granted.status, granted.body, wantConclusion)
	}
	if got := s.call(http.MethodGet, flawed.review, "author", ""); !reflect.DeepEqual(got.body, granted.body) {
		t.Errorf("the review reads %v, want it as it was answered", got.body)
	}

	// A rejection says why, and what was rejected is not submitted again:
	// what is wrong with it is mended in another revision.
	refused := s.inReview(revisionSource+"# another draft\n", "author", "author", "coauthor")
	rejected := s.call(http.MethodPost, refused.review+"/decision", "reviewer", deciding("REJECTED", "the partner team asked for paging first", time.Time{}))
	conclusion, _ = rejected.body["conclusion"].(map[string]any)
	wantConclusion = map[string]any{
		"outcome": "REJECTED", "reason": "the partner team asked for paging first",
		"concluded_by": by("reviewer"), "concluded_at": conclusion["concluded_at"],
	}
	if rejected.status != http.StatusOK || rejected.body["status"] != "REJECTED" || !reflect.DeepEqual(conclusion, wantConclusion) ||
		s.statusOf(refused.revision) != "REJECTED" {
		t.Fatalf("the rejection: %d %v\nwant the conclusion\n %v", rejected.status, rejected.body, wantConclusion)
	}
	s.expect("a rejection is where a review ends", "coauthor", []exchange{
		{http.MethodPost, refused.review + "/withdrawal", `{}`, http.StatusConflict, "REVIEW_CONCLUDED"},
		{http.MethodPost, refused.revision + "/reviews", asking(refused.check, partnerSandbox), http.StatusConflict, "REVISION_STATUS_CONFLICT"},
	})
	s.expect("a rejection is where a review ends", "second", []exchange{
		{http.MethodPost, refused.review + "/decision", deciding("APPROVED", "", until), http.StatusConflict, "REVIEW_CONCLUDED"},
	})
	if items, _ := s.page(refused.revision+"/reviews", "reviewer"); len(items) != 1 || !reflect.DeepEqual(items[0], rejected.body) {
		t.Errorf("the reviews of the rejected revision: %v, want the one that was rejected", items)
	}

	// What is on record: three conclusions and one waiver; an event for each
	// decision that says how it ended and, for an approval, until when, and
	// that carries no word anybody wrote; an audit entry for each under the
	// rule of the reviewer. What was refused for what it said is on no
	// record.
	var approvals, rejections, worded int
	if err := s.owner.QueryRow(t.Context(), `
SELECT (SELECT count(*) FROM bento.outbox WHERE event_type = 'fabric.policyreview.review.decided' AND scope = $1
          AND payload->>'outcome' = 'APPROVED' AND payload->>'valid_until' IS NOT NULL AND payload->>'aggregate_version' = '2'),
       (SELECT count(*) FROM bento.outbox WHERE event_type = 'fabric.policyreview.review.decided' AND scope = $1
          AND payload->>'outcome' = 'REJECTED' AND payload->>'valid_until' IS NULL AND payload->>'aggregate_id' = $2
          AND payload->>'aggregate_version' = '2'),
       (SELECT count(*) FROM bento.outbox WHERE event_type LIKE 'fabric.policyreview.review.%'
          AND (payload::text LIKE '%partner team%' OR payload::text LIKE '%generated clients%' OR payload::text LIKE '%F04-GOV-003%'
               OR payload::text LIKE '%gateway%'))`,
		s.product, last(refused.review)).Scan(&approvals, &rejections, &worded); err != nil {
		t.Fatal(err)
	}
	decided := s.recorded(func(row audited) bool { return row.Action == "review.approve" || row.Action == "review.reject" })
	wantDecided := map[audited]int{
		{Subject: "reviewer", Action: "review.approve", Outcome: "SUCCEEDED", Rule: "AC-03", ProductID: s.product, ResourceType: "Review", ResourceID: last(clean.review)}:  1,
		{Subject: "second", Action: "review.approve", Outcome: "SUCCEEDED", Rule: "AC-03", ProductID: s.product, ResourceType: "Review", ResourceID: last(flawed.review)}:   1,
		{Subject: "reviewer", Action: "review.reject", Outcome: "SUCCEEDED", Rule: "AC-03", ProductID: s.product, ResourceType: "Review", ResourceID: last(refused.review)}: 1,
	}
	if approvals != 2 || rejections != 1 || worded != 0 || !reflect.DeepEqual(decided, wantDecided) ||
		s.count("policyreview.review_conclusion") != 3 || s.count("policyreview.review_waiver") != 1 {
		t.Fatalf("the evidence: %d and %d events, %d of them with words; audit entries %v, want %v; %d conclusions, %d waivers",
			approvals, rejections, worded, decided, wantDecided, s.count("policyreview.review_conclusion"), s.count("policyreview.review_waiver"))
	}
}

// A review is decided by a reviewer of the workspace who had no part in what
// is reviewed. Whoever recorded the revision, ran its check or asked for the
// review decides nothing about it, also when holding the reviewer grant;
// holding the author grant as well excludes nobody from what they had no
// part in. Each refusal for who the caller is is on record.
func TestADecisionIsMadeByAReviewerWhoHadNoPartInWhatIsReviewed(t *testing.T) {
	s := reviewsFor(t)
	s.join(s.product, s.workspace, "owner", "second", "internal_reviewer")
	s.join(s.product, s.workspace, "owner", "both", "internal_author")
	grant := s.join(s.product, s.workspace, "owner", "both", "internal_reviewer")
	operations := s.createWorkspace(s.product, "owner", "operations")
	s.join(s.product, operations, "owner", "elsewhere", "internal_reviewer")
	until := time.Now().UTC().Add(90 * 24 * time.Hour).Truncate(time.Second)
	approve, reject := deciding("APPROVED", "", until), deciding("REJECTED", "not yet", time.Time{})

	recorded := s.inReview(revisionSource+"# recorded by both\n", "both", "author", "coauthor")
	ran := s.inReview(revisionSource+"# checked by both\n", "author", "both", "coauthor")
	asked := s.inReview(revisionSource+"# asked by both\n", "author", "coauthor", "both")
	apart := s.inReview(revisionSource+"# none of it by both\n", "author", "coauthor", "author")
	later := s.inReview(revisionSource+"# decided after the grant ended\n", "author", "author", "author")

	for part, review := range map[string]submitted{"recorded the revision": recorded, "ran the check": ran, "asked for the review": asked} {
		s.expect("whoever "+part+" does not decide on it", "both", []exchange{
			{http.MethodPost, review.review + "/decision", approve, http.StatusForbidden, "REVIEWER_NOT_INDEPENDENT"},
			{http.MethodPost, review.review + "/decision", reject, http.StatusForbidden, "REVIEWER_NOT_INDEPENDENT"},
		})
	}
	// Who holds no reviewer grant in the workspace decides nothing.
	for caller, want := range map[string]exchange{
		"author":     {http.MethodPost, recorded.review + "/decision", approve, http.StatusForbidden, "FORBIDDEN"},
		"coauthor":   {http.MethodPost, recorded.review + "/decision", reject, http.StatusForbidden, "FORBIDDEN"},
		"maintainer": {http.MethodPost, recorded.review + "/decision", approve, http.StatusForbidden, "FORBIDDEN"},
		"owner":      {http.MethodPost, recorded.review + "/decision", approve, http.StatusForbidden, "FORBIDDEN"},
		"elsewhere":  {http.MethodPost, recorded.review + "/decision", approve, http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"stranger":   {http.MethodPost, recorded.review + "/decision", approve, http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	} {
		s.expect("only a reviewer of the workspace decides", caller, []exchange{want})
	}
	if got := s.call(http.MethodPost, recorded.review+"/decision", "", approve); got.status != http.StatusUnauthorized {
		t.Errorf("a decision without a token: %d %v", got.status, got.body)
	}
	for _, review := range []submitted{recorded, ran, asked, apart, later} {
		if got := s.statusOf(review.revision); got != "IN_REVIEW" {
			t.Errorf("after the refused decisions a revision is %v, want it in review", got)
		}
	}
	if s.count("policyreview.review_conclusion") != 0 {
		t.Errorf("the refused decisions left %d conclusions", s.count("policyreview.review_conclusion"))
	}

	// Holding the author grant as well excludes nobody from what they had
	// no part in, and what one reviewer may not decide another does.
	s.expect("a reviewer who had no part in it decides", "both", []exchange{
		{http.MethodPost, apart.review + "/decision", approve, http.StatusOK, ""},
	})
	s.expect("a reviewer who had no part in it decides", "reviewer", []exchange{
		{http.MethodPost, recorded.review + "/decision", reject, http.StatusOK, ""},
		{http.MethodPost, asked.review + "/decision", approve, http.StatusOK, ""},
	})
	s.expect("a reviewer who had no part in it decides", "second", []exchange{
		{http.MethodPost, ran.review + "/decision", approve, http.StatusOK, ""},
	})
	// The grant is read with every decision.
	s.revoke(grant, "owner")
	s.expect("a decision needs the reviewer grant when it is made", "both", []exchange{
		{http.MethodPost, later.review + "/decision", approve, http.StatusForbidden, "FORBIDDEN"},
	})
	for review, want := range map[string]string{
		recorded.revision: "REJECTED", ran.revision: "APPROVED", asked.revision: "APPROVED", apart.revision: "APPROVED", later.revision: "IN_REVIEW",
	} {
		if got := s.statusOf(review); got != want {
			t.Errorf("a revision is %v, want %s", got, want)
		}
	}

	// The refusals for who the caller is are on record as refusals of what
	// was asked, each under the rule that refused: who holds no reviewer
	// grant under DENY-03, and a reviewer who had a part in what is reviewed
	// under DENY-06. The owner and the maintainer were refused the reading
	// a decision begins with, and whoever does not see the asset was told
	// there is none.
	refusals := map[[3]string]int{}
	for row, count := range s.recorded(func(row audited) bool {
		return (row.Action == "review.approve" || row.Action == "review.reject") && row.Outcome != "SUCCEEDED"
	}) {
		if row.ResourceType != "Review" || row.ResourceID != "" || row.ProductID != s.product {
			t.Errorf("a refused decision is on record as %+v, want it as the refusal of a review in the product", row)
		}
		refusals[[3]string{row.Subject, row.Action, row.Rule}] += count
	}
	wantRefusals := map[[3]string]int{
		{"both", "review.approve", "DENY-06"}: 3, {"both", "review.reject", "DENY-06"}: 3, {"both", "review.approve", "DENY-03"}: 1,
		{"author", "review.approve", "DENY-03"}: 1, {"coauthor", "review.reject", "DENY-03"}: 1,
	}
	made := occurrences(s.recorded(func(row audited) bool {
		return (row.Action == "review.approve" || row.Action == "review.reject") && row.Outcome == "SUCCEEDED" && row.Rule == "AC-03"
	}))
	if !reflect.DeepEqual(refusals, wantRefusals) || made != 4 || s.count("policyreview.review_conclusion") != 4 {
		t.Errorf("the refusals on record are %v, want %v; %d decisions are on record and %d conclusions stored, want 4 of each",
			refusals, wantRefusals, made, s.count("policyreview.review_conclusion"))
	}
}

// askedBefore puts the revision in review on the check that is named, as a
// build recorded it whose tools were the tools of that check: the request is
// on record and the revision is in review. It returns where the review is.
func (s *reviewFixture) askedBefore(revision string, imported answer, check string) string {
	s.t.Helper()
	ref := domain.RevisionRef{
		ProductID: s.product, WorkspaceID: s.workspace, AssetID: imported.id("api_asset_id"), RevisionID: imported.id("revision_id"),
	}
	database, err := db.Open(s.t.Context(), s.database.Application)
	if err != nil {
		s.t.Fatal(err)
	}
	defer database.Close()
	request := domain.ReviewRequest{
		ID: ids.NewUUID(), Revision: ref, CheckRunID: check, InputDigest: imported.id("ref_closure_digest"),
		Scope:       []domain.Target{{Audience: "partner", Environment: "sandbox"}},
		RequestedBy: domain.Principal{Issuer: s.issuer.URL(), Subject: "author"}, RequestedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
	if _, err := s.owner.Exec(s.t.Context(), `UPDATE contractregistry.contract_revision
		SET status = 'IN_REVIEW', status_version = status_version + 1 WHERE revision_id = $1`, ref.RevisionID); err != nil {
		s.t.Fatal(err)
	}
	if err := database.WithinTransaction(s.t.Context(), func(ctx context.Context) error {
		return checkdb.NewReviews(database.Framework).Request(ctx, request, 2)
	}); err != nil {
		s.t.Fatal(err)
	}
	return revision + "/reviews/" + request.ID
}

// An approval rests on a check of the policy and the tools of today, in a
// product that takes new work, by a reviewer whose grant is in force when
// the decision commits. A decision is made with its record, its audit entry
// and its event or not at all, and of decisions that arrive together one is
// made.
func TestADecisionRestsOnACurrentCheckAndIsMadeOnceOrNotAtAll(t *testing.T) {
	s := reviewsFor(t)
	s.join(s.product, s.workspace, "owner", "second", "internal_reviewer")
	leaving := s.join(s.product, s.workspace, "owner", "leaving", "internal_reviewer")
	until := time.Now().UTC().Add(90 * 24 * time.Hour).Truncate(time.Second)
	approve, reject := deciding("APPROVED", "", until), deciding("REJECTED", "not yet", time.Time{})

	// A review that was asked on a check of tools no check is made with any
	// more: nothing approves on it, and it is still rejected or taken back.
	revision, imported, check := s.checked(revisionSource)
	stale := recordAnotherCheck(t, checkFixture{stack: s.stack}, domain.RevisionRef{
		ProductID: s.product, WorkspaceID: s.workspace, AssetID: imported.id("api_asset_id"), RevisionID: imported.id("revision_id"),
	}, check)
	outdated := s.askedBefore(revision, imported, stale)
	s.expect("an approval rests on a check of today", "reviewer", []exchange{
		{http.MethodGet, outdated, "", http.StatusOK, ""},
		{http.MethodPost, outdated + "/decision", approve, http.StatusConflict, "POLICY_STALE"},
	})
	if got := s.statusOf(revision); got != "IN_REVIEW" {
		t.Fatalf("after the refused approval the revision is %v", got)
	}
	s.expect("a rejection needs no check of today", "reviewer", []exchange{
		{http.MethodPost, outdated + "/decision", reject, http.StatusOK, ""},
	})

	open := s.inReview(unnamedSource, "author", "author", "author")
	waived := deciding("APPROVED", "", until, waiving("F04-GOV-003", until))
	if _, err := s.owner.Exec(t.Context(), `UPDATE productgovernance.product SET status = 'SUSPENDED' WHERE product_id = $1`, s.product); err != nil {
		t.Fatal(err)
	}
	s.expect("a suspended product takes no decision", "reviewer", []exchange{
		{http.MethodPost, open.review + "/decision", waived, http.StatusConflict, "PRODUCT_NOT_ACTIVE"},
		{http.MethodPost, open.review + "/decision", reject, http.StatusConflict, "PRODUCT_NOT_ACTIVE"},
		{http.MethodGet, open.review, "", http.StatusOK, ""},
	})
	if _, err := s.owner.Exec(t.Context(), `UPDATE productgovernance.product SET status = 'ACTIVE' WHERE product_id = $1`, s.product); err != nil {
		t.Fatal(err)
	}

	// An event that cannot be written takes the conclusion, its waivers and
	// the move of the revision with it. The events there are stay.
	if _, err := s.owner.Exec(t.Context(), `ALTER TABLE bento.outbox
		ADD CONSTRAINT reject_review_events CHECK (event_type NOT LIKE 'fabric.policyreview.review.%') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	s.expect("an outbox error rolls back the decision", "reviewer", []exchange{
		{http.MethodPost, open.review + "/decision", waived, http.StatusInternalServerError, "INTERNAL_ERROR"},
		{http.MethodPost, open.review + "/decision", reject, http.StatusInternalServerError, "INTERNAL_ERROR"},
	})
	if _, err := s.owner.Exec(t.Context(), `ALTER TABLE bento.outbox DROP CONSTRAINT reject_review_events`); err != nil {
		t.Fatal(err)
	}
	onRecord := occurrences(s.recorded(func(row audited) bool {
		return (row.Action == "review.approve" || row.Action == "review.reject") && row.ResourceID == last(open.review)
	}))
	if got := s.call(http.MethodGet, open.review, "author", ""); got.body["status"] != "IN_REVIEW" || got.body["conclusion"] != nil ||
		s.statusOf(open.revision) != "IN_REVIEW" || s.count("policyreview.review_conclusion") != 1 || s.count("policyreview.review_waiver") != 0 || onRecord != 0 {
		t.Fatalf("after the decisions that failed the review is %v and the revision %v; %d conclusions, %d waivers, %d audit entries of it",
			got.body, s.statusOf(open.revision), s.count("policyreview.review_conclusion"), s.count("policyreview.review_waiver"), onRecord)
	}

	// A revocation and a decision take effect in the order they commit: a
	// decision that waits for a revocation finds the grant gone.
	ctx := t.Context()
	conn := pgtest.Connect(t, s.database.Migrator)
	revoking, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = revoking.Rollback(ctx) }()
	tag, err := revoking.Exec(ctx, `UPDATE productgovernance.workspace_membership
		SET revoked_at = greatest(granted_at, clock_timestamp()), revoked_by_issuer = $2,
		    revoked_by_subject = 'owner', membership_version = membership_version + 1
		WHERE membership_id = $1`, last(leaving), s.issuer.URL())
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("stage the revocation of the reviewer: %v %v", tag, err)
	}
	token := s.token("leaving")
	waiting := make(chan answer, 1)
	go func() { waiting <- s.callWith(http.MethodPost, open.review+"/decision", token, waived) }()
	s.waiting(1)
	if err := revoking.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if got := <-waiting; got.status != http.StatusForbidden || got.code() != "FORBIDDEN" || s.statusOf(open.revision) != "IN_REVIEW" {
		t.Fatalf("a decision that waited for the revocation of its reviewer: %d %v; the revision is %v", got.status, got.body, s.statusOf(open.revision))
	}

	// Decisions that arrive together are made one after the other: one is
	// made, and the others find the revision decided.
	deciders := []struct{ subject, body string }{
		{"reviewer", waived}, {"second", waived}, {"reviewer", reject}, {"second", reject},
	}
	results := make(chan answer, len(deciders))
	for _, decider := range deciders {
		token := s.token(decider.subject)
		go func() { results <- s.callWith(http.MethodPost, open.review+"/decision", token, decider.body) }()
	}
	var made []answer
	for range deciders {
		switch got := <-results; {
		case got.status == http.StatusOK:
			made = append(made, got)
		case got.status != http.StatusConflict || (got.code() != "REVISION_STATUS_CONFLICT" && got.code() != "REVIEW_CONCLUDED"):
			t.Errorf("a decision among several: %d %v, want it made or told of the conflict", got.status, got.body)
		}
	}
	if len(made) != 1 {
		t.Fatalf("%d of %d decisions were made, want one", len(made), len(deciders))
	}
	outcome := made[0].body["status"]
	waivers := 0
	if outcome == "APPROVED" {
		waivers = 1
	}
	if got := s.call(http.MethodGet, open.review, "author", ""); !reflect.DeepEqual(got.body, made[0].body) || s.statusOf(open.revision) != outcome ||
		s.count("policyreview.review_conclusion") != 2 || s.count("policyreview.review_waiver") != waivers {
		t.Errorf("the review reads %v and the revision is %v, want what the one decision answered: %v; %d conclusions and %d waivers are stored",
			got.body, s.statusOf(open.revision), made[0].body, s.count("policyreview.review_conclusion"), s.count("policyreview.review_waiver"))
	}
	events := 0
	if err := s.owner.QueryRow(ctx, `SELECT count(*) FROM bento.outbox
		WHERE event_type = 'fabric.policyreview.review.decided' AND payload->>'aggregate_id' = $1`, last(open.review)).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if succeeded := occurrences(s.recorded(func(row audited) bool {
		return row.ResourceID == last(open.review) && row.Outcome == "SUCCEEDED" && row.Rule == "AC-03"
	})); events != 1 || succeeded != 1 {
		t.Errorf("the one decision left %d events and %d audit entries, want one of each", events, succeeded)
	}
}
