package postgres_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// A supplementary request of before is read back as one. It recorded the
// version the status of the approved revision had, which no review changed,
// so the requests of an approved revision share it; the table held a request
// that put a revision in review to being the only one at its version.
func TestSupplementaryRequestsShareTheVersionOfTheApprovedRevision(t *testing.T) {
	f, database := newReviewFixture(t)
	revision := revisionRef()
	run := f.check(revision)
	asked := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	sandbox := domain.Target{Audience: "partner", Environment: "sandbox"}
	production := domain.Target{Audience: "partner", Environment: "production"}
	supplementary := func(at time.Time) domain.ReviewRequest {
		request := f.ask(run, at, production)
		request.Supplementary = true
		return request
	}

	// The review that approved the revision: asked at version 2 of its
	// status, and decided, which left the revision approved at version 3.
	first := f.asked(f.ask(run, asked, sandbox), 2)
	first = f.concluded(first, approval(first, "", asked.Add(time.Hour), asked.Add(48*time.Hour)))

	// Other reviews of the approved revision, at the version it has: one
	// withdrawn, and two more, one taken while the other is open. That a
	// revision was under one review at a time was, for these, for the use
	// case to see to behind a hold of the revision.
	second := f.asked(supplementary(asked.Add(2*time.Hour)), 3)
	if got, found, err := f.reviews.Find(t.Context(), revision, second.Request.ID); err != nil || !found || !reflect.DeepEqual(got, second) || !got.Request.Supplementary {
		t.Fatalf("read back %+v found %t, want %+v: %v", got, found, second, err)
	}
	if got, _, err := f.reviews.Find(t.Context(), revision, first.Request.ID); err != nil || !reflect.DeepEqual(got, first) || got.Request.Supplementary {
		t.Fatalf("the first review reads %+v, want %+v: %v", got, first, err)
	}
	second = f.concluded(second, withdrawal(second, carol, "the wrong scope", asked.Add(3*time.Hour)))
	third := f.asked(supplementary(asked.Add(4*time.Hour)), 3)
	fourth := f.asked(supplementary(asked.Add(5*time.Hour)), 3)

	// A request that puts a revision in review is the only one at its
	// version, whatever supplementary requests share it.
	entering := f.ask(run, asked.Add(6*time.Hour), sandbox)
	if err := f.record(entering, 2); stateOf(err) != "23505 review_request_once_key" {
		t.Errorf("a second request at the version the first one put the revision in review at: got %v, want it refused by review_request_once_key", err)
	}
	if err := f.record(entering, 3); err != nil {
		t.Errorf("a request at a version only supplementary requests have: %v", err)
	}
	if err := f.record(f.ask(run, asked.Add(7*time.Hour), sandbox), 3); stateOf(err) != "23505 review_request_once_key" {
		t.Errorf("a second request of its kind at that version: got %v, want it refused by review_request_once_key", err)
	}

	history := []domain.Review{first, second, third, fourth, {Request: entering}}
	if listed, err := f.reviews.List(t.Context(), revision, nil, 10); err != nil || !reflect.DeepEqual(listed, history) {
		t.Fatalf("the history is %+v, want %+v: %v", listed, history, err)
	}

	// A build before supplementary reviews named no such column: what it
	// recorded is a request of the kind there was.
	other := revisionRef()
	earlier, id := f.check(other), ids.NewUUID()
	if _, err := f.owner.Exec(t.Context(), `
INSERT INTO policyreview.review_request (
    review_id, product_id, workspace_id, api_asset_id, revision_id, revision_status_version, check_run_id,
    input_digest, note, requested_at, requested_by_issuer, requested_by_subject
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id, other.ProductID, other.WorkspaceID, other.AssetID, other.RevisionID, 2,
		earlier.ID, earlier.InputDigest, nil, asked, carol.Issuer, carol.Subject); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(t.Context(), `INSERT INTO policyreview.review_scope (review_id, audience, environment) VALUES ($1, 'partner', 'sandbox')`, id); err != nil {
		t.Fatal(err)
	}
	if got, found, err := f.reviews.Find(t.Context(), other, id); err != nil || !found || got.Request.Supplementary {
		t.Errorf("a request recorded without the column reads %+v found %t: %v; want one that is not supplementary", got.Request, found, err)
	}

	// What a request is does not change afterwards.
	app := pgtest.Connect(t, database.Application)
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
// writes the row: the owner of the table is not exempt from the column. The
// application role records no request since CR-0001, and is refused the row
// for lack of privilege before the column is looked at.
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
		want string
	}{
		{"the application role", pgtest.Connect(t, database.Application), "42501"},
		{"the owner of the table", pgtest.Connect(t, database.Migrator), "23502"},
	} {
		revision := revisionRef()
		run := f.check(revision)
		if _, err := writer.conn.Exec(t.Context(), insert, ids.NewUUID(), revision.ProductID, revision.WorkspaceID,
			revision.AssetID, revision.RevisionID, run.ID, run.InputDigest, asked, carol.Issuer, carol.Subject); !strings.HasPrefix(stateOf(err), writer.want) {
			t.Errorf("%s inserting a request with supplementary null: got %v, want it refused (%s)", writer.name, err, writer.want)
		}
	}
}
