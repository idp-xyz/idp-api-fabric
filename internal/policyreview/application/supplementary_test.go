package application_test

import (
	"cmp"
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

// production is a scope the first approval of the revision did not name.
var production = []domain.Target{{Audience: "partner", Environment: "production"}}

// approvedBefore is a world in which rita approved, for the partner sandbox,
// the review erin asked for of the revision dave recorded, on the check
// carol ran, and in which the caller is an author again.
func approvedBefore(t *testing.T) (*world, *application.Reviews, domain.Review) {
	t.Helper()
	w, reviews, first := approved(t, approval{})
	w.author, w.holds = true, 0
	return w, reviews, first
}

// again asks, as erin, for another review of the approved revision.
func again(t *testing.T, w *world, reviews *application.Reviews) domain.Review {
	t.Helper()
	review, err := reviews.RequestReview(context.Background(), target(erin), application.RequestReview{CheckRunID: "check", Scope: production})
	if err != nil {
		t.Fatal(err)
	}
	w.audited, w.events, w.moves, w.holds = nil, nil, nil, 0
	return review
}

// stays fails the test unless the revision is where its first approval left
// it: approved, at the version it took then, and never moved since.
func stays(t *testing.T, w *world) {
	t.Helper()
	if w.status != "APPROVED" || w.version != 3 || len(w.moves) != 0 {
		t.Errorf("the revision is %s at version %d after the moves %v, want it approved at 3 and never moved", w.status, w.version, w.moves)
	}
}

// A revision that is approved is reviewed again: an author asks, on a check
// of today and for a scope, as for a draft. The review is supplementary and
// moves nothing. The revision is held while the request is recorded, so that
// it is under one review at a time; whatever is refused leaves nothing
// behind but, for a refusal of who the caller is, its audit record.
func TestAnApprovedRevisionIsSubmittedForAnotherReviewAndStaysApproved(t *testing.T) {
	t.Run("an approved revision and a current check", func(t *testing.T) {
		w, reviews, first := approvedBefore(t)
		review, err := reviews.RequestReview(context.Background(), target(erin),
			application.RequestReview{CheckRunID: "check", Scope: production, Note: "for production"})
		if err != nil {
			t.Fatal(err)
		}
		id := review.Request.ID
		want := domain.Review{Request: domain.ReviewRequest{
			ID: id, Revision: shipment, CheckRunID: "check", InputDigest: closure, Scope: production, Note: "for production",
			RequestedBy: erin, RequestedAt: w.now, Supplementary: true,
		}}
		// The request is recorded at the version the approved revision
		// has, which the request did not change.
		if id == first.Request.ID || !reflect.DeepEqual(review, want) || !reflect.DeepEqual(w.reviews[id], want) || w.versions[id] != 3 {
			t.Errorf("asked %+v and stored %+v at version %d, want %+v at version 3", review, w.reviews[id], w.versions[id], want)
		}
		stays(t, w)
		if w.holds != 1 {
			t.Errorf("the revision was held %d times, want once", w.holds)
		}
		audit := ports.AuditEntry{Target: target(erin), ReviewID: id, Action: "review.request", Rule: "AC-50", Allowed: true}
		if !reflect.DeepEqual(w.audited, []ports.AuditEntry{audit}) || !reflect.DeepEqual(w.events, []string{"requested " + id}) {
			t.Errorf("audited %+v and announced %v, want %+v and one request", w.audited, w.events, audit)
		}
		// What was approved before is what it was.
		if !reflect.DeepEqual(w.reviews[first.Request.ID], first) {
			t.Errorf("the first review is %+v, want it as it was decided", w.reviews[first.Request.ID])
		}
	})

	refusal := ports.AuditEntry{Target: target(erin), Action: "review.request", Rule: "DENY-03"}
	refused := map[string]struct {
		arrange func(*testing.T, *world, *application.Reviews)
		command application.RequestReview
		want    error
		audited []ports.AuditEntry
	}{
		"another review of it is open": {
			arrange: func(t *testing.T, w *world, reviews *application.Reviews) { again(t, w, reviews) },
			want:    application.ErrReviewInProgress},
		"someone who may not read the revision": {
			arrange: func(_ *testing.T, w *world, _ *application.Reviews) { w.reading = errNoReading }, want: errNoReading},
		"a check nobody recorded": {command: application.RequestReview{CheckRunID: "another", Scope: production}, want: application.ErrCheckRunNotFound},
		"someone who is no author": {
			arrange: func(_ *testing.T, w *world, _ *application.Reviews) { w.author = false },
			want:    application.ErrForbidden, audited: []ports.AuditEntry{refusal}},
		"a suspended product": {
			arrange: func(_ *testing.T, w *world, _ *application.Reviews) { w.active = false }, want: application.ErrProductSuspended},
		// The approval the revision has rested on a check of then. Another
		// one is asked on a check of today.
		"the check of the first approval, which the tools have moved on from": {
			arrange: func(t *testing.T, w *world, _ *application.Reviews) {
				w.runs["check"] = approval{}.check(t, w, domain.Tool{Name: rulesTool.Name, Version: "2"})
			},
			want: domain.ErrCheckStale},
		"a check in which a rule nothing waives failed": {
			arrange: func(t *testing.T, w *world, _ *application.Reviews) { w.check(t, "check", rulesTool, leaked) },
			want:    domain.ErrReviewBlocked},
		"a scope that is none": {command: application.RequestReview{CheckRunID: "check"}, want: domain.ErrInvalidReview},
		"an event that cannot be written": {
			arrange: func(_ *testing.T, w *world, _ *application.Reviews) { w.announce = errOutbox }, want: errOutbox},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			w, reviews, _ := approvedBefore(t)
			if c.arrange != nil {
				c.arrange(t, w, reviews)
			}
			command := c.command
			if command.CheckRunID == "" {
				command = application.RequestReview{CheckRunID: "check", Scope: production}
			}
			stored := len(w.reviews)
			review, err := reviews.RequestReview(context.Background(), target(erin), command)
			if !errors.Is(err, c.want) || !reflect.DeepEqual(review, domain.Review{}) {
				t.Fatalf("got %+v and %v, want nothing and %v", review, err, c.want)
			}
			if len(w.reviews) != stored || len(w.events) != 0 {
				t.Errorf("%d reviews are stored and %v was announced, want the %d there were and nothing", len(w.reviews), w.events, stored)
			}
			stays(t, w)
			if !reflect.DeepEqual(w.audited, c.audited) {
				t.Errorf("audited %+v, want %+v", w.audited, c.audited)
			}
		})
	}

	// A rejection is where a review ends for good: what was rejected is
	// mended in another revision, and is not reviewed again as it is.
	t.Run("a revision that was rejected", func(t *testing.T) {
		w := newWorld()
		w.check(t, "check", rulesTool)
		w.status, w.version = "REJECTED", 3
		review, err := w.reviewing(t).RequestReview(context.Background(), target(erin), application.RequestReview{CheckRunID: "check", Scope: production})
		if !errors.Is(err, errStatus) || !reflect.DeepEqual(review, domain.Review{}) || len(w.reviews) != 0 || w.status != "REJECTED" {
			t.Errorf("got %+v and %v with %d reviews stored and the revision %s; want the registry to refuse", review, err, len(w.reviews), w.status)
		}
	})
}

// Whoever asked for a supplementary review takes it back while it is open.
// The revision stays approved, and another review of it can be asked for.
func TestASupplementaryReviewIsWithdrawnAndLeavesTheRevisionApproved(t *testing.T) {
	t.Run("whoever asked, while it is open", func(t *testing.T) {
		w, reviews, _ := approvedBefore(t)
		review := again(t, w, reviews)
		withdrawn, err := reviews.WithdrawReview(context.Background(), target(erin), review.Request.ID, "the wrong scope")
		if err != nil {
			t.Fatal(err)
		}
		conclusion := domain.Conclusion{
			ReviewID: review.Request.ID, Outcome: domain.ReviewWithdrawn, Reason: "the wrong scope", ConcludedBy: erin, ConcludedAt: w.now,
		}
		want := domain.Review{Request: review.Request, Conclusion: &conclusion}
		if !reflect.DeepEqual(withdrawn, want) || !reflect.DeepEqual(w.reviews[review.Request.ID], want) {
			t.Errorf("withdrawn as %+v and stored as %+v, want %+v", withdrawn, w.reviews[review.Request.ID], want)
		}
		stays(t, w)
		audit := ports.AuditEntry{Target: target(erin), ReviewID: review.Request.ID, Action: "review.withdraw", Rule: "AC-51", Allowed: true}
		if w.holds != 1 || !reflect.DeepEqual(w.audited, []ports.AuditEntry{audit}) ||
			!reflect.DeepEqual(w.events, []string{"concluded " + review.Request.ID + " WITHDRAWN"}) {
			t.Errorf("held %d times, audited %+v, announced %v", w.holds, w.audited, w.events)
		}

		// Nothing is open now, so another review is asked for, at the
		// same version of the revision.
		next, err := reviews.RequestReview(context.Background(), target(erin), application.RequestReview{CheckRunID: "check", Scope: production})
		if err != nil || !next.Request.Supplementary || next.Request.ID == review.Request.ID || w.versions[next.Request.ID] != 3 || len(w.reviews) != 3 {
			t.Errorf("asked again: %+v, %v; %d reviews are stored", next, err, len(w.reviews))
		}
		stays(t, w)
	})

	refused := map[string]struct {
		arrange func(*world, domain.Review)
		caller  domain.Principal
		want    error
		denied  bool
	}{
		"whoever asked, no longer an author": {arrange: func(w *world, _ domain.Review) { w.author = false }, caller: erin, want: application.ErrForbidden, denied: true},
		"another author":                     {caller: carol, want: domain.ErrNotRequester, denied: true},
		"a review that is concluded": {
			arrange: func(w *world, review domain.Review) {
				conclusion := domain.Conclusion{ReviewID: review.Request.ID, Outcome: domain.ReviewWithdrawn, ConcludedBy: erin, ConcludedAt: w.now}
				w.reviews[review.Request.ID] = domain.Review{Request: review.Request, Conclusion: &conclusion}
			}, caller: erin, want: domain.ErrReviewConcluded},
		// Nothing leaves approved: a supplementary review of a revision
		// that is anything else is no review the model recorded.
		"a revision that is not approved": {
			arrange: func(w *world, _ domain.Review) { w.status = "IN_REVIEW" }, caller: erin, want: domain.ErrReviewDamaged},
		"an event that cannot be written": {arrange: func(w *world, _ domain.Review) { w.announce = errOutbox }, caller: erin, want: errOutbox},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			w, reviews, _ := approvedBefore(t)
			review := again(t, w, reviews)
			if c.arrange != nil {
				c.arrange(w, review)
			}
			stored, status := w.reviews[review.Request.ID], w.status
			got, err := reviews.WithdrawReview(context.Background(), target(c.caller), review.Request.ID, "")
			if !errors.Is(err, c.want) || !reflect.DeepEqual(got, domain.Review{}) {
				t.Fatalf("got %+v and %v, want nothing and %v", got, err, c.want)
			}
			if !reflect.DeepEqual(w.reviews[review.Request.ID], stored) || w.status != status || w.version != 3 || len(w.events) != 0 || len(w.moves) != 0 {
				t.Errorf("the review is %+v and the revision %s at %d after events %v and moves %v; want both as they were",
					w.reviews[review.Request.ID], w.status, w.version, w.events, w.moves)
			}
			var audited []ports.AuditEntry
			if c.denied {
				audited = []ports.AuditEntry{{Target: target(c.caller), Action: "review.withdraw", Rule: "DENY-03"}}
			}
			if !reflect.DeepEqual(w.audited, audited) {
				t.Errorf("audited %+v, want %+v", w.audited, audited)
			}
		})
	}
}

// A reviewer who had no part in it decides on a supplementary review as on
// any other. The revision stays approved either way: an approval is another
// approval of the same content, for the scope and the time it names, and a
// rejection takes back nothing that was approved before.
func TestASupplementaryReviewIsDecidedAndLeavesTheRevisionApproved(t *testing.T) {
	sandbox, live := partner[0], production[0]

	t.Run("an approval", func(t *testing.T) {
		w, reviews, first := approvedBefore(t)
		review := again(t, w, reviews)
		// Whoever approved the revision before decides again: it had no
		// part in what is reviewed.
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
		stays(t, w)
		audit := ports.AuditEntry{Target: target(rita), ReviewID: review.Request.ID, Action: "review.approve", Rule: "AC-03", Allowed: true}
		if w.holds != 1 || !reflect.DeepEqual(w.audited, []ports.AuditEntry{audit}) ||
			!reflect.DeepEqual(w.events, []string{"concluded " + review.Request.ID + " APPROVED"}) {
			t.Errorf("held %d times, audited %+v, announced %v", w.holds, w.audited, w.events)
		}

		// Each approval covers the scope it was asked for and no other.
		members := &grant{held: true}
		covers := func(approved domain.Review, scope domain.Target) error {
			input := releasing(approved)
			input.Target = &scope
			_, err := reviews.VerifyReleaseApproval(context.Background(), input, members.hold)
			return err
		}
		for name, c := range map[string]struct {
			approved domain.Review
			scope    domain.Target
			want     error
		}{
			"the first approval, for the scope it named":     {first, sandbox, nil},
			"the first approval, for the scope of the other": {first, live, application.ErrApprovalScope},
			"the other approval, for the scope it named":     {decided, live, nil},
			"the other approval, for the scope of the first": {decided, sandbox, application.ErrApprovalScope},
		} {
			if err := covers(c.approved, c.scope); !errors.Is(err, c.want) {
				t.Errorf("%s: %v, want %v", name, err, c.want)
			}
		}
	})

	t.Run("an approval that waives what did not pass", func(t *testing.T) {
		w, reviews, _ := approved(t, approval{waived: true})
		w.author = true
		review := again(t, w, reviews)
		waiver := domain.Waiver{RuleID: undescribed.RuleID, Reason: "generated clients are still not used", ExpiresAt: w.now.Add(10 * 24 * time.Hour)}
		decided, err := reviews.DecideReview(context.Background(), target(rita), review.Request.ID, w.approving(waiver))
		if err != nil || !reflect.DeepEqual(decided.Conclusion.Waivers, []domain.Waiver{waiver}) {
			t.Fatalf("decided %+v, %v", decided.Conclusion, err)
		}
		stays(t, w)
	})

	t.Run("a rejection", func(t *testing.T) {
		w, reviews, first := approvedBefore(t)
		review := again(t, w, reviews)
		decided, err := reviews.DecideReview(context.Background(), target(rita), review.Request.ID, rejecting)
		if err != nil {
			t.Fatal(err)
		}
		conclusion := domain.Conclusion{
			ReviewID: review.Request.ID, Outcome: domain.ReviewRejected, Reason: rejecting.Reason, ConcludedBy: rita, ConcludedAt: w.now,
		}
		want := domain.Review{Request: review.Request, Conclusion: &conclusion}
		audit := ports.AuditEntry{Target: target(rita), ReviewID: review.Request.ID, Action: "review.reject", Rule: "AC-03", Allowed: true}
		if !reflect.DeepEqual(decided, want) || !reflect.DeepEqual(w.audited, []ports.AuditEntry{audit}) ||
			!reflect.DeepEqual(w.events, []string{"concluded " + review.Request.ID + " REJECTED"}) {
			t.Errorf("decided %+v, audited %+v, announced %v", decided.Conclusion, w.audited, w.events)
		}
		stays(t, w)

		// What was approved before is approved still, and a release
		// rests on it as before.
		members := &grant{held: true}
		if got, err := reviews.VerifyReleaseApproval(context.Background(), releasing(first), members.hold); err != nil || got.CheckRunID != "check" {
			t.Errorf("the first approval after the rejection: %+v, %v; want it to hold", got, err)
		}
		// The rejection itself approves nothing.
		if _, err := reviews.VerifyReleaseApproval(context.Background(), releasing(decided), members.hold); !errors.Is(err, application.ErrApprovalRequired) {
			t.Errorf("a release under the rejected review: %v, want %v", err, application.ErrApprovalRequired)
		}
		// And the revision is reviewed again.
		if next, err := reviews.RequestReview(context.Background(), target(erin), application.RequestReview{CheckRunID: "check", Scope: production}); err != nil || !next.Request.Supplementary {
			t.Errorf("asked again after the rejection: %+v, %v", next, err)
		}
	})

	refused := map[string]struct {
		arrange func(*testing.T, *world, domain.Review)
		caller  domain.Principal
		reject  bool
		want    error
		// denied is the rule the refusal is on record under, if it is.
		denied string
	}{
		"someone who is no reviewer": {
			arrange: func(_ *testing.T, w *world, _ domain.Review) { w.reviewer = false }, want: application.ErrForbidden, denied: "DENY-03"},
		"a suspended product": {
			arrange: func(_ *testing.T, w *world, _ domain.Review) { w.active = false }, want: application.ErrProductSuspended},
		"whoever asked for the review":  {caller: erin, want: domain.ErrNotIndependent, denied: "DENY-06"},
		"whoever ran the check":         {caller: carol, want: domain.ErrNotIndependent, denied: "DENY-06"},
		"whoever recorded the revision": {caller: dave, reject: true, want: domain.ErrNotIndependent, denied: "DENY-06"},
		"a review that is concluded": {
			arrange: func(_ *testing.T, w *world, review domain.Review) {
				conclusion := domain.Conclusion{ReviewID: review.Request.ID, Outcome: domain.ReviewWithdrawn, ConcludedBy: erin, ConcludedAt: w.now}
				w.reviews[review.Request.ID] = domain.Review{Request: review.Request, Conclusion: &conclusion}
			}, want: domain.ErrReviewConcluded},
		"a check the tools have moved on from": {
			arrange: func(t *testing.T, w *world, _ domain.Review) {
				w.runs["check"] = approval{}.check(t, w, domain.Tool{Name: rulesTool.Name, Version: "2"})
			},
			want: domain.ErrCheckStale},
		"a check that is gone": {
			arrange: func(_ *testing.T, w *world, _ domain.Review) { delete(w.runs, "check") }, want: domain.ErrReviewDamaged},
		"a revision whose content is another": {
			arrange: func(_ *testing.T, w *world, _ domain.Review) { w.digest = "sha256:" + strings.Repeat("b", 64) }, want: domain.ErrReviewDamaged},
		"a revision that is not approved": {
			arrange: func(_ *testing.T, w *world, _ domain.Review) { w.status = "IN_REVIEW" }, want: domain.ErrReviewDamaged},
		"an event that cannot be written": {
			arrange: func(_ *testing.T, w *world, _ domain.Review) { w.announce = errOutbox }, want: errOutbox},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			w, reviews, _ := approvedBefore(t)
			review := again(t, w, reviews)
			w.author = false
			if c.arrange != nil {
				c.arrange(t, w, review)
			}
			caller := cmp.Or(c.caller, rita)
			decision := w.approving()
			action := "review.approve"
			if c.reject {
				decision, action = rejecting, "review.reject"
			}
			stored, status := w.reviews[review.Request.ID], w.status
			got, err := reviews.DecideReview(context.Background(), target(caller), review.Request.ID, decision)
			if !errors.Is(err, c.want) || !reflect.DeepEqual(got, domain.Review{}) {
				t.Fatalf("got %+v and %v, want nothing and %v", got, err, c.want)
			}
			if !reflect.DeepEqual(w.reviews[review.Request.ID], stored) || w.status != status || w.version != 3 || len(w.events) != 0 || len(w.moves) != 0 {
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

	// A check the tools moved on from still ends its review: the reviewer
	// rejects it, and the author asks again on a check of today.
	t.Run("a rejection on a check the tools have moved on from", func(t *testing.T) {
		w, reviews, _ := approvedBefore(t)
		review := again(t, w, reviews)
		w.runs["check"] = approval{}.check(t, w, domain.Tool{Name: rulesTool.Name, Version: "2"})
		if decided, err := reviews.DecideReview(context.Background(), target(rita), review.Request.ID, rejecting); err != nil || decided.Status() != "REJECTED" {
			t.Errorf("decided %+v, %v; want the review rejected", decided.Conclusion, err)
		}
		stays(t, w)
	})
}
