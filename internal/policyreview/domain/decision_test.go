package domain_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

var (
	rita    = domain.Principal{Issuer: "https://issuer.example", Subject: "rita"}
	dave    = domain.Principal{Issuer: "https://issuer.example", Subject: "dave"}
	decided = asked.Add(2 * time.Hour)
	// The parties of the reviews of these tests: dave recorded the
	// revision, carol ran the check and erin asked for the review.
	involved = []domain.Principal{dave, carol, erin}
)

// reviewOf is an open review of the revision a check is of, asked by erin.
func reviewOf(t *testing.T, run domain.CheckRun) domain.Review {
	t.Helper()
	request, err := domain.RequestReview("review-1", run, reviewPolicy(t), current, []domain.Target{partner}, "", erin, asked)
	if err != nil {
		t.Fatal(err)
	}
	return domain.Review{Request: request}
}

func waiver(ruleID string) domain.Waiver {
	return domain.Waiver{RuleID: ruleID, Reason: "accepted for the pilot", ExpiresAt: decided.Add(30 * 24 * time.Hour)}
}

func approval(waivers ...domain.Waiver) domain.Decision {
	return domain.Decision{Outcome: domain.ReviewApproved, ValidUntil: decided.Add(90 * 24 * time.Hour), Waivers: waivers}
}

// A reviewer approves a revision for a time, on a check a decision can rest
// on, and waives by name every rule that failed or could not be judged. A
// rule that warns and one that was not run need no waiver.
func TestAnApprovalHoldsForATimeAndWaivesWhatDidNotPass(t *testing.T) {
	policy := reviewPolicy(t)
	zone := time.FixedZone("UTC+8", 8*60*60)

	t.Run("a check in which nothing stands in the way", func(t *testing.T) {
		review := reviewOf(t, checked(t, found("R-RULE-003")))
		decision := domain.Decision{Outcome: domain.ReviewApproved, Reason: "reviewed with the partner team", ValidUntil: decided.Add(90 * 24 * time.Hour).In(zone).Add(456 * time.Nanosecond)}
		conclusion, err := domain.Decide(review, checked(t, found("R-RULE-003")), policy, current, decision, involved, rita, decided.In(zone).Add(123*time.Nanosecond))
		want := domain.Conclusion{
			ReviewID: "review-1", Outcome: domain.ReviewApproved, Reason: "reviewed with the partner team",
			ConcludedBy: rita, ConcludedAt: decided, ValidUntil: decided.Add(90 * 24 * time.Hour),
		}
		if err != nil || !reflect.DeepEqual(conclusion, want) || conclusion.ConcludedAt.Location() != time.UTC || conclusion.ValidUntil.Location() != time.UTC {
			t.Fatalf("approved as %+v, want %+v in UTC: %v", conclusion, want, err)
		}
		if status := (domain.Review{Request: review.Request, Conclusion: &conclusion}).Status(); status != "APPROVED" {
			t.Errorf("an approved review is %s", status)
		}
		if recorded, err := domain.RecordedReview(domain.Review{Request: review.Request, Conclusion: &conclusion}); err != nil || recorded.Conclusion == nil {
			t.Errorf("the approval is not read back as one: %v", err)
		}
	})

	t.Run("rules that did not pass, waived out of order", func(t *testing.T) {
		run := checked(t, found("R-RULE-002"), undecided("R-RULE-003"))
		late := waiver("R-RULE-003")
		late.CompensatingControl, late.ExpiresAt = "the gateway rejects the old path", approval().ValidUntil.In(zone)
		decision := approval(late, waiver("R-RULE-002"))
		conclusion, err := domain.Decide(reviewOf(t, run), run, policy, current, decision, involved, rita, decided)
		want := []domain.Waiver{waiver("R-RULE-002"), {
			RuleID: "R-RULE-003", Reason: "accepted for the pilot", CompensatingControl: "the gateway rejects the old path", ExpiresAt: approval().ValidUntil,
		}}
		if err != nil || !reflect.DeepEqual(conclusion.Waivers, want) || conclusion.Waivers[1].ExpiresAt.Location() != time.UTC {
			t.Fatalf("waived %+v, want %+v in the order of the rules: %v", conclusion.Waivers, want, err)
		}
		if decision.Waivers[0].RuleID != "R-RULE-003" {
			t.Error("the waivers that were handed over were reordered")
		}
		review := domain.Review{Request: reviewOf(t, run).Request, Conclusion: &conclusion}
		if recorded, err := domain.RecordedReview(review); err != nil || !reflect.DeepEqual(recorded, review) {
			t.Errorf("the approval with its waivers is read back as %+v: %v", recorded, err)
		}
	})

	longest := approval()
	longest.ValidUntil = decided.Add(domain.MaxApprovalValidity)
	if _, err := domain.Decide(reviewOf(t, checked(t)), checked(t), policy, current, longest, involved, rita, decided); err != nil {
		t.Errorf("an approval for the longest time there is: %v", err)
	}

	blockedRun := checked(t, found("R-RULE-001"))
	blocked := domain.Review{Request: domain.ReviewRequest{
		ID: "review-1", Revision: blockedRun.Revision, CheckRunID: blockedRun.ID, InputDigest: blockedRun.InputDigest,
		Scope: []domain.Target{partner}, RequestedBy: erin, RequestedAt: asked,
	}}
	change := func(of domain.Waiver, how func(*domain.Waiver)) domain.Waiver { how(&of); return of }
	failing := checked(t, found("R-RULE-002"))
	refused := map[string]struct {
		run      domain.CheckRun
		review   *domain.Review
		policy   *domain.Policy
		tools    []domain.Tool
		decision domain.Decision
		want     error
		names    string
	}{
		"a check under another policy":              {run: checked(t), policy: ptr(teamPolicy(t)), decision: approval(), want: domain.ErrCheckStale},
		"a check by another version of the tool":    {run: checked(t), tools: []domain.Tool{{Name: linter.Name, Version: "2"}}, decision: approval(), want: domain.ErrCheckStale},
		"a rule nothing waives that failed":         {run: blockedRun, review: &blocked, decision: approval(), want: domain.ErrReviewBlocked, names: "R-RULE-001"},
		"a rule nothing waives that failed, waived": {run: blockedRun, review: &blocked, decision: approval(waiver("R-RULE-001")), want: domain.ErrWaiverForbidden, names: "R-RULE-001"},
		"a rule nothing waives that passed, waived": {run: failing, decision: approval(waiver("R-RULE-001"), waiver("R-RULE-002")), want: domain.ErrWaiverForbidden, names: "R-RULE-001"},
		"a rule that failed and is not waived":      {run: failing, decision: approval(), want: domain.ErrWaiverMissing, names: "R-RULE-002"},
		"one of two rules left unwaived":            {run: checked(t, found("R-RULE-002"), undecided("R-RULE-003")), decision: approval(waiver("R-RULE-002")), want: domain.ErrWaiverMissing, names: "R-RULE-003"},
		"a waiver of a rule that passed":            {run: failing, decision: approval(waiver("R-RULE-002"), waiver("R-RULE-003")), want: domain.ErrInvalidReview},
		"a waiver of a rule that warns":             {run: checked(t, found("R-RULE-003")), decision: approval(waiver("R-RULE-003")), want: domain.ErrInvalidReview},
		"a waiver of a rule that was not run":       {run: checked(t), decision: approval(waiver("R-RULE-004")), want: domain.ErrInvalidReview},
		"a waiver of a rule the policy has not":     {run: failing, decision: approval(waiver("R-RULE-002"), waiver("R-RULE-009")), want: domain.ErrInvalidReview},
		"a rule waived twice":                       {run: failing, decision: approval(waiver("R-RULE-002"), waiver("R-RULE-002")), want: domain.ErrInvalidReview},
		"a waiver without a reason":                 {run: failing, decision: approval(change(waiver("R-RULE-002"), func(w *domain.Waiver) { w.Reason = "" })), want: domain.ErrInvalidReview},
		"a waiver whose reason is two lines":        {run: failing, decision: approval(change(waiver("R-RULE-002"), func(w *domain.Waiver) { w.Reason = "accepted\nfor now" })), want: domain.ErrInvalidReview},
		"a waiver whose control is two lines":       {run: failing, decision: approval(change(waiver("R-RULE-002"), func(w *domain.Waiver) { w.CompensatingControl = "a\nb" })), want: domain.ErrInvalidReview},
		"a waiver that ended already":               {run: failing, decision: approval(change(waiver("R-RULE-002"), func(w *domain.Waiver) { w.ExpiresAt = decided })), want: domain.ErrInvalidReview},
		"a waiver until no time":                    {run: failing, decision: approval(change(waiver("R-RULE-002"), func(w *domain.Waiver) { w.ExpiresAt = time.Time{} })), want: domain.ErrInvalidReview},
		"a waiver that outlives the approval":       {run: failing, decision: approval(change(waiver("R-RULE-002"), func(w *domain.Waiver) { w.ExpiresAt = approval().ValidUntil.Add(time.Second) })), want: domain.ErrInvalidReview},
		"an approval until no time":                 {run: checked(t), decision: domain.Decision{Outcome: domain.ReviewApproved}, want: domain.ErrInvalidReview},
		"an approval that ended already":            {run: checked(t), decision: domain.Decision{Outcome: domain.ReviewApproved, ValidUntil: decided}, want: domain.ErrInvalidReview},
		"an approval for more than a year":          {run: checked(t), decision: domain.Decision{Outcome: domain.ReviewApproved, ValidUntil: decided.Add(domain.MaxApprovalValidity + time.Second)}, want: domain.ErrInvalidReview},
		"an approval whose reason is two lines":     {run: checked(t), decision: domain.Decision{Outcome: domain.ReviewApproved, Reason: "fine\nreally", ValidUntil: approval().ValidUntil}, want: domain.ErrInvalidReview},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			review, held, tools := reviewOf(t, checked(t)), policy, current
			if c.review != nil {
				review = *c.review
			} else {
				review.Request.CheckRunID, review.Request.InputDigest = c.run.ID, c.run.InputDigest
			}
			if c.policy != nil {
				held = *c.policy
			}
			if c.tools != nil {
				tools = c.tools
			}
			conclusion, err := domain.Decide(review, c.run, held, tools, c.decision, involved, rita, decided)
			if !errors.Is(err, c.want) || !reflect.DeepEqual(conclusion, domain.Conclusion{}) {
				t.Fatalf("got %+v and %v, want nothing and %v", conclusion, err, c.want)
			}
			if c.names != "" && !strings.HasSuffix(err.Error(), ": "+c.names) {
				t.Errorf("the refusal is %q, want it to name %s and no other rule", err, c.names)
			}
		})
	}
}

func ptr[T any](value T) *T { return &value }

// A rejection says why and nothing else, and it needs no current check:
// what is rejected is not relied on.
func TestARejectionSaysWhy(t *testing.T) {
	review := reviewOf(t, checked(t))
	conclusion, err := domain.Decide(review, checked(t), teamPolicy(t), nil,
		domain.Decision{Outcome: domain.ReviewRejected, Reason: "the 400 response lost its problem body"}, involved, rita, decided)
	want := domain.Conclusion{
		ReviewID: "review-1", Outcome: domain.ReviewRejected, Reason: "the 400 response lost its problem body", ConcludedBy: rita, ConcludedAt: decided,
	}
	if err != nil || !reflect.DeepEqual(conclusion, want) {
		t.Fatalf("rejected as %+v, want %+v: %v", conclusion, want, err)
	}
	concluded := domain.Review{Request: review.Request, Conclusion: &conclusion}
	if recorded, err := domain.RecordedReview(concluded); err != nil || concluded.Status() != "REJECTED" || !reflect.DeepEqual(recorded, concluded) {
		t.Errorf("the rejection is read back as %+v, %s: %v", recorded, concluded.Status(), err)
	}
	// A clock that was set back does not make a decision precede its request.
	if early, err := domain.Decide(review, checked(t), reviewPolicy(t), current,
		domain.Decision{Outcome: domain.ReviewRejected, Reason: "not yet"}, involved, rita, asked.Add(-time.Hour)); err != nil || !early.ConcludedAt.Equal(asked) {
		t.Errorf("decided at %v with the clock set back, want the instant the review was asked for: %v", early.ConcludedAt, err)
	}

	for name, decision := range map[string]domain.Decision{
		"no reason":                    {Outcome: domain.ReviewRejected},
		"a reason of two lines":        {Outcome: domain.ReviewRejected, Reason: "not\nyet"},
		"a reason longer than allowed": {Outcome: domain.ReviewRejected, Reason: strings.Repeat("r", domain.MaxReviewReason+1)},
		"a time it holds until":        {Outcome: domain.ReviewRejected, Reason: "not yet", ValidUntil: decided.Add(time.Hour)},
		"a waiver":                     {Outcome: domain.ReviewRejected, Reason: "not yet", Waivers: []domain.Waiver{waiver("R-RULE-002")}},
		"a withdrawal by a reviewer":   {Outcome: domain.ReviewWithdrawn, Reason: "not yet"},
		"no outcome":                   {Reason: "not yet"},
		"an outcome there is not":      {Outcome: "ESCALATED", Reason: "not yet"},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := domain.Decide(review, checked(t), reviewPolicy(t), current, decision, involved, rita, decided); !errors.Is(err, domain.ErrInvalidReview) || !reflect.DeepEqual(got, domain.Conclusion{}) {
				t.Errorf("got %+v and %v, want nothing and %v", got, err, domain.ErrInvalidReview)
			}
		})
	}
}

// Whoever decides on a review had no part in what is reviewed, whatever the
// decision: who recorded the revision, who ran the check and who asked for
// the review decide nothing about it. A review is decided once, on the check
// it rests on, by someone who says who they are.
func TestAReviewIsDecidedOnceByWhoHadNoPartInIt(t *testing.T) {
	review := reviewOf(t, checked(t))
	decisions := map[string]domain.Decision{
		"an approval":                 approval(),
		"a rejection":                 {Outcome: domain.ReviewRejected, Reason: "not yet"},
		"no decision":                 {},
		"an approval that is not one": {Outcome: domain.ReviewApproved},
	}
	for what, decision := range decisions {
		for who, party := range map[string]domain.Principal{"who recorded the revision": dave, "who ran the check": carol, "who asked for the review": erin} {
			if got, err := domain.Decide(review, checked(t), reviewPolicy(t), current, decision, involved, party, decided); !errors.Is(err, domain.ErrNotIndependent) || !reflect.DeepEqual(got, domain.Conclusion{}) {
				t.Errorf("%s by %s: got %+v and %v, want %v", what, who, got, err, domain.ErrNotIndependent)
			}
		}
	}
	// The same subject under another issuer is someone else.
	stranger := domain.Principal{Issuer: "https://other.example", Subject: "carol"}
	if _, err := domain.Decide(review, checked(t), reviewPolicy(t), current, approval(), involved, stranger, decided); err != nil {
		t.Errorf("a decision by the namesake of a party under another issuer: %v", err)
	}

	withdrawn, err := domain.Withdraw(review, erin, "", asked.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	concluded := domain.Review{Request: review.Request, Conclusion: &withdrawn}
	other := checked(t)
	other.ID = "check-2"
	misread := checked(t)
	misread.InputDigest = digestOf("another closure")
	for name, c := range map[string]struct {
		review domain.Review
		run    domain.CheckRun
		by     domain.Principal
		want   error
	}{
		"a review that is concluded":             {concluded, checked(t), rita, domain.ErrReviewConcluded},
		"a concluded review, by a party":         {concluded, checked(t), erin, domain.ErrReviewConcluded},
		"another check than the review rests on": {review, other, rita, domain.ErrInvalidReview},
		"a check that read something else":       {review, misread, rita, domain.ErrInvalidReview},
		"nobody who decides":                     {review, checked(t), domain.Principal{Issuer: "https://issuer.example"}, domain.ErrInvalidReview},
	} {
		t.Run(name, func(t *testing.T) {
			for _, decision := range []domain.Decision{approval(), {Outcome: domain.ReviewRejected, Reason: "not yet"}} {
				if got, err := domain.Decide(c.review, c.run, reviewPolicy(t), current, decision, involved, c.by, decided); !errors.Is(err, c.want) || !reflect.DeepEqual(got, domain.Conclusion{}) {
					t.Errorf("%s: got %+v and %v, want nothing and %v", decision.Outcome, got, err, c.want)
				}
			}
		})
	}
}
