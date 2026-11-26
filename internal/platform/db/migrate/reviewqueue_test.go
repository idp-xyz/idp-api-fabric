package migrate_test

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/migrate"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

// reviewQueueOrder is the step that indexes the requests of reviews for the
// reading of the reviews of a workspace, oldest first.
const reviewQueueOrder = "policyreview/006_review_queue_order"

// The step does one thing: it creates the index of the queue, on the columns
// of its order and in that order, the product and the workspace ahead of the
// instant and the identifier. It changes no row, no column, no constraint and
// no privilege, which the other tests of this file hold to, and it says in
// its text what it is for.
func TestTheStepOfTheQueueIsTheIndexOfItsListAndNothingElse(t *testing.T) {
	plan, err := migrate.Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	index := slices.IndexFunc(plan, func(step migrate.Step) bool { return step.ID == reviewQueueOrder })
	if index < 0 {
		t.Fatalf("the plan does not carry %s", reviewQueueOrder)
	}
	step := plan[index]
	if step.Origin != migrate.OriginFabric || step.Schema != "policyreview" {
		t.Errorf("the step is %+v, want a step of Policy & Review that Fabric owns", step)
	}
	// What is left of the text once the comments are taken out is one
	// statement.
	var statements []string
	for _, line := range strings.Split(step.SQL, "\n") {
		if code, _, _ := strings.Cut(line, "--"); strings.TrimSpace(code) != "" {
			statements = append(statements, strings.TrimSpace(code))
		}
	}
	text := strings.Join(statements, " ")
	want := regexp.MustCompile(`^CREATE INDEX review_request_queue_order ON policyreview\.review_request \(product_id, workspace_id, requested_at, review_id\);$`)
	if !want.MatchString(text) {
		t.Errorf("the step is\n %s\nwant the one statement that creates review_request_queue_order on (product_id, workspace_id, requested_at, review_id)", text)
	}
	if !strings.Contains(step.SQL, "-- ") {
		t.Error("the step says nothing of why it is there")
	}
}

// A database the build before the queue has migrated and written to is
// brought up to date with what it holds. The reviews stay as they were
// written, the index of the queue is there from then on beside those it had,
// and the step is no grant: a process of this build starts only once the
// database is migrated, and does not need its role to be granted again. The
// build before is still running while this one is rolled out, and records and
// reads reviews as it did, with the same statements, on the migrated database.
func TestRunIndexesTheReviewRequestsForTheQueueAndLeavesTheReviewsAsTheyAre(t *testing.T) {
	database := pgtest.FreshDatabase(t)
	conn := pgtest.Connect(t, database.Migrator)

	plan, err := migrate.Plan()
	if err != nil {
		t.Fatalf("build the plan: %v", err)
	}
	// That build had the steps of Policy & Review before this one: neither
	// this one nor the ones that came after it.
	before := slices.DeleteFunc(slices.Clone(plan), func(step migrate.Step) bool {
		return step.Schema == "policyreview" && step.ID >= reviewQueueOrder
	})
	if len(before) > len(plan)-1 {
		t.Fatalf("the plan does not carry %s; this test would pass vacuously", reviewQueueOrder)
	}
	if err := migrate.Apply(t.Context(), conn, quiet, before); err != nil {
		t.Fatalf("migrate as the build before: %v", err)
	}
	grantOnWhatIsThere(t, conn, database.ApplicationRole)

	const (
		cargopai = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
		team     = "4f2d7b0e-85a3-4c1b-a9d4-c3f6a5b7d8e9"
		shipping = "3e1c6a9d-74f2-4b0a-98c3-b2e5f4a6c7d8"
		snapshot = "9e70c25d-daf8-4b60-8e29-b8ebfa0c1d34"

		recordPolicy = `
			INSERT INTO policyreview.policy_snapshot (policy_snapshot_id, product_id, policy_set_id, policy_digest, policy_document, recorded_at)
			VALUES ($1, $2, 'fabric-global-contract-governance', 'sha256:' || encode(sha256('{}'::bytea), 'hex'), '{}'::bytea, now())`
		recordCheck = `
			INSERT INTO policyreview.check_run (
			    check_run_id, product_id, workspace_id, api_asset_id, revision_id, policy_snapshot_id, input_digest,
			    tool_manifest, tool_digest, status, started_at, completed_at, requested_by_issuer, requested_by_subject
			) VALUES ($1, $2, $3, $4, $5, $6, $7, '[]'::bytea, 'sha256:' || encode(sha256('[]'::bytea), 'hex'), 'PASS',
			          now(), now(), 'https://issuer.example', 'author')`
		// What the build before writes for a request, and for the review it
		// asks for to end.
		recordRequest = `
			INSERT INTO policyreview.review_request (
			    review_id, product_id, workspace_id, api_asset_id, revision_id, revision_status_version, check_run_id,
			    input_digest, note, requested_at, requested_by_issuer, requested_by_subject, supplementary
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULL, $9, 'https://issuer.example', 'author', false)`
		recordScope      = `INSERT INTO policyreview.review_scope (review_id, audience, environment) VALUES ($1, 'partner', 'sandbox')`
		recordConclusion = `
			INSERT INTO policyreview.review_conclusion (review_id, outcome, reason, concluded_at, concluded_by_issuer, concluded_by_subject)
			VALUES ($1, 'WITHDRAWN', NULL, $2, 'https://issuer.example', 'author')`
		// What it reads for the history of a revision.
		readHistory = `
			SELECT q.review_id::text FROM policyreview.review_request q
			WHERE q.product_id = $1 AND q.api_asset_id = $2 AND q.workspace_id = $3 AND q.revision_id = $4
			ORDER BY q.requested_at, q.review_id`
	)
	digest := "sha256:" + strings.Repeat("a", 64)
	asked := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	app := pgtest.Connect(t, database.Application)
	if _, err := app.Exec(t.Context(), recordPolicy, snapshot, cargopai); err != nil {
		t.Fatalf("record a policy as the build before: %v", err)
	}
	revisions := []string{"b2c3d4e5-0001-4b6c-9d7e-8f9a0b1c2d3e", "b2c3d4e5-0002-4b6c-9d7e-8f9a0b1c2d3e"}
	checks := []string{"c3d4e5f6-0001-4c7d-ae8f-9a0b1c2d3e4f", "c3d4e5f6-0002-4c7d-ae8f-9a0b1c2d3e4f"}
	for i := range revisions {
		if _, err := app.Exec(t.Context(), recordCheck, checks[i], cargopai, team, shipping, revisions[i], snapshot, digest); err != nil {
			t.Fatalf("record a check as the build before: %v", err)
		}
	}
	request := func(n int, revision int, version int64) {
		t.Helper()
		id := "a1b2c3d4-" + string(rune('0'+n)) + "000-4a5b-8c6d-7e8f9a0b1c2d"
		if _, err := app.Exec(t.Context(), recordRequest, id, cargopai, team, shipping, revisions[revision], version, checks[revision], digest,
			asked.Add(time.Duration(n)*time.Minute)); err != nil {
			t.Fatalf("record a request as the build: %v", err)
		}
		if _, err := app.Exec(t.Context(), recordScope, id); err != nil {
			t.Fatalf("record its scope as the build: %v", err)
		}
	}
	request(1, 0, 2)
	request(2, 1, 2)
	if _, err := app.Exec(t.Context(), recordConclusion, "a1b2c3d4-1000-4a5b-8c6d-7e8f9a0b1c2d", asked.Add(time.Hour)); err != nil {
		t.Fatalf("record a conclusion as the build before: %v", err)
	}

	reviews := func() []string {
		t.Helper()
		rows, err := conn.Query(t.Context(), `
			SELECT concat_ws('|', q.review_id, q.product_id, q.workspace_id, q.api_asset_id, q.revision_id, q.revision_status_version,
			                 q.check_run_id, q.requested_at AT TIME ZONE 'UTC', q.supplementary, c.outcome)
			FROM policyreview.review_request q LEFT JOIN policyreview.review_conclusion c USING (review_id) ORDER BY q.review_id`)
		if err != nil {
			t.Fatalf("read the reviews: %v", err)
		}
		kept, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("read the reviews: %v", err)
		}
		return kept
	}
	indexes := func() []string {
		t.Helper()
		rows, err := conn.Query(t.Context(), `
			SELECT indexname || ': ' || regexp_replace(indexdef, '^.* USING ', '') FROM pg_indexes
			WHERE schemaname = 'policyreview' AND tablename = 'review_request' ORDER BY indexname`)
		if err != nil {
			t.Fatalf("read the indexes: %v", err)
		}
		found, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("read the indexes: %v", err)
		}
		return found
	}
	historyOf := func(revision int) []string {
		t.Helper()
		rows, err := app.Query(t.Context(), readHistory, cargopai, shipping, team, revisions[revision])
		if err != nil {
			t.Fatalf("read the history of a revision: %v", err)
		}
		listed, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("read the history of a revision: %v", err)
		}
		return listed
	}
	written := reviews()
	if len(written) != 2 {
		t.Fatalf("the build before wrote %d reviews, want 2", len(written))
	}
	earlier := []string{
		"review_request_history: btree (product_id, api_asset_id, revision_id, requested_at, review_id)",
		"review_request_once_key: btree (revision_id, revision_status_version) WHERE (NOT supplementary)",
		"review_request_pkey: btree (review_id)",
	}
	if got := indexes(); !slices.Equal(got, earlier) {
		t.Fatalf("before the step the requests have the indexes\n %v\nwant only\n %v", got, earlier)
	}

	// A process of this build does not start on the database as that build
	// left it.
	opened, err := db.Open(t.Context(), database.Application)
	if err == nil {
		opened.Close()
	}
	if !errors.Is(err, db.ErrNotMigrated) || !strings.Contains(err.Error(), "migration "+reviewQueueOrder+" is not recorded") {
		t.Fatalf("on the database as the build before left it: got %v, want %v naming %s", err, db.ErrNotMigrated, reviewQueueOrder)
	}

	if err := migrate.Run(t.Context(), conn, quiet); err != nil {
		t.Fatalf("migrate as this build: %v", err)
	}
	applied, planned := history(t, conn), planRows(t)
	byID := func(a, b historyRow) int { return strings.Compare(a.ID, b.ID) }
	slices.SortFunc(applied, byID)
	slices.SortFunc(planned, byID)
	if !slices.Equal(applied, planned) {
		t.Errorf("the history records\n %+v\nwant every step of the plan, each once\n %+v", applied, planned)
	}
	// The step leaves the reviews as they were written. The one that was
	// still open is withdrawn by a step of this build that comes after it
	// (reviewsWithdrawn), and by nothing else.
	withdrawn := slices.Clone(written)
	withdrawn[1] += "|WITHDRAWN"
	if got := reviews(); !slices.Equal(got, withdrawn) {
		t.Errorf("the reviews are\n %v\nwant them as they were written, the open one withdrawn\n %v", got, withdrawn)
	}
	wantIndexes := slices.Concat(earlier, []string{
		"review_request_queue_order: btree (product_id, workspace_id, requested_at, review_id)",
	})
	slices.Sort(wantIndexes)
	if got := indexes(); !slices.Equal(got, wantIndexes) {
		t.Errorf("the requests have the indexes\n %v\nwant\n %v", got, wantIndexes)
	}

	// Migrated, a process starts: the step grants nothing and needs nothing,
	// and the role still reads the requests and records more, and changes and
	// removes nothing.
	opened, err = db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("once it is migrated: %v", err)
	}
	opened.Close()
	for _, statement := range []string{
		`UPDATE policyreview.review_request SET requested_at = now()`,
		`DELETE FROM policyreview.review_request`,
		`TRUNCATE policyreview.review_request`,
		`CREATE INDEX review_request_by_hand ON policyreview.review_request (requested_at)`,
	} {
		_, err := app.Exec(t.Context(), statement)
		if state := sqlState(err); state != "42501" {
			t.Errorf("%s\n got %v, want it refused for lack of privilege", statement, err)
		}
	}

	// The build before is still running while this one is rolled out. It
	// records the way it did, and reads the history of a revision with the
	// statement it did.
	if got := historyOf(0); len(got) != 1 {
		t.Errorf("the history of the first revision as the build before reads it: %v, want its one review", got)
	}
	request(3, 0, 3)
	request(4, 1, 3)
	if got := historyOf(0); !slices.Equal(got, []string{"a1b2c3d4-1000-4a5b-8c6d-7e8f9a0b1c2d", "a1b2c3d4-3000-4a5b-8c6d-7e8f9a0b1c2d"}) {
		t.Errorf("the history of the first revision after the build before recorded another: %v", got)
	}
}
