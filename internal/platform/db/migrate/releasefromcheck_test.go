package migrate_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/migrate"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

// The steps with which a release is prepared on a check and the reviews that
// were still open are withdrawn (CR-0001). Each context changes its own
// tables, so there are three. A test of a build before them leaves them out
// of the plan of that build, as it leaves out the steps that came after the
// one it is about.
const (
	releaseFromCheck   = "contractregistry/005_release_from_check"
	revisionsWithdrawn = "contractregistry/006_open_reviews_withdrawn"
	reviewsWithdrawn   = "policyreview/007_open_reviews_withdrawn"
)

var checkRelease = []string{releaseFromCheck, revisionsWithdrawn, reviewsWithdrawn}

// A database the build before CR-0001 has migrated and written to is brought
// up to date with what it holds. A revision that is in review is withdrawn,
// one step and at the next version of its status, and the review it is in is
// concluded as withdrawn, for a reason that names the change and by a
// principal no token is issued for, never before it was asked. A
// supplementary review that is open is withdrawn as well and leaves its
// revision approved. What was concluded before, and every revision that is
// not in review, is what it was. A release of that build keeps its review
// and waives nothing of its own; one of this build names no review and
// carries its waivers, which the table keeps to an array and keeps off a
// release that names a review. The steps grant nothing: a process of this
// build starts on the role as that build granted it, and that build goes on
// recording releases on the migrated database.
func TestRunWithdrawsTheReviewsThatAreOpenAndLetsAReleaseRestOnItsCheck(t *testing.T) {
	database := pgtest.FreshDatabase(t)
	conn := pgtest.Connect(t, database.Migrator)

	plan, err := migrate.Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	before := slices.DeleteFunc(slices.Clone(plan), func(step migrate.Step) bool { return slices.Contains(checkRelease, step.ID) })
	if len(plan)-len(before) != len(checkRelease) {
		t.Fatalf("the plan does not carry %v; this test would not be of the build before", checkRelease)
	}
	if err := migrate.Apply(t.Context(), conn, quiet, before); err != nil {
		t.Fatalf("migrate as the build before: %v", err)
	}
	grantOnWhatIsThere(t, conn, database.ApplicationRole)

	const (
		cargopai = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
		team     = "4f2d7b0e-85a3-4c1b-a9d4-c3f6a5b7d8e9"
		shipping = "3e1c6a9d-74f2-4b0a-98c3-b2e5f4a6c7d8"
		policy   = "9e70c25d-daf8-4b60-8e29-b8ebfa0c1d34"

		recordPolicy = `
			INSERT INTO policyreview.policy_snapshot (policy_snapshot_id, product_id, policy_set_id, policy_digest, policy_document, recorded_at)
			VALUES ($1, $2, 'fabric-global-contract-governance', 'sha256:' || encode(sha256('{}'::bytea), 'hex'), '{}'::bytea, now())`
		recordSnapshot = `
			INSERT INTO contractregistry.source_snapshot (
			    source_snapshot_id, product_id, api_asset_id, source_kind, entrypoint, source_digest,
			    ref_closure_digest, ref_closure_manifest, captured_at, captured_by_issuer, captured_by_subject
			) VALUES ($1, $2, $3, 'UPLOAD', 'openapi.yaml', $4, $4, '{}', now(), 'https://issuer.example', 'author')`
		recordRevision = `
			INSERT INTO contractregistry.contract_revision (
			    revision_id, product_id, workspace_id, api_asset_id, source_snapshot_id, revision_sequence, spec_version, status
			) VALUES ($1, $2, $3, $4, $5, $6, '3.1.2', 'DRAFT')`
		move = `
			UPDATE contractregistry.contract_revision SET status = $2, status_version = status_version + 1 WHERE revision_id = $1`
		recordCheck = `
			INSERT INTO policyreview.check_run (
			    check_run_id, product_id, workspace_id, api_asset_id, revision_id, policy_snapshot_id, input_digest,
			    tool_manifest, tool_digest, status, started_at, completed_at, requested_by_issuer, requested_by_subject
			) VALUES ($1, $2, $3, $4, $5, $6, $7, '[]'::bytea, 'sha256:' || encode(sha256('[]'::bytea), 'hex'), 'PASS',
			          now(), now(), 'https://issuer.example', 'author')`
		recordRequest = `
			INSERT INTO policyreview.review_request (
			    review_id, product_id, workspace_id, api_asset_id, revision_id, revision_status_version, check_run_id,
			    input_digest, note, requested_at, requested_by_issuer, requested_by_subject, supplementary
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULL, $9, 'https://issuer.example', 'author', $10)`
		recordScope = `INSERT INTO policyreview.review_scope (review_id, audience, environment) VALUES ($1, 'internal', 'sandbox')`
		conclude    = `
			INSERT INTO policyreview.review_conclusion (
			    review_id, outcome, reason, concluded_at, concluded_by_issuer, concluded_by_subject, valid_until
			) VALUES ($1, $2, $3, $4, 'https://issuer.example', $5, $6)`
		recordArtifact = `
			INSERT INTO contractregistry.release_artifact (artifact_digest, payload)
			VALUES ('sha256:' || encode(sha256('{}'::bytea), 'hex'), '{}'::bytea)`
		// What the build before writes for a release: it names a review and
		// no waivers.
		recordApproved = `
			INSERT INTO contractregistry.contract_release (
			    release_id, product_id, workspace_id, api_asset_id, revision_id, review_id, check_run_id, policy_snapshot_id,
			    policy_digest, input_digest, artifact_digest, release_version, prepared_at, prepared_by_issuer, prepared_by_subject
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, 'sha256:' || encode(sha256('{}'::bytea), 'hex'), $10, now(),
			          'https://issuer.example', 'publisher')`
		// What this build writes for one.
		recordChecked = `
			INSERT INTO contractregistry.contract_release (
			    release_id, product_id, workspace_id, api_asset_id, revision_id, review_id, check_run_id, policy_snapshot_id,
			    policy_digest, input_digest, artifact_digest, release_version, prepared_at, prepared_by_issuer, prepared_by_subject, waivers
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9, 'sha256:' || encode(sha256('{}'::bytea), 'hex'), $10, now(),
			          'https://issuer.example', 'publisher', $11)`
		checkViolation  = "23514"
		undefinedColumn = "42703"
	)
	asked := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	digest := func(of string) string { return "sha256:" + strings.Repeat(of, 64) }
	app := pgtest.Connect(t, database.Application)
	exec := func(what, statement string, arguments ...any) {
		t.Helper()
		if _, err := app.Exec(t.Context(), statement, arguments...); err != nil {
			t.Fatalf("%s as the build before: %v", what, err)
		}
	}
	exec("record a policy", recordPolicy, policy, cargopai)
	exec("record an artifact", recordArtifact)

	// Four revisions, each checked: the first in review, the second
	// approved and under a supplementary review, the third withdrawn, and
	// the fourth a draft nobody submitted.
	revision := func(n int) string { return "b2c3d4e5-000" + string(rune('0'+n)) + "-4b6c-9d7e-8f9a0b1c2d3e" }
	check := func(n int) string { return "c3d4e5f6-000" + string(rune('0'+n)) + "-4c7d-ae8f-9a0b1c2d3e4f" }
	review := func(n int) string { return "a1b2c3d4-000" + string(rune('0'+n)) + "-4a5b-8c6d-7e8f9a0b1c2d" }
	for n := 1; n <= 4; n++ {
		snapshot := "7c5ea03b-b8d6-4f4e-8c07-f6c9d8eafb1" + string(rune('0'+n))
		closure := string(rune('a' + n))
		exec("record a snapshot", recordSnapshot, snapshot, cargopai, shipping, digest(closure))
		exec("record a revision", recordRevision, revision(n), cargopai, team, shipping, snapshot, n)
		exec("record a check", recordCheck, check(n), cargopai, team, shipping, revision(n), policy, digest(closure))
	}
	request := func(n, of int, version int64, at time.Time, supplementary bool) {
		t.Helper()
		closure := string(rune('a' + of))
		exec("record a request", recordRequest, review(n), cargopai, team, shipping, revision(of), version, check(of), digest(closure), at, supplementary)
		exec("record its scope", recordScope, review(n))
	}
	// In review.
	exec("submit a revision", move, revision(1), "IN_REVIEW")
	request(1, 1, 2, asked, false)
	// Approved, and asked to be approved again.
	exec("submit a revision", move, revision(2), "IN_REVIEW")
	request(2, 2, 2, asked, false)
	exec("approve a revision", move, revision(2), "APPROVED")
	exec("approve the review", conclude, review(2), "APPROVED", nil, asked.Add(time.Hour), "reviewer", asked.Add(90*24*time.Hour))
	// Its request is in the future of the clock of the migration: the
	// conclusion is never earlier than the request.
	future := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Microsecond)
	request(3, 2, 3, future, true)
	// Withdrawn.
	exec("submit a revision", move, revision(3), "IN_REVIEW")
	request(4, 3, 2, asked, false)
	exec("withdraw a revision", move, revision(3), "WITHDRAWN")
	exec("withdraw the review", conclude, review(4), "WITHDRAWN", "the wrong baseline", asked.Add(time.Hour), "author", nil)
	// A release of the approved revision, under its approval.
	approvedRelease := "6f708192-a3b4-4c5d-9e6f-7a8b9c0d1e21"
	exec("record a release", recordApproved, approvedRelease, cargopai, team, shipping, revision(2), review(2), check(2), policy, digest("c"), "1.0.0")
	if _, err := app.Exec(t.Context(), recordChecked, "6f708192-a3b4-4c5d-9e6f-7a8b9c0d1e22", cargopai, team, shipping,
		revision(4), nil, check(4), policy, digest("e"), "0.9.0", `[]`); sqlState(err) != undefinedColumn {
		t.Errorf("a release of this build on the database of the build before: got %v, want it refused for the column that is not there", err)
	}

	// A process of this build does not start on the database as that build
	// left it.
	opened, err := db.Open(t.Context(), database.Application)
	if err == nil {
		opened.Close()
	}
	if !errors.Is(err, db.ErrNotMigrated) || !strings.Contains(err.Error(), "is not recorded") {
		t.Fatalf("on the database as the build before left it: got %v, want %v", err, db.ErrNotMigrated)
	}

	if err := migrate.Run(t.Context(), conn, quiet); err != nil {
		t.Fatalf("migrate as this build: %v", err)
	}
	applied, planned := history(t, conn), planRows(t, before...)
	if !slices.Equal(applied, planned) {
		t.Errorf("the history records\n %+v\nwant the steps of the build before, then those of this build\n %+v", applied, planned)
	}
	opened, err = db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("once it is migrated, with the role as the build before granted it: %v", err)
	}
	opened.Close()

	type revisionRow struct {
		ID      string
		Status  string
		Version int64
	}
	rows, err := conn.Query(t.Context(), `
		SELECT revision_id::text, status, status_version FROM contractregistry.contract_revision ORDER BY revision_sequence`)
	if err != nil {
		t.Fatalf("read the revisions: %v", err)
	}
	revisions, err := pgx.CollectRows(rows, pgx.RowToStructByPos[revisionRow])
	if err != nil {
		t.Fatalf("read the revisions: %v", err)
	}
	if want := []revisionRow{
		{revision(1), "WITHDRAWN", 3}, {revision(2), "APPROVED", 3}, {revision(3), "WITHDRAWN", 3}, {revision(4), "DRAFT", 1},
	}; !slices.Equal(revisions, want) {
		t.Errorf("the revisions are\n %+v\nwant the one in review withdrawn at the next version and the others as they were\n %+v", revisions, want)
	}

	type conclusionRow struct {
		Review     string
		Outcome    string
		Reason     string
		Issuer     string
		Subject    string
		NotEarlier bool
		Holds      bool
		Waivers    int
	}
	rows, err = conn.Query(t.Context(), `
		SELECT q.review_id::text, c.outcome, coalesce(c.reason, ''), c.concluded_by_issuer, c.concluded_by_subject,
		       c.concluded_at >= q.requested_at, c.valid_until IS NOT NULL,
		       (SELECT count(*) FROM policyreview.review_waiver w WHERE w.review_id = q.review_id)
		FROM policyreview.review_request q JOIN policyreview.review_conclusion c USING (review_id)
		ORDER BY q.review_id`)
	if err != nil {
		t.Fatalf("read the conclusions: %v", err)
	}
	conclusions, err := pgx.CollectRows(rows, pgx.RowToStructByPos[conclusionRow])
	if err != nil {
		t.Fatalf("read the conclusions: %v", err)
	}
	if len(conclusions) != 4 {
		t.Fatalf("%d reviews are concluded, want every one of the four: %+v", len(conclusions), conclusions)
	}
	for _, withdrawn := range []conclusionRow{conclusions[0], conclusions[2]} {
		if withdrawn.Outcome != "WITHDRAWN" || !strings.Contains(withdrawn.Reason, "CR-0001") ||
			withdrawn.Issuer != "urn:idp-api-fabric:system" || withdrawn.Subject != "migration/"+reviewsWithdrawn ||
			!withdrawn.NotEarlier || withdrawn.Holds || withdrawn.Waivers != 0 {
			t.Errorf("an open review is concluded as %+v, want it withdrawn for CR-0001 by the migration, never before it was asked", withdrawn)
		}
	}
	if got := conclusions[1]; got.Outcome != "APPROVED" || got.Subject != "reviewer" || !got.Holds {
		t.Errorf("the approval of before is %+v, want it as it was decided", got)
	}
	if got := conclusions[3]; got.Outcome != "WITHDRAWN" || got.Reason != "the wrong baseline" || got.Subject != "author" {
		t.Errorf("the withdrawal of before is %+v, want it as it was recorded", got)
	}

	// The release of that build keeps its review and has no waivers of its
	// own. This build records one that names no review, with its waivers;
	// the table takes an array of them and nothing else, and none on a
	// release that names a review.
	var reviewOf, waivers string
	if err := conn.QueryRow(t.Context(), `SELECT review_id::text, waivers::text FROM contractregistry.contract_release WHERE release_id = $1`,
		approvedRelease).Scan(&reviewOf, &waivers); err != nil {
		t.Fatalf("read the release of the build before: %v", err)
	}
	if reviewOf != review(2) || waivers != "[]" {
		t.Errorf("the release of the build before names review %s and waives %s, want %s and nothing", reviewOf, waivers, review(2))
	}
	waived := `[{"rule_id": "F04-GOV-003", "reason": "generated clients are not used yet"}]`
	if _, err := app.Exec(t.Context(), recordChecked, "6f708192-a3b4-4c5d-9e6f-7a8b9c0d1e23", cargopai, team, shipping,
		revision(4), nil, check(4), policy, digest("e"), "2.0.0", waived); err != nil {
		t.Errorf("a release on a check, with its waivers: %v", err)
	}
	for name, c := range map[string]struct {
		review  any
		waivers string
	}{
		"waivers that are no array":           {nil, `{"rule_id": "F04-GOV-003"}`},
		"waivers of a release under a review": {review(2), waived},
	} {
		if _, err := app.Exec(t.Context(), recordChecked, "6f708192-a3b4-4c5d-9e6f-7a8b9c0d1e24", cargopai, team, shipping,
			revision(2), c.review, check(2), policy, digest("c"), "3.0.0", c.waivers); sqlState(err) != checkViolation {
			t.Errorf("%s: got %v, want the release refused by the table", name, err)
		}
	}
	// That build is still running while this one is rolled out, and records
	// a release the way it does.
	exec("record a release after the migration", recordApproved, "6f708192-a3b4-4c5d-9e6f-7a8b9c0d1e25", cargopai, team, shipping,
		revision(2), review(2), check(2), policy, digest("c"), "1.0.1")
	// The application role adds and reads releases, and changes and removes
	// none, as before.
	for _, statement := range []string{
		`UPDATE contractregistry.contract_release SET waivers = '[]'`,
		`DELETE FROM contractregistry.contract_release`,
	} {
		if _, err := app.Exec(t.Context(), statement); sqlState(err) != "42501" {
			t.Errorf("%s\n got %v, want it refused for lack of privilege", statement, err)
		}
	}
}
