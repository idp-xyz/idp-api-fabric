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
)

// named is what a release prepared under the review recorded of it: the
// review, the check the review rests on, what the check read and the policy
// it was held to.
func named(review domain.Review) application.ReleaseEvidenceQuery {
	return application.ReleaseEvidenceQuery{
		Revision: shipment, ReviewID: review.Request.ID, CheckRunID: "check",
		PolicySnapshotID: "snapshot", PolicyDigest: domain.Baseline().Digest(), InputDigest: closure,
	}
}

// The evidence of a release is the review and the check it was prepared
// under, as they were recorded, and it is the same whether or not that
// approval holds now. An approval that ran out, a waiver that did, a reviewer
// who lost the grant and tools that moved on from the check each make
// VerifyReleaseApproval refuse, and none of them changes what the release
// was prepared on: it is asked what it was, not whether it still counts.
func TestTheEvidenceOfAReleaseIsWhatItWasPreparedOnWhetherOrNotTheApprovalStillHolds(t *testing.T) {
	cases := map[string]struct {
		given approval
		// arrange makes the world as the case says and returns the use
		// cases to ask, which are those of w unless the case changes them.
		arrange func(*testing.T, *world, domain.Review, *grant) *application.Reviews
		// refused is why the approval does not hold in that world.
		refused error
	}{
		"an approval that holds": {},
		"an approval that ran out": {
			arrange: func(_ *testing.T, w *world, review domain.Review, _ *grant) *application.Reviews {
				w.now = review.Conclusion.ValidUntil.Add(24 * time.Hour)
				return nil
			},
			refused: application.ErrApprovalExpired,
		},
		"a waiver that ran out while the approval lasts": {
			given: approval{waived: true},
			arrange: func(_ *testing.T, w *world, review domain.Review, _ *grant) *application.Reviews {
				w.now = review.Conclusion.Waivers[0].ExpiresAt.Add(24 * time.Hour)
				return nil
			},
			refused: application.ErrWaiverExpired,
		},
		"a reviewer whose grant is not in force": {
			arrange: func(_ *testing.T, _ *world, _ domain.Review, members *grant) *application.Reviews {
				members.held = false
				return nil
			},
			refused: application.ErrReviewerGrantRevoked,
		},
		"tools that moved on from the check": {
			arrange: func(t *testing.T, w *world, _ domain.Review, _ *grant) *application.Reviews {
				dependencies := w.dependencies()
				dependencies.Evaluator = toolOf(domain.Tool{Name: rulesTool.Name, Version: "4"})
				moved, err := application.NewReviews(dependencies)
				if err != nil {
					t.Fatal(err)
				}
				return moved
			},
			refused: domain.ErrCheckStale,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w, reviews, review := approved(t, c.given)
			members := &grant{held: true}
			if c.arrange != nil {
				if moved := c.arrange(t, w, review, members); moved != nil {
					reviews = moved
				}
			}
			run := w.runs["check"]

			// The approval is as the case says.
			if _, err := reviews.VerifyReleaseApproval(context.Background(), releasing(review), members.hold); !errors.Is(err, c.refused) {
				t.Fatalf("the approval is refused with %v, want %v", err, c.refused)
			}
			w.audited, w.events, w.moves = nil, nil, nil

			got, err := reviews.ReleaseEvidence(context.Background(), named(review))
			want := application.ReleaseEvidence{Review: review, Check: run.Overview()}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, %v; want the review and the check as they were recorded %+v", got, err, want)
			}
			if got.Review.Conclusion.ConcludedBy != rita || !reflect.DeepEqual(got.Check.Tools, []domain.Tool{rulesTool}) ||
				got.Check.PolicyDigest != domain.Baseline().Digest() {
				t.Errorf("the evidence is %+v, want the approval of rita on the check of %v under the baseline", got, rulesTool)
			}
			unchanged(t, w, review)
		})
	}
}

// What a release names is what it was prepared on, so evidence that is not
// what it names is no evidence of it: a review that is not an approval, one
// that rests on another check or read other content, a check that is gone or
// read other content or was held to another policy. Each is a record that
// is not the one the release was prepared on, and nothing is handed over.
func TestEvidenceThatIsNotWhatTheReleaseNamesIsDamaged(t *testing.T) {
	type arrange func(*world, domain.Review, *application.ReleaseEvidenceQuery)
	other := "sha256:" + strings.Repeat("c", 64)
	concluded := func(outcome domain.ReviewOutcome) arrange {
		return func(w *world, review domain.Review, _ *application.ReleaseEvidenceQuery) {
			conclusion := domain.Conclusion{ReviewID: review.Request.ID, Outcome: outcome, Reason: "not this one", ConcludedBy: rita, ConcludedAt: w.now}
			w.reviews[review.Request.ID] = domain.Review{Request: review.Request, Conclusion: &conclusion}
		}
	}
	// naming changes what the release is said to have recorded.
	naming := func(change func(*application.ReleaseEvidenceQuery)) arrange {
		return func(_ *world, _ domain.Review, query *application.ReleaseEvidenceQuery) { change(query) }
	}
	damaged := map[string]struct {
		arrange arrange
		want    error
	}{
		"a review nobody recorded": {naming(func(q *application.ReleaseEvidenceQuery) { q.ReviewID = "another" }), domain.ErrReviewDamaged},
		"a review of another revision": {
			naming(func(q *application.ReleaseEvidenceQuery) { q.Revision.RevisionID = "another" }), domain.ErrReviewDamaged},
		"a review that is still open": {
			func(w *world, review domain.Review, _ *application.ReleaseEvidenceQuery) {
				w.reviews[review.Request.ID] = domain.Review{Request: review.Request}
			}, domain.ErrReviewDamaged},
		"a review that was withdrawn":         {concluded(domain.ReviewWithdrawn), domain.ErrReviewDamaged},
		"a review that rejected the revision": {concluded(domain.ReviewRejected), domain.ErrReviewDamaged},
		"a review that rests on another check": {
			naming(func(q *application.ReleaseEvidenceQuery) { q.CheckRunID = "another" }), domain.ErrReviewDamaged},
		"a review of other content than the release was prepared from": {
			naming(func(q *application.ReleaseEvidenceQuery) { q.InputDigest = other }), domain.ErrReviewDamaged},
		"a check that is gone": {
			func(w *world, _ domain.Review, _ *application.ReleaseEvidenceQuery) { delete(w.runs, "check") }, domain.ErrCheckRunDamaged},
		// A check that is not there is damaged evidence by itself, and not
		// only because what it says is not what the release names: were both
		// to say nothing, the comparison alone would find them alike.
		"a check that is gone, of a release that names nothing of it": {
			func(w *world, review domain.Review, query *application.ReleaseEvidenceQuery) {
				request := review.Request
				request.InputDigest = ""
				w.reviews[request.ID] = domain.Review{Request: request, Conclusion: review.Conclusion}
				delete(w.runs, "check")
				query.InputDigest, query.PolicySnapshotID, query.PolicyDigest = "", "", ""
			}, domain.ErrCheckRunDamaged},
		"a check that read other content": {
			func(w *world, _ domain.Review, _ *application.ReleaseEvidenceQuery) {
				run := w.runs["check"]
				run.InputDigest = other
				w.runs["check"] = run
			}, domain.ErrCheckRunDamaged},
		"a check of another snapshot of the policy": {
			naming(func(q *application.ReleaseEvidenceQuery) { q.PolicySnapshotID = "another" }), domain.ErrCheckRunDamaged},
		"a check held to another policy": {
			naming(func(q *application.ReleaseEvidenceQuery) { q.PolicyDigest = other }), domain.ErrCheckRunDamaged},
	}
	for name, c := range damaged {
		t.Run(name, func(t *testing.T) {
			w, reviews, review := approved(t, approval{})
			query := named(review)
			c.arrange(w, review, &query)
			stored := w.reviews[review.Request.ID]

			got, err := reviews.ReleaseEvidence(context.Background(), query)
			if !errors.Is(err, c.want) || !reflect.DeepEqual(got, application.ReleaseEvidence{}) {
				t.Fatalf("got %+v, %v; want %v and no evidence", got, err, c.want)
			}
			unchanged(t, w, stored)
		})
	}
}
