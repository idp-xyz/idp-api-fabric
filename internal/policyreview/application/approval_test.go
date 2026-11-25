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

// paula publishes: she had no part in the revision, its check or its review.
var paula = domain.Principal{Issuer: "https://issuer.example", Subject: "paula"}

// answered are the rules a check of a contract answers for: the ones its
// tools evaluate. The other rules of the baseline are held by other gates
// (the registry, publication, rendering, delivery), and a check says of them
// that they were not run.
var answered = []string{
	"F04-GOV-001", "F04-GOV-002", "F04-GOV-003", "F04-GOV-004", "F04-GOV-005", "F04-GOV-006",
	"F04-GOV-007", "F04-GOV-008", "F04-GOV-009", "F04-GOV-010", "F04-GOV-011", "F04-GOV-012",
}

// before is the revision that is published when another replaces it.
var before = domain.ComparisonBaseline{RevisionID: "before", InputDigest: "sha256:" + strings.Repeat("b", 64)}

// grant stands in for the members of the workspace: whether the reviewer
// still holds the grant it approved under.
type grant struct {
	held  bool
	err   error
	asked []string
}

func (g *grant) hold(_ context.Context, reviewer domain.Principal, approvedAt time.Time) (bool, error) {
	g.asked = append(g.asked, reviewer.Subject+" at "+approvedAt.Format(time.RFC3339Nano))
	return g.held, g.err
}

// approval says what the check of an approved revision was like.
type approval struct {
	// compared is the revision the check compared this one with, if any.
	compared *domain.ComparisonBaseline
	// unanswered is a rule of the check that was not run.
	unanswered string
	// waived says that the check found an operation without an operationId
	// and the reviewer waived it for thirty days.
	waived bool
}

// check is the check carol ran, by the tool.
func (a approval) check(t *testing.T, w *world, tool domain.Tool) domain.CheckRun {
	t.Helper()
	var observed []domain.Observation
	for _, rule := range answered {
		switch {
		case rule == a.unanswered:
		case rule == undescribed.RuleID && a.waived:
			observed = append(observed, undescribed)
		default:
			observed = append(observed, domain.Observation{RuleID: rule})
		}
	}
	draft := domain.Draft{
		ID: "check", PolicySnapshotID: "snapshot", Revision: shipment, RequestedBy: carol, StartedAt: w.now.Add(-time.Hour),
	}
	if a.compared != nil {
		draft.Compatibility = &domain.ComparisonEvidence{
			Baseline: *a.compared, Tool: compareTool, Comparison: domain.Comparison{Classification: domain.Compatible},
		}
	}
	source := domain.Source{
		Entrypoint: "openapi.yaml", Digest: closure, Files: []domain.SourceFile{{Path: "openapi.yaml", Content: "openapi: 3.1.2\n"}},
	}
	run, err := domain.Complete(draft, domain.Baseline(), source, tool, observed, w.now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// approved is a world in which rita approved, for ninety days, the review
// erin asked for of the revision dave recorded, on the check carol ran.
func approved(t *testing.T, given approval) (*world, *application.Reviews, domain.Review) {
	t.Helper()
	w := newWorld()
	w.runs["check"] = given.check(t, w, rulesTool)
	reviews := w.reviewing(t)
	review, err := reviews.RequestReview(context.Background(), target(erin), application.RequestReview{CheckRunID: "check", Scope: partner})
	if err != nil {
		t.Fatal(err)
	}
	w.author, w.reviewer = false, true
	decision := w.approving()
	if given.waived {
		decision.Waivers = []domain.Waiver{{
			RuleID: undescribed.RuleID, Reason: "generated clients are not used yet", ExpiresAt: w.now.Add(30 * 24 * time.Hour),
		}}
	}
	decided, err := reviews.DecideReview(context.Background(), target(rita), review.Request.ID, decision)
	if err != nil {
		t.Fatal(err)
	}
	w.audited, w.events, w.moves = nil, nil, nil
	return w, reviews, decided
}

// releasing is what paula asks when she prepares a release of the revision
// under the review.
func releasing(review domain.Review) application.ReleaseApproval {
	return application.ReleaseApproval{
		Revision: shipment, ReviewID: review.Request.ID, InputDigest: closure, Author: dave, Publisher: paula,
	}
}

// unchanged fails the test unless the verification left the review as it
// was decided and wrote nothing: it reads, and whoever asks decides what to
// record.
func unchanged(t *testing.T, w *world, review domain.Review) {
	t.Helper()
	if !reflect.DeepEqual(w.reviews[review.Request.ID], review) || w.status != "APPROVED" ||
		len(w.audited)+len(w.events)+len(w.moves) != 0 {
		t.Errorf("the review is %+v and the revision %s after %v, audited %+v, announced %v; want all of it as it was",
			w.reviews[review.Request.ID].Conclusion, w.status, w.moves, w.audited, w.events)
	}
}

// A release is prepared, published and served under the approval of its
// revision for as long as that approval holds. It is asked again every time:
// of the review that approved this content, by someone who had no part in
// it, under a grant that is still in force, before the approval and every
// waiver it needs run out, for a target the review was asked for.
func TestAReleaseRestsOnAnApprovalThatStillHolds(t *testing.T) {
	sandbox, replaced := partner[0], before
	holds := map[string]struct {
		given   approval
		arrange func(*world, domain.Review, *application.ReleaseApproval)
	}{
		"preparing a release": {},
		"publishing to a target the review was asked for": {
			arrange: func(_ *world, _ domain.Review, input *application.ReleaseApproval) { input.Target = &sandbox }},
		"serving what is published, which names no publisher": {
			arrange: func(_ *world, _ domain.Review, input *application.ReleaseApproval) {
				input.Publisher, input.Target = domain.Principal{}, &sandbox
			}},
		"a rule that was waived, while the waiver lasts": {given: approval{waived: true}},
		"a rule that only warns and was not run":         {given: approval{unanswered: "F04-GOV-007"}},
		"replacing the revision the check was compared with": {
			given:   approval{compared: &before},
			arrange: func(_ *world, _ domain.Review, input *application.ReleaseApproval) { input.Baseline = &replaced }},
		"a check that was compared, where nothing is replaced": {given: approval{compared: &before}},
		"the last minute of the approval": {
			arrange: func(w *world, review domain.Review, _ *application.ReleaseApproval) {
				w.now = review.Conclusion.ValidUntil.Add(-2 * time.Minute)
			}},
		"the last minute of a waiver": {
			given: approval{waived: true},
			arrange: func(w *world, review domain.Review, _ *application.ReleaseApproval) {
				w.now = review.Conclusion.Waivers[0].ExpiresAt.Add(-2 * time.Minute)
			}},
	}
	for name, c := range holds {
		t.Run(name, func(t *testing.T) {
			w, reviews, review := approved(t, c.given)
			input, members := releasing(review), &grant{held: true}
			if c.arrange != nil {
				c.arrange(w, review, &input)
			}
			got, err := reviews.VerifyReleaseApproval(context.Background(), input, members.hold)
			want := application.VerifiedApproval{CheckRunID: "check", PolicySnapshotID: "snapshot", PolicyDigest: domain.Baseline().Digest()}
			if err != nil || got != want {
				t.Fatalf("got %+v, %v; want %+v", got, err, want)
			}
			// The grant that counts is the one of whoever approved, as it
			// stood when the approval was given.
			asked := []string{"rita at " + review.Conclusion.ConcludedAt.Format(time.RFC3339Nano)}
			if !reflect.DeepEqual(members.asked, asked) {
				t.Errorf("the grant was asked of %v, want %v", members.asked, asked)
			}
			unchanged(t, w, review)
		})
	}
}

func TestAnApprovalThatDoesNotHoldReleasesNothing(t *testing.T) {
	type arrange func(*testing.T, *world, domain.Review, *application.ReleaseApproval, *grant)
	concluded := func(outcome domain.ReviewOutcome) arrange {
		return func(_ *testing.T, w *world, review domain.Review, _ *application.ReleaseApproval, _ *grant) {
			conclusion := domain.Conclusion{ReviewID: review.Request.ID, Outcome: outcome, Reason: "not this one", ConcludedBy: rita, ConcludedAt: w.now}
			w.reviews[review.Request.ID] = domain.Review{Request: review.Request, Conclusion: &conclusion}
		}
	}
	publisher := func(who domain.Principal) arrange {
		return func(_ *testing.T, _ *world, _ domain.Review, input *application.ReleaseApproval, _ *grant) {
			input.Publisher = who
		}
	}
	to := func(audience, environment string) arrange {
		return func(_ *testing.T, _ *world, _ domain.Review, input *application.ReleaseApproval, _ *grant) {
			input.Target = &domain.Target{Audience: audience, Environment: environment}
		}
	}
	// at sets the clock so that the approval is asked about at that time.
	at := func(when func(domain.Conclusion) time.Time) arrange {
		return func(_ *testing.T, w *world, review domain.Review, _ *application.ReleaseApproval, _ *grant) {
			w.now = when(*review.Conclusion).Add(-time.Minute)
		}
	}
	over := func(baseline domain.ComparisonBaseline) arrange {
		return func(_ *testing.T, _ *world, _ domain.Review, input *application.ReleaseApproval, _ *grant) {
			input.Baseline = &baseline
		}
	}
	refused := map[string]struct {
		given   approval
		arrange arrange
		want    error
		// names is what the refusal says besides.
		names string
	}{
		"a review nobody asked for": {
			arrange: func(_ *testing.T, _ *world, _ domain.Review, input *application.ReleaseApproval, _ *grant) {
				input.ReviewID = "another"
			},
			want: application.ErrApprovalRequired},
		"a review of another revision": {
			arrange: func(_ *testing.T, _ *world, _ domain.Review, input *application.ReleaseApproval, _ *grant) {
				input.Revision.RevisionID = "another"
			},
			want: application.ErrApprovalRequired},
		"a review that is still open": {
			arrange: func(_ *testing.T, w *world, review domain.Review, _ *application.ReleaseApproval, _ *grant) {
				w.reviews[review.Request.ID] = domain.Review{Request: review.Request}
			},
			want: application.ErrApprovalRequired},
		"a review that was withdrawn":         {arrange: concluded(domain.ReviewWithdrawn), want: application.ErrApprovalRequired},
		"a review that rejected the revision": {arrange: concluded(domain.ReviewRejected), want: application.ErrApprovalRequired},
		"other content than the review was of": {
			arrange: func(_ *testing.T, _ *world, _ domain.Review, input *application.ReleaseApproval, _ *grant) {
				input.InputDigest = "sha256:" + strings.Repeat("c", 64)
			},
			want: domain.ErrReviewDamaged},
		"a check that is gone": {
			arrange: func(_ *testing.T, w *world, _ domain.Review, _ *application.ReleaseApproval, _ *grant) {
				delete(w.runs, "check")
			},
			want: domain.ErrReviewDamaged},

		"another environment than the review was asked for": {arrange: to("partner", "production"), want: application.ErrApprovalScope},
		"another audience than the review was asked for":    {arrange: to("internal", "sandbox"), want: application.ErrApprovalScope},

		// Whoever had a part in the revision does not publish it.
		"a publisher who recorded the revision": {arrange: publisher(dave), want: application.ErrPublisherInvolved},
		"a publisher who ran the check":         {arrange: publisher(carol), want: application.ErrPublisherInvolved},
		"a publisher who asked for the review":  {arrange: publisher(erin), want: application.ErrPublisherInvolved},
		"a publisher who approved the revision": {arrange: publisher(rita), want: application.ErrPublisherInvolved},

		"a reviewer whose grant is not in force": {
			arrange: func(_ *testing.T, _ *world, _ domain.Review, _ *application.ReleaseApproval, members *grant) {
				members.held = false
			},
			want: application.ErrReviewerGrantRevoked},
		"a grant that cannot be read": {
			arrange: func(_ *testing.T, _ *world, _ domain.Review, _ *application.ReleaseApproval, members *grant) {
				members.err = errNoReading
			},
			want: errNoReading},

		"the time the approval holds until": {
			arrange: at(func(c domain.Conclusion) time.Time { return c.ValidUntil }), want: application.ErrApprovalExpired},
		"a day after the approval ended": {
			arrange: at(func(c domain.Conclusion) time.Time { return c.ValidUntil.Add(24 * time.Hour) }), want: application.ErrApprovalExpired},
		"a clock that is behind the approval": {
			arrange: at(func(c domain.Conclusion) time.Time { return c.ConcludedAt.Add(-time.Minute) }), want: application.ErrApprovalExpired},
		"the time a waiver lasts until": {
			given:   approval{waived: true},
			arrange: at(func(c domain.Conclusion) time.Time { return c.Waivers[0].ExpiresAt }), want: application.ErrWaiverExpired},
		"a waiver that ran out while the approval lasts": {
			given:   approval{waived: true},
			arrange: at(func(c domain.Conclusion) time.Time { return c.Waivers[0].ExpiresAt.Add(24 * time.Hour) }), want: application.ErrWaiverExpired},

		"a check the tools have moved on from": {
			arrange: func(t *testing.T, w *world, _ domain.Review, _ *application.ReleaseApproval, _ *grant) {
				w.runs["check"] = approval{}.check(t, w, domain.Tool{Name: rulesTool.Name, Version: "2"})
			},
			want: domain.ErrCheckStale},

		// Replacing what is published takes a check that compared this
		// revision with the one that is replaced.
		"a check that compared with nothing":             {arrange: over(before), want: application.ErrBaselineMismatch},
		"a check that compared with another revision":    {given: approval{compared: &before}, arrange: over(domain.ComparisonBaseline{RevisionID: "earlier", InputDigest: before.InputDigest}), want: application.ErrBaselineMismatch},
		"a check that compared with other content of it": {given: approval{compared: &before}, arrange: over(domain.ComparisonBaseline{RevisionID: before.RevisionID, InputDigest: closure}), want: application.ErrBaselineMismatch},
	}
	// A rule that blocks and that a check of a contract answers for has to
	// have been run: nothing else holds a release to it.
	for _, rule := range answered {
		if rule != "F04-GOV-007" {
			refused["a check that did not run "+rule] = struct {
				given   approval
				arrange arrange
				want    error
				names   string
			}{given: approval{unanswered: rule}, want: application.ErrReleaseCheckMissing, names: rule}
		}
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			w, reviews, review := approved(t, c.given)
			input, members := releasing(review), &grant{held: true}
			if c.arrange != nil {
				c.arrange(t, w, review, &input, members)
			}
			stored := w.reviews[review.Request.ID]
			got, err := reviews.VerifyReleaseApproval(context.Background(), input, members.hold)
			if !errors.Is(err, c.want) || got != (application.VerifiedApproval{}) || !strings.Contains(err.Error(), c.names) {
				t.Fatalf("got %+v, %v; want %v naming %q and no evidence", got, err, c.want, c.names)
			}
			unchanged(t, w, stored)
		})
	}
}

// returning is what paula asks when she returns a slot of the sandbox to a
// release of the revision that it served before, under the review.
func returning(review domain.Review) application.ReleaseApproval {
	input := releasing(review)
	input.Target, input.Restoring = &partner[0], true
	return input
}

// A slot returns to a release it served before whatever became of the
// approval since. What decides whether a release is put on a slot for the
// first time does not decide what a slot falls back to: the approval may
// have run out, or a waiver of it, the tools may have moved on from its
// check, a rule may not have been run, and the target may be one the review
// was not asked for. The grant of the reviewer is not asked for, in force
// or not. The evidence is the one the release was prepared on.
func TestAReturnToAReleaseDoesNotAskWhetherItsApprovalStillHolds(t *testing.T) {
	type arrange func(*testing.T, *world, domain.Review, *application.ReleaseApproval)
	// at sets the clock so that the approval is asked about at that time.
	at := func(when func(domain.Conclusion) time.Time) arrange {
		return func(_ *testing.T, w *world, review domain.Review, _ *application.ReleaseApproval) {
			w.now = when(*review.Conclusion).Add(-time.Minute)
		}
	}
	returns := map[string]struct {
		given   approval
		arrange arrange
	}{
		"an approval that holds": {},
		"the time the approval holds until": {
			arrange: at(func(c domain.Conclusion) time.Time { return c.ValidUntil })},
		"a year after the approval ended": {
			arrange: at(func(c domain.Conclusion) time.Time { return c.ValidUntil.Add(365 * 24 * time.Hour) })},
		"a clock that is behind the approval": {
			arrange: at(func(c domain.Conclusion) time.Time { return c.ConcludedAt.Add(-time.Minute) })},
		"a waiver that ran out while the approval lasts": {
			given:   approval{waived: true},
			arrange: at(func(c domain.Conclusion) time.Time { return c.Waivers[0].ExpiresAt.Add(24 * time.Hour) })},
		"a check the tools have moved on from": {
			arrange: func(t *testing.T, w *world, _ domain.Review, _ *application.ReleaseApproval) {
				w.runs["check"] = approval{}.check(t, w, domain.Tool{Name: rulesTool.Name, Version: "2"})
			}},
		"a check that did not run a rule that blocks": {given: approval{unanswered: "F04-GOV-004"}},
		"another environment than the review was asked for": {
			arrange: func(_ *testing.T, _ *world, _ domain.Review, input *application.ReleaseApproval) {
				input.Target = &domain.Target{Audience: "partner", Environment: "production"}
			}},
		"a baseline the check did not compare with": {
			arrange: func(_ *testing.T, _ *world, _ domain.Review, input *application.ReleaseApproval) {
				input.Baseline = &before
			}},
	}
	for name, c := range returns {
		t.Run(name, func(t *testing.T) {
			w, reviews, review := approved(t, c.given)
			input := returning(review)
			if c.arrange != nil {
				c.arrange(t, w, review, &input)
			}
			// Nobody holds the grant, and it cannot even be read.
			members := &grant{err: errNoReading}
			got, err := reviews.VerifyReleaseApproval(context.Background(), input, members.hold)
			want := application.VerifiedApproval{CheckRunID: "check", PolicySnapshotID: "snapshot", PolicyDigest: domain.Baseline().Digest()}
			if err != nil || got != want {
				t.Fatalf("got %+v, %v; want %+v", got, err, want)
			}
			if len(members.asked) != 0 {
				t.Errorf("the grant was asked of %v, want it not asked", members.asked)
			}
			unchanged(t, w, review)
		})
	}
}

// A return is still a publication by someone, of something that was
// approved. Whoever had a part in the revision does not put it back, and a
// review that is not the approval of this content is none to return under.
func TestAReturnToAReleaseIsRefusedToWhoeverHadAPartInItAndWithoutItsApproval(t *testing.T) {
	type arrange func(*world, domain.Review, *application.ReleaseApproval)
	concluded := func(outcome domain.ReviewOutcome) arrange {
		return func(w *world, review domain.Review, _ *application.ReleaseApproval) {
			conclusion := domain.Conclusion{ReviewID: review.Request.ID, Outcome: outcome, Reason: "not this one", ConcludedBy: rita, ConcludedAt: w.now}
			w.reviews[review.Request.ID] = domain.Review{Request: review.Request, Conclusion: &conclusion}
		}
	}
	publisher := func(who domain.Principal) arrange {
		return func(_ *world, _ domain.Review, input *application.ReleaseApproval) { input.Publisher = who }
	}
	refused := map[string]struct {
		arrange arrange
		want    error
	}{
		"a publisher who recorded the revision": {publisher(dave), application.ErrPublisherInvolved},
		"a publisher who ran the check":         {publisher(carol), application.ErrPublisherInvolved},
		"a publisher who asked for the review":  {publisher(erin), application.ErrPublisherInvolved},
		"a publisher who approved the revision": {publisher(rita), application.ErrPublisherInvolved},

		"a review nobody asked for": {
			func(_ *world, _ domain.Review, input *application.ReleaseApproval) { input.ReviewID = "another" },
			application.ErrApprovalRequired},
		"a review of another revision": {
			func(_ *world, _ domain.Review, input *application.ReleaseApproval) {
				input.Revision.RevisionID = "another"
			},
			application.ErrApprovalRequired},
		"a review that is still open": {
			func(w *world, review domain.Review, _ *application.ReleaseApproval) {
				w.reviews[review.Request.ID] = domain.Review{Request: review.Request}
			},
			application.ErrApprovalRequired},
		"a review that was withdrawn":         {concluded(domain.ReviewWithdrawn), application.ErrApprovalRequired},
		"a review that rejected the revision": {concluded(domain.ReviewRejected), application.ErrApprovalRequired},
		"other content than the review was of": {
			func(_ *world, _ domain.Review, input *application.ReleaseApproval) {
				input.InputDigest = "sha256:" + strings.Repeat("c", 64)
			},
			domain.ErrReviewDamaged},
		"a check that is gone": {
			func(w *world, _ domain.Review, _ *application.ReleaseApproval) { delete(w.runs, "check") },
			domain.ErrReviewDamaged},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			w, reviews, review := approved(t, approval{})
			input, members := returning(review), &grant{held: true}
			c.arrange(w, review, &input)
			stored := w.reviews[review.Request.ID]
			got, err := reviews.VerifyReleaseApproval(context.Background(), input, members.hold)
			if !errors.Is(err, c.want) || got != (application.VerifiedApproval{}) {
				t.Fatalf("got %+v, %v; want %v and no evidence", got, err, c.want)
			}
			if len(members.asked) != 0 {
				t.Errorf("the grant was asked of %v, want it not asked", members.asked)
			}
			unchanged(t, w, stored)
		})
	}
}
