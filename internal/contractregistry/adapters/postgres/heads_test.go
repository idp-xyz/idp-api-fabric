package postgres_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
)

// forAsset is the fixture of another asset of the same product and workspace,
// with revisions of its own to record.
func (f *statusFixture) forAsset(asset domain.AssetRef) *statusFixture {
	other := *f
	other.asset, other.count = asset, 0
	return &other
}

// headsOf reads the heads of the assets and returns them by asset.
func headsOf(t *testing.T, heads *postgres.Heads, product string, assets ...domain.AssetRef) map[string]ports.Head {
	t.Helper()

	found, err := heads.Heads(t.Context(), product, assets)
	if err != nil {
		t.Fatalf("read the heads: %v", err)
	}
	byAsset := make(map[string]ports.Head, len(found))
	for _, head := range found {
		if _, twice := byAsset[head.Revision.Asset.AssetID]; twice {
			t.Fatalf("the asset %s has two heads", head.Revision.Asset.AssetID)
		}
		byAsset[head.Revision.Asset.AssetID] = head
	}
	return byAsset
}

// sameRevision and sameRelease compare what two reads of the same thing say,
// instants by the moment they name and not by the place they are written in.
func sameRevision(a, b domain.Revision) bool {
	same := a.CreatedAt.Equal(b.CreatedAt)
	a.CreatedAt, b.CreatedAt = time.Time{}, time.Time{}
	return same && a == b
}

func sameRelease(a, b domain.Release) bool {
	same := a.PreparedAt.Equal(b.PreparedAt)
	a.PreparedAt, b.PreparedAt = time.Time{}, time.Time{}
	return same && reflect.DeepEqual(a, b)
}

// The head of an asset is the revision of the greatest sequence, as a read of
// that revision gives it, and the release that was prepared last, as the list
// of its releases gives it last, whichever revision that was prepared from. A
// revision that was recorded after the last release leaves it the release
// that was prepared last and is not released: a release names the revision it
// was prepared from, and the head is released when some release names the
// last revision. An asset without a revision has no head, and a set of assets
// has the head of each, all of them read at once.
func TestTheHeadOfEachAssetOfASetIsItsOwnLastRevisionAndItsOwnLastRelease(t *testing.T) {
	f, _ := newStatusFixture(t)
	revisions, releases, heads := f.store, postgres.NewReleases(f.fabric.Framework), postgres.NewHeads(f.fabric.Framework)
	product, workspace := f.asset.ProductID, f.asset.WorkspaceID
	newAsset := func() *statusFixture {
		return f.forAsset(domain.AssetRef{ProductID: product, WorkspaceID: workspace, AssetID: ids.NewUUID()})
	}
	morning := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	prepare := func(g *statusFixture, revision domain.Revision, version string, at time.Time, waivers ...domain.Waiver) domain.Release {
		t.Helper()
		release, payload := g.release(revision, version, at, waivers...)
		if created, err := g.keep(releases, release, payload); err != nil || !created {
			t.Fatalf("release %s: recorded %t, %v", version, created, err)
		}
		return release
	}

	// A draft that follows two revisions that were released, the last of
	// them with a waiver, which the head reads as the list does.
	grown := newAsset()
	first, second := grown.record(), grown.record()
	prepare(grown, first, "1.0.0", morning)
	prepare(grown, second, "2.0.0", morning.Add(time.Hour), domain.Waiver{RuleID: "F04-GOV-003", Reason: "named in the next revision"})
	third := grown.record()
	// One revision, released; one that is not released.
	released, waiting := newAsset(), newAsset()
	onlyRevision := released.record()
	prepare(released, onlyRevision, "1.0.0", morning)
	waiting.record()
	// Two revisions released, and the older of them released again, later:
	// the last release was prepared from the revision that is not the last.
	older := newAsset()
	one, two := older.record(), older.record()
	prepare(older, two, "2.0.0", morning)
	prepare(older, one, "1.0.1", morning.Add(2*time.Hour))
	// An asset that has no revision.
	empty := newAsset()

	assets := []domain.AssetRef{grown.asset, released.asset, waiting.asset, older.asset, empty.asset}
	found := headsOf(t, heads, product, assets...)
	if len(found) != 4 {
		t.Fatalf("the heads of five assets, one of them without a revision, are %d, want 4", len(found))
	}
	if _, present := found[empty.asset.AssetID]; present {
		t.Errorf("an asset without a revision has the head %+v, want none", found[empty.asset.AssetID])
	}
	for _, c := range []struct {
		name     string
		asset    *statusFixture
		revision domain.Revision
		release  string // the version of the last release, if any
		released bool
	}{
		{"a draft after two revisions that were released", grown, third, "2.0.0", false},
		{"one revision, released", released, onlyRevision, "1.0.0", true},
		{"one revision, not released", waiting, mustFind(t, revisions, waiting.asset, 1), "", false},
		{"the older revision released again, later", older, two, "1.0.1", true},
	} {
		head, present := found[c.asset.asset.AssetID]
		if !present {
			t.Errorf("%s: no head", c.name)
			continue
		}
		// What the head says of the revision is what a read of it says.
		if read, _, err := revisions.Find(t.Context(), c.asset.asset, head.Revision.ID); err != nil || !sameRevision(head.Revision, read) {
			t.Errorf("%s: the head names the revision %+v, a read of it gives %+v (%v)", c.name, head.Revision, read, err)
		}
		if head.Revision.ID != c.revision.ID || head.Released != c.released {
			t.Errorf("%s: the head is the revision %s (sequence %d), released %t; want the revision %s, released %t",
				c.name, head.Revision.ID, head.Revision.Sequence, head.Released, c.revision.ID, c.released)
		}
		listed, err := releases.List(t.Context(), c.asset.asset, nil, 100)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case c.release == "" && head.Release != nil:
			t.Errorf("%s: the head names the release %+v, want none", c.name, head.Release)
		case c.release != "" && (head.Release == nil || head.Release.Version != c.release):
			t.Errorf("%s: the head names the release %+v, want %s", c.name, head.Release, c.release)
		case c.release != "" && !sameRelease(*head.Release, listed[len(listed)-1]):
			// What the head says of the release is the last item of the list of them.
			t.Errorf("%s: the head names the release %+v, the list of releases ends with %+v", c.name, *head.Release, listed[len(listed)-1])
		}
	}
	if found[older.asset.AssetID].Release.RevisionID != one.ID {
		t.Errorf("the last release of the asset was prepared from the revision %s, want %s",
			found[older.asset.AssetID].Release.RevisionID, one.ID)
	}
}

func mustFind(t *testing.T, store *postgres.Revisions, asset domain.AssetRef, sequence int64) domain.Revision {
	t.Helper()

	revisions, err := store.List(t.Context(), asset, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, revision := range revisions {
		if revision.Sequence == sequence {
			return revision
		}
	}
	t.Fatalf("the asset has no revision of the sequence %d", sequence)
	return domain.Revision{}
}

// An asset is named with its Workspace and its product, as every read of it
// is, and what the registry holds under the same asset identifier in another
// product or another Workspace is not its own: nothing of it is read, and no
// head of the other product is among the heads of this.
func TestTheHeadOfAnAssetIsReadUnderItsProductAndItsWorkspace(t *testing.T) {
	f, _ := newStatusFixture(t)
	heads, releases := postgres.NewHeads(f.fabric.Framework), postgres.NewReleases(f.fabric.Framework)
	mine := f.asset
	// The same asset and Workspace identifiers under another product, and the
	// same asset identifier under another Workspace of this product.
	elsewhere := f.forAsset(domain.AssetRef{ProductID: ids.NewUUID(), WorkspaceID: mine.WorkspaceID, AssetID: mine.AssetID})
	beside := f.forAsset(domain.AssetRef{ProductID: mine.ProductID, WorkspaceID: ids.NewUUID(), AssetID: mine.AssetID})
	beside.count = 10
	inMine, inElsewhere, inBeside := f.record(), elsewhere.record(), beside.record()
	// Each of the three has a release, and the two that are not mine were
	// prepared later: whichever of them a read took for the last release of
	// this asset would show.
	morning := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	released := map[*statusFixture]domain.Release{}
	for index, c := range []struct {
		fixture  *statusFixture
		revision domain.Revision
	}{{f, inMine}, {elsewhere, inElsewhere}, {beside, inBeside}} {
		release, payload := c.fixture.release(c.revision, fmt.Sprintf("1.%d.0", index), morning.Add(time.Duration(index)*time.Hour))
		if created, err := c.fixture.keep(releases, release, payload); err != nil || !created {
			t.Fatalf("release %s: recorded %t, %v", release.Version, created, err)
		}
		released[c.fixture] = release
	}

	if got := headsOf(t, heads, mine.ProductID, mine); len(got) != 1 || got[mine.AssetID].Revision.ID != inMine.ID ||
		got[mine.AssetID].Release == nil || got[mine.AssetID].Release.ID != released[f].ID || !got[mine.AssetID].Released {
		t.Errorf("the head of the asset of this product is %+v, want the revision %s released by %s", got, inMine.ID, released[f].ID)
	}
	if got := headsOf(t, heads, elsewhere.asset.ProductID, elsewhere.asset); len(got) != 1 ||
		got[mine.AssetID].Revision.ID != inElsewhere.ID || got[mine.AssetID].Revision.Status != domain.RevisionDraft ||
		got[mine.AssetID].Release == nil || got[mine.AssetID].Release.ID != released[elsewhere].ID {
		t.Errorf("the head of the asset of the other product is %+v, want the revision %s released by %s", got, inElsewhere.ID, released[elsewhere].ID)
	}
	if got := headsOf(t, heads, beside.asset.ProductID, beside.asset); len(got) != 1 ||
		got[mine.AssetID].Revision.ID != inBeside.ID || got[mine.AssetID].Release == nil || got[mine.AssetID].Release.ID != released[beside].ID {
		t.Errorf("the head of the asset of the other Workspace is %+v, want the revision %s released by %s", got, inBeside.ID, released[beside].ID)
	}
	// The asset of one product asked of another is not there, and neither is
	// the asset of one Workspace asked of another.
	crossed := map[string]struct {
		product string
		asset   domain.AssetRef
	}{
		"this asset, asked of a third product":     {ids.NewUUID(), mine},
		"this asset under a third Workspace":       {mine.ProductID, domain.AssetRef{ProductID: mine.ProductID, WorkspaceID: ids.NewUUID(), AssetID: mine.AssetID}},
		"an asset that this product does not have": {mine.ProductID, domain.AssetRef{ProductID: mine.ProductID, WorkspaceID: mine.WorkspaceID, AssetID: ids.NewUUID()}},
	}
	for name, c := range crossed {
		if got := headsOf(t, heads, c.product, c.asset); len(got) != 0 {
			t.Errorf("%s: the heads are %+v, want none", name, got)
		}
	}
	// An empty set reads no asset.
	if got, err := heads.Heads(t.Context(), mine.ProductID, nil); err != nil || len(got) != 0 {
		t.Errorf("the heads of no asset are %+v (%v), want none", got, err)
	}
}

// Of two releases prepared at the same instant the last is the one of the
// greater identifier, as the list of the releases of the asset orders them.
func TestTheLastReleaseOfTwoPreparedAtTheSameInstantIsTheOneOfTheGreaterIdentifier(t *testing.T) {
	f, _ := newStatusFixture(t)
	releases, heads := postgres.NewReleases(f.fabric.Framework), postgres.NewHeads(f.fabric.Framework)
	revision := f.record()
	at := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	names := []string{ids.NewUUID(), ids.NewUUID()}
	slices.Sort(names)
	// The greater one is recorded first, and has the lesser version.
	for index, id := range []string{names[1], names[0]} {
		release, payload := f.release(revision, "1.0."+string(rune('0'+index)), at)
		release.ID = id
		if created, err := f.keep(releases, release, payload); err != nil || !created {
			t.Fatalf("record a release: %t, %v", created, err)
		}
	}
	got := headsOf(t, heads, f.asset.ProductID, f.asset)[f.asset.AssetID]
	listed, err := releases.List(t.Context(), f.asset, nil, 100)
	if err != nil || len(listed) != 2 {
		t.Fatalf("the releases of the asset: %+v, %v", listed, err)
	}
	if got.Release == nil || got.Release.ID != names[1] || !sameRelease(*got.Release, listed[1]) {
		t.Errorf("the last release is %+v, want %s, the last of %v", got.Release, names[1], identifiers(listed))
	}
}

// headsPlan is a node of the plan PostgreSQL makes of the statement, with
// what it did when it ran.
type headsPlan struct {
	Type     string      `json:"Node Type"`
	Relation string      `json:"Relation Name"`
	Rows     float64     `json:"Actual Rows"`
	Loops    float64     `json:"Actual Loops"`
	Removed  float64     `json:"Rows Removed by Filter"`
	Recheck  float64     `json:"Rows Removed by Index Recheck"`
	Children []headsPlan `json:"Plans"`
}

// rowsOf is how many rows of the table the plan read, whether it kept them or
// not, and whether it read the table from its first row to its last.
func rowsOf(root headsPlan, table string) (read float64, scanned bool) {
	var walk func(headsPlan)
	walk = func(node headsPlan) {
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

// This guards work performed, not elapsed time: the heads of a set of assets
// are found by the indexes of the assets, and what is read grows with the set
// and not with the registry around it. The tables are made big on purpose:
// twenty thousand revisions and releases of other products, and nine hundred
// revisions and a hundred releases of this one.
func TestTheHeadsOfASetAreReadInProportionToTheSetAndNotByReadingTheRegistry(t *testing.T) {
	f, database := newStatusFixture(t)
	owner, app := pgtest.Connect(t, database.Migrator), pgtest.Connect(t, database.Application)
	ctx := t.Context()
	exec := func(statement string, arguments ...any) {
		t.Helper()

		if _, err := owner.Exec(ctx, statement, arguments...); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	const recordSnapshots = `
INSERT INTO contractregistry.source_snapshot (source_snapshot_id, product_id, api_asset_id, source_kind, entrypoint, source_digest,
    ref_closure_digest, ref_closure_manifest, captured_at, captured_by_issuer, captured_by_subject)
SELECT snapshot_id, product_id, asset_id, 'UPLOAD', 'openapi.yaml', digest, digest, '{}'::bytea, now(), 'https://issuer.example', 'author'
FROM `
	const recordRevisions = `
INSERT INTO contractregistry.contract_revision (revision_id, product_id, workspace_id, api_asset_id, source_snapshot_id, revision_sequence, spec_version, status)
SELECT revision_id, product_id, workspace_id, asset_id, snapshot_id, sequence, '3.1.2', 'DRAFT' FROM `

	// Twenty thousand assets of other products, with a revision each.
	exec(`CREATE TEMPORARY TABLE others AS
SELECT gen_random_uuid() AS product_id, gen_random_uuid() AS workspace_id, gen_random_uuid() AS asset_id,
       gen_random_uuid() AS snapshot_id, gen_random_uuid() AS revision_id, 1 AS sequence,
       'sha256:' || encode(sha256(gen_random_uuid()::text::bytea), 'hex') AS digest
FROM generate_series(1, 20000)`)
	exec(recordSnapshots + `others`)
	exec(recordRevisions + `others`)
	// The product of the test: three hundred assets with three revisions each,
	// and a release of the second revision of a hundred of them.
	product, workspace := f.asset.ProductID, f.asset.WorkspaceID
	exec(fmt.Sprintf(`CREATE TEMPORARY TABLE mine AS
SELECT '%s'::uuid AS product_id, '%s'::uuid AS workspace_id, a.asset_id, a.n, v AS sequence, gen_random_uuid() AS snapshot_id,
       gen_random_uuid() AS revision_id, 'sha256:' || encode(sha256(gen_random_uuid()::text::bytea), 'hex') AS digest
FROM (SELECT gen_random_uuid() AS asset_id, g AS n FROM generate_series(1, 300) g) a CROSS JOIN generate_series(1, 3) v`, product, workspace))
	exec(recordSnapshots + `mine`)
	exec(recordRevisions + `mine`)
	exec(`INSERT INTO contractregistry.release_artifact (artifact_digest, payload)
VALUES ('sha256:' || encode(sha256('{}'::bytea), 'hex'), '{}'::bytea)`)
	// A table of a hundred rows is read from its first row to its last
	// whatever the statement, so the releases of the other products are made
	// as many as their revisions.
	const recordReleases = `
INSERT INTO contractregistry.contract_release (release_id, product_id, workspace_id, api_asset_id, revision_id, review_id, check_run_id,
    policy_snapshot_id, policy_digest, input_digest, artifact_digest, release_version, prepared_at, prepared_by_issuer, prepared_by_subject)
SELECT gen_random_uuid(), product_id, workspace_id, asset_id, revision_id, gen_random_uuid(), gen_random_uuid(), gen_random_uuid(),
       digest, digest, 'sha256:' || encode(sha256('{}'::bytea), 'hex'), '1.0.0', now(), 'https://issuer.example', 'publisher'
FROM `
	exec(recordReleases + `others`)
	exec(recordReleases + `mine WHERE sequence = 2 AND n <= 100`)
	for _, table := range []string{"source_snapshot", "contract_revision", "contract_release"} {
		exec("ANALYZE contractregistry." + table)
	}

	assetsOf := func(from, to int) (names, workspaces []string) {
		t.Helper()

		rows, err := owner.Query(ctx, `SELECT asset_id::text FROM mine WHERE sequence = 1 AND n BETWEEN $1 AND $2 ORDER BY n`, from, to)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			names, workspaces = append(names, name), append(workspaces, workspace)
		}
		return names, workspaces
	}
	explain := func(names, workspaces []string) headsPlan {
		t.Helper()

		var encoded []byte
		if err := app.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+postgres.SelectHeadsForTest,
			product, names, workspaces).Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		var plans []struct{ Plan headsPlan }
		if err := json.Unmarshal(encoded, &plans); err != nil || len(plans) != 1 {
			t.Fatalf("decode the execution plan: %v: %s", err, encoded)
		}
		return plans[0].Plan
	}

	var total int
	if err := owner.QueryRow(ctx, `SELECT count(*) FROM contractregistry.contract_revision`).Scan(&total); err != nil || total != 20900 {
		t.Fatalf("the registry holds %d revisions (%v), want 20900", total, err)
	}
	small, large := explain(assetsOf(1, 10)), explain(assetsOf(1, 100))
	for _, table := range []string{"contract_revision", "source_snapshot", "contract_release"} {
		few, fewScanned := rowsOf(small, table)
		many, manyScanned := rowsOf(large, table)
		t.Logf("%s: the heads of 10 assets read %.0f rows, those of 100 read %.0f, of %d revisions", table, few, many, total)
		if few < 1 || fewScanned || manyScanned {
			t.Errorf("%s: the heads of 10 assets read %.0f rows (every one: %t, %t)", table, few, fewScanned, manyScanned)
		}
		// A row for each asset, and the releases of an asset that the check of
		// whether its last revision is released reads: never more than a few.
		if many > 400 || many > 12*few {
			t.Errorf("%s: the heads of 100 assets read %.0f rows and those of 10 read %.0f, want about ten times as many and no more than 400", table, many, few)
		}
	}
}

// savedRow stands in for a row of the statement of the heads: it hands over
// the values it was given, in the order the statement has its columns, and
// nil stands for a column that is absent.
type savedRow struct {
	pgx.CollectableRow
	values []any
}

func (r savedRow) Scan(destinations ...any) error {
	if len(destinations) != len(r.values) {
		return fmt.Errorf("%d destinations for %d values", len(destinations), len(r.values))
	}
	for index, destination := range destinations {
		target := reflect.ValueOf(destination).Elem()
		if r.values[index] == nil {
			target.SetZero()
			continue
		}
		target.Set(reflect.ValueOf(r.values[index]))
	}
	return nil
}

// The columns of a release come all together or not at all: the statement
// joins a release whole or none. A row that has some of them is a row the
// registry cannot have made, and it is not made into a head with a release
// that lacks a member; one that has none is the head of an asset without a
// release. The review is the one member a whole release may lack: a release
// prepared on its check alone names none.
func TestARowWithAReleaseThatIsNotWholeIsNoHead(t *testing.T) {
	text := func(value string) *string { return &value }
	instant := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	const (
		releaseColumns = 13
		reviewColumn   = 2
	)
	row := func(release []any, released bool) savedRow {
		values := []any{
			"6d7e8f90-a1b2-4c3d-8e4f-5a6b7c8d9e0f", "8f90a1b2-c3d4-4e5f-a6b7-c8d9e0f1a2b3", "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6",
			"7d2e9f10-3a4b-4c5d-8e6f-0a1b2c3d4e5f", "4b5c6d7e-8f90-4a1b-9c2d-3e4f5a6b7c8d",
			int64(3), "APPROVED", int64(3), "UPLOAD", "", "", "", "openapi.yaml", domain.OpenAPI, "3.1.2",
			"sha256:" + fmt.Sprintf("%064x", 1), "sha256:" + fmt.Sprintf("%064x", 2), instant, "https://issuer.example", "author",
		}
		return savedRow{values: append(append(values, release...), released)}
	}
	whole := func() []any {
		return []any{
			text("6f708192-a3b4-4c5d-9e6f-7a8b9c0d1e2f"), text("6d7e8f90-a1b2-4c3d-8e4f-5a6b7c8d9e0f"), text("9a0b1c2d-3e4f-4a5b-8c6d-7e8f9a0b1c2d"),
			text("7c8d9e0f-1a2b-4c3d-9e4f-5a6b7c8d9e0f"), text("5e6f7081-92a3-4b4c-8d5e-6f708192a3b4"), text("sha256:" + fmt.Sprintf("%064x", 3)),
			text("sha256:" + fmt.Sprintf("%064x", 2)), text("sha256:" + fmt.Sprintf("%064x", 4)), text("1.0.0"), &instant, text("https://issuer.example"), text("publisher"),
			[]byte(`[{"rule_id": "F04-GOV-003", "reason": "accepted"}]`),
		}
	}

	head, err := postgres.ScanHeadForTest(row(whole(), true))
	if err != nil || head.Release == nil || head.Release.ID != "6f708192-a3b4-4c5d-9e6f-7a8b9c0d1e2f" || head.Release.Version != "1.0.0" ||
		head.Release.RevisionID != "6d7e8f90-a1b2-4c3d-8e4f-5a6b7c8d9e0f" || !head.Release.PreparedAt.Equal(instant) || !head.Released ||
		head.Revision.Sequence != 3 || head.Release.Asset != head.Revision.Asset ||
		head.Release.ReviewID != "9a0b1c2d-3e4f-4a5b-8c6d-7e8f9a0b1c2d" ||
		!reflect.DeepEqual(head.Release.Waivers, []domain.Waiver{{RuleID: "F04-GOV-003", Reason: "accepted"}}) {
		t.Fatalf("a row with a whole release is the head %+v (%v)", head, err)
	}
	unreviewed := whole()
	unreviewed[reviewColumn] = nil
	if head, err := postgres.ScanHeadForTest(row(unreviewed, true)); err != nil || head.Release == nil || head.Release.ReviewID != "" {
		t.Errorf("a row with a release that names no review is the head %+v (%v), want the release without a review", head, err)
	}
	if head, err = postgres.ScanHeadForTest(row(make([]any, releaseColumns), false)); err != nil || head.Release != nil || head.Released {
		t.Errorf("a row with no release is the head %+v (%v), want one that has none", head, err)
	}
	for index := 1; index < releaseColumns; index++ {
		if index == reviewColumn {
			continue
		}
		release := whole()
		release[index] = nil
		if head, err := postgres.ScanHeadForTest(row(release, false)); err == nil || head.Release != nil || !strings.Contains(err.Error(), "not whole") {
			t.Errorf("a row whose release lacks its column %d is the head %+v (%v), want the failure that it is not whole", index, head, err)
		}
	}
}
