package application_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// named is what a release prepared on the check recorded of it, and of the
// review it was prepared under if it names one: the check, what it read and
// the policy it was held to.
func named(reviewID string) application.ReleaseEvidenceQuery {
	return application.ReleaseEvidenceQuery{
		Revision: shipment, ReviewID: reviewID, CheckRunID: "check",
		PolicySnapshotID: "snapshot", PolicyDigest: domain.Baseline().Digest(), InputDigest: closure,
	}
}

// The evidence of a release is the check it was prepared on, and the review
// it was prepared under if it was prepared under one, as they were recorded.
// It is the same whatever became of the policy and the tools since, and
// whatever the check found: a release is asked what it was prepared on, not
// whether a release would be prepared on it today.
func TestTheEvidenceOfAReleaseIsWhatItWasPreparedOn(t *testing.T) {
	cases := map[string]struct {
		given checked
		// review says that the release was prepared under an approval, as
		// releases were before reviews were retired.
		review bool
		// tool is the rules tool of today, if it is not the one that ran
		// the check.
		tool domain.Tool
	}{
		"a release prepared on its check":                     {},
		"a release prepared under an approval":                {review: true},
		"a release prepared on a check with a waived failure": {given: checked{observed: []domain.Observation{undescribed}}},
		"a check the tools have moved on from":                {tool: domain.Tool{Name: rulesTool.Name, Version: "4"}},
		"an approval on a check the tools have moved on from": {review: true, tool: domain.Tool{Name: rulesTool.Name, Version: "4"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld()
			run := c.given.run(t, w, rulesTool)
			want := application.ReleaseEvidence{Check: run.Overview()}
			query := named("")
			if c.review {
				review := w.recorded("review", "check", domain.ReviewApproved)
				want.Review, query.ReviewID = &review, review.Request.ID
			}
			if c.tool != (domain.Tool{}) {
				// The check is no longer one a release is prepared on.
				if _, err := w.checking(t, c.tool, compareTool).VerifyReleaseCheck(context.Background(), preparing()); !errors.Is(err, domain.ErrCheckStale) {
					t.Fatalf("the check is refused with %v for a release prepared now, want %v", err, domain.ErrCheckStale)
				}
			}

			got, err := w.reviewing(t).ReleaseEvidence(context.Background(), query)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, %v; want the check and the review as they were recorded %+v", got, err, want)
			}
			if !reflect.DeepEqual(got.Check.Tools, []domain.Tool{rulesTool}) || got.Check.PolicyDigest != domain.Baseline().Digest() {
				t.Errorf("the evidence is %+v, want the check of %v under the baseline", got, rulesTool)
			}
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
	// unreviewed is a release prepared on its check alone.
	unreviewed := func(change func(*application.ReleaseEvidenceQuery)) arrange {
		return naming(func(q *application.ReleaseEvidenceQuery) {
			q.ReviewID = ""
			change(q)
		})
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

		// A release prepared on its check alone is held to its check alone.
		"a check that is gone, of a release on its check": {
			func(w *world, review domain.Review, query *application.ReleaseEvidenceQuery) {
				delete(w.runs, "check")
				query.ReviewID = ""
			}, domain.ErrCheckRunDamaged},
		"a check of another revision, of a release on its check": {
			unreviewed(func(q *application.ReleaseEvidenceQuery) { q.Revision.RevisionID = "another" }), domain.ErrCheckRunDamaged},
		"a check that read other content, of a release on its check": {
			unreviewed(func(q *application.ReleaseEvidenceQuery) { q.InputDigest = other }), domain.ErrCheckRunDamaged},
		"a check held to another policy, of a release on its check": {
			unreviewed(func(q *application.ReleaseEvidenceQuery) { q.PolicyDigest = other }), domain.ErrCheckRunDamaged},
	}
	for name, c := range damaged {
		t.Run(name, func(t *testing.T) {
			w := newWorld()
			checked{}.run(t, w, rulesTool)
			review := w.recorded("review", "check", domain.ReviewApproved)
			query := named(review.Request.ID)
			c.arrange(w, review, &query)

			got, err := w.reviewing(t).ReleaseEvidence(context.Background(), query)
			if !errors.Is(err, c.want) || !reflect.DeepEqual(got, application.ReleaseEvidence{}) {
				t.Fatalf("got %+v, %v; want %v and no evidence", got, err, c.want)
			}
		})
	}
}
