package http

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	reviewdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	reviewports "go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

const (
	reviewID   = "9a0b1c2d-3e4f-4a5b-8c6d-7e8f9a0b1c2d"
	checkRunID = "7c8d9e0f-1a2b-4c3d-9e4f-5a6b7c8d9e0f"
	// reviews is where the reviews of the revision are asked for and read.
	reviews = revisions + "/" + revisionID + "/reviews"
)

// contractReviews stands in for the reads of a review. It keeps what it was
// asked and answers what the test set. The use cases of a check are not
// reached by these tests.
type contractReviews struct {
	PolicyReview

	review reviewdomain.Review
	page   checks.ReviewPage
	err    error

	targets []reviewports.Target
	read    []string
	listed  []checks.ListReviews
}

func (r *contractReviews) GetReview(_ context.Context, target reviewports.Target, id string) (reviewdomain.Review, error) {
	r.targets, r.read = append(r.targets, target), append(r.read, id)
	return r.review, r.err
}

func (r *contractReviews) ListReviews(_ context.Context, query checks.ListReviews) (checks.ReviewPage, error) {
	r.listed = append(r.listed, query)
	return r.page, r.err
}

func newReviewService(t *testing.T, fake *contractReviews) *service {
	t.Helper()

	s := &service{governance: newGovernance(), log: &bytes.Buffer{}}
	handler, err := NewHandler(Config{
		Logger: slog.New(slog.NewTextHandler(s.log, nil)), Build: testBuild, Verifier: tokens{}, Governance: s.governance,
		Catalog: unusedCatalog{}, Registry: unusedRegistry{}, PolicyReview: fake,
		Releases: unusedReleases{}, Publication: unusedPublications{}, Audit: unusedAudit{},
	})
	if err != nil {
		t.Fatalf("build the handler: %v", err)
	}
	s.handler = handler
	return s
}

// wantRefusal checks that the answer is a problem with the status and the
// code, whatever it says in detail of what the contract refused.
func wantRefusal(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()

	if got := members(t, recorder); recorder.Code != status || got["error_code"] != code ||
		recorder.Header().Get("Content-Type") != ProblemContentType {
		t.Errorf("answered %d %s, want the problem %d %s", recorder.Code, recorder.Body, status, code)
	}
}

// asked is the review alice asked for, of the revision for partners in the
// sandbox and for the engineers in production.
func askedReview() reviewdomain.Review {
	return reviewdomain.Review{Request: reviewdomain.ReviewRequest{
		ID: reviewID, CheckRunID: checkRunID, InputDigest: "sha256:" + fmt.Sprintf("%064x", 2),
		Revision: reviewdomain.RevisionRef{ProductID: productID, WorkspaceID: workspaceID, AssetID: apiAssetID, RevisionID: revisionID},
		Scope: []reviewdomain.Target{
			{Audience: "internal", Environment: "production"}, {Audience: "partner", Environment: "sandbox"},
		},
		Note:        "ready for partners",
		RequestedBy: reviewdomain.Principal{Issuer: alice.Issuer, Subject: alice.Subject},
		RequestedAt: createdAt,
	}}
}

func withdrawnReview() reviewdomain.Review {
	review := askedReview()
	review.Conclusion = &reviewdomain.Conclusion{
		ReviewID: reviewID, Outcome: reviewdomain.ReviewWithdrawn, Reason: "the wrong baseline",
		ConcludedBy: reviewdomain.Principal{Issuer: alice.Issuer, Subject: alice.Subject}, ConcludedAt: createdAt.Add(time.Hour),
	}
	return review
}

// reviewer is who decided the reviews of these tests.
var reviewer = reviewdomain.Principal{Issuer: alice.Issuer, Subject: "rita"}

// approvedReview is the review rita approved for ninety days, waiving two
// rules that did not pass.
func approvedReview() reviewdomain.Review {
	review, decided := askedReview(), createdAt.Add(time.Hour)
	review.Conclusion = &reviewdomain.Conclusion{
		ReviewID: reviewID, Outcome: reviewdomain.ReviewApproved, Reason: "reviewed with the partner team",
		ConcludedBy: reviewer, ConcludedAt: decided, ValidUntil: decided.Add(90 * 24 * time.Hour),
		Waivers: []reviewdomain.Waiver{
			{RuleID: "F04-GOV-002", Reason: "the title is settled next sprint", CompensatingControl: "the portal shows the asset name",
				ExpiresAt: decided.Add(60 * 24 * time.Hour)},
			{RuleID: "F04-GOV-003", Reason: "generated clients are not used yet", ExpiresAt: decided.Add(30 * 24 * time.Hour)},
		},
	}
	return review
}

func rejectedReview() reviewdomain.Review {
	review := askedReview()
	review.Conclusion = &reviewdomain.Conclusion{
		ReviewID: reviewID, Outcome: reviewdomain.ReviewRejected, Reason: "every operation needs its operationId",
		ConcludedBy: reviewer, ConcludedAt: createdAt.Add(time.Hour),
	}
	return review
}

// shownApproval and shownRejection are the conclusions of those reviews as
// the contract states them.
func shownApproval() map[string]any {
	decided := createdAt.Add(time.Hour)
	return map[string]any{
		"outcome": "APPROVED", "reason": "reviewed with the partner team",
		"concluded_by": map[string]any{"issuer": reviewer.Issuer, "subject": reviewer.Subject},
		"concluded_at": decided.Format(time.RFC3339Nano),
		"valid_until":  decided.Add(90 * 24 * time.Hour).Format(time.RFC3339Nano),
		"waivers": []any{
			map[string]any{
				"rule_id": "F04-GOV-002", "reason": "the title is settled next sprint",
				"compensating_control": "the portal shows the asset name",
				"expires_at":           decided.Add(60 * 24 * time.Hour).Format(time.RFC3339Nano),
			},
			map[string]any{
				"rule_id": "F04-GOV-003", "reason": "generated clients are not used yet",
				"expires_at": decided.Add(30 * 24 * time.Hour).Format(time.RFC3339Nano),
			},
		},
	}
}

func shownRejection() map[string]any {
	return map[string]any{
		"outcome": "REJECTED", "reason": "every operation needs its operationId",
		"concluded_by": map[string]any{"issuer": reviewer.Issuer, "subject": reviewer.Subject},
		"concluded_at": createdAt.Add(time.Hour).Format(time.RFC3339Nano),
	}
}

func shownReview(conclusion map[string]any) map[string]any {
	shown := map[string]any{
		"review_id": reviewID, "product_id": productID, "api_asset_id": apiAssetID, "revision_id": revisionID,
		"check_run_id": checkRunID, "input_digest": "sha256:" + fmt.Sprintf("%064x", 2), "status": "IN_REVIEW",
		"supplementary": false,
		"scope": []any{
			map[string]any{"audience": "internal", "environment": "production"},
			map[string]any{"audience": "partner", "environment": "sandbox"},
		},
		"note":         "ready for partners",
		"requested_by": map[string]any{"issuer": alice.Issuer, "subject": alice.Subject},
		"requested_at": createdAt.Format(time.RFC3339Nano),
	}
	if conclusion != nil {
		shown["status"], shown["conclusion"] = conclusion["outcome"], conclusion
	}
	return shown
}

// A review is read by its identifier and listed page by page, under the
// cursor of its own list. The reviews read are those recorded before reviews
// were retired (CR-0001).
func TestAReviewIsReadAndListedAsTheContractStatesIt(t *testing.T) {
	conclusion := map[string]any{
		"outcome": "WITHDRAWN", "reason": "the wrong baseline",
		"concluded_by": map[string]any{"issuer": alice.Issuer, "subject": alice.Subject},
		"concluded_at": createdAt.Add(time.Hour).Format(time.RFC3339Nano),
	}

	fake := &contractReviews{review: withdrawnReview()}
	s := newReviewService(t, fake)
	recorder := s.do(t, call{Method: http.MethodGet, Target: reviews + "/" + reviewID, Token: alice.Subject})
	if recorder.Code != http.StatusOK || !reflect.DeepEqual(members(t, recorder), shownReview(conclusion)) ||
		!reflect.DeepEqual(fake.read, []string{reviewID}) || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("read: %d %s, asked for %v", recorder.Code, recorder.Body, fake.read)
	}
	target := fake.targets[0]
	if target.ProductID != productID || target.AssetID != apiAssetID || target.RevisionID != revisionID ||
		target.Caller != (reviewdomain.Principal{Issuer: alice.Issuer, Subject: alice.Subject}) || target.CorrelationID == "" {
		t.Errorf("the use case was asked for %+v, want the revision of the path and the caller of the token", target)
	}

	// The list, and the cursor of its next page.
	position := reviewports.Position{At: createdAt, ID: reviewID}
	fake = &contractReviews{page: checks.ReviewPage{Items: []reviewdomain.Review{askedReview(), withdrawnReview()}, Next: &position}}
	s = newReviewService(t, fake)
	recorder = s.do(t, call{Method: http.MethodGet, Target: reviews + "?page_size=2", Token: alice.Subject})
	listed := members(t, recorder)
	token, _ := listed["next_page_token"].(string)
	if recorder.Code != http.StatusOK || !reflect.DeepEqual(listed["items"], []any{shownReview(nil), shownReview(conclusion)}) || token == "" ||
		recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("listed: %d %s", recorder.Code, recorder.Body)
	}
	if query := fake.listed[0]; query.PageSize != 2 || query.After != nil || query.RevisionID != revisionID || query.AssetID != apiAssetID {
		t.Errorf("the use case was asked for the page %+v", query)
	}
	fake.page = checks.ReviewPage{}
	recorder = s.do(t, call{Method: http.MethodGet, Target: reviews + "?page_token=" + token, Token: alice.Subject})
	if _, more := members(t, recorder)["next_page_token"]; recorder.Code != http.StatusOK || more ||
		fake.listed[1].After == nil || !fake.listed[1].After.At.Equal(createdAt) || fake.listed[1].After.ID != reviewID {
		t.Errorf("the page behind the cursor: %d %s, asked for %+v", recorder.Code, recorder.Body, fake.listed[1])
	}
	// A cursor belongs to its list: the reviews of this revision.
	other := revisions + "/" + sourceSnapshotID + "/reviews?page_token=" + token
	wantRefusal(t, s.do(t, call{Method: http.MethodGet, Target: other, Token: alice.Subject}), http.StatusBadRequest, CodePageTokenInvalid)
	if len(fake.listed) != 2 {
		t.Errorf("the use case was asked %d times, want the cursor of another list refused before it", len(fake.listed))
	}
}

// Nothing asks for, withdraws or decides a review any more, so the contract
// has no operation that does and the service no route: what was asked of
// those is refused before any use case is reached, as anything the contract
// does not declare is.
func TestAReviewIsNoLongerAskedForWithdrawnOrDecided(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/v1/products/{product_id}/assets/{api_asset_id}/revisions/{revision_id}/reviews",
		"/v1/products/{product_id}/assets/{api_asset_id}/revisions/{revision_id}/reviews/{review_id}",
	} {
		if item := contract.Paths.Find(path); item == nil || item.Post != nil || item.Get == nil {
			t.Errorf("%s is not the read it is and nothing else: %+v", path, item)
		}
	}
	for _, path := range []string{
		"/v1/products/{product_id}/assets/{api_asset_id}/revisions/{revision_id}/reviews/{review_id}/withdrawal",
		"/v1/products/{product_id}/assets/{api_asset_id}/revisions/{revision_id}/reviews/{review_id}/decision",
	} {
		if contract.Paths.Find(path) != nil {
			t.Errorf("the contract still declares %s", path)
		}
	}
	for _, name := range []string{"ReviewSubmission", "ReviewWithdrawal", "ReviewDecision"} {
		if contract.Components.Schemas[name] != nil {
			t.Errorf("the contract still declares %s, which nothing sends any more", name)
		}
	}

	for name, c := range map[string]struct {
		call   call
		status int
		code   string
	}{
		"a submission": {call{Method: http.MethodPost, Target: reviews, Token: alice.Subject,
			Body: `{"check_run_id":"` + checkRunID + `","scope":[{"audience":"partner","environment":"sandbox"}]}`},
			http.StatusMethodNotAllowed, CodeMethodNotAllowed},
		"a withdrawal": {call{Method: http.MethodPost, Target: reviews + "/" + reviewID + "/withdrawal", Token: alice.Subject, Body: `{}`},
			http.StatusNotFound, CodeRouteNotFound},
		"a decision": {call{Method: http.MethodPost, Target: reviews + "/" + reviewID + "/decision", Token: alice.Subject,
			Body: `{"outcome":"APPROVED","valid_until":"2027-02-09T10:00:00Z"}`},
			http.StatusNotFound, CodeRouteNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &contractReviews{review: approvedReview()}
			wantRefusal(t, newReviewService(t, fake).do(t, c.call), c.status, c.code)
			if len(fake.targets) != 0 || len(fake.listed) != 0 {
				t.Errorf("a use case was reached: %+v %+v", fake.targets, fake.listed)
			}
		})
	}
}

// What the service answers for a review satisfies the schema the contract
// declares for it, and that schema is as closed as the answer.
func TestTheContractStatesAReviewAsItIsAnswered(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	declared := contract.Components.Schemas["Review"]
	if declared == nil {
		t.Fatal("the contract declares no review")
	}
	again := approvedReview()
	again.Request.Supplementary = true
	for name, review := range map[string]reviewdomain.Review{
		"asked": askedReview(), "withdrawn": withdrawnReview(), "approved": approvedReview(), "rejected": rejectedReview(),
		"approved again": again,
	} {
		fake := &contractReviews{review: review}
		recorder := newReviewService(t, fake).do(t, call{Method: http.MethodGet, Target: reviews + "/" + reviewID, Token: alice.Subject})
		if err := declared.Value.VisitJSON(members(t, recorder)); err != nil {
			t.Fatalf("the contract refuses a review that was %s: %v\n%s", name, err, recorder.Body)
		}
	}
	recorder := newReviewService(t, &contractReviews{review: approvedReview()}).do(t,
		call{Method: http.MethodGet, Target: reviews + "/" + reviewID, Token: alice.Subject})
	without := func(member string) func(map[string]any) { return func(body map[string]any) { delete(body, member) } }
	conclusion := func(change func(map[string]any)) func(map[string]any) {
		return func(body map[string]any) { change(body["conclusion"].(map[string]any)) }
	}
	waiver := func(change func(map[string]any)) func(map[string]any) {
		return conclusion(func(c map[string]any) { change(c["waivers"].([]any)[0].(map[string]any)) })
	}
	refused := map[string]func(map[string]any){
		"a member the contract does not declare": func(body map[string]any) { body["decided_by"] = "bob" },
		"no review":                              without("review_id"),
		"no revision":                            without("revision_id"),
		"no check":                               without("check_run_id"),
		"no input digest":                        without("input_digest"),
		"no status":                              without("status"),
		"no word on whether it is supplementary": without("supplementary"),
		"a supplementary that is no yes or no":   func(body map[string]any) { body["supplementary"] = "yes" },
		"no scope":                               without("scope"),
		"nobody who asked":                       without("requested_by"),
		"no time it was asked at":                without("requested_at"),
		"a status there is not":                  func(body map[string]any) { body["status"] = "ESCALATED" },
		"a scope of nothing":                     func(body map[string]any) { body["scope"] = []any{} },
		"a target there is not": func(body map[string]any) {
			body["scope"] = []any{map[string]any{"audience": "customers", "environment": "sandbox"}}
		},
		"a conclusion without an outcome": conclusion(func(c map[string]any) { delete(c, "outcome") }),
		"a conclusion by nobody":          conclusion(func(c map[string]any) { delete(c, "concluded_by") }),
		"a conclusion at no time":         conclusion(func(c map[string]any) { delete(c, "concluded_at") }),
		"an outcome there is not":         conclusion(func(c map[string]any) { c["outcome"] = "ESCALATED" }),
		"a conclusion that says more":     conclusion(func(c map[string]any) { c["decided_with"] = "bob" }),
		"a time that is none":             conclusion(func(c map[string]any) { c["valid_until"] = "next year" }),
		"a waiver of no rule":             waiver(func(w map[string]any) { delete(w, "rule_id") }),
		"a waiver of what is no rule":     waiver(func(w map[string]any) { w["rule_id"] = "every rule" }),
		"a waiver without a reason":       waiver(func(w map[string]any) { delete(w, "reason") }),
		"a waiver that never ends":        waiver(func(w map[string]any) { delete(w, "expires_at") }),
		"a waiver that says more":         waiver(func(w map[string]any) { w["approved_by"] = "bob" }),
	}
	for name, change := range refused {
		t.Run(name, func(t *testing.T) {
			body := members(t, recorder)
			change(body)
			if err := declared.Value.VisitJSON(body); err == nil {
				t.Errorf("the contract accepts a review with %s: %v", name, body)
			}
		})
	}

	// A revision is recorded as the draft the registry records, and the
	// other statuses are where the revisions reviewed before stand: the
	// contract still answers them.
	var statuses []string
	for _, value := range contract.Components.Schemas["ContractRevision"].Value.Properties["status"].Value.Enum {
		status, _ := value.(string)
		statuses = append(statuses, status)
	}
	if want := []string{registrydomain.RevisionDraft, "IN_REVIEW", "APPROVED", "REJECTED", "WITHDRAWN"}; !reflect.DeepEqual(statuses, want) {
		t.Errorf("the contract declares the statuses %v of a revision, want %v", statuses, want)
	}
}

// What a read of a review refuses is a problem of its own code. A review
// that is not what was recorded is a failure of the service, logged for who
// operates it.
func TestWhatAReviewRefusesIsAProblemOfItsOwnCode(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
		detail string
	}{
		"a review nobody asked for":      {checks.ErrReviewNotFound, http.StatusNotFound, "REVIEW_NOT_FOUND", ""},
		"someone who holds no such role": {checks.ErrForbidden, http.StatusForbidden, CodeForbidden, "The caller holds no role in the workspace that allows this."},
		"a page the use case refuses":    {checks.ErrInvalidReviewPage, http.StatusBadRequest, CodeRequestInvalid, ""},
	}
	requests := []call{
		{Method: http.MethodGet, Target: reviews + "/" + reviewID, Token: alice.Subject},
		{Method: http.MethodGet, Target: reviews, Token: alice.Subject},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newReviewService(t, &contractReviews{err: c.err})
			for _, request := range requests {
				wantProblem(t, s.do(t, request), c.status, c.code, c.detail)
			}
		})
	}

	s := newReviewService(t, &contractReviews{err: fmt.Errorf("review %s: %w", reviewID, reviewdomain.ErrReviewDamaged)})
	for _, request := range requests {
		logged := s.log.Len()
		trace := wantProblem(t, s.do(t, request), http.StatusInternalServerError, CodeInternalError, "")
		if entry := s.log.String()[logged:]; !strings.Contains(entry, "review "+reviewID+": the stored review is not the one that was recorded") ||
			!strings.Contains(entry, "correlation_id="+trace) {
			t.Errorf("the log does not name the review and the cause under the identifier of the request %s:\n%s", trace, entry)
		}
	}
}
