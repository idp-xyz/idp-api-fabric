package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
)

// release is a release of the revision under the version, prepared at the
// time on a check of it with the waivers, and the bytes of its artifact. A
// release is prepared from a revision whatever its status, so the revisions
// of these tests are the drafts they were recorded as.
func (f *statusFixture) release(revision domain.Revision, version string, at time.Time, waivers ...domain.Waiver) (domain.Release, []byte) {
	f.t.Helper()
	files, err := f.store.Files(f.t.Context(), f.asset, revision.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	payload, digest, err := domain.BuildArtifact(revision, files)
	if err != nil {
		f.t.Fatal(err)
	}
	return domain.Release{
		ID: ids.NewUUID(), Asset: f.asset, RevisionID: revision.ID, Specification: revision.Specification,
		Evidence: domain.CheckEvidence{
			CheckRunID: ids.NewUUID(), PolicySnapshotID: ids.NewUUID(), PolicyDigest: "sha256:" + strings.Repeat("a", 64),
		},
		Waivers: waivers,
		Version: version, InputDigest: revision.RefClosureDigest, ArtifactDigest: digest,
		PreparedAt: at, PreparedBy: domain.Principal{Issuer: "https://issuer.example", Subject: "publisher"},
	}, payload
}

func (f *statusFixture) keep(releases *postgres.Releases, release domain.Release, payload []byte) (bool, error) {
	f.t.Helper()
	var created bool
	err := f.fabric.WithinTransaction(f.t.Context(), func(ctx context.Context) error {
		var err error
		created, err = releases.Record(ctx, release, payload)
		return err
	})
	return created, err
}

func identifiers(releases []domain.Release) []string {
	listed := make([]string, 0, len(releases))
	for _, release := range releases {
		listed = append(listed, release.ID)
	}
	return listed
}

// A release is recorded once under its version of the asset, with the bytes
// of its artifact under their digest: the same bytes are kept once, however
// many releases are of them. It is read under its asset, its workspace and
// its product, and listed in the order it was prepared.
func TestAReleaseIsRecordedOnceUnderItsVersionAndReadUnderItsAsset(t *testing.T) {
	f, database := newStatusFixture(t)
	releases := postgres.NewReleases(f.fabric.Framework)
	owner := pgtest.Connect(t, database.Migrator)
	count := func(table string) (rows int) {
		t.Helper()
		if err := owner.QueryRow(t.Context(), `SELECT count(*) FROM contractregistry.`+table).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	revision, next := f.record(), f.record()
	morning := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

	first, payload := f.release(revision, "1.0.0", morning)
	if created, err := f.keep(releases, first, payload); err != nil || !created {
		t.Fatalf("the first release: recorded %t, %v", created, err)
	}
	for name, read := range map[string]func() (domain.Release, bool, error){
		"by its identifier": func() (domain.Release, bool, error) { return releases.Find(t.Context(), f.asset, first.ID) },
		"by its version":    func() (domain.Release, bool, error) { return releases.Version(t.Context(), f.asset, "1.0.0") },
	} {
		if got, found, err := read(); err != nil || !found || !sameRelease(got, first) {
			t.Errorf("read %s: %+v found %t, want %+v: %v", name, got, found, first, err)
		}
	}
	if stored, err := releases.Artifact(t.Context(), first.ArtifactDigest); err != nil || !bytes.Equal(stored, payload) {
		t.Errorf("the artifact reads as %d bytes, want the %d that were recorded: %v", len(stored), len(payload), err)
	}

	// The version is taken: whatever else asks for it is told so and
	// changes nothing.
	again, _ := f.release(revision, "1.0.0", morning.Add(time.Hour))
	if created, err := f.keep(releases, again, payload); err != nil || created {
		t.Errorf("a second release under the version: recorded %t, %v; want it not recorded", created, err)
	}
	if got, _, _ := releases.Version(t.Context(), f.asset, "1.0.0"); got.ID != first.ID || count("contract_release") != 1 {
		t.Errorf("the version names %s and %d releases are stored, want %s and one", got.ID, count("contract_release"), first.ID)
	}

	// The same bytes under another version are another release of one
	// artifact; other bytes are another artifact.
	second, _ := f.release(revision, "1.0.1", morning)
	third, other := f.release(next, "2.0.0", morning.Add(2*time.Hour))
	for _, c := range []struct {
		release domain.Release
		payload []byte
	}{{second, payload}, {third, other}} {
		if created, err := f.keep(releases, c.release, c.payload); err != nil || !created {
			t.Fatalf("release %s: recorded %t, %v", c.release.Version, created, err)
		}
	}
	if second.ArtifactDigest != first.ArtifactDigest || third.ArtifactDigest == first.ArtifactDigest ||
		count("contract_release") != 3 || count("release_artifact") != 2 {
		t.Errorf("%d releases of %d artifacts are stored, want 3 of 2", count("contract_release"), count("release_artifact"))
	}

	// The two that were prepared at the same time come in the order of
	// their identifiers, the later one after them.
	order := []string{first.ID, second.ID}
	slices.Sort(order)
	order = append(order, third.ID)
	listed, err := releases.List(t.Context(), f.asset, nil, 10)
	if err != nil || !slices.Equal(identifiers(listed), order) {
		t.Fatalf("listed %v, want %v: %v", identifiers(listed), order, err)
	}
	page, err := releases.List(t.Context(), f.asset, nil, 2)
	rest, restErr := releases.List(t.Context(), f.asset, &ports.Position{At: morning, ID: order[0]}, 10)
	if err != nil || restErr != nil || !slices.Equal(identifiers(page), order[:2]) || !slices.Equal(identifiers(rest), order[1:]) {
		t.Errorf("a page of two is %v and what follows the first is %v, want %v and %v: %v, %v",
			identifiers(page), identifiers(rest), order[:2], order[1:], err, restErr)
	}

	// A release belongs to its asset, its workspace and its product.
	elsewhere := map[string]domain.AssetRef{
		"another asset":     {ProductID: f.asset.ProductID, WorkspaceID: f.asset.WorkspaceID, AssetID: ids.NewUUID()},
		"another workspace": {ProductID: f.asset.ProductID, WorkspaceID: ids.NewUUID(), AssetID: f.asset.AssetID},
		"another product":   {ProductID: ids.NewUUID(), WorkspaceID: f.asset.WorkspaceID, AssetID: f.asset.AssetID},
	}
	for name, asset := range elsewhere {
		_, byID, err := releases.Find(t.Context(), asset, first.ID)
		_, byVersion, versionErr := releases.Version(t.Context(), asset, "1.0.0")
		listed, listErr := releases.List(t.Context(), asset, nil, 10)
		if byID || byVersion || len(listed) != 0 || err != nil || versionErr != nil || listErr != nil {
			t.Errorf("under %s: found %t by identifier, %t by version and %d listed; want none (%v, %v, %v)",
				name, byID, byVersion, len(listed), err, versionErr, listErr)
		}
	}
	if _, found, err := releases.Find(t.Context(), f.asset, ids.NewUUID()); found || err != nil {
		t.Errorf("a release nobody prepared: found %t, %v", found, err)
	}
	if _, err := releases.Artifact(t.Context(), "sha256:"+strings.Repeat("0", 64)); err == nil {
		t.Error("an artifact nobody stored was read")
	}

	// A release is recorded in the transaction of what prepares it, and
	// only with the bytes its digest names.
	fourth, _ := f.release(next, "2.0.1", morning)
	if _, err := releases.Record(t.Context(), fourth, other); err == nil {
		t.Error("a release was recorded outside a transaction")
	}
	fourth.ArtifactDigest = "sha256:" + strings.Repeat("0", 64)
	if created, err := f.keep(releases, fourth, other); err == nil || created {
		t.Errorf("a release whose bytes are not those of its digest: recorded %t, %v", created, err)
	}
	if count("contract_release") != 3 || count("release_artifact") != 2 {
		t.Errorf("%d releases of %d artifacts are stored after the refusals, want 3 of 2", count("contract_release"), count("release_artifact"))
	}
}

// The tables hold every writer to what a release is, whatever the model let
// through: the bytes of an artifact are the ones of its digest and within
// the bound, a release is of a revision of its own asset under a version
// that is taken once, and the role a process connects with adds releases
// and artifacts and changes or removes none.
func TestTheTablesKeepAReleaseAndItsArtifactAsTheyWereRecorded(t *testing.T) {
	f, database := newStatusFixture(t)
	releases := postgres.NewReleases(f.fabric.Framework)
	first, payload := f.release(f.record(), "1.0.0", time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC))
	if created, err := f.keep(releases, first, payload); err != nil || !created {
		t.Fatalf("the release: recorded %t, %v", created, err)
	}

	const (
		uniqueViolation       = "23505"
		foreignKeyViolation   = "23503"
		checkViolation        = "23514"
		notNullViolation      = "23502"
		insufficientPrivilege = "42501"
	)
	// another is a copy of the release under a new identifier, with the
	// columns that are named replaced.
	another := func(replaced map[string]string) string {
		columns := []string{
			"release_id", "product_id", "workspace_id", "api_asset_id", "revision_id", "review_id", "check_run_id",
			"policy_snapshot_id", "policy_digest", "input_digest", "artifact_digest", "release_version",
			"prepared_at", "prepared_by_issuer", "prepared_by_subject", "waivers",
		}
		values := slices.Clone(columns)
		values[0] = "gen_random_uuid()"
		for i, column := range columns {
			if value, found := replaced[column]; found {
				values[i] = value
			}
		}
		return fmt.Sprintf(`INSERT INTO contractregistry.contract_release (%s) SELECT %s FROM contractregistry.contract_release WHERE release_id = '%s'`,
			strings.Join(columns, ", "), strings.Join(values, ", "), first.ID)
	}
	artifact := func(digest, payload string) string {
		return fmt.Sprintf(`INSERT INTO contractregistry.release_artifact (artifact_digest, payload) SELECT %s, p FROM (SELECT %s AS p) AS bytes`, digest, payload)
	}
	const digestOfBytes = `'sha256:' || encode(sha256(p), 'hex')`
	zeros := func(count int) string { return fmt.Sprintf(`decode(repeat('00', %d), 'hex')`, count) }

	statements := []struct {
		name      string
		statement string
		state     string
	}{
		{"a second release under a version", another(nil), uniqueViolation},
		{"a version that begins with a dot", another(map[string]string{"release_version": `'.1'`}), checkViolation},
		{"a version of 65 characters", another(map[string]string{"release_version": `repeat('9', 65)`}), checkViolation},
		{"an empty version", another(map[string]string{"release_version": `''`}), checkViolation},
		{"a release under another asset than its revision", another(map[string]string{"release_version": `'2'`, "api_asset_id": "gen_random_uuid()"}), foreignKeyViolation},
		{"a release under another workspace than its revision", another(map[string]string{"release_version": `'2'`, "workspace_id": "gen_random_uuid()"}), foreignKeyViolation},
		{"a release under another product than its revision", another(map[string]string{"release_version": `'2'`, "product_id": "gen_random_uuid()"}), foreignKeyViolation},
		{"a release of a revision nobody recorded", another(map[string]string{"release_version": `'2'`, "revision_id": "gen_random_uuid()"}), foreignKeyViolation},
		{"a release of an artifact nobody stored", another(map[string]string{"release_version": `'2'`, "artifact_digest": `'sha256:' || repeat('0', 64)`}), foreignKeyViolation},
		{"a policy digest that is none", another(map[string]string{"release_version": `'2'`, "policy_digest": `'md5:0123'`}), checkViolation},
		{"an input digest that is none", another(map[string]string{"release_version": `'2'`, "input_digest": `repeat('a', 64)`}), checkViolation},
		{"waivers that are no list", another(map[string]string{"release_version": `'2'`, "waivers": `'{"rule_id": "F04-GOV-003"}'::jsonb`}), checkViolation},
		{"no waivers at all", another(map[string]string{"release_version": `'2'`, "waivers": `NULL`}), notNullViolation},
		{"waivers of a release prepared under a review", another(map[string]string{
			"release_version": `'2'`, "review_id": "gen_random_uuid()",
			"waivers": `'[{"rule_id": "F04-GOV-003", "reason": "accepted"}]'::jsonb`,
		}), checkViolation},
		{"bytes that are not those of the digest", artifact(`'sha256:' || repeat('0', 64)`, zeros(8)), checkViolation},
		{"an artifact of no bytes", artifact(digestOfBytes, `''::bytea`), checkViolation},
		{"an artifact a byte over the bound", artifact(digestOfBytes, zeros(domain.MaxArtifactBytes+1)), checkViolation},
		{"change a release", `UPDATE contractregistry.contract_release SET release_version = '9'`, insufficientPrivilege},
		{"remove a release", `DELETE FROM contractregistry.contract_release`, insufficientPrivilege},
		{"change an artifact", `UPDATE contractregistry.release_artifact SET payload = 'x'`, insufficientPrivilege},
		{"remove an artifact", `DELETE FROM contractregistry.release_artifact`, insufficientPrivilege},
	}
	state := func(err error) string {
		var refused *pgconn.PgError
		if errors.As(err, &refused) {
			return refused.Code
		}
		return ""
	}
	// The role a process connects with, and the owner of the tables: a
	// constraint is no privilege, so it holds both.
	app, owner := pgtest.Connect(t, database.Application), pgtest.Connect(t, database.Migrator)
	for _, s := range statements {
		if _, err := app.Exec(t.Context(), s.statement); state(err) != s.state {
			t.Errorf("%s, as the application role: got %v, want SQLSTATE %s", s.name, err, s.state)
		}
		if s.state == insufficientPrivilege {
			continue
		}
		if _, err := owner.Exec(t.Context(), s.statement); state(err) != s.state {
			t.Errorf("%s, as the owner of the tables: got %v, want SQLSTATE %s", s.name, err, s.state)
		}
	}
	if got, found, err := releases.Find(t.Context(), f.asset, first.ID); err != nil || !found || got.Version != "1.0.0" || got.ArtifactDigest != first.ArtifactDigest {
		t.Errorf("after the refused statements the release reads %+v, found %t: %v", got, found, err)
	}
	var stored, artifacts int
	if err := owner.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM contractregistry.contract_release),
       (SELECT count(*) FROM contractregistry.release_artifact)`).Scan(&stored, &artifacts); err != nil || stored != 1 || artifacts != 1 {
		t.Errorf("%d releases and %d artifacts are stored, want the one of each: %v", stored, artifacts, err)
	}

	// What the model writes is what the tables take, by hand as well: an
	// artifact of exactly the bound, another version of the revision with a
	// waiver, and a release under a review as a build before this one
	// records it, which names no waivers.
	for name, statement := range map[string]string{
		"an artifact of the largest size": artifact(digestOfBytes, zeros(domain.MaxArtifactBytes)),
		"another version of the revision with a waiver": another(map[string]string{
			"release_version": `'1.0.1'`, "waivers": `'[{"rule_id": "F04-GOV-003", "reason": "accepted"}]'::jsonb`,
		}),
		"a release under a review, as the build before records it": fmt.Sprintf(`
INSERT INTO contractregistry.contract_release (release_id, product_id, workspace_id, api_asset_id, revision_id, review_id,
    check_run_id, policy_snapshot_id, policy_digest, input_digest, artifact_digest, release_version, prepared_at,
    prepared_by_issuer, prepared_by_subject)
SELECT gen_random_uuid(), product_id, workspace_id, api_asset_id, revision_id, gen_random_uuid(), check_run_id,
       policy_snapshot_id, policy_digest, input_digest, artifact_digest, '1.0.2', prepared_at, prepared_by_issuer, prepared_by_subject
FROM contractregistry.contract_release WHERE release_id = '%s'`, first.ID),
	} {
		if _, err := app.Exec(t.Context(), statement); err != nil {
			t.Errorf("%s, as the application role: %v", name, err)
		}
	}
}

// A release prepared on a check names no review and keeps the waivers it was
// prepared with, rule and reason, in the order it was given them, however it
// is read. One that waives nothing reads no waivers. A release a build before
// this one prepared under a review is read with its review and no waivers of
// its own: those are the review's.
func TestAReleaseKeepsItsWaiversAndTheReviewOfBefore(t *testing.T) {
	f, database := newStatusFixture(t)
	releases := postgres.NewReleases(f.fabric.Framework)
	owner := pgtest.Connect(t, database.Migrator)
	revision := f.record()
	morning := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)

	waived, payload := f.release(revision, "1.0.0", morning,
		domain.Waiver{RuleID: "F04-GOV-002", Reason: "the summary follows in 1.1"},
		domain.Waiver{RuleID: "F04-GOV-003", Reason: "operationIds are named by the gateway, \"as agreed\""})
	plain, _ := f.release(revision, "1.0.1", morning.Add(time.Minute))
	for _, release := range []domain.Release{waived, plain} {
		if created, err := f.keep(releases, release, payload); err != nil || !created {
			t.Fatalf("release %s: recorded %t, %v", release.Version, created, err)
		}
	}
	listed, err := releases.List(t.Context(), f.asset, nil, 10)
	if err != nil || len(listed) != 2 {
		t.Fatalf("listed %+v: %v", listed, err)
	}
	for i, want := range []domain.Release{waived, plain} {
		byID, _, err := releases.Find(t.Context(), f.asset, want.ID)
		byVersion, _, versionErr := releases.Version(t.Context(), f.asset, want.Version)
		if err != nil || versionErr != nil || !sameRelease(byID, want) || !sameRelease(byVersion, want) || !sameRelease(listed[i], want) {
			t.Errorf("release %s reads %+v by identifier, %+v by version and %+v in the list, want %+v (%v, %v)",
				want.Version, byID, byVersion, listed[i], want, err, versionErr)
		}
	}
	if plain.Waivers != nil || listed[1].Waivers != nil || listed[1].ReviewID != "" {
		t.Errorf("a release that waives nothing reads waivers %v and review %q, want none", listed[1].Waivers, listed[1].ReviewID)
	}
	// The table says the same: no review, and the waivers as a list.
	var review *string
	var stored string
	if err := owner.QueryRow(t.Context(), `SELECT review_id::text, waivers::text FROM contractregistry.contract_release
WHERE release_id = $1`, plain.ID).Scan(&review, &stored); err != nil || review != nil || stored != "[]" {
		t.Errorf("the release that waives nothing is stored with review %v and waivers %s, want NULL and []: %v", review, stored, err)
	}

	// A release of before: the build before this one names its review and
	// nothing of waivers.
	before, reviewID := ids.NewUUID(), ids.NewUUID()
	if _, err := owner.Exec(t.Context(), `
INSERT INTO contractregistry.contract_release (release_id, product_id, workspace_id, api_asset_id, revision_id, review_id,
    check_run_id, policy_snapshot_id, policy_digest, input_digest, artifact_digest, release_version, prepared_at,
    prepared_by_issuer, prepared_by_subject)
SELECT $1, product_id, workspace_id, api_asset_id, revision_id, $2, check_run_id, policy_snapshot_id, policy_digest,
       input_digest, artifact_digest, '0.9.0', prepared_at, prepared_by_issuer, prepared_by_subject
FROM contractregistry.contract_release WHERE release_id = $3`, before, reviewID, plain.ID); err != nil {
		t.Fatal(err)
	}
	got, found, err := releases.Find(t.Context(), f.asset, before)
	want := plain
	want.ID, want.ReviewID, want.Version = before, reviewID, "0.9.0"
	if err != nil || !found || !sameRelease(got, want) {
		t.Errorf("the release of before reads %+v (found %t), want %+v: %v", got, found, want, err)
	}
}
