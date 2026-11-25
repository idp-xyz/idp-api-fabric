package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// reviewFixture is a product with a workspace, its members and an asset: a
// maintainer, two authors, a reviewer and the owner of the product.
type reviewFixture struct {
	*stack
	product   string
	workspace string
	revisions string
	imported  int
}

func reviewsFor(t *testing.T) *reviewFixture {
	t.Helper()
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "owner")
	workspace := s.createWorkspace(product, "owner", "engineering")
	s.join(product, workspace, "owner", "maintainer", "workspace_maintainer")
	s.join(product, workspace, "owner", "author", "internal_author")
	s.join(product, workspace, "owner", "coauthor", "internal_author")
	s.join(product, workspace, "owner", "reviewer", "internal_reviewer")
	return &reviewFixture{
		stack: s, product: product, workspace: workspace,
		revisions: s.revisionAsset(product, workspace, "maintainer", "shipping-api", "HTTP"),
	}
}

// checked imports a revision of the source as the author and checks it. It
// returns where the revision is read, what the import answered and the
// identifier of the check.
func (s *reviewFixture) checked(source string) (string, answer, string) {
	s.t.Helper()
	imported := s.call(http.MethodPost, s.revisions, "author", uploadRevision("openapi.yaml", source))
	if imported.status != http.StatusCreated {
		s.t.Fatalf("import a revision: %d %v", imported.status, imported.body)
	}
	revision := s.revisions + "/" + imported.id("revision_id")
	check := s.call(http.MethodPost, revision+"/check-runs", "author", "")
	if check.status != http.StatusCreated {
		s.t.Fatalf("check the revision: %d %v", check.status, check.body)
	}
	return revision, imported, check.body["check_run"].(map[string]any)["check_run_id"].(string)
}

// asking is the body a review is asked with: on a check, for targets given
// as audience and environment.
func asking(check string, targets ...[2]string) string {
	scope := make([]map[string]string, 0, len(targets))
	for _, target := range targets {
		scope = append(scope, map[string]string{"audience": target[0], "environment": target[1]})
	}
	encoded, err := json.Marshal(map[string]any{"check_run_id": check, "scope": scope, "note": "ready for partners"})
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

var (
	partnerSandbox    = [2]string{"partner", "sandbox"}
	partnerProduction = [2]string{"partner", "production"}
	publicSandbox     = [2]string{"public", "sandbox"}
)

func (s *reviewFixture) statusOf(revision string) any {
	s.t.Helper()
	return s.call(http.MethodGet, revision, "author", "").body["status"]
}

// An author submits a checked draft for review and the revision is in review
// under that one request. Whoever asked takes the request back, and what was
// withdrawn is submitted again as the next review of the revision. Each step
// leaves its record, its audit entry and its event, and the content of the
// revision is what it was throughout.
func TestARevisionIsSubmittedForReviewWithdrawnAndSubmittedAgain(t *testing.T) {
	s := reviewsFor(t)
	revision, imported, check := s.checked(revisionSource)
	reviews := revision + "/reviews"
	if got := s.statusOf(revision); got != "DRAFT" {
		t.Fatalf("a revision that was just recorded is %v", got)
	}
	if items, token := s.page(reviews, "author"); len(items) != 0 || token != "" {
		t.Fatalf("the reviews of a revision nobody submitted: %v, token %q", items, token)
	}

	created := s.call(http.MethodPost, reviews, "author", asking(check, publicSandbox, partnerProduction, partnerSandbox))
	first := created.id("review_id")
	want := map[string]any{
		"review_id": first, "product_id": s.product, "api_asset_id": imported.id("api_asset_id"),
		"revision_id": imported.id("revision_id"), "check_run_id": check, "input_digest": imported.body["ref_closure_digest"],
		"status": "IN_REVIEW", "supplementary": false, "note": "ready for partners",
		"scope": []any{
			map[string]any{"audience": "partner", "environment": "production"},
			map[string]any{"audience": "partner", "environment": "sandbox"},
			map[string]any{"audience": "public", "environment": "sandbox"},
		},
		"requested_by": map[string]any{"issuer": s.issuer.URL(), "subject": "author"},
		"requested_at": created.body["requested_at"],
	}
	if created.status != http.StatusCreated || first == "" || !reflect.DeepEqual(created.body, want) ||
		created.header.Get("Location") != reviews+"/"+first || created.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the submission: %d %v %v\nwant\n %v at %s", created.status, created.body, created.header, want, reviews+"/"+first)
	}
	if got := s.statusOf(revision); got != "IN_REVIEW" {
		t.Errorf("a submitted revision is %v, want IN_REVIEW", got)
	}
	// Its content is what it was: only its status moved.
	moved := s.call(http.MethodGet, revision, "author", "").body
	moved["status"] = "DRAFT"
	if !reflect.DeepEqual(moved, imported.body) {
		t.Errorf("the submitted revision reads %v, want what was imported but for its status %v", moved, imported.body)
	}
	for _, reader := range []string{"author", "coauthor", "reviewer"} {
		if got := s.call(http.MethodGet, reviews+"/"+first, reader, ""); got.status != http.StatusOK || !reflect.DeepEqual(got.body, created.body) {
			t.Errorf("%s reads the review: %d %v, want it as it was answered", reader, got.status, got.body)
		}
	}

	// It is in review under this one request.
	s.expect("a revision in review is not submitted again", "coauthor", []exchange{
		{http.MethodPost, reviews, asking(check, partnerSandbox), http.StatusConflict, "REVISION_STATUS_CONFLICT"},
	})
	// Whoever asked takes the request back, and nobody else.
	withdrawal := reviews + "/" + first + "/withdrawal"
	for _, caller := range []string{"coauthor", "reviewer"} {
		s.expect("a review is withdrawn by whoever asked for it", caller, []exchange{
			{http.MethodPost, withdrawal, `{}`, http.StatusForbidden, "FORBIDDEN"},
		})
	}
	if got := s.statusOf(revision); got != "IN_REVIEW" {
		t.Errorf("after the refused withdrawals the revision is %v", got)
	}
	withdrawn := s.call(http.MethodPost, withdrawal, "author", `{"reason":"the baseline was the wrong one"}`)
	conclusion, _ := withdrawn.body["conclusion"].(map[string]any)
	wantConclusion := map[string]any{
		"outcome": "WITHDRAWN", "reason": "the baseline was the wrong one",
		"concluded_by": map[string]any{"issuer": s.issuer.URL(), "subject": "author"}, "concluded_at": conclusion["concluded_at"],
	}
	want["status"], want["conclusion"] = "WITHDRAWN", wantConclusion
	if withdrawn.status != http.StatusOK || !reflect.DeepEqual(withdrawn.body, want) || conclusion["concluded_at"] == nil {
		t.Fatalf("the withdrawal: %d %v\nwant\n %v", withdrawn.status, withdrawn.body, want)
	}
	if got := s.statusOf(revision); got != "WITHDRAWN" {
		t.Errorf("a withdrawn revision is %v", got)
	}
	s.expect("a review is concluded once", "author", []exchange{
		{http.MethodPost, withdrawal, `{}`, http.StatusConflict, "REVIEW_CONCLUDED"},
	})

	// What was withdrawn is submitted again, by any author, as the next
	// review. The same content imported again is still this revision.
	again := s.call(http.MethodPost, reviews, "coauthor", asking(check, partnerSandbox))
	second := again.id("review_id")
	if again.status != http.StatusCreated || second == "" || second == first || again.body["status"] != "IN_REVIEW" {
		t.Fatalf("the second submission: %d %v", again.status, again.body)
	}
	if reimported := s.call(http.MethodPost, s.revisions, "author", uploadRevision("openapi.yaml", revisionSource)); reimported.status != http.StatusOK ||
		reimported.id("revision_id") != imported.id("revision_id") || reimported.body["status"] != "IN_REVIEW" {
		t.Errorf("the same content imported again: %d %v, want the revision that is in review", reimported.status, reimported.body)
	}
	items, token := s.page(reviews, "reviewer")
	if len(items) != 2 || token != "" || !reflect.DeepEqual(items[0], withdrawn.body) || !reflect.DeepEqual(items[1], again.body) {
		t.Fatalf("the reviews of the revision: %v, want the withdrawn one and then the open one", items)
	}
	page, next := s.page(reviews+"?page_size=1", "reviewer")
	rest, end := s.page(reviews+"?page_size=1&page_token="+next, "reviewer")
	if len(page) != 1 || page[0]["review_id"] != first || next == "" || len(rest) != 1 || rest[0]["review_id"] != second || end != "" {
		t.Errorf("the reviews page by page: %v then %v", page, rest)
	}
	other, _, _ := s.checked(revisionSource + "# another draft\n")
	s.expect("a review and a cursor belong to the revision in the path", "author", []exchange{
		{http.MethodGet, other + "/reviews/" + first, "", http.StatusNotFound, "REVIEW_NOT_FOUND"},
		{http.MethodPost, other + "/reviews/" + second + "/withdrawal", `{}`, http.StatusNotFound, "REVIEW_NOT_FOUND"},
		{http.MethodGet, other + "/reviews?page_token=" + next, "", http.StatusBadRequest, "PAGE_TOKEN_INVALID"},
		{http.MethodGet, reviews + "/00000000-0000-4000-8000-000000000000", "", http.StatusNotFound, "REVIEW_NOT_FOUND"},
	})

	// What is on record: two requests, one conclusion, and for each of the
	// three an audit entry and an event; the two withdrawals that were
	// refused for who asked are on record as refusals.
	var requested, concluded int
	if err := s.owner.QueryRow(t.Context(), `
SELECT (SELECT count(*) FROM bento.outbox WHERE event_type = 'fabric.policyreview.review.requested' AND scope = $1
          AND payload->>'revision_id' = $2 AND payload->>'check_run_id' = $3 AND payload->>'aggregate_version' = '1'),
       (SELECT count(*) FROM bento.outbox WHERE event_type = 'fabric.policyreview.review.withdrawn' AND scope = $1
          AND payload->>'aggregate_id' = $4 AND payload->>'outcome' = 'WITHDRAWN' AND payload->>'aggregate_version' = '2')`,
		s.product, imported.id("revision_id"), check, first).Scan(&requested, &concluded); err != nil {
		t.Fatal(err)
	}
	asked := s.recorded(func(row audited) bool {
		return row.Action == "review.request" && row.Outcome == "SUCCEEDED" && row.Rule == "AC-50"
	})
	taken := s.recorded(func(row audited) bool {
		return row.Action == "review.withdraw" && row.Outcome == "SUCCEEDED" && row.Rule == "AC-51" && row.Subject == "author"
	})
	denied := s.recorded(func(row audited) bool {
		return row.Action == "review.withdraw" && row.Outcome != "SUCCEEDED" && row.Rule == "DENY-03"
	})
	every := s.recorded(func(row audited) bool { return row.Action == "review.request" || row.Action == "review.withdraw" })
	if requested != 2 || concluded != 1 || occurrences(asked) != 2 || occurrences(taken) != 1 || occurrences(denied) != 2 || occurrences(every) != 5 ||
		s.count("policyreview.review_request") != 2 || s.count("policyreview.review_conclusion") != 1 || s.count("policyreview.review_scope") != 4 {
		t.Fatalf("the evidence: %d and %d events, audit entries %v; %d requests, %d conclusions, %d targets",
			requested, concluded, every, s.count("policyreview.review_request"), s.count("policyreview.review_conclusion"), s.count("policyreview.review_scope"))
	}
}

// A review is asked by an author of the workspace, in a product that takes
// new work, on a check of the revision a decision can rest on. Whatever is
// refused leaves the revision a draft and no review behind.
func TestAReviewIsAskedByAnAuthorOnACheckADecisionCanRestOn(t *testing.T) {
	s := reviewsFor(t)
	revision, imported, check := s.checked(revisionSource)
	reviews := revision + "/reviews"
	other, _, foreign := s.checked(revisionSource + "# another draft\n")
	// A contract that states no security requirement: a rule nothing waives fails.
	flawed, _, failed := s.checked(checkSource)
	// The same revision as a later version of the tool checked it.
	stale := recordAnotherCheck(t, checkFixture{stack: s.stack}, domain.RevisionRef{
		ProductID: s.product, WorkspaceID: s.workspace, AssetID: imported.id("api_asset_id"), RevisionID: imported.id("revision_id"),
	}, check)

	s.expect("a check a decision cannot rest on", "author", []exchange{
		{http.MethodPost, reviews, asking(foreign, partnerSandbox), http.StatusNotFound, "CHECK_RUN_NOT_FOUND"},
		{http.MethodPost, reviews, asking("00000000-0000-4000-8000-000000000000", partnerSandbox), http.StatusNotFound, "CHECK_RUN_NOT_FOUND"},
		{http.MethodPost, reviews, asking(stale, partnerSandbox), http.StatusConflict, "POLICY_STALE"},
		{http.MethodPost, flawed + "/reviews", asking(failed, partnerSandbox), http.StatusConflict, "REVIEW_BLOCKED"},
		{http.MethodPost, reviews, asking(check), http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, reviews, asking(check, partnerSandbox, partnerSandbox), http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, reviews, asking(check, [2]string{"customers", "sandbox"}), http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, revision + "/reviews/latest/withdrawal", `{}`, http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPut, reviews, asking(check, partnerSandbox), http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED"},
	})
	for caller, want := range map[string]exchange{
		"owner":      {http.MethodPost, reviews, asking(check, partnerSandbox), http.StatusForbidden, "FORBIDDEN"},
		"maintainer": {http.MethodPost, reviews, asking(check, partnerSandbox), http.StatusForbidden, "FORBIDDEN"},
		"reviewer":   {http.MethodPost, reviews, asking(check, partnerSandbox), http.StatusForbidden, "FORBIDDEN"},
		"stranger":   {http.MethodPost, reviews, asking(check, partnerSandbox), http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	} {
		s.expect("only an author asks for a review", caller, []exchange{want})
	}
	if got := s.call(http.MethodPost, reviews, "", asking(check, partnerSandbox)); got.status != http.StatusUnauthorized {
		t.Errorf("a submission without a token: %d %v", got.status, got.body)
	}
	// The owner, maintainer and reviewer can read the revision. Submission requires an author.
	refusals := s.recorded(func(row audited) bool { return row.Action == "review.request" })
	if occurrences(refusals) != 3 || s.on("reviewer", "review.request", "DENY-03", false) != 1 ||
		s.on("owner", "review.request", "DENY-03", false) != 1 || s.on("maintainer", "review.request", "DENY-03", false) != 1 {
		t.Errorf("the refused submissions are on record as %v, want those of the owner, maintainer and reviewer", refusals)
	}

	if _, err := s.owner.Exec(t.Context(), `UPDATE productgovernance.product SET status = 'SUSPENDED' WHERE product_id = $1`, s.product); err != nil {
		t.Fatal(err)
	}
	s.expect("a suspended product takes no new review", "author", []exchange{
		{http.MethodPost, reviews, asking(check, partnerSandbox), http.StatusConflict, "PRODUCT_NOT_ACTIVE"},
		{http.MethodGet, reviews, "", http.StatusOK, ""},
	})
	if _, err := s.owner.Exec(t.Context(), `UPDATE productgovernance.product SET status = 'ACTIVE' WHERE product_id = $1`, s.product); err != nil {
		t.Fatal(err)
	}

	// An event that cannot be written takes the request and the move of the
	// revision with it.
	if _, err := s.owner.Exec(t.Context(), `ALTER TABLE bento.outbox
		ADD CONSTRAINT reject_review_events CHECK (event_type NOT LIKE 'fabric.policyreview.review.%')`); err != nil {
		t.Fatal(err)
	}
	s.expect("an outbox error rolls back the submission", "author", []exchange{
		{http.MethodPost, reviews, asking(check, partnerSandbox), http.StatusInternalServerError, "INTERNAL_ERROR"},
	})
	for _, draft := range []string{revision, other, flawed} {
		if got := s.statusOf(draft); got != "DRAFT" {
			t.Errorf("after the refused submissions a revision is %v, want it a draft", got)
		}
	}
	if s.count("policyreview.review_request") != 0 || s.count("policyreview.review_scope") != 0 ||
		occurrences(s.recorded(func(row audited) bool { return row.Action == "review.request" && row.Outcome == "SUCCEEDED" })) != 0 {
		t.Errorf("the refused submissions left %d requests and %d targets", s.count("policyreview.review_request"), s.count("policyreview.review_scope"))
	}
	if _, err := s.owner.Exec(t.Context(), `ALTER TABLE bento.outbox DROP CONSTRAINT reject_review_events`); err != nil {
		t.Fatal(err)
	}

	// Submissions that arrive together are decided one after the other: one
	// is made and the others find the revision in review.
	const submitters = 4
	token := s.token("author")
	results := make(chan answer, submitters)
	for range submitters {
		go func() { results <- s.callWith(http.MethodPost, reviews, token, asking(check, partnerSandbox)) }()
	}
	made := 0
	for range submitters {
		switch got := <-results; {
		case got.status == http.StatusCreated:
			made++
		case got.status != http.StatusConflict || got.code() != "REVISION_STATUS_CONFLICT":
			t.Errorf("a submission among several: %d %v, want it made or told of the conflict", got.status, got.body)
		}
	}
	if made != 1 || s.count("policyreview.review_request") != 1 || s.statusOf(revision) != "IN_REVIEW" {
		t.Errorf("%d of %d submissions were made, %d requests are stored and the revision is %v",
			made, submitters, s.count("policyreview.review_request"), s.statusOf(revision))
	}
}
