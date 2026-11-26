package domain_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

var (
	carol   = domain.Principal{Issuer: "https://issuer.example", Subject: "carol"}
	erin    = domain.Principal{Issuer: "https://issuer.example", Subject: "erin"}
	asked   = time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	partner = domain.Target{Audience: "partner", Environment: "sandbox"}
)

// recordedReview is a review as one was recorded before reviews were
// retired: asked by carol on a check, for two targets in the order a review
// keeps them in, and withdrawn a minute later.
func recordedReview() (domain.ReviewRequest, domain.Conclusion) {
	request := domain.ReviewRequest{
		ID: "review-1", Revision: domain.RevisionRef{ProductID: "product-1", WorkspaceID: "workspace-1", AssetID: "asset-1", RevisionID: "revision-1"},
		CheckRunID: "check-1", InputDigest: digestOf("the closure of the revision"),
		Scope:       []domain.Target{{Audience: "internal", Environment: "sandbox"}, partner},
		RequestedBy: carol, RequestedAt: asked,
	}
	return request, domain.Conclusion{
		ReviewID: "review-1", Outcome: domain.ReviewWithdrawn, ConcludedBy: carol, ConcludedAt: asked.Add(time.Minute),
	}
}

// No review is asked for any more, and the ones that were are read back
// from storage only as the model could have recorded them: a request that
// says what it is of, with a scope in its order, and a conclusion there is
// that belongs to it and does not precede it.
func TestAStoredReviewIsReadBackOnlyAsItWasRecorded(t *testing.T) {
	request, conclusion := recordedReview()
	approved := domain.Conclusion{
		ReviewID: "review-1", Outcome: domain.ReviewApproved, ConcludedBy: erin, ConcludedAt: asked.Add(time.Hour),
		ValidUntil: asked.Add(90 * 24 * time.Hour),
		Waivers: []domain.Waiver{
			{RuleID: "R-RULE-002", Reason: "accepted for the pilot", ExpiresAt: asked.Add(30 * 24 * time.Hour)},
			{RuleID: "R-RULE-003", Reason: "accepted for the pilot", CompensatingControl: "the gateway rejects the old path", ExpiresAt: asked.Add(60 * 24 * time.Hour)},
		},
	}
	rejected := domain.Conclusion{
		ReviewID: "review-1", Outcome: domain.ReviewRejected, Reason: "the 400 response lost its problem body",
		ConcludedBy: erin, ConcludedAt: asked.Add(time.Hour),
	}
	for name, c := range map[string]struct {
		review domain.Review
		status string
	}{
		"a request":                    {domain.Review{Request: request}, "IN_REVIEW"},
		"a request and its withdrawal": {domain.Review{Request: request, Conclusion: &conclusion}, "WITHDRAWN"},
		"an approval with waivers":     {domain.Review{Request: request, Conclusion: &approved}, "APPROVED"},
		"a rejection":                  {domain.Review{Request: request, Conclusion: &rejected}, "REJECTED"},
	} {
		if got, err := domain.RecordedReview(c.review); err != nil || !reflect.DeepEqual(got, c.review) || got.Status() != c.status {
			t.Errorf("%s: read back as %+v at %s, want %+v at %s: %v", name, got, got.Status(), c.review, c.status, err)
		}
	}
	if domain.ReviewOpen != "IN_REVIEW" {
		t.Errorf("a review without a conclusion is %s, want IN_REVIEW", domain.ReviewOpen)
	}

	damaged := map[string]func(*domain.Review){
		"no identifier":            func(r *domain.Review) { r.Request.ID = "" },
		"no check":                 func(r *domain.Review) { r.Request.CheckRunID = "" },
		"no revision":              func(r *domain.Review) { r.Request.Revision.RevisionID = "" },
		"a digest that is none":    func(r *domain.Review) { r.Request.InputDigest = "sha256:abc" },
		"nobody who asked":         func(r *domain.Review) { r.Request.RequestedBy.Subject = "" },
		"no scope":                 func(r *domain.Review) { r.Request.Scope = nil },
		"a scope out of its order": func(r *domain.Review) { r.Request.Scope = []domain.Target{r.Request.Scope[1], r.Request.Scope[0]} },
		"a target twice":           func(r *domain.Review) { r.Request.Scope = []domain.Target{partner, partner} },
		"a target there is not": func(r *domain.Review) {
			r.Request.Scope = []domain.Target{{Audience: "partner", Environment: "staging"}}
		},
		"an outcome there is not":         func(r *domain.Review) { r.Conclusion.Outcome = "ESCALATED" },
		"a conclusion of another review":  func(r *domain.Review) { r.Conclusion.ReviewID = "review-2" },
		"a conclusion before the request": func(r *domain.Review) { r.Conclusion.ConcludedAt = asked.Add(-time.Second) },
		"nobody who concluded":            func(r *domain.Review) { r.Conclusion.ConcludedBy.Issuer = "" },
		"a withdrawal that holds for a time": func(r *domain.Review) {
			r.Conclusion.ValidUntil = asked.Add(time.Hour)
		},
		"a withdrawal that waives a rule": func(r *domain.Review) {
			r.Conclusion.Waivers = []domain.Waiver{{RuleID: "R-RULE-002", Reason: "accepted", ExpiresAt: asked.Add(time.Hour)}}
		},
		"an approval that holds for no time": func(r *domain.Review) { r.Conclusion.Outcome = domain.ReviewApproved },
		"an approval that ended before it was given": func(r *domain.Review) {
			r.Conclusion.Outcome, r.Conclusion.ValidUntil = domain.ReviewApproved, asked
		},
		"an approval whose waiver outlives it": func(r *domain.Review) {
			r.Conclusion.Outcome, r.Conclusion.ValidUntil = domain.ReviewApproved, asked.Add(time.Hour)
			r.Conclusion.Waivers = []domain.Waiver{{RuleID: "R-RULE-002", Reason: "accepted", ExpiresAt: asked.Add(2 * time.Hour)}}
		},
		"an approval whose waivers are out of order": func(r *domain.Review) {
			r.Conclusion.Outcome, r.Conclusion.ValidUntil = domain.ReviewApproved, asked.Add(time.Hour)
			r.Conclusion.Waivers = []domain.Waiver{
				{RuleID: "R-RULE-003", Reason: "accepted", ExpiresAt: asked.Add(30 * time.Minute)},
				{RuleID: "R-RULE-002", Reason: "accepted", ExpiresAt: asked.Add(30 * time.Minute)},
			}
		},
		"a rejection without a reason": func(r *domain.Review) { r.Conclusion.Outcome = domain.ReviewRejected },
		"a rejection that holds for a time": func(r *domain.Review) {
			r.Conclusion.Outcome, r.Conclusion.Reason, r.Conclusion.ValidUntil = domain.ReviewRejected, "not yet", asked.Add(time.Hour)
		},
	}
	for name, damage := range damaged {
		t.Run(name, func(t *testing.T) {
			copied := conclusion
			review := domain.Review{Request: request, Conclusion: &copied}
			review.Request.Scope = append([]domain.Target(nil), request.Scope...)
			damage(&review)
			if got, err := domain.RecordedReview(review); !errors.Is(err, domain.ErrReviewDamaged) || !reflect.DeepEqual(got, domain.Review{}) {
				t.Errorf("read back as %+v and %v, want nothing and %v", got, err, domain.ErrReviewDamaged)
			}
		})
	}
}
