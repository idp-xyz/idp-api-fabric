package http

import (
	"bytes"
	"context"
	"encoding/json"
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

// contractReviews stands in for the use cases of a review. It keeps what it
// was asked and answers what the test set. The use cases of a check are not
// reached by these tests.
type contractReviews struct {
	PolicyReview

	review reviewdomain.Review
	page   checks.ReviewPage
	err    error

	targets   []reviewports.Target
	asked     []checks.RequestReview
	withdrawn [][2]string
	decided   []reviewdomain.Decision
	read      []string
	listed    []checks.ListReviews
}

func (r *contractReviews) DecideReview(_ context.Context, target reviewports.Target, id string, decision reviewdomain.Decision) (reviewdomain.Review, error) {
	r.targets, r.read, r.decided = append(r.targets, target), append(r.read, id), append(r.decided, decision)
	return r.review, r.err
}

func (r *contractReviews) RequestReview(_ context.Context, target reviewports.Target, command checks.RequestReview) (reviewdomain.Review, error) {
	r.targets, r.asked = append(r.targets, target), append(r.asked, command)
	return r.review, r.err
}

func (r *contractReviews) WithdrawReview(_ context.Context, target reviewports.Target, id, reason string) (reviewdomain.Review, error) {
	r.targets, r.withdrawn = append(r.targets, target), append(r.withdrawn, [2]string{id, reason})
	return r.review, r.err
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

// decision is what a reviewer sends to approve the review as approvedReview
// has it, changed by the test.
func decision(change func(map[string]any)) string {
	decided := createdAt.Add(time.Hour)
	body := map[string]any{
		"outcome": "APPROVED", "reason": "reviewed with the partner team",
		"valid_until": decided.Add(90 * 24 * time.Hour).Format(time.RFC3339Nano),
		"waivers": []any{
			map[string]any{
				"rule_id": "F04-GOV-003", "reason": "generated clients are not used yet",
				"expires_at": decided.Add(30 * 24 * time.Hour).Format(time.RFC3339Nano),
			},
			map[string]any{
				"rule_id": "F04-GOV-002", "reason": "the title is settled next sprint",
				"compensating_control": "the portal shows the asset name",
				"expires_at":           decided.Add(60 * 24 * time.Hour).Format(time.RFC3339Nano),
			},
		},
	}
	if change != nil {
		change(body)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func shownReview(conclusion map[string]any) map[string]any {
	shown := map[string]any{
		"review_id": reviewID, "product_id": productID, "api_asset_id": apiAssetID, "revision_id": revisionID,
		"check_run_id": checkRunID, "input_digest": "sha256:" + fmt.Sprintf("%064x", 2), "status": "IN_REVIEW",
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

func submission(change func(map[string]any)) string {
	body := map[string]any{
		"check_run_id": checkRunID,
		"scope": []any{
			map[string]any{"audience": "partner", "environment": "sandbox"},
			map[string]any{"audience": "internal", "environment": "production"},
		},
		"note": "ready for partners",
	}
	if change != nil {
		change(body)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// What an author asks a review with reaches the use case as it was said: the
// check, the targets in the order they were named, the note, for the
// revision of the path and the caller of the token. The answer is the
// review as the contract states it, and where to read it.
func TestAReviewIsAskedAndAnsweredAsTheContractStatesIt(t *testing.T) {
	fake := &contractReviews{review: askedReview()}
	s := newReviewService(t, fake)
	recorder := s.do(t, call{Method: http.MethodPost, Target: reviews, Token: alice.Subject, Body: submission(nil)})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", recorder.Code, recorder.Body)
	}
	want := checks.RequestReview{
		CheckRunID: checkRunID, Note: "ready for partners",
		Scope: []reviewdomain.Target{{Audience: "partner", Environment: "sandbox"}, {Audience: "internal", Environment: "production"}},
	}
	if len(fake.asked) != 1 || !reflect.DeepEqual(fake.asked[0], want) {
		t.Errorf("the use case was asked %+v, want %+v", fake.asked, want)
	}
	target := fake.targets[0]
	if target.ProductID != productID || target.AssetID != apiAssetID || target.RevisionID != revisionID ||
		target.Caller != (reviewdomain.Principal{Issuer: alice.Issuer, Subject: alice.Subject}) || target.CorrelationID == "" {
		t.Errorf("the use case was asked for %+v, want the revision of the path and the caller of the token", target)
	}
	if got := members(t, recorder); !reflect.DeepEqual(got, shownReview(nil)) {
		t.Errorf("answered\n %v\nwant\n %v", got, shownReview(nil))
	}
	if got := recorder.Header().Get("Location"); got != reviews+"/"+reviewID {
		t.Errorf("Location %q, want the review", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", got)
	}

	// A review without a note is asked and answered without one.
	silent := askedReview()
	silent.Request.Note = ""
	fake = &contractReviews{review: silent}
	recorder = newReviewService(t, fake).do(t, call{Method: http.MethodPost, Target: reviews, Token: alice.Subject,
		Body: submission(func(body map[string]any) { delete(body, "note") })})
	if _, said := members(t, recorder)["note"]; recorder.Code != http.StatusCreated || said || fake.asked[0].Note != "" {
		t.Errorf("a review without a note: %d %s, asked %+v", recorder.Code, recorder.Body, fake.asked)
	}

	// What the contract does not state is refused before the use case.
	long := strings.Repeat("r", 501)
	refused := map[string]string{
		"no body":                       "",
		"no check":                      submission(func(body map[string]any) { delete(body, "check_run_id") }),
		"a check that is no identifier": submission(func(body map[string]any) { body["check_run_id"] = "latest" }),
		"no scope":                      submission(func(body map[string]any) { delete(body, "scope") }),
		"a scope of nothing":            submission(func(body map[string]any) { body["scope"] = []any{} }),
		"a target named twice": submission(func(body map[string]any) {
			body["scope"] = []any{map[string]any{"audience": "partner", "environment": "sandbox"}, map[string]any{"environment": "sandbox", "audience": "partner"}}
		}),
		"an audience there is not": submission(func(body map[string]any) {
			body["scope"] = []any{map[string]any{"audience": "customers", "environment": "sandbox"}}
		}),
		"an environment there is not": submission(func(body map[string]any) {
			body["scope"] = []any{map[string]any{"audience": "partner", "environment": "staging"}}
		}),
		"a target without an environment": submission(func(body map[string]any) { body["scope"] = []any{map[string]any{"audience": "partner"}} }),
		"a target that says more": submission(func(body map[string]any) {
			body["scope"] = []any{map[string]any{"audience": "partner", "environment": "sandbox", "portal": "cargopai"}}
		}),
		"a note of nothing":           submission(func(body map[string]any) { body["note"] = "" }),
		"a note longer than allowed":  submission(func(body map[string]any) { body["note"] = long }),
		"a note of two lines":         submission(func(body map[string]any) { body["note"] = "ready\nfor partners" }),
		"a member nobody declared":    submission(func(body map[string]any) { body["reviewer"] = "bob" }),
		"a decision with the request": submission(func(body map[string]any) { body["outcome"] = "APPROVED" }),
	}
	for name, body := range refused {
		t.Run(name, func(t *testing.T) {
			fake := &contractReviews{review: askedReview()}
			recorder := newReviewService(t, fake).do(t, call{Method: http.MethodPost, Target: reviews, Token: alice.Subject, Body: body})
			wantRefusal(t, recorder, http.StatusBadRequest, CodeRequestInvalid)
			if len(fake.asked) != 0 {
				t.Errorf("the use case was asked %+v", fake.asked)
			}
		})
	}
}

// A withdrawal says which review and, if it gives one, why. A review is read
// by its identifier and listed page by page, under the cursor of its own
// list.
func TestAReviewIsWithdrawnReadAndListedAsTheContractStatesIt(t *testing.T) {
	conclusion := map[string]any{
		"outcome": "WITHDRAWN", "reason": "the wrong baseline",
		"concluded_by": map[string]any{"issuer": alice.Issuer, "subject": alice.Subject},
		"concluded_at": createdAt.Add(time.Hour).Format(time.RFC3339Nano),
	}
	withdrawal := reviews + "/" + reviewID + "/withdrawal"

	fake := &contractReviews{review: withdrawnReview()}
	s := newReviewService(t, fake)
	recorder := s.do(t, call{Method: http.MethodPost, Target: withdrawal, Token: alice.Subject, Body: `{"reason":"the wrong baseline"}`})
	if recorder.Code != http.StatusOK || !reflect.DeepEqual(members(t, recorder), shownReview(conclusion)) {
		t.Fatalf("withdrawn: %d\n %s\nwant\n %v", recorder.Code, recorder.Body, shownReview(conclusion))
	}
	if recorder.Header().Get("Location") != "" || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("the headers of a withdrawal: %v", recorder.Header())
	}
	recorder = s.do(t, call{Method: http.MethodPost, Target: withdrawal, Token: alice.Subject, Body: `{}`})
	if recorder.Code != http.StatusOK || !reflect.DeepEqual(fake.withdrawn, [][2]string{{reviewID, "the wrong baseline"}, {reviewID, ""}}) {
		t.Errorf("the use case was asked to withdraw %v (status %d), want the review with its reason and then without one", fake.withdrawn, recorder.Code)
	}
	for name, body := range map[string]string{
		"no body":                        "",
		"a reason of nothing":            `{"reason":""}`,
		"a reason longer than allowed":   `{"reason":"` + strings.Repeat("r", 1001) + `"}`,
		"a member nobody declared":       `{"reason":"wrong","outcome":"REJECTED"}`,
		"a review that is no identifier": "-",
	} {
		t.Run(name, func(t *testing.T) {
			fake := &contractReviews{review: withdrawnReview()}
			c := call{Method: http.MethodPost, Target: withdrawal, Token: alice.Subject, Body: body}
			if body == "-" {
				c.Target, c.Body = reviews+"/latest/withdrawal", `{}`
			}
			wantRefusal(t, newReviewService(t, fake).do(t, c), http.StatusBadRequest, CodeRequestInvalid)
			if len(fake.withdrawn) != 0 {
				t.Errorf("the use case was asked to withdraw %v", fake.withdrawn)
			}
		})
	}

	fake = &contractReviews{review: withdrawnReview()}
	s = newReviewService(t, fake)
	recorder = s.do(t, call{Method: http.MethodGet, Target: reviews + "/" + reviewID, Token: alice.Subject})
	if recorder.Code != http.StatusOK || !reflect.DeepEqual(members(t, recorder), shownReview(conclusion)) ||
		!reflect.DeepEqual(fake.read, []string{reviewID}) || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("read: %d %s, asked for %v", recorder.Code, recorder.Body, fake.read)
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

// What a reviewer decides with reaches the use case as it was said: the
// outcome, the reason, the time an approval holds until and its waivers in
// the order they were named, for the review of the path and the caller of
// the token. The answer is the review with its conclusion.
func TestAReviewIsDecidedAndAnsweredAsTheContractStatesIt(t *testing.T) {
	deciding := reviews + "/" + reviewID + "/decision"
	decided := createdAt.Add(time.Hour)

	fake := &contractReviews{review: approvedReview()}
	s := newReviewService(t, fake)
	recorder := s.do(t, call{Method: http.MethodPost, Target: deciding, Token: alice.Subject, Body: decision(nil)})
	if recorder.Code != http.StatusOK || !reflect.DeepEqual(members(t, recorder), shownReview(shownApproval())) {
		t.Fatalf("approved: %d\n %s\nwant\n %v", recorder.Code, recorder.Body, shownReview(shownApproval()))
	}
	if recorder.Header().Get("Location") != "" || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("the headers of a decision: %v", recorder.Header())
	}
	want := reviewdomain.Decision{
		Outcome: reviewdomain.ReviewApproved, Reason: "reviewed with the partner team", ValidUntil: decided.Add(90 * 24 * time.Hour),
		Waivers: []reviewdomain.Waiver{
			{RuleID: "F04-GOV-003", Reason: "generated clients are not used yet", ExpiresAt: decided.Add(30 * 24 * time.Hour)},
			{RuleID: "F04-GOV-002", Reason: "the title is settled next sprint", CompensatingControl: "the portal shows the asset name",
				ExpiresAt: decided.Add(60 * 24 * time.Hour)},
		},
	}
	if len(fake.decided) != 1 || !sameDecision(fake.decided[0], want) || !reflect.DeepEqual(fake.read, []string{reviewID}) {
		t.Errorf("the use case was asked to decide %v with %+v, want %s with %+v", fake.read, fake.decided, reviewID, want)
	}
	target := fake.targets[0]
	if target.ProductID != productID || target.AssetID != apiAssetID || target.RevisionID != revisionID ||
		target.Caller != (reviewdomain.Principal{Issuer: alice.Issuer, Subject: alice.Subject}) || target.CorrelationID == "" {
		t.Errorf("the use case was asked for %+v, want the revision of the path and the caller of the token", target)
	}

	// An approval of a check nothing failed in says until when and no more.
	plain := approvedReview()
	plain.Conclusion.Reason, plain.Conclusion.Waivers = "", nil
	fake = &contractReviews{review: plain}
	recorder = newReviewService(t, fake).do(t, call{Method: http.MethodPost, Target: deciding, Token: alice.Subject,
		Body: decision(func(body map[string]any) { delete(body, "reason"); delete(body, "waivers") })})
	shown, _ := members(t, recorder)["conclusion"].(map[string]any)
	_, reason := shown["reason"]
	_, waivers := shown["waivers"]
	if recorder.Code != http.StatusOK || reason || waivers || shown["valid_until"] == nil || len(fake.decided) != 1 ||
		!sameDecision(fake.decided[0], reviewdomain.Decision{Outcome: reviewdomain.ReviewApproved, ValidUntil: want.ValidUntil}) {
		t.Errorf("an approval without a reason and without waivers: %d %s, asked %+v", recorder.Code, recorder.Body, fake.decided)
	}

	fake = &contractReviews{review: rejectedReview()}
	recorder = newReviewService(t, fake).do(t, call{Method: http.MethodPost, Target: deciding, Token: alice.Subject,
		Body: `{"outcome":"REJECTED","reason":"every operation needs its operationId"}`})
	if recorder.Code != http.StatusOK || !reflect.DeepEqual(members(t, recorder), shownReview(shownRejection())) {
		t.Fatalf("rejected: %d\n %s\nwant\n %v", recorder.Code, recorder.Body, shownReview(shownRejection()))
	}
	if rejection := (reviewdomain.Decision{Outcome: reviewdomain.ReviewRejected, Reason: "every operation needs its operationId"}); len(fake.decided) != 1 ||
		!sameDecision(fake.decided[0], rejection) {
		t.Errorf("the use case was asked to decide with %+v, want %+v", fake.decided, rejection)
	}

	// What the contract does not state is refused before the use case.
	firstWaiver := func(change func(map[string]any)) func(map[string]any) {
		return func(body map[string]any) { change(body["waivers"].([]any)[0].(map[string]any)) }
	}
	many := make([]any, 101)
	for i := range many {
		many[i] = map[string]any{"rule_id": fmt.Sprintf("F04-GOV-%03d", i), "reason": "accepted", "expires_at": decided.Format(time.RFC3339)}
	}
	refused := map[string]string{
		"no body":                             "",
		"no outcome":                          decision(func(body map[string]any) { delete(body, "outcome") }),
		"an outcome a reviewer does not give": decision(func(body map[string]any) { body["outcome"] = "WITHDRAWN" }),
		"an outcome there is not":             decision(func(body map[string]any) { body["outcome"] = "approved" }),
		"a reason of nothing":                 decision(func(body map[string]any) { body["reason"] = "" }),
		"a reason longer than allowed":        decision(func(body map[string]any) { body["reason"] = strings.Repeat("r", 1001) }),
		"a reason of two lines":               decision(func(body map[string]any) { body["reason"] = "reviewed\nwith the partner team" }),
		"a time that is none":                 decision(func(body map[string]any) { body["valid_until"] = "next year" }),
		"a time without its zone":             decision(func(body map[string]any) { body["valid_until"] = "2027-02-09T10:00:00" }),
		"waivers that are no list":            decision(func(body map[string]any) { body["waivers"] = "F04-GOV-003" }),
		"more waivers than there are rules":   decision(func(body map[string]any) { body["waivers"] = many }),
		"a waiver of no rule":                 decision(firstWaiver(func(w map[string]any) { delete(w, "rule_id") })),
		"a waiver of what is no rule":         decision(firstWaiver(func(w map[string]any) { w["rule_id"] = "every rule" })),
		"a waiver without a reason":           decision(firstWaiver(func(w map[string]any) { delete(w, "reason") })),
		"a waiver whose reason is nothing":    decision(firstWaiver(func(w map[string]any) { w["reason"] = "" })),
		"a waiver that never ends":            decision(firstWaiver(func(w map[string]any) { delete(w, "expires_at") })),
		"a waiver whose end is no time":       decision(firstWaiver(func(w map[string]any) { w["expires_at"] = "never" })),
		"a waiver whose control is nothing":   decision(firstWaiver(func(w map[string]any) { w["compensating_control"] = "" })),
		"a waiver that says more":             decision(firstWaiver(func(w map[string]any) { w["approved_by"] = "bob" })),
		"a member nobody declared":            decision(func(body map[string]any) { body["decided_by"] = "bob" }),
		"a scope with the decision": decision(func(body map[string]any) {
			body["scope"] = []any{map[string]any{"audience": "public", "environment": "production"}}
		}),
		"a review that is no identifier": "-",
	}
	for name, body := range refused {
		t.Run(name, func(t *testing.T) {
			fake := &contractReviews{review: approvedReview()}
			c := call{Method: http.MethodPost, Target: deciding, Token: alice.Subject, Body: body}
			if body == "-" {
				c.Target, c.Body = reviews+"/latest/decision", decision(nil)
			}
			wantRefusal(t, newReviewService(t, fake).do(t, c), http.StatusBadRequest, CodeRequestInvalid)
			if len(fake.decided) != 0 {
				t.Errorf("the use case was asked to decide with %+v", fake.decided)
			}
		})
	}
}

// sameDecision compares two decisions, their times as instants.
func sameDecision(got, want reviewdomain.Decision) bool {
	if got.Outcome != want.Outcome || got.Reason != want.Reason || !got.ValidUntil.Equal(want.ValidUntil) || len(got.Waivers) != len(want.Waivers) {
		return false
	}
	for i, waiver := range got.Waivers {
		other := want.Waivers[i]
		if waiver.RuleID != other.RuleID || waiver.Reason != other.Reason || waiver.CompensatingControl != other.CompensatingControl ||
			!waiver.ExpiresAt.Equal(other.ExpiresAt) {
			return false
		}
	}
	return true
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
	for name, review := range map[string]reviewdomain.Review{
		"asked": askedReview(), "withdrawn": withdrawnReview(), "approved": approvedReview(), "rejected": rejectedReview(),
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

	// The statuses of a revision are the ones the registry has.
	var statuses []string
	for _, value := range contract.Components.Schemas["ContractRevision"].Value.Properties["status"].Value.Enum {
		status, _ := value.(string)
		statuses = append(statuses, status)
	}
	if want := []string{registrydomain.RevisionDraft, registrydomain.RevisionInReview, registrydomain.RevisionApproved,
		registrydomain.RevisionRejected, registrydomain.RevisionWithdrawn}; !reflect.DeepEqual(statuses, want) {
		t.Errorf("the contract declares the statuses %v of a revision, the registry %v", statuses, want)
	}
}

// What a review refuses is a problem of its own code. A review that is not
// what was recorded is a failure of the service, logged for who operates it.
func TestWhatAReviewRefusesIsAProblemOfItsOwnCode(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
		detail string
	}{
		"a review nobody asked for":      {checks.ErrReviewNotFound, http.StatusNotFound, "REVIEW_NOT_FOUND", ""},
		"a check nobody recorded":        {checks.ErrCheckRunNotFound, http.StatusNotFound, "CHECK_RUN_NOT_FOUND", ""},
		"someone who holds no such role": {checks.ErrForbidden, http.StatusForbidden, CodeForbidden, "The caller holds no role in the workspace that allows this."},
		"someone who did not ask for it": {reviewdomain.ErrNotRequester, http.StatusForbidden, CodeForbidden, "A review is withdrawn by whoever asked for it."},
		"someone who had a part in it": {reviewdomain.ErrNotIndependent, http.StatusForbidden, "REVIEWER_NOT_INDEPENDENT",
			"Whoever recorded the revision, ran its check or asked for the review does not decide on it."},
		"a waiver of a rule nothing waives": {fmt.Errorf("%w: F04-GOV-004", reviewdomain.ErrWaiverForbidden), http.StatusUnprocessableEntity, "WAIVER_FORBIDDEN",
			"A rule that nothing waives cannot be waived."},
		"rules that are not waived": {fmt.Errorf("%w: F04-GOV-002, F04-GOV-003", reviewdomain.ErrWaiverMissing), http.StatusConflict, "WAIVER_REQUIRED",
			"Rules failed or could not be judged in the check of the review. An approval waives each of them by name."},
		"a suspended product": {checks.ErrProductSuspended, http.StatusConflict, CodeProductNotActive, "The product is suspended and takes no new work."},
		"a check of another policy": {reviewdomain.ErrCheckStale, http.StatusConflict, "POLICY_STALE",
			"The check was not made under the policy and by the tool versions a check is made with now. Run the check again."},
		"rules nothing waives that failed": {fmt.Errorf("%w: F04-GOV-004, F04-GOV-010", reviewdomain.ErrReviewBlocked), http.StatusConflict, "REVIEW_BLOCKED",
			"In the check, rules that nothing waives failed or could not be judged. No decision approves such a revision."},
		"a review that is concluded": {reviewdomain.ErrReviewConcluded, http.StatusConflict, "REVIEW_CONCLUDED", "The review was concluded already."},
		"a revision at another status": {fmt.Errorf("%w: it is APPROVED", registrydomain.ErrStatusConflict), http.StatusConflict, "REVISION_STATUS_CONFLICT",
			"The revision is not at a status this starts from."},
		"a review the model refuses":  {fmt.Errorf("%w: a target is named twice", reviewdomain.ErrInvalidReview), http.StatusUnprocessableEntity, CodeRequestUnprocessable, ""},
		"a page the use case refuses": {checks.ErrInvalidReviewPage, http.StatusBadRequest, CodeRequestInvalid, ""},
	}
	requests := []call{
		{Method: http.MethodPost, Target: reviews, Token: alice.Subject, Body: submission(nil)},
		{Method: http.MethodPost, Target: reviews + "/" + reviewID + "/withdrawal", Token: alice.Subject, Body: `{}`},
		{Method: http.MethodPost, Target: reviews + "/" + reviewID + "/decision", Token: alice.Subject, Body: decision(nil)},
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
