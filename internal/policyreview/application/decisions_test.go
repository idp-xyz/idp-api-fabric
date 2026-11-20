package application_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

var (
	rita = domain.Principal{Issuer: "https://issuer.example", Subject: "rita"}
	dave = domain.Principal{Issuer: "https://issuer.example", Subject: "dave"}
)

// undescribed is a quality rule that blocks and can be waived: an operation
// without an operationId.
var undescribed = domain.Observation{RuleID: "F04-GOV-003", Findings: []domain.Finding{{
	RuleID: "F04-GOV-003", Path: "openapi.yaml", Line: 1, Message: "the operation has no operationId",
}}}

// inReview is a world in which erin asked for a review of the revision dave
// recorded, on a check carol ran, and rita holds the reviewer grant.
func inReview(t *testing.T, observed ...domain.Observation) (*world, *application.Reviews, domain.Review) {
	t.Helper()
	w := newWorld()
	w.check(t, "check", rulesTool, observed...)
	reviews := w.reviewing(t)
	review, err := reviews.RequestReview(context.Background(), target(erin), application.RequestReview{CheckRunID: "check", Scope: partner})
	if err != nil {
		t.Fatal(err)
	}
	w.audited, w.events, w.moves = nil, nil, nil
	w.author, w.reviewer = false, true
	return w, reviews, review
}

func (w *world) approving(waivers ...domain.Waiver) domain.Decision {
	return domain.Decision{Outcome: domain.ReviewApproved, ValidUntil: w.now.Add(90 * 24 * time.Hour), Waivers: waivers}
}

var rejecting = domain.Decision{Outcome: domain.ReviewRejected, Reason: "the 400 response lost its problem body"}

// A reviewer of the workspace who had no part in what is reviewed approves
// or rejects it. The conclusion, the move of the revision, the audit record
// and the event are made together.
func TestAReviewerDecidesOnAReviewItHadNoPartIn(t *testing.T) {
	t.Run("an approval", func(t *testing.T) {
		w, reviews, review := inReview(t)
		decision := w.approving()
		decided, err := reviews.DecideReview(context.Background(), target(rita), review.Request.ID, decision)
		if err != nil {
			t.Fatal(err)
		}
		conclusion := domain.Conclusion{
			ReviewID: review.Request.ID, Outcome: domain.ReviewApproved, ConcludedBy: rita, ConcludedAt: w.now, ValidUntil: decision.ValidUntil,
		}
		want := domain.Review{Request: review.Request, Conclusion: &conclusion}
		if !reflect.DeepEqual(decided, want) || !reflect.DeepEqual(w.reviews[review.Request.ID], want) {
			t.Errorf("decided %+v and stored %+v, want %+v", decided.Conclusion, w.reviews[review.Request.ID].Conclusion, conclusion)
		}
		audit := ports.AuditEntry{Target: target(rita), ReviewID: review.Request.ID, Action: "review.approve", Rule: "AC-03", Allowed: true}
		if w.status != "APPROVED" || w.version != 3 || !reflect.DeepEqual(w.moves, []string{"APPROVED"}) ||
			!reflect.DeepEqual(w.audited, []ports.AuditEntry{audit}) || !reflect.DeepEqual(w.events, []string{"concluded " + review.Request.ID + " APPROVED"}) {
			t.Errorf("the revision is %s at %d after %v, audited %+v, announced %v", w.status, w.version, w.moves, w.audited, w.events)
		}
	})

	t.Run("an approval that waives what did not pass", func(t *testing.T) {
		w, reviews, review := inReview(t, undescribed)
		waiver := domain.Waiver{RuleID: "F04-GOV-003", Reason: "generated clients are not used yet", ExpiresAt: w.now.Add(30 * 24 * time.Hour)}
		decided, err := reviews.DecideReview(context.Background(), target(rita), review.Request.ID, w.approving(waiver))
		if err != nil || !reflect.DeepEqual(decided.Conclusion.Waivers, []domain.Waiver{waiver}) || w.status != "APPROVED" {
			t.Fatalf("decided %+v, %v; the revision is %s", decided.Conclusion, err, w.status)
		}
	})

	t.Run("a rejection", func(t *testing.T) {
		w, reviews, review := inReview(t, undescribed)
		decided, err := reviews.DecideReview(context.Background(), target(rita), review.Request.ID, rejecting)
		if err != nil {
			t.Fatal(err)
		}
		conclusion := domain.Conclusion{
			ReviewID: review.Request.ID, Outcome: domain.ReviewRejected, Reason: rejecting.Reason, ConcludedBy: rita, ConcludedAt: w.now,
		}
		want := domain.Review{Request: review.Request, Conclusion: &conclusion}
		audit := ports.AuditEntry{Target: target(rita), ReviewID: review.Request.ID, Action: "review.reject", Rule: "AC-03", Allowed: true}
		if !reflect.DeepEqual(decided, want) || w.status != "REJECTED" || !reflect.DeepEqual(w.moves, []string{"REJECTED"}) ||
			!reflect.DeepEqual(w.audited, []ports.AuditEntry{audit}) || !reflect.DeepEqual(w.events, []string{"concluded " + review.Request.ID + " REJECTED"}) {
			t.Errorf("decided %+v; the revision is %s after %v, audited %+v, announced %v", decided.Conclusion, w.status, w.moves, w.audited, w.events)
		}
		// What was rejected is not submitted again: what is wrong with it
		// is mended in another revision.
		w.author = true
		if _, err := reviews.RequestReview(context.Background(), target(erin), application.RequestReview{CheckRunID: "check", Scope: partner}); !errors.Is(err, errStatus) {
			t.Errorf("a rejected revision submitted again: %v, want the registry to refuse the move", err)
		}
	})

	refused := map[string]struct {
		observed []domain.Observation
		arrange  func(*testing.T, *world, domain.Review)
		caller   domain.Principal
		id       string
		reject   bool
		want     error
		// denied is the rule the refusal is on record under, if it is.
		denied string
	}{
		"someone who may not read the revision": {arrange: func(_ *testing.T, w *world, _ domain.Review) { w.reading = errNoReading }, want: errNoReading},
		"a review nobody asked for":             {id: "another", want: application.ErrReviewNotFound},
		"someone who is no reviewer":            {arrange: func(_ *testing.T, w *world, _ domain.Review) { w.reviewer = false }, want: application.ErrForbidden, denied: "DENY-03"},
		"someone who is no reviewer, rejecting": {arrange: func(_ *testing.T, w *world, _ domain.Review) { w.reviewer = false }, reject: true, want: application.ErrForbidden, denied: "DENY-03"},
		// Who had a part in it and holds no grant either is refused for the
		// grant: nobody is told what a reviewer would be refused for.
		"an author who asked for the review": {
			arrange: func(_ *testing.T, w *world, _ domain.Review) { w.reviewer = false }, caller: erin, want: application.ErrForbidden, denied: "DENY-03"},
		"a suspended product":           {arrange: func(_ *testing.T, w *world, _ domain.Review) { w.active = false }, want: application.ErrProductSuspended},
		"whoever asked for the review":  {caller: erin, want: domain.ErrNotIndependent, denied: "DENY-06"},
		"whoever ran the check":         {caller: carol, want: domain.ErrNotIndependent, denied: "DENY-06"},
		"whoever recorded the revision": {caller: dave, reject: true, want: domain.ErrNotIndependent, denied: "DENY-06"},
		"a review that is concluded": {
			arrange: func(_ *testing.T, w *world, review domain.Review) {
				conclusion := domain.Conclusion{ReviewID: review.Request.ID, Outcome: domain.ReviewWithdrawn, ConcludedBy: erin, ConcludedAt: w.now}
				w.reviews[review.Request.ID] = domain.Review{Request: review.Request, Conclusion: &conclusion}
				w.status, w.version = "WITHDRAWN", 3
			}, want: domain.ErrReviewConcluded},
		"a check the tools have moved on from": {
			arrange: func(t *testing.T, w *world, _ domain.Review) {
				w.check(t, "check", domain.Tool{Name: rulesTool.Name, Version: "2"})
			},
			want: domain.ErrCheckStale},
		"a rule that did not pass and is not waived": {observed: []domain.Observation{undescribed}, want: domain.ErrWaiverMissing},
		"a check that is gone": {
			arrange: func(_ *testing.T, w *world, _ domain.Review) { delete(w.runs, "check") }, want: domain.ErrReviewDamaged},
		"a revision whose content is another": {
			arrange: func(_ *testing.T, w *world, _ domain.Review) { w.digest = "sha256:" + strings.Repeat("b", 64) }, want: domain.ErrReviewDamaged},
		"a revision the registry has moved on": {
			arrange: func(_ *testing.T, w *world, _ domain.Review) { w.status, w.version = "WITHDRAWN", 3 }, want: errStatus},
		"an event that cannot be written": {arrange: func(_ *testing.T, w *world, _ domain.Review) { w.announce = errOutbox }, want: errOutbox},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			w, reviews, review := inReview(t, c.observed...)
			if c.arrange != nil {
				c.arrange(t, w, review)
			}
			caller, id, decision, action := rita, review.Request.ID, w.approving(), "review.approve"
			if c.caller != (domain.Principal{}) {
				caller = c.caller
			}
			if c.id != "" {
				id = c.id
			}
			if c.reject {
				decision, action = rejecting, "review.reject"
			}
			stored, status, version := w.reviews[review.Request.ID], w.status, w.version
			got, err := reviews.DecideReview(context.Background(), target(caller), id, decision)
			if !errors.Is(err, c.want) || !reflect.DeepEqual(got, domain.Review{}) {
				t.Fatalf("got %+v and %v, want nothing and %v", got, err, c.want)
			}
			if !reflect.DeepEqual(w.reviews[review.Request.ID], stored) || w.status != status || w.version != version || len(w.events) != 0 || len(w.moves) != 0 {
				t.Errorf("the review is %+v and the revision %s at %d after events %v and moves %v; want both as they were",
					w.reviews[review.Request.ID], w.status, w.version, w.events, w.moves)
			}
			var audited []ports.AuditEntry
			if c.denied != "" {
				audited = []ports.AuditEntry{{Target: target(caller), Action: action, Rule: c.denied}}
			}
			if !reflect.DeepEqual(w.audited, audited) {
				t.Errorf("audited %+v, want %+v", w.audited, audited)
			}
		})
	}

}
