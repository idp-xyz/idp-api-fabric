package main

import (
	"net/http"
	"reflect"
	"testing"
)

// reviewFixture is a product with a workspace, its members and an asset: a
// maintainer, two authors, a reviewer made before reviewers stopped being
// made, and the owner of the product.
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
	s.reviewerBefore(product, workspace, "owner", "reviewer")
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

var (
	partnerSandbox     = [2]string{"partner", "sandbox"}
	partnerProduction  = [2]string{"partner", "production"}
	publicSandbox      = [2]string{"public", "sandbox"}
	internalProduction = [2]string{"internal", "production"}
)

func (s *reviewFixture) statusOf(revision string) any {
	s.t.Helper()
	return s.call(http.MethodGet, revision, "author", "").body["status"]
}

// The reviews a revision had before reviews were retired are read as they
// were recorded: what was asked, on which check and for which scope, and how
// it ended, one review alone or the reviews of the revision a page at a time,
// to whoever reads the revision. A review and a page token belong to the
// revision in the path.
func TestTheReviewsOfBeforeAreReadAsTheyWereRecorded(t *testing.T) {
	s := reviewsFor(t)
	revision, imported, check := s.checked(revisionSource)
	reviews := revision + "/reviews"
	if items, token := s.page(reviews, "author"); len(items) != 0 || token != "" {
		t.Fatalf("the reviews of a revision nobody submitted: %v, token %q", items, token)
	}

	first := s.asked(revision, check, "author", publicSandbox, partnerProduction, partnerSandbox)
	withdrawn := s.concluded(first, "author", "WITHDRAWN", "the baseline was the wrong one")
	second := s.asked(revision, check, "coauthor", partnerSandbox)
	approved := s.concluded(second, "reviewer", "APPROVED", "")
	if got := s.statusOf(revision); got != "APPROVED" {
		t.Fatalf("the revision whose review was approved is %v", got)
	}

	conclusion, _ := withdrawn.body["conclusion"].(map[string]any)
	want := map[string]any{
		"review_id": last(first), "product_id": s.product, "api_asset_id": imported.id("api_asset_id"),
		"revision_id": imported.id("revision_id"), "check_run_id": check, "input_digest": imported.body["ref_closure_digest"],
		"status": "WITHDRAWN", "supplementary": false, "note": "ready for partners",
		"scope": []any{
			map[string]any{"audience": "partner", "environment": "production"},
			map[string]any{"audience": "partner", "environment": "sandbox"},
			map[string]any{"audience": "public", "environment": "sandbox"},
		},
		"requested_by": map[string]any{"issuer": s.issuer.URL(), "subject": "author"},
		"requested_at": withdrawn.body["requested_at"],
		"conclusion": map[string]any{
			"outcome": "WITHDRAWN", "reason": "the baseline was the wrong one",
			"concluded_by": map[string]any{"issuer": s.issuer.URL(), "subject": "author"}, "concluded_at": conclusion["concluded_at"],
		},
	}
	if !reflect.DeepEqual(withdrawn.body, want) || conclusion["concluded_at"] == nil || withdrawn.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the withdrawn review reads\n %v\nwant\n %v", withdrawn.body, want)
	}
	if decided, _ := approved.body["conclusion"].(map[string]any); approved.body["status"] != "APPROVED" ||
		decided["valid_until"] == nil || decided["concluded_by"].(map[string]any)["subject"] != "reviewer" {
		t.Errorf("the approved review reads %v", approved.body)
	}
	for _, reader := range []string{"author", "coauthor", "reviewer", "maintainer", "owner"} {
		for _, review := range []answer{withdrawn, approved} {
			if got := s.call(http.MethodGet, reviews+"/"+review.id("review_id"), reader, ""); got.status != http.StatusOK || !reflect.DeepEqual(got.body, review.body) {
				t.Errorf("%s reads the review: %d %v, want it as it was recorded", reader, got.status, got.body)
			}
		}
	}

	items, token := s.page(reviews, "reviewer")
	if len(items) != 2 || token != "" || !reflect.DeepEqual(items[0], withdrawn.body) || !reflect.DeepEqual(items[1], approved.body) {
		t.Fatalf("the reviews of the revision: %v, want the withdrawn one and then the approved one", items)
	}
	page, next := s.page(reviews+"?page_size=1", "reviewer")
	rest, end := s.page(reviews+"?page_size=1&page_token="+next, "reviewer")
	if len(page) != 1 || page[0]["review_id"] != last(first) || next == "" || len(rest) != 1 || rest[0]["review_id"] != last(second) || end != "" {
		t.Errorf("the reviews page by page: %v then %v", page, rest)
	}
	other, _, _ := s.checked(revisionSource + "# another draft\n")
	s.expect("a review and a cursor belong to the revision in the path", "author", []exchange{
		{http.MethodGet, other + "/reviews/" + last(first), "", http.StatusNotFound, "REVIEW_NOT_FOUND"},
		{http.MethodGet, other + "/reviews?page_token=" + next, "", http.StatusBadRequest, "PAGE_TOKEN_INVALID"},
		{http.MethodGet, reviews + "/00000000-0000-4000-8000-000000000000", "", http.StatusNotFound, "REVIEW_NOT_FOUND"},
	})
	s.expect("a reader of nothing in the product", "stranger", []exchange{
		{http.MethodGet, reviews, "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
		{http.MethodGet, reviews + "/" + last(first), "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	})
}

// Nobody asks for a review, takes one back or decides one any more: the
// operations are not there, for any caller, and nothing is recorded of the
// attempt. The revision stays the draft it is, and a release is prepared on
// its check instead.
func TestNoReviewIsAskedWithdrawnOrDecidedAnyMore(t *testing.T) {
	s := reviewsFor(t)
	revision, _, check := s.checked(revisionSource)
	reviews := revision + "/reviews"
	open := s.asked(revision, check, "author", partnerSandbox)
	audited, requests, conclusions := len(s.audit()), s.count("policyreview.review_request"), s.count("policyreview.review_conclusion")

	ask := `{"check_run_id":"` + check + `","scope":[{"audience":"partner","environment":"sandbox"}]}`
	for _, caller := range []string{"author", "reviewer", "owner"} {
		s.expect("the review operations that were retired", caller, []exchange{
			{http.MethodPost, reviews, ask, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED"},
			{http.MethodPost, open + "/withdrawal", `{}`, http.StatusNotFound, "ROUTE_NOT_FOUND"},
			{http.MethodPost, open + "/decision", `{"outcome":"REJECTED","reason":"no"}`, http.StatusNotFound, "ROUTE_NOT_FOUND"},
		})
	}
	if len(s.audit()) != audited || s.count("policyreview.review_request") != requests || s.count("policyreview.review_conclusion") != conclusions {
		t.Errorf("the attempts left %d audit records, %d requests and %d conclusions, want %d, %d and %d",
			len(s.audit()), s.count("policyreview.review_request"), s.count("policyreview.review_conclusion"), audited, requests, conclusions)
	}
	if got := s.call(http.MethodGet, open, "author", ""); got.status != http.StatusOK || got.body["status"] != "IN_REVIEW" {
		t.Errorf("the review recorded before: %d %v", got.status, got.body)
	}
}
