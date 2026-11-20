package postgres_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// A supplementary request is stored as one and read back as one. It records
// the version the status of the approved revision has, which no review
// changes, so the requests of an approved revision share it; a request that
// puts a revision in review is still the only one at its version. Whether a
// review of the revision is open is read from what has no conclusion.
func TestSupplementaryRequestsShareTheVersionOfTheApprovedRevision(t *testing.T) {
	f, database := newReviewFixture(t)
	revision := revisionRef()
	run := f.check(revision)
	asked := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	sandbox := domain.Target{Audience: "partner", Environment: "sandbox"}
	production := domain.Target{Audience: "partner", Environment: "production"}
	store := func(request domain.ReviewRequest, version int64) error {
		return f.within(func(ctx context.Context) error { return f.reviews.Request(ctx, request, version) })
	}
	open := func(of domain.RevisionRef) bool {
		t.Helper()
		found, err := f.reviews.Open(t.Context(), of)
		if err != nil {
			t.Fatal(err)
		}
		return found
	}
	supplementary := func(at time.Time) domain.ReviewRequest {
		request := f.ask(run, at, production)
		request.Supplementary = true
		return request
	}

	if open(revision) {
		t.Fatal("a revision nobody asked a review of has one open")
	}
	// The review that approves the revision: asked at version 2 of its
	// status, and decided, which leaves the revision approved at version 3.
	first := domain.Review{Request: f.ask(run, asked, sandbox)}
	if err := store(first.Request, 2); err != nil {
		t.Fatal(err)
	}
	if !open(revision) {
		t.Error("the review that was asked for is not open")
	}
	approval := f.decide(first, run, domain.Decision{Outcome: domain.ReviewApproved, ValidUntil: asked.Add(48 * time.Hour)}, asked.Add(time.Hour))
	first.Conclusion = &approval
	if open(revision) {
		t.Error("a review is open after the only one was decided")
	}

	// Another review of the approved revision, at the version it has.
	second := domain.Review{Request: supplementary(asked.Add(2 * time.Hour))}
	if err := store(second.Request, 3); err != nil {
		t.Fatal(err)
	}
	if got, found, err := f.reviews.Find(t.Context(), revision, second.Request.ID); err != nil || !found || !reflect.DeepEqual(got, second) || !got.Request.Supplementary {
		t.Fatalf("read back %+v found %t, want %+v: %v", got, found, second, err)
	}
	if got, _, err := f.reviews.Find(t.Context(), revision, first.Request.ID); err != nil || !reflect.DeepEqual(got, first) || got.Request.Supplementary {
		t.Fatalf("the first review reads %+v, want %+v: %v", got, first, err)
	}
	if !open(revision) {
		t.Error("the supplementary review that was asked for is not open")
	}
	// A review is open under its revision and no other.
	for name, other := range map[string]domain.RevisionRef{
		"another revision":  {ProductID: revision.ProductID, WorkspaceID: revision.WorkspaceID, AssetID: revision.AssetID, RevisionID: ids.NewUUID()},
		"another asset":     {ProductID: revision.ProductID, WorkspaceID: revision.WorkspaceID, AssetID: ids.NewUUID(), RevisionID: revision.RevisionID},
		"another workspace": {ProductID: revision.ProductID, WorkspaceID: ids.NewUUID(), AssetID: revision.AssetID, RevisionID: revision.RevisionID},
		"another product":   {ProductID: ids.NewUUID(), WorkspaceID: revision.WorkspaceID, AssetID: revision.AssetID, RevisionID: revision.RevisionID},
	} {
		if open(other) {
			t.Errorf("under %s a review is open", name)
		}
	}
	withdrawal, err := domain.Withdraw(second, carol, "the wrong scope", asked.Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.within(func(ctx context.Context) error { return f.reviews.Conclude(ctx, withdrawal) }); err != nil {
		t.Fatal(err)
	}
	second.Conclusion = &withdrawal
	if open(revision) {
		t.Error("a review is open after the supplementary one was withdrawn")
	}

	// The next one records the same version: the revision did not move.
	third := domain.Review{Request: supplementary(asked.Add(4 * time.Hour))}
	if err := store(third.Request, 3); err != nil {
		t.Fatalf("a second supplementary request at the version of the approved revision: %v", err)
	}
	// The table takes one more while that one is open. That a revision is
	// under one review at a time is, for these, for the use case to see to
	// behind a hold of the revision.
	fourth := domain.Review{Request: supplementary(asked.Add(5 * time.Hour))}
	if err := store(fourth.Request, 3); err != nil {
		t.Errorf("a supplementary request while another is open: %v, want the table to take it", err)
	}

	// A request that puts a revision in review is the only one at its
	// version, whatever supplementary requests share it.
	entering := f.ask(run, asked.Add(6*time.Hour), sandbox)
	if err := store(entering, 2); stateOf(err) != "23505 review_request_once_key" {
		t.Errorf("a second request at the version the first one put the revision in review at: got %v, want it refused by review_request_once_key", err)
	}
	if err := store(entering, 3); err != nil {
		t.Errorf("a request at a version only supplementary requests have: %v", err)
	}
	if err := store(f.ask(run, asked.Add(7*time.Hour), sandbox), 3); stateOf(err) != "23505 review_request_once_key" {
		t.Errorf("a second request of its kind at that version: got %v, want it refused by review_request_once_key", err)
	}

	history := []domain.Review{first, second, third, fourth, {Request: entering}}
	if listed, err := f.reviews.List(t.Context(), revision, nil, 10); err != nil || !reflect.DeepEqual(listed, history) {
		t.Fatalf("the history is %+v, want %+v: %v", listed, history, err)
	}

	// A build before this one names no such column: what it records is a
	// request of the kind there was.
	app := pgtest.Connect(t, database.Application)
	other := revisionRef()
	earlier, id := f.check(other), ids.NewUUID()
	if _, err := app.Exec(t.Context(), `
INSERT INTO policyreview.review_request (
    review_id, product_id, workspace_id, api_asset_id, revision_id, revision_status_version, check_run_id,
    input_digest, note, requested_at, requested_by_issuer, requested_by_subject
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id, other.ProductID, other.WorkspaceID, other.AssetID, other.RevisionID, 2,
		earlier.ID, earlier.InputDigest, nil, asked, carol.Issuer, carol.Subject); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(t.Context(), `INSERT INTO policyreview.review_scope (review_id, audience, environment) VALUES ($1, 'partner', 'sandbox')`, id); err != nil {
		t.Fatal(err)
	}
	if got, found, err := f.reviews.Find(t.Context(), other, id); err != nil || !found || got.Request.Supplementary {
		t.Errorf("a request recorded without the column reads %+v found %t: %v; want one that is not supplementary", got.Request, found, err)
	}

	// What a request is does not change afterwards.
	for name, statement := range map[string]string{
		"make a request supplementary":      `UPDATE policyreview.review_request SET supplementary = true`,
		"make a supplementary request none": `UPDATE policyreview.review_request SET supplementary = false`,
	} {
		if _, err := app.Exec(t.Context(), statement); !strings.HasPrefix(stateOf(err), "42501") {
			t.Errorf("%s, as the application role: got %v, want it refused for lack of privilege", name, err)
		}
	}
}

// A request names whether it is supplementary. The column has no reading
// for NULL, so the table refuses a row that leaves it unset, whoever
// writes the row. The application role records requests; the owner of the
// table is not exempt from the column either.
func TestARequestThatLeavesSupplementaryUnsetIsRefused(t *testing.T) {
	f, database := newReviewFixture(t)
	asked := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	const insert = `
INSERT INTO policyreview.review_request (
    review_id, product_id, workspace_id, api_asset_id, revision_id, revision_status_version, check_run_id,
    input_digest, note, requested_at, requested_by_issuer, requested_by_subject, supplementary
) VALUES ($1,$2,$3,$4,$5,2,$6,$7,NULL,$8,$9,$10,NULL)`
	for _, writer := range []struct {
		name string
		conn *pgx.Conn
	}{
		{"the application role", pgtest.Connect(t, database.Application)},
		{"the owner of the table", pgtest.Connect(t, database.Migrator)},
	} {
		revision := revisionRef()
		run := f.check(revision)
		if _, err := writer.conn.Exec(t.Context(), insert, ids.NewUUID(), revision.ProductID, revision.WorkspaceID,
			revision.AssetID, revision.RevisionID, run.ID, run.InputDigest, asked, carol.Issuer, carol.Subject); !strings.HasPrefix(stateOf(err), "23502") {
			t.Errorf("%s inserting a request with supplementary null: got %v, want it refused (23502)", writer.name, err)
		}
	}
}
