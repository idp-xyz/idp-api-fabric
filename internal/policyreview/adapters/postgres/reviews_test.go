package postgres_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

var (
	reviewTool = domain.Tool{Name: "fabric-openapi-rules", Version: "test"}
	carol      = domain.Principal{Issuer: "https://issuer.example", Subject: "carol"}
	rita       = domain.Principal{Issuer: "https://issuer.example", Subject: "rita"}
)

type reviewFixture struct {
	t       *testing.T
	fabric  *db.Database
	checks  *postgres.CheckRuns
	reviews *postgres.Reviews
	// app is a connection of the role a process connects with, which the
	// reviews of before are recorded under.
	app *pgx.Conn
}

func newReviewFixture(t *testing.T) (*reviewFixture, pgtest.Database) {
	t.Helper()
	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fabric.Close)
	return &reviewFixture{
		t: t, fabric: fabric, checks: postgres.NewCheckRuns(fabric.Framework), reviews: postgres.NewReviews(fabric.Framework),
		app: pgtest.Connect(t, database.Application),
	}, database
}

func revisionRef() domain.RevisionRef {
	return domain.RevisionRef{ProductID: ids.NewUUID(), WorkspaceID: ids.NewUUID(), AssetID: ids.NewUUID(), RevisionID: ids.NewUUID()}
}

// check records a check of the revision under the baseline: one rule passed,
// the rules that are named failed, and the others were not run.
func (f *reviewFixture) check(revision domain.RevisionRef, failed ...string) domain.CheckRun {
	f.t.Helper()
	observed := []domain.Observation{{RuleID: "F04-GOV-001"}}
	for _, rule := range failed {
		observed = append(observed, domain.Observation{RuleID: rule, Findings: []domain.Finding{{
			RuleID: rule, Path: "openapi.yaml", Line: 1, Message: "the rule is broken here",
		}}})
	}
	policy := domain.Baseline()
	source := domain.Source{
		Entrypoint: "openapi.yaml", Digest: "sha256:" + strings.Repeat("a", 64),
		Files: []domain.SourceFile{{Path: "openapi.yaml", Content: "openapi: 3.1.2\n"}},
	}
	at := time.Now().UTC()
	var run domain.CheckRun
	err := f.fabric.WithinTransaction(f.t.Context(), func(ctx context.Context) error {
		snapshot, err := f.checks.Snapshot(ctx, revision.ProductID, policy, ids.NewUUID(), at)
		if err != nil {
			return err
		}
		run, err = domain.Complete(domain.Draft{
			ID: ids.NewUUID(), PolicySnapshotID: snapshot, Revision: revision, RequestedBy: carol, StartedAt: at,
		}, policy, source, reviewTool, observed, at.Add(time.Second))
		if err != nil {
			return err
		}
		return f.checks.Record(ctx, run)
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return run
}

// No review is asked for or concluded any more (CR-0001), and the adapter
// only reads them. The reviews these tests read are written as the build
// before recorded them, with the statements it ran.

// instant is a time as the tables keep it.
func instant(at time.Time) time.Time { return at.UTC().Truncate(time.Microsecond) }

// ask is a request carol made of the revision of the check, for the scope,
// as the model kept it: the scope in the order of its audiences, then of its
// environments.
func (f *reviewFixture) ask(run domain.CheckRun, at time.Time, scope ...domain.Target) domain.ReviewRequest {
	ordered := slices.Clone(scope)
	slices.SortFunc(ordered, func(a, b domain.Target) int {
		return cmp.Or(strings.Compare(a.Audience, b.Audience), strings.Compare(a.Environment, b.Environment))
	})
	return domain.ReviewRequest{
		ID: ids.NewUUID(), Revision: run.Revision, CheckRunID: run.ID, InputDigest: run.InputDigest, Scope: ordered,
		Note: "ready for partners", RequestedBy: carol, RequestedAt: instant(at),
	}
}

// withdrawal, approval and rejection are the conclusions a review could
// have, as the model gave them: an approval waives its rules in their order.
func withdrawal(review domain.Review, by domain.Principal, reason string, at time.Time) domain.Conclusion {
	return domain.Conclusion{ReviewID: review.Request.ID, Outcome: domain.ReviewWithdrawn, Reason: reason, ConcludedBy: by, ConcludedAt: instant(at)}
}

func approval(review domain.Review, reason string, at, until time.Time, waivers ...domain.Waiver) domain.Conclusion {
	conclusion := domain.Conclusion{
		ReviewID: review.Request.ID, Outcome: domain.ReviewApproved, Reason: reason, ConcludedBy: rita,
		ConcludedAt: instant(at), ValidUntil: instant(until),
	}
	for _, waiver := range waivers {
		waiver.ExpiresAt = instant(waiver.ExpiresAt)
		conclusion.Waivers = append(conclusion.Waivers, waiver)
	}
	slices.SortFunc(conclusion.Waivers, func(a, b domain.Waiver) int { return strings.Compare(a.RuleID, b.RuleID) })
	return conclusion
}

func rejection(review domain.Review, reason string, at time.Time) domain.Conclusion {
	return domain.Conclusion{ReviewID: review.Request.ID, Outcome: domain.ReviewRejected, Reason: reason, ConcludedBy: rita, ConcludedAt: instant(at)}
}

// nullable is what the tables keep of a word nobody said.
func nullable(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// record stores a request, as the build before did when it was asked for:
// at the version the status of the revision took, with its scope.
func (f *reviewFixture) record(request domain.ReviewRequest, version int64) error {
	tx, err := f.app.Begin(f.t.Context())
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(f.t.Context()) }()
	revision := request.Revision
	if _, err := tx.Exec(f.t.Context(), `
INSERT INTO policyreview.review_request (
    review_id, product_id, workspace_id, api_asset_id, revision_id, revision_status_version, check_run_id,
    input_digest, note, requested_at, requested_by_issuer, requested_by_subject, supplementary
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, request.ID, revision.ProductID, revision.WorkspaceID, revision.AssetID,
		revision.RevisionID, version, request.CheckRunID, request.InputDigest, nullable(request.Note),
		request.RequestedAt, request.RequestedBy.Issuer, request.RequestedBy.Subject, request.Supplementary); err != nil {
		return fmt.Errorf("store a review request: %w", err)
	}
	var audiences, environments []string
	for _, target := range request.Scope {
		audiences, environments = append(audiences, target.Audience), append(environments, target.Environment)
	}
	if _, err := tx.Exec(f.t.Context(), `
INSERT INTO policyreview.review_scope (review_id, audience, environment)
SELECT $1::uuid, * FROM unnest($2::text[], $3::text[])`, request.ID, audiences, environments); err != nil {
		return fmt.Errorf("store the scope of a review: %w", err)
	}
	return tx.Commit(f.t.Context())
}

// conclude stores a conclusion, with the waivers of an approval, as the build
// before did when the review ended.
func (f *reviewFixture) conclude(conclusion domain.Conclusion) error {
	tx, err := f.app.Begin(f.t.Context())
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(f.t.Context()) }()
	var validUntil *time.Time
	if !conclusion.ValidUntil.IsZero() {
		validUntil = &conclusion.ValidUntil
	}
	if _, err := tx.Exec(f.t.Context(), `
INSERT INTO policyreview.review_conclusion (
    review_id, outcome, reason, concluded_at, concluded_by_issuer, concluded_by_subject, valid_until
) VALUES ($1,$2,$3,$4,$5,$6,$7)`, conclusion.ReviewID, string(conclusion.Outcome), nullable(conclusion.Reason),
		conclusion.ConcludedAt, conclusion.ConcludedBy.Issuer, conclusion.ConcludedBy.Subject, validUntil); err != nil {
		return fmt.Errorf("store the conclusion of a review: %w", err)
	}
	if len(conclusion.Waivers) > 0 {
		var (
			rules, reasons []string
			controls       []*string
			expiries       []time.Time
		)
		for _, waiver := range conclusion.Waivers {
			rules, reasons = append(rules, waiver.RuleID), append(reasons, waiver.Reason)
			controls, expiries = append(controls, nullable(waiver.CompensatingControl)), append(expiries, waiver.ExpiresAt)
		}
		if _, err := tx.Exec(f.t.Context(), `
INSERT INTO policyreview.review_waiver (review_id, rule_id, reason, compensating_control, expires_at)
SELECT $1::uuid, * FROM unnest($2::text[], $3::text[], $4::text[], $5::timestamptz[])`,
			conclusion.ReviewID, rules, reasons, controls, expiries); err != nil {
			return fmt.Errorf("store the waivers of a review: %w", err)
		}
	}
	return tx.Commit(f.t.Context())
}

// asked stores the request and fails the test if it cannot be stored.
func (f *reviewFixture) asked(request domain.ReviewRequest, version int64) domain.Review {
	f.t.Helper()
	if err := f.record(request, version); err != nil {
		f.t.Fatal(err)
	}
	return domain.Review{Request: request}
}

// concluded stores the conclusion of the review and fails the test if it
// cannot be stored.
func (f *reviewFixture) concluded(review domain.Review, conclusion domain.Conclusion) domain.Review {
	f.t.Helper()
	if err := f.conclude(conclusion); err != nil {
		f.t.Fatal(err)
	}
	review.Conclusion = &conclusion
	return review
}

func stateOf(err error) string {
	var refused *pgconn.PgError
	if errors.As(err, &refused) {
		return refused.Code + " " + refused.ConstraintName
	}
	return ""
}

// A review that was recorded is read back as it was asked, with its scope and
// its conclusion, under the revision it is of and no other, and the history
// of a revision is its reviews in the order they were asked for. The tables
// held a revision to one request at a time, at the version its status took,
// and a review to one conclusion.
func TestAReviewIsReadBackAsItWasRecordedUnderItsRevision(t *testing.T) {
	f, _ := newReviewFixture(t)
	revision := revisionRef()
	run := f.check(revision)
	asked := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	partner := domain.Target{Audience: "partner", Environment: "sandbox"}
	request := f.ask(run, asked, partner, domain.Target{Audience: "internal", Environment: "production"})

	open := f.asked(request, 2)
	if got, found, err := f.reviews.Find(t.Context(), revision, request.ID); err != nil || !found || !reflect.DeepEqual(got, open) {
		t.Fatalf("read back %+v found %t, want %+v: %v", got, found, open, err)
	}

	elsewhere := map[string]domain.RevisionRef{
		"another revision":  {ProductID: revision.ProductID, WorkspaceID: revision.WorkspaceID, AssetID: revision.AssetID, RevisionID: ids.NewUUID()},
		"another asset":     {ProductID: revision.ProductID, WorkspaceID: revision.WorkspaceID, AssetID: ids.NewUUID(), RevisionID: revision.RevisionID},
		"another workspace": {ProductID: revision.ProductID, WorkspaceID: ids.NewUUID(), AssetID: revision.AssetID, RevisionID: revision.RevisionID},
		"another product":   {ProductID: ids.NewUUID(), WorkspaceID: revision.WorkspaceID, AssetID: revision.AssetID, RevisionID: revision.RevisionID},
	}
	for name, other := range elsewhere {
		if _, found, err := f.reviews.Find(t.Context(), other, request.ID); found || err != nil {
			t.Errorf("under %s: found %t, %v; want it not found", name, found, err)
		}
		if listed, err := f.reviews.List(t.Context(), other, nil, 10); err != nil || len(listed) != 0 {
			t.Errorf("under %s: listed %d reviews, %v; want none", name, len(listed), err)
		}
	}
	if _, found, err := f.reviews.Find(t.Context(), revision, ids.NewUUID()); found || err != nil {
		t.Errorf("a review nobody asked for: found %t, %v", found, err)
	}

	// One request at a version of the status of the revision.
	if err := f.record(f.ask(run, asked.Add(time.Minute), partner), 2); stateOf(err) != "23505 review_request_once_key" {
		t.Errorf("a second request at the same version: got %v, want it refused by review_request_once_key", err)
	}

	concluded := f.concluded(open, withdrawal(open, carol, "the wrong baseline", asked.Add(time.Hour)))
	if got, _, err := f.reviews.Find(t.Context(), revision, request.ID); err != nil || !reflect.DeepEqual(got, concluded) {
		t.Fatalf("read back %+v, want %+v: %v", got, concluded, err)
	}
	if err := f.conclude(*concluded.Conclusion); stateOf(err) != "23505 review_conclusion_pkey" {
		t.Errorf("a second conclusion: got %v, want it refused by the key of the table", err)
	}

	// The next review of the revision, and the history in the order it was
	// asked in, page by page.
	again := f.asked(f.ask(run, asked.Add(2*time.Hour), partner), 4)
	history := []domain.Review{concluded, again}
	if listed, err := f.reviews.List(t.Context(), revision, nil, 10); err != nil || !reflect.DeepEqual(listed, history) {
		t.Fatalf("the history is %+v, want %+v: %v", listed, history, err)
	}
	first, err := f.reviews.List(t.Context(), revision, nil, 1)
	if err != nil || len(first) != 1 || first[0].Request.ID != request.ID {
		t.Fatalf("the first page of one: %+v, %v", first, err)
	}
	behind, err := f.reviews.List(t.Context(), revision, &ports.Position{At: request.RequestedAt, ID: request.ID}, 10)
	if err != nil || len(behind) != 1 || behind[0].Request.ID != again.Request.ID {
		t.Fatalf("the page behind the first review: %+v, %v", behind, err)
	}
	if last, err := f.reviews.List(t.Context(), revision, &ports.Position{At: again.Request.RequestedAt, ID: again.Request.ID}, 10); err != nil || len(last) != 0 {
		t.Errorf("the page behind the last review: %+v, %v", last, err)
	}
}

// The tables hold every writer to what a review is: it rests on a check of
// its own revision, its scope is audiences in environments, and nothing of
// it is changed or removed by the role a process connects with. A review
// whose rows are not what the model recorded is not read as one.
func TestTheTablesHoldAReviewToItsCheckAndItsScope(t *testing.T) {
	f, database := newReviewFixture(t)
	revision, other := revisionRef(), revisionRef()
	run, foreign := f.check(revision), f.check(other)
	asked := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	partner := domain.Target{Audience: "partner", Environment: "sandbox"}

	// A check of another revision is no evidence for this one.
	misplaced := f.ask(run, asked, partner)
	misplaced.CheckRunID = foreign.ID
	if err := f.record(misplaced, 2); stateOf(err) != "23503 review_request_check_fkey" {
		t.Errorf("a request on the check of another revision: got %v, want it refused by review_request_check_fkey", err)
	}
	// A revision is recorded at version 1, so no request finds it there.
	if err := f.record(f.ask(run, asked, partner), 1); !strings.HasPrefix(stateOf(err), "23514 ") {
		t.Errorf("a request at version 1 of the status: got %v, want a check violation", err)
	}
	foreignScope := f.ask(run, asked, partner)
	foreignScope.Scope = []domain.Target{{Audience: "customers", Environment: "sandbox"}}
	if err := f.record(foreignScope, 2); !strings.HasPrefix(stateOf(err), "23514 ") {
		t.Errorf("a scope with an audience there is not: got %v, want a check violation", err)
	}
	if listed, err := f.reviews.List(t.Context(), revision, nil, 10); err != nil || len(listed) != 0 {
		t.Fatalf("the refused requests left %d reviews: %v", len(listed), err)
	}

	open := f.asked(f.ask(run, asked, partner), 2)
	request := open.Request
	f.concluded(open, withdrawal(open, carol, "", asked.Add(time.Hour)))

	app := pgtest.Connect(t, database.Application)
	for name, statement := range map[string]string{
		"change who asked":       `UPDATE policyreview.review_request SET requested_by_subject = 'mallory'`,
		"remove a request":       `DELETE FROM policyreview.review_request`,
		"widen a scope":          `UPDATE policyreview.review_scope SET audience = 'public'`,
		"remove a scope":         `DELETE FROM policyreview.review_scope`,
		"rewrite a conclusion":   `UPDATE policyreview.review_conclusion SET outcome = 'WITHDRAWN', reason = 'another'`,
		"remove a conclusion":    `DELETE FROM policyreview.review_conclusion`,
		"empty the conclusions":  `TRUNCATE policyreview.review_conclusion`,
		"move a check elsewhere": `UPDATE policyreview.check_run SET revision_id = gen_random_uuid()`,
	} {
		if _, err := app.Exec(t.Context(), statement); !strings.HasPrefix(stateOf(err), "42501") {
			t.Errorf("%s, as the application role: got %v, want it refused for lack of privilege", name, err)
		}
	}
	if _, err := app.Exec(t.Context(), `
INSERT INTO policyreview.review_conclusion (review_id, outcome, concluded_at, concluded_by_issuer, concluded_by_subject)
VALUES (gen_random_uuid(), 'WITHDRAWN', now(), 'https://issuer.example', 'carol')`); !strings.HasPrefix(stateOf(err), "23503") {
		t.Errorf("a conclusion of no review: got %v, want a foreign key violation", err)
	}

	// What the owner of the tables can still do by hand is found out when
	// the review is read.
	owner := pgtest.Connect(t, database.Migrator)
	for name, damage := range map[string]string{
		"the scope is gone":                   `DELETE FROM policyreview.review_scope WHERE review_id = $1`,
		"the conclusion precedes the request": `UPDATE policyreview.review_conclusion SET concluded_at = '2026-11-19T08:00:00Z' WHERE review_id = $1`,
	} {
		t.Run(name, func(t *testing.T) {
			tx, err := owner.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(t.Context()) }()
			if _, err := tx.Exec(t.Context(), damage, request.ID); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			_, _, err = f.reviews.Find(t.Context(), revision, request.ID)
			if !errors.Is(err, domain.ErrReviewDamaged) || !strings.Contains(err.Error(), "review "+request.ID) {
				t.Errorf("read: %v, want %v naming the review", err, domain.ErrReviewDamaged)
			}
			if _, err := f.reviews.List(t.Context(), revision, nil, 10); !errors.Is(err, domain.ErrReviewDamaged) {
				t.Errorf("listed: %v, want %v", err, domain.ErrReviewDamaged)
			}
		})
	}
}
