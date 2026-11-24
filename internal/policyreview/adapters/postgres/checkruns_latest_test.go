package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// latestFixture is a store of check runs, a product with a Workspace and an
// asset, and a way to record checks of revisions of it at the instants a test
// names.
type latestFixture struct {
	t        *testing.T
	database pgtest.Database
	fabric   *db.Database
	store    *postgres.CheckRuns
	policy   domain.Policy
	source   domain.Source
	product  string
	asset    domain.RevisionRef // its product, Workspace and asset; the revision is for the test to name
	versions int
}

func newLatestFixture(t *testing.T) *latestFixture {
	t.Helper()

	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fabric.Close)
	product := ids.NewUUID()
	return &latestFixture{
		t: t, database: database, fabric: fabric, store: postgres.NewCheckRuns(fabric.Framework), policy: domain.Baseline(),
		source: domain.Source{
			Entrypoint: "openapi.yaml", Digest: "sha256:" + strings.Repeat("a", 64),
			Files: []domain.SourceFile{{Path: "openapi.yaml", Content: "openapi: 3.1.2\n"}},
		},
		product: product,
		asset:   domain.RevisionRef{ProductID: product, WorkspaceID: ids.NewUUID(), AssetID: ids.NewUUID()},
	}
}

// revision names a revision of the asset of the fixture.
func (f *latestFixture) revision() domain.RevisionRef {
	revision := f.asset
	revision.RevisionID = ids.NewUUID()
	return revision
}

// check records a check of the revision that concluded at the instant, under
// the identifier if one is given. A check that fails has a finding of a rule
// that blocks; one that does not is unknown, since the rules no tool
// evaluates are not run. Each check has a tool of its own, which is what lets
// a revision have more than one.
func (f *latestFixture) check(revision domain.RevisionRef, id string, concluded time.Time, failing bool) domain.CheckRun {
	f.t.Helper()

	f.versions++
	tool := domain.Tool{Name: "fabric-openapi-rules", Version: fmt.Sprintf("t%d", f.versions)}
	observations := []domain.Observation{{RuleID: "F04-GOV-001"}}
	if failing {
		observations = append(observations, domain.Observation{RuleID: "F04-GOV-003", Findings: []domain.Finding{
			{RuleID: "F04-GOV-003", Path: "openapi.yaml", Pointer: "/paths", Line: 1, Message: "operationId is missing."},
		}})
	}
	if id == "" {
		id = ids.NewUUID()
	}
	var run domain.CheckRun
	err := f.fabric.WithinTransaction(f.t.Context(), func(ctx context.Context) error {
		snapshot, err := f.store.Snapshot(ctx, revision.ProductID, f.policy, ids.NewUUID(), concluded)
		if err != nil {
			return err
		}
		run, err = domain.Complete(domain.Draft{
			ID: id, PolicySnapshotID: snapshot, Revision: revision,
			RequestedBy: domain.Principal{Issuer: "https://issuer.example", Subject: "author"}, StartedAt: concluded.Add(-time.Second),
		}, f.policy, f.source, tool, observations, concluded)
		if err != nil {
			return err
		}
		return f.store.Record(ctx, run)
	})
	if err != nil {
		f.t.Fatalf("record a check: %v", err)
	}
	return run
}

// latest reads the last checks of the revisions and returns them by revision.
func (f *latestFixture) latest(revisions []domain.RevisionRef, status domain.Status) map[string]domain.Overview {
	f.t.Helper()

	found, err := f.store.Latest(f.t.Context(), f.product, revisions, status)
	if err != nil {
		f.t.Fatalf("read the last checks: %v", err)
	}
	byRevision := make(map[string]domain.Overview, len(found))
	for _, overview := range found {
		if _, twice := byRevision[overview.Revision.RevisionID]; twice {
			f.t.Fatalf("the revision %s has two last checks", overview.Revision.RevisionID)
		}
		byRevision[overview.Revision.RevisionID] = overview
	}
	return byRevision
}

// whichRevisions are the revisions a map of last checks has them for, in order.
func whichRevisions(found map[string]domain.Overview) []string {
	names := make([]string, 0, len(found))
	for revision := range found {
		names = append(names, revision)
	}
	slices.Sort(names)
	return names
}

func sorted(revisions ...domain.RevisionRef) []string {
	names := make([]string, 0, len(revisions))
	for _, revision := range revisions {
		names = append(names, revision.RevisionID)
	}
	slices.Sort(names)
	return names
}

// The last check of a revision is the one that was concluded last, as the
// history of the revision lists them, and what is read of it is the check as
// that history shows it. A revision that has no check has no last check, and
// a set of revisions has the last check of each, all of them read at once.
func TestTheLastCheckOfEachRevisionOfASetIsTheOneConcludedLast(t *testing.T) {
	f := newLatestFixture(t)
	morning := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	checked, once, unchecked := f.revision(), f.revision(), f.revision()
	// A check that failed, between two that did not conclude: the last of
	// the three is the one that is read, and not the one that failed.
	f.check(checked, "", morning, false)
	f.check(checked, "", morning.Add(time.Hour), true)
	last := f.check(checked, "", morning.Add(2*time.Hour), false)
	only := f.check(once, "", morning, true)

	found := f.latest([]domain.RevisionRef{checked, once, unchecked}, "")
	if want := sorted(checked, once); !slices.Equal(whichRevisions(found), want) {
		t.Fatalf("the last checks are of the revisions %v, want %v", whichRevisions(found), want)
	}
	for revision, want := range map[string]domain.CheckRun{checked.RevisionID: last, once.RevisionID: only} {
		if got := found[revision]; !reflect.DeepEqual(got, want.Overview()) {
			t.Errorf("the last check of %s is %+v, want %+v", revision, got, want.Overview())
		}
	}
	// What is read is the last item of the history of the revision.
	history, err := f.store.List(t.Context(), checked, nil, 100)
	if err != nil || len(history) != 3 || !reflect.DeepEqual(found[checked.RevisionID], history[2]) {
		t.Errorf("the history of the revision is %+v (%v), want it to end with %+v", history, err, found[checked.RevisionID])
	}
	if found[checked.RevisionID].Status != domain.StatusUnknown || found[once.RevisionID].Status != domain.StatusFail {
		t.Errorf("the last checks concluded %s and %s, want UNKNOWN and FAIL", found[checked.RevisionID].Status, found[once.RevisionID].Status)
	}
	if none, err := f.store.Latest(t.Context(), f.product, nil, ""); err != nil || len(none) != 0 {
		t.Errorf("the last checks of no revision are %+v (%v), want none", none, err)
	}
}

// Of two checks concluded at the same instant the last is the one of the
// greater identifier, as the history of the revision orders them.
func TestTheLastCheckOfTwoConcludedAtTheSameInstantIsTheOneOfTheGreaterIdentifier(t *testing.T) {
	f := newLatestFixture(t)
	at := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	revision := f.revision()
	names := []string{ids.NewUUID(), ids.NewUUID()}
	slices.Sort(names)
	// The greater one is recorded first; the lesser one is the one that failed.
	greater := f.check(revision, names[1], at, false)
	f.check(revision, names[0], at, true)

	got := f.latest([]domain.RevisionRef{revision}, "")[revision.RevisionID]
	history, err := f.store.List(t.Context(), revision, nil, 10)
	if err != nil || len(history) != 2 || got.ID != names[1] || !reflect.DeepEqual(got, greater.Overview()) || !reflect.DeepEqual(got, history[1]) {
		t.Errorf("the last check is %+v, want %s, the last of %+v (%v)", got, names[1], history, err)
	}
}

// A status that is asked for is asked of the last check: a revision whose
// last check concluded another status is not kept, though an earlier one of
// it concluded this.
func TestOnlyTheChecksOfTheStatusAskedForAreKeptOfTheLastOnes(t *testing.T) {
	f := newLatestFixture(t)
	morning := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	failing, mended, unknown, unchecked := f.revision(), f.revision(), f.revision(), f.revision()
	f.check(failing, "", morning, false)
	f.check(failing, "", morning.Add(time.Hour), true)
	f.check(mended, "", morning, true)
	f.check(mended, "", morning.Add(time.Hour), false)
	f.check(unknown, "", morning, false)
	revisions := []domain.RevisionRef{failing, mended, unknown, unchecked}

	for _, c := range []struct {
		status domain.Status
		want   []string
	}{
		{"", sorted(failing, mended, unknown)},
		{domain.StatusFail, sorted(failing)},
		{domain.StatusUnknown, sorted(mended, unknown)},
		{domain.StatusPass, nil},
		{domain.StatusWarn, nil},
	} {
		if got := whichRevisions(f.latest(revisions, c.status)); !slices.Equal(got, c.want) {
			t.Errorf("the last checks that concluded %q are of the revisions %v, want %v", c.status, got, c.want)
		}
	}
}

// A revision is named under its product, its Workspace and its asset, as every
// read of its checks names it: a revision of the same identifier under another
// is not it, and the checks of one product are not among the last checks of
// another.
func TestTheLastCheckOfARevisionIsReadUnderItsProductItsWorkspaceAndItsAsset(t *testing.T) {
	f := newLatestFixture(t)
	revision := f.revision()
	at := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	f.check(revision, "", at, true)
	elsewhere := revision
	elsewhere.ProductID, elsewhere.WorkspaceID, elsewhere.AssetID = ids.NewUUID(), ids.NewUUID(), ids.NewUUID()
	f.check(elsewhere, "", at.Add(time.Hour), false)

	if got := f.latest([]domain.RevisionRef{revision}, ""); len(got) != 1 || got[revision.RevisionID].Status != domain.StatusFail {
		t.Errorf("the last check of the revision is %+v, want its own, which failed", got)
	}
	other := elsewhere
	if got, err := f.store.Latest(t.Context(), other.ProductID, []domain.RevisionRef{other}, ""); err != nil || len(got) != 1 || got[0].Status != domain.StatusUnknown {
		t.Errorf("the last check of the revision of the other product is %+v (%v), want its own, which did not fail", got, err)
	}
	crossed := map[string]struct {
		product  string
		revision domain.RevisionRef
	}{
		"this revision asked of the other product":    {elsewhere.ProductID, revision},
		"the other revision asked of this product":    {f.product, elsewhere},
		"this revision under another Workspace":       {f.product, domain.RevisionRef{ProductID: f.product, WorkspaceID: ids.NewUUID(), AssetID: revision.AssetID, RevisionID: revision.RevisionID}},
		"this revision under another asset":           {f.product, domain.RevisionRef{ProductID: f.product, WorkspaceID: revision.WorkspaceID, AssetID: ids.NewUUID(), RevisionID: revision.RevisionID}},
		"a revision this product does not have":       {f.product, f.revision()},
		"the other product, with this revision's ids": {elsewhere.ProductID, domain.RevisionRef{ProductID: elsewhere.ProductID, WorkspaceID: revision.WorkspaceID, AssetID: revision.AssetID, RevisionID: revision.RevisionID}},
	}
	for name, c := range crossed {
		if got, err := f.store.Latest(t.Context(), c.product, []domain.RevisionRef{c.revision}, ""); err != nil || len(got) != 0 {
			t.Errorf("%s: the last checks are %+v (%v), want none", name, got, err)
		}
	}
}

// A check that is not what the model recorded fails the read as it fails the
// history of its revision, with the same error: its status is not what its
// outcomes add up to. A status that keeps a check out leaves its outcomes
// unread, and with them what they say of its status: the status of a check
// that is kept out is the one that is stored.
func TestACheckThatIsNotWhatWasRecordedFailsTheReadAsItFailsTheHistory(t *testing.T) {
	f := newLatestFixture(t)
	owner := pgtest.Connect(t, f.database.Migrator)
	revision := f.revision()
	run := f.check(revision, "", time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC), true)
	asked := []domain.RevisionRef{revision}

	if got := f.latest(asked, domain.StatusFail); len(got) != 1 || got[revision.RevisionID].ID != run.ID {
		t.Fatalf("the check that failed is read as %+v", got)
	}
	// Someone with more than the application role rewrites the status.
	if _, err := owner.Exec(t.Context(), `UPDATE policyreview.check_run SET status = 'PASS' WHERE check_run_id = $1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.List(t.Context(), revision, nil, 10); !errors.Is(err, domain.ErrCheckRunDamaged) {
		t.Fatalf("the history of the revision fails with %v, want %v", err, domain.ErrCheckRunDamaged)
	}
	for _, status := range []domain.Status{"", domain.StatusPass} {
		if got, err := f.store.Latest(t.Context(), f.product, asked, status); !errors.Is(err, domain.ErrCheckRunDamaged) || len(got) != 0 {
			t.Errorf("the last checks, of the status %q, are %+v (%v), want no check and %v", status, got, err, domain.ErrCheckRunDamaged)
		}
	}
	// The stored status is PASS: asked for FAIL, the check is kept out
	// before its outcomes are read.
	if got, err := f.store.Latest(t.Context(), f.product, asked, domain.StatusFail); err != nil || len(got) != 0 {
		t.Errorf("the last checks of the status FAIL are %+v (%v), want none and no failure", got, err)
	}
}

// latestPlan is a node of the plan PostgreSQL makes of a statement, with what
// it did when it ran.
type latestPlan struct {
	Type     string       `json:"Node Type"`
	Relation string       `json:"Relation Name"`
	Rows     float64      `json:"Actual Rows"`
	Loops    float64      `json:"Actual Loops"`
	Removed  float64      `json:"Rows Removed by Filter"`
	Recheck  float64      `json:"Rows Removed by Index Recheck"`
	Children []latestPlan `json:"Plans"`
}

func rowsReadOf(root latestPlan, table string) (read float64, scanned bool) {
	var walk func(latestPlan)
	walk = func(node latestPlan) {
		if node.Relation == table {
			read += (node.Rows + node.Removed + node.Recheck) * node.Loops
			scanned = scanned || node.Type == "Seq Scan"
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return read, scanned
}

// This guards work performed, not elapsed time: the last checks of a set of
// revisions are found by the index of the checks of a revision, what is read
// grows with the set and not with the checks around it, and the outcomes of a
// check are read only of the checks that are kept, so that the status that is
// asked for is what the statement reads outcomes for. The tables are made big
// on purpose: twenty thousand checks of other products, and six hundred of
// this one, with twenty-three outcomes each.
func TestTheLastChecksOfASetAreReadInProportionToTheSetAndTheOutcomesOnlyOfThoseKept(t *testing.T) {
	f := newLatestFixture(t)
	owner, app := pgtest.Connect(t, f.database.Migrator), pgtest.Connect(t, f.database.Application)
	ctx := t.Context()
	exec := func(statement string) {
		t.Helper()

		if _, err := owner.Exec(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	const manifest = `'[{"name":"fabric-openapi-rules","version":"3"}]'::bytea`
	const document = `'{}'::bytea`
	const recordChecks = `
INSERT INTO policyreview.check_run (check_run_id, product_id, workspace_id, api_asset_id, revision_id, policy_snapshot_id, input_digest,
    tool_manifest, tool_digest, status, started_at, completed_at, requested_by_issuer, requested_by_subject)
SELECT check_run_id, product_id, workspace_id, asset_id, revision_id, snapshot_id, 'sha256:' || repeat('a', 64),
       manifest, 'sha256:' || encode(sha256(manifest), 'hex'), status, concluded - interval '1 second', concluded, 'https://issuer.example', 'author'
FROM `

	// Twenty thousand checks of other products, a hundred for each of two hundred.
	exec(`CREATE TEMPORARY TABLE others AS
SELECT gen_random_uuid() AS check_run_id, p.product_id, gen_random_uuid() AS workspace_id, gen_random_uuid() AS asset_id,
       gen_random_uuid() AS revision_id, p.snapshot_id, ` + manifest + ` AS manifest, 'UNKNOWN' AS status, now() AS concluded
FROM (SELECT gen_random_uuid() AS product_id, gen_random_uuid() AS snapshot_id FROM generate_series(1, 200)) p
CROSS JOIN generate_series(1, 100) g`)
	exec(`INSERT INTO policyreview.policy_snapshot (policy_snapshot_id, product_id, policy_set_id, policy_digest, policy_document, recorded_at)
SELECT DISTINCT snapshot_id, product_id, 'fabric-global-contract-governance', 'sha256:' || encode(sha256(` + document + `), 'hex'), ` + document + `, now()
FROM others`)
	exec(recordChecks + `others`)

	// The product of the test: three hundred revisions, each with an earlier
	// check and a last one. A tenth of them have a last check that failed,
	// and a tenth an earlier one that did.
	product, workspace, snapshot := f.product, f.asset.WorkspaceID, ids.NewUUID()
	exec(fmt.Sprintf(`INSERT INTO policyreview.policy_snapshot (policy_snapshot_id, product_id, policy_set_id, policy_digest, policy_document, recorded_at)
VALUES ('%s', '%s', 'fabric-global-contract-governance', 'sha256:' || encode(sha256(%s), 'hex'), %s, now())`, snapshot, product, document, document))
	exec(fmt.Sprintf(`CREATE TEMPORARY TABLE mine AS
SELECT '%s'::uuid AS product_id, '%s'::uuid AS workspace_id, '%s'::uuid AS snapshot_id, gen_random_uuid() AS asset_id,
       gen_random_uuid() AS revision_id, g AS n
FROM generate_series(1, 300) g`, product, workspace, snapshot))
	exec(`CREATE TEMPORARY TABLE mine_checks AS
SELECT gen_random_uuid() AS check_run_id, m.product_id, m.workspace_id, m.asset_id, m.revision_id, m.snapshot_id, m.n, v,
       ('[{"name":"fabric-openapi-rules","version":"' || v || '"}]')::bytea AS manifest,
       CASE WHEN v = 2 THEN CASE WHEN m.n % 10 = 0 THEN 'FAIL' ELSE 'UNKNOWN' END
            ELSE CASE WHEN m.n % 10 = 5 THEN 'FAIL' ELSE 'UNKNOWN' END END AS status,
       now() - (3 - v) * interval '1 hour' AS concluded
FROM mine m CROSS JOIN generate_series(1, 2) v`)
	exec(recordChecks + `mine_checks`)
	exec(`INSERT INTO policyreview.check_rule_outcome (check_run_id, rule_id, rule_position, status, finding_count, tool_name, tool_version)
SELECT c.check_run_id, 'F04-GOV-' || lpad(r::text, 3, '0'), r - 1, 'NOT_RUN', 0, NULL, NULL
FROM mine_checks c CROSS JOIN generate_series(1, 23) r`)
	for _, table := range []string{"policy_snapshot", "check_run", "check_rule_outcome"} {
		exec("ANALYZE policyreview." + table)
	}

	revisionsOf := func(count int) (assets, workspaces, revisions []string) {
		t.Helper()

		rows, err := owner.Query(ctx, `SELECT asset_id::text, workspace_id::text, revision_id::text FROM mine WHERE n <= $1 ORDER BY n`, count)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var asset, workspace, revision string
			if err := rows.Scan(&asset, &workspace, &revision); err != nil {
				t.Fatal(err)
			}
			assets, workspaces, revisions = append(assets, asset), append(workspaces, workspace), append(revisions, revision)
		}
		return assets, workspaces, revisions
	}
	explain := func(statement string, arguments ...any) latestPlan {
		t.Helper()

		var encoded []byte
		if err := app.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+statement, arguments...).Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		var plans []struct{ Plan latestPlan }
		if err := json.Unmarshal(encoded, &plans); err != nil || len(plans) != 1 {
			t.Fatalf("decode the execution plan: %v: %s", err, encoded)
		}
		return plans[0].Plan
	}

	var checks, outcomes int
	if err := owner.QueryRow(ctx, `SELECT (SELECT count(*) FROM policyreview.check_run), (SELECT count(*) FROM policyreview.check_rule_outcome)`).Scan(&checks, &outcomes); err != nil ||
		checks != 20600 || outcomes != 13800 {
		t.Fatalf("the tables hold %d checks and %d outcomes (%v), want 20600 and 13800", checks, outcomes, err)
	}

	read := func(plan latestPlan, table string) float64 {
		t.Helper()

		rows, scanned := rowsReadOf(plan, table)
		if scanned {
			t.Errorf("the statement read every row of %s", table)
		}
		return rows
	}
	assets10, workspaces10, revisions10 := revisionsOf(10)
	assets100, workspaces100, revisions100 := revisionsOf(100)

	any10 := explain(postgres.LatestOverviewsForTest, product, assets10, workspaces10, revisions10)
	any100 := explain(postgres.LatestOverviewsForTest, product, assets100, workspaces100, revisions100)
	failed100 := explain(postgres.LatestOverviewsOfStatusForTest, product, assets100, workspaces100, revisions100, "FAIL")
	t.Logf("the last checks of 10 revisions read %.0f checks and %.0f outcomes; of 100, %.0f and %.0f; of the 10 of 100 that failed, %.0f and %.0f; of %d checks and %d outcomes",
		read(any10, "check_run"), read(any10, "check_rule_outcome"), read(any100, "check_run"), read(any100, "check_rule_outcome"),
		read(failed100, "check_run"), read(failed100, "check_rule_outcome"), checks, outcomes)

	// A check of the revision, found by the index, and the check before it
	// that the order passes over.
	if few, many := read(any10, "check_run"), read(any100, "check_run"); few < 10 || many > 400 || many > 12*few {
		t.Errorf("the last checks of 10 revisions read %.0f checks and those of 100 read %.0f, want about ten times as many and no more than 400", few, many)
	}
	// Twenty-three outcomes for each check that is kept.
	if few, many := read(any10, "check_rule_outcome"), read(any100, "check_rule_outcome"); few != 23*10 || many != 23*100 {
		t.Errorf("the last checks of 10 revisions read %.0f outcomes and those of 100 read %.0f, want %d and %d", few, many, 23*10, 23*100)
	}
	// A status that is asked for keeps the others out before their outcomes
	// are read: ten of the hundred failed.
	if kept := read(failed100, "check_rule_outcome"); kept != 23*10 {
		t.Errorf("the last checks of 100 revisions that failed read %.0f outcomes, want those of the ten that did: %d", kept, 23*10)
	}
	if many := read(failed100, "check_run"); many > 400 {
		t.Errorf("the last checks that failed, of 100 revisions, read %.0f checks, want no more than 400", many)
	}
}
