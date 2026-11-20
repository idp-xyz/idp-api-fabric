package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

var rita = domain.Principal{Issuer: "https://issuer.example", Subject: "rita"}

// submitted stores a request for a review of a new revision whose check
// failed the rules that are named, and returns the review and its check.
func (f *reviewFixture) submitted(at time.Time, failed ...string) (domain.Review, domain.CheckRun) {
	f.t.Helper()
	revision := revisionRef()
	run := f.check(revision, failed...)
	request := f.ask(run, at, domain.Target{Audience: "partner", Environment: "sandbox"})
	if err := f.within(func(ctx context.Context) error { return f.reviews.Request(ctx, request, 2) }); err != nil {
		f.t.Fatal(err)
	}
	return domain.Review{Request: request}, run
}

func (f *reviewFixture) decide(review domain.Review, run domain.CheckRun, decision domain.Decision, at time.Time) domain.Conclusion {
	f.t.Helper()
	conclusion, err := domain.Decide(review, run, domain.Baseline(), []domain.Tool{reviewTool}, decision, []domain.Principal{carol}, rita, at)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.within(func(ctx context.Context) error { return f.reviews.Conclude(ctx, conclusion) }); err != nil {
		f.t.Fatal(err)
	}
	return conclusion
}

// An approval is stored with the time it holds until and with its waivers,
// and read back with them in the order of their rules. A rejection is stored
// with its reason. Neither is stored twice, and both are read under the
// revision of their review.
func TestADecisionIsStoredWithWhatItHoldsForAndWhatItWaives(t *testing.T) {
	f, _ := newReviewFixture(t)
	asked := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	decided := asked.Add(2 * time.Hour)

	review, run := f.submitted(asked, "F04-GOV-003", "F04-GOV-002")
	approval := domain.Decision{
		Outcome: domain.ReviewApproved, Reason: "reviewed with the partner team", ValidUntil: decided.Add(90 * 24 * time.Hour),
		Waivers: []domain.Waiver{
			{RuleID: "F04-GOV-003", Reason: "generated clients are not used yet", ExpiresAt: decided.Add(30 * 24 * time.Hour)},
			{RuleID: "F04-GOV-002", Reason: "the title is settled next sprint", CompensatingControl: "the portal shows the asset name", ExpiresAt: decided.Add(60 * 24 * time.Hour)},
		},
	}
	conclusion := f.decide(review, run, approval, decided)
	if len(conclusion.Waivers) != 2 || conclusion.Waivers[0].RuleID != "F04-GOV-002" {
		t.Fatalf("the model keeps the waivers as %+v, want them in the order of their rules", conclusion.Waivers)
	}
	approved := domain.Review{Request: review.Request, Conclusion: &conclusion}
	if got, found, err := f.reviews.Find(t.Context(), review.Request.Revision, review.Request.ID); err != nil || !found || !reflect.DeepEqual(got, approved) {
		t.Fatalf("read back %+v found %t, want %+v: %v", got.Conclusion, found, conclusion, err)
	}
	if listed, err := f.reviews.List(t.Context(), review.Request.Revision, nil, 10); err != nil || !reflect.DeepEqual(listed, []domain.Review{approved}) {
		t.Errorf("listed %+v, want the approved review: %v", listed, err)
	}
	if err := f.within(func(ctx context.Context) error { return f.reviews.Conclude(ctx, conclusion) }); stateOf(err) != "23505 review_conclusion_pkey" {
		t.Errorf("a second conclusion: got %v, want it refused by the key of the table", err)
	}

	clean, cleanRun := f.submitted(asked)
	plain := f.decide(clean, cleanRun, domain.Decision{Outcome: domain.ReviewApproved, ValidUntil: decided.Add(24 * time.Hour)}, decided)
	if got, _, err := f.reviews.Find(t.Context(), clean.Request.Revision, clean.Request.ID); err != nil ||
		!reflect.DeepEqual(got, domain.Review{Request: clean.Request, Conclusion: &plain}) || got.Conclusion.Waivers != nil || got.Conclusion.Reason != "" {
		t.Errorf("an approval without a reason and without waivers is read back as %+v: %v", got.Conclusion, err)
	}

	refused, refusedRun := f.submitted(asked, "F04-GOV-003")
	rejection := f.decide(refused, refusedRun, domain.Decision{Outcome: domain.ReviewRejected, Reason: "every operation needs its operationId"}, decided)
	rejected := domain.Review{Request: refused.Request, Conclusion: &rejection}
	if got, _, err := f.reviews.Find(t.Context(), refused.Request.Revision, refused.Request.ID); err != nil || !reflect.DeepEqual(got, rejected) ||
		!got.Conclusion.ValidUntil.IsZero() {
		t.Errorf("a rejection is read back as %+v, want %+v: %v", got.Conclusion, rejection, err)
	}
	// A decision is read under the revision of its review.
	if _, found, err := f.reviews.Find(t.Context(), review.Request.Revision, refused.Request.ID); found || err != nil {
		t.Errorf("the rejection under another revision: found %t, %v", found, err)
	}
}

// The tables hold every writer to what a decision is: an approval holds
// until a time after it was given and nothing else holds for a time, a
// rejection says why, a waiver belongs to a conclusion and says why and
// until when, and nothing of either is changed or removed by the role a
// process connects with. What the tables cannot say the model finds out
// when the review is read.
func TestTheTablesHoldADecisionToItsOutcome(t *testing.T) {
	f, database := newReviewFixture(t)
	asked := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	decided := asked.Add(2 * time.Hour)
	app := pgtest.Connect(t, database.Application)

	const conclude = `
INSERT INTO policyreview.review_conclusion (review_id, outcome, reason, concluded_at, concluded_by_issuer, concluded_by_subject, valid_until)
VALUES ($1, $2, $3, $4, 'https://issuer.example', 'rita', $5)`
	refusals := []struct {
		name               string
		outcome            string
		reason, validUntil any
		want               string
	}{
		{"an approval that holds for no time", "APPROVED", nil, nil, "23514 review_conclusion_validity_check"},
		{"an approval that ended when it was given", "APPROVED", nil, decided, "23514 review_conclusion_validity_check"},
		{"a rejection that holds for a time", "REJECTED", "not yet", decided.Add(time.Hour), "23514 review_conclusion_validity_check"},
		{"a withdrawal that holds for a time", "WITHDRAWN", nil, decided.Add(time.Hour), "23514 review_conclusion_validity_check"},
		{"a rejection without a reason", "REJECTED", nil, nil, "23514 review_conclusion_rejection_check"},
		{"an outcome there is not", "ESCALATED", "not yet", nil, "23514 review_conclusion_outcome_check"},
		{"a reason of nothing", "REJECTED", "", nil, "23514 review_conclusion_reason_check"},
	}
	open, _ := f.submitted(asked)
	for _, c := range refusals {
		if _, err := app.Exec(t.Context(), conclude, open.Request.ID, c.outcome, c.reason, decided, c.validUntil); stateOf(err) != c.want {
			t.Errorf("%s: got %v, want it refused by %s", c.name, err, c.want)
		}
	}
	if got, _, err := f.reviews.Find(t.Context(), open.Request.Revision, open.Request.ID); err != nil || got.Conclusion != nil {
		t.Fatalf("the refused conclusions left %+v: %v", got.Conclusion, err)
	}

	review, run := f.submitted(asked, "F04-GOV-003")
	waiver := domain.Waiver{RuleID: "F04-GOV-003", Reason: "generated clients are not used yet", ExpiresAt: decided.Add(30 * 24 * time.Hour)}
	f.decide(review, run, domain.Decision{Outcome: domain.ReviewApproved, ValidUntil: decided.Add(90 * 24 * time.Hour), Waivers: []domain.Waiver{waiver}}, decided)
	const waive = `
INSERT INTO policyreview.review_waiver (review_id, rule_id, reason, compensating_control, expires_at) VALUES ($1, $2, $3, $4, $5)`
	for name, c := range map[string]struct {
		review          string
		rule            string
		reason, control any
		want            string
	}{
		"a waiver of a review without a conclusion": {open.Request.ID, "F04-GOV-003", "accepted", nil, "23503 "},
		"a rule waived twice":                       {review.Request.ID, "F04-GOV-003", "accepted", nil, "23505 review_waiver_pkey"},
		"a waiver without a reason":                 {review.Request.ID, "F04-GOV-002", nil, nil, "23502 "},
		"a waiver whose reason is nothing":          {review.Request.ID, "F04-GOV-002", "", nil, "23514 "},
		"a waiver whose control is nothing":         {review.Request.ID, "F04-GOV-002", "accepted", "", "23514 "},
		"a waiver of no rule":                       {review.Request.ID, "", "accepted", nil, "23514 "},
	} {
		if _, err := app.Exec(t.Context(), waive, c.review, c.rule, c.reason, c.control, decided.Add(time.Hour)); !strings.HasPrefix(stateOf(err), c.want) {
			t.Errorf("%s: got %v, want it refused with %s", name, err, c.want)
		}
	}
	for name, statement := range map[string]string{
		"extend a waiver":       `UPDATE policyreview.review_waiver SET expires_at = expires_at + interval '1 year'`,
		"remove a waiver":       `DELETE FROM policyreview.review_waiver`,
		"extend an approval":    `UPDATE policyreview.review_conclusion SET valid_until = valid_until + interval '1 year'`,
		"turn a decision round": `UPDATE policyreview.review_conclusion SET outcome = 'REJECTED', reason = 'no', valid_until = NULL`,
		"empty the waivers":     `TRUNCATE policyreview.review_waiver`,
	} {
		if _, err := app.Exec(t.Context(), statement); !strings.HasPrefix(stateOf(err), "42501") {
			t.Errorf("%s, as the application role: got %v, want it refused for lack of privilege", name, err)
		}
	}
	if got, _, err := f.reviews.Find(t.Context(), review.Request.Revision, review.Request.ID); err != nil || !reflect.DeepEqual(got.Conclusion.Waivers, []domain.Waiver{waiver}) {
		t.Fatalf("after the refused statements the approval waives %+v, want %+v: %v", got.Conclusion, waiver, err)
	}

	// What the owner of the tables can still write by hand.
	withdrawn, _ := f.submitted(asked)
	taken, err := domain.Withdraw(withdrawn, carol, "", decided)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.within(func(ctx context.Context) error { return f.reviews.Conclude(ctx, taken) }); err != nil {
		t.Fatal(err)
	}
	owner := pgtest.Connect(t, database.Migrator)
	for name, damage := range map[string]struct {
		review    domain.Review
		statement string
	}{
		"a withdrawal that waives a rule": {withdrawn, `
INSERT INTO policyreview.review_waiver (review_id, rule_id, reason, expires_at) VALUES ($1, 'F04-GOV-003', 'accepted', now() + interval '1 day')`},
		"a waiver that outlives its approval": {review, `
UPDATE policyreview.review_waiver SET expires_at = '2099-01-01T00:00:00Z' WHERE review_id = $1`},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := owner.Exec(t.Context(), damage.statement, damage.review.Request.ID); err != nil {
				t.Fatal(err)
			}
			_, _, err := f.reviews.Find(t.Context(), damage.review.Request.Revision, damage.review.Request.ID)
			if !errors.Is(err, domain.ErrReviewDamaged) || !strings.Contains(err.Error(), "review "+damage.review.Request.ID) {
				t.Errorf("read: %v, want %v naming the review", err, domain.ErrReviewDamaged)
			}
		})
	}
}
