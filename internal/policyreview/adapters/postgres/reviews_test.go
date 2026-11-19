package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

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
)

type reviewFixture struct {
	t       *testing.T
	fabric  *db.Database
	checks  *postgres.CheckRuns
	reviews *postgres.Reviews
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
	}, database
}

func revisionRef() domain.RevisionRef {
	return domain.RevisionRef{ProductID: ids.NewUUID(), WorkspaceID: ids.NewUUID(), AssetID: ids.NewUUID(), RevisionID: ids.NewUUID()}
}

// check records a check of the revision under the baseline: one rule passed
// and the others were not run.
func (f *reviewFixture) check(revision domain.RevisionRef) domain.CheckRun {
	f.t.Helper()
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
		}, policy, source, reviewTool, []domain.Observation{{RuleID: "F04-GOV-001"}}, at.Add(time.Second))
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

func (f *reviewFixture) ask(run domain.CheckRun, at time.Time, scope ...domain.Target) domain.ReviewRequest {
	f.t.Helper()
	request, err := domain.RequestReview(ids.NewUUID(), run, domain.Baseline(), []domain.Tool{reviewTool}, scope, "ready for partners", carol, at)
	if err != nil {
		f.t.Fatal(err)
	}
	return request
}

func (f *reviewFixture) within(work func(context.Context) error) error {
	return f.fabric.WithinTransaction(f.t.Context(), work)
}

func stateOf(err error) string {
	var refused *pgconn.PgError
	if errors.As(err, &refused) {
		return refused.Code + " " + refused.ConstraintName
	}
	return ""
}

// A request is stored with its scope and read back as it was asked, under
// the revision it is of and no other. Its conclusion is stored once. A
// revision is in review under one request at a time: a second request at
// the same version of its status is refused, and one at a later version is
// the next review of its history.
func TestAReviewIsStoredOnceAndReadUnderItsRevision(t *testing.T) {
	f, _ := newReviewFixture(t)
	revision := revisionRef()
	run := f.check(revision)
	asked := time.Date(2026, 11, 19, 9, 0, 0, 0, time.UTC)
	partner := domain.Target{Audience: "partner", Environment: "sandbox"}
	request := f.ask(run, asked, partner, domain.Target{Audience: "internal", Environment: "production"})

	if err := f.reviews.Request(t.Context(), request, 2); err == nil {
		t.Fatal("a request was stored outside a transaction")
	}
	if err := f.within(func(ctx context.Context) error { return f.reviews.Request(ctx, request, 2) }); err != nil {
		t.Fatal(err)
	}
	open := domain.Review{Request: request}
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
	twice := f.ask(run, asked.Add(time.Minute), partner)
	err := f.within(func(ctx context.Context) error { return f.reviews.Request(ctx, twice, 2) })
	if stateOf(err) != "23505 review_request_once_key" {
		t.Errorf("a second request at the same version: got %v, want it refused by review_request_once_key", err)
	}

	conclusion, err := domain.Withdraw(open, carol, "the wrong baseline", asked.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.reviews.Conclude(t.Context(), conclusion); err == nil {
		t.Fatal("a conclusion was stored outside a transaction")
	}
	if err := f.within(func(ctx context.Context) error { return f.reviews.Conclude(ctx, conclusion) }); err != nil {
		t.Fatal(err)
	}
	concluded := domain.Review{Request: request, Conclusion: &conclusion}
	if got, _, err := f.reviews.Find(t.Context(), revision, request.ID); err != nil || !reflect.DeepEqual(got, concluded) {
		t.Fatalf("read back %+v, want %+v: %v", got, concluded, err)
	}
	err = f.within(func(ctx context.Context) error { return f.reviews.Conclude(ctx, conclusion) })
	if stateOf(err) != "23505 review_conclusion_pkey" {
		t.Errorf("a second conclusion: got %v, want it refused by the key of the table", err)
	}

	// The next review of the revision, and the history in the order it was
	// asked in, page by page.
	again := f.ask(run, asked.Add(2*time.Hour), partner)
	if err := f.within(func(ctx context.Context) error { return f.reviews.Request(ctx, again, 4) }); err != nil {
		t.Fatal(err)
	}
	history := []domain.Review{concluded, {Request: again}}
	if listed, err := f.reviews.List(t.Context(), revision, nil, 10); err != nil || !reflect.DeepEqual(listed, history) {
		t.Fatalf("the history is %+v, want %+v: %v", listed, history, err)
	}
	first, err := f.reviews.List(t.Context(), revision, nil, 1)
	if err != nil || len(first) != 1 || first[0].Request.ID != request.ID {
		t.Fatalf("the first page of one: %+v, %v", first, err)
	}
	behind, err := f.reviews.List(t.Context(), revision, &ports.Position{At: request.RequestedAt, ID: request.ID}, 10)
	if err != nil || len(behind) != 1 || behind[0].Request.ID != again.ID {
		t.Fatalf("the page behind the first review: %+v, %v", behind, err)
	}
	if last, err := f.reviews.List(t.Context(), revision, &ports.Position{At: again.RequestedAt, ID: again.ID}, 10); err != nil || len(last) != 0 {
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
	err := f.within(func(ctx context.Context) error { return f.reviews.Request(ctx, misplaced, 2) })
	if stateOf(err) != "23503 review_request_check_fkey" {
		t.Errorf("a request on the check of another revision: got %v, want it refused by review_request_check_fkey", err)
	}
	// A revision is recorded at version 1, so no request finds it there.
	err = f.within(func(ctx context.Context) error { return f.reviews.Request(ctx, f.ask(run, asked, partner), 1) })
	if !strings.HasPrefix(stateOf(err), "23514 ") {
		t.Errorf("a request at version 1 of the status: got %v, want a check violation", err)
	}
	foreignScope := f.ask(run, asked, partner)
	foreignScope.Scope = []domain.Target{{Audience: "customers", Environment: "sandbox"}}
	err = f.within(func(ctx context.Context) error { return f.reviews.Request(ctx, foreignScope, 2) })
	if !strings.HasPrefix(stateOf(err), "23514 ") {
		t.Errorf("a scope with an audience there is not: got %v, want a check violation", err)
	}
	if listed, err := f.reviews.List(t.Context(), revision, nil, 10); err != nil || len(listed) != 0 {
		t.Fatalf("the refused requests left %d reviews: %v", len(listed), err)
	}

	request := f.ask(run, asked, partner)
	if err := f.within(func(ctx context.Context) error { return f.reviews.Request(ctx, request, 2) }); err != nil {
		t.Fatal(err)
	}
	conclusion, err := domain.Withdraw(domain.Review{Request: request}, carol, "", asked.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.within(func(ctx context.Context) error { return f.reviews.Conclude(ctx, conclusion) }); err != nil {
		t.Fatal(err)
	}

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
