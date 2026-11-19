package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
)

func TestConcurrentRegistrationReusesContentAndKeepsTheOriginalBytes(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fabric.Close)
	store := postgres.NewRevisions(fabric.Framework)
	asset := domain.AssetRef{ProductID: ids.NewUUID(), WorkspaceID: ids.NewUUID(), AssetID: ids.NewUUID()}
	actor := domain.Principal{Issuer: "https://issuer.example", Subject: "author"}
	content := "openapi: 3.1.2\r\ninfo: {title: Shipments, version: v1}\r\n"
	capture := func(bytes string) domain.SourceSnapshot {
		t.Helper()
		snapshot, err := domain.CaptureSource("openapi.yaml", "3.1.2", []domain.SourceFile{
			{Path: "openapi.yaml", Content: bytes},
		})
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	source := capture(content)
	newDraft := func() domain.Draft {
		return domain.Draft{
			ID: ids.NewUUID(), SourceSnapshotID: ids.NewUUID(), Asset: asset,
			CapturedBy: actor, CapturedAt: time.Now(),
		}
	}

	const writers = 4
	type result struct {
		record ports.Registration
		err    error
	}
	results := make(chan result, writers)
	for range writers {
		go func() {
			var registered ports.Registration
			err := fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
				var err error
				registered, err = store.Register(ctx, newDraft(), source)
				return err
			})
			results <- result{record: registered, err: err}
		}()
	}
	var revision domain.Revision
	created := 0
	for range writers {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.record.Created {
			created++
		}
		if revision.ID == "" {
			revision = got.record.Revision
		}
		if got.record.Revision != revision || revision.Sequence != 1 || revision.RefClosureDigest != source.RefClosureDigest {
			t.Fatalf("concurrent registration disagree: %+v / %+v", got.record.Revision, revision)
		}
	}
	if created != 1 {
		t.Fatalf("created %d revisions for the same closure", created)
	}

	// A failure after registration rolls back the snapshot, files and sequence.
	changed := capture(content + "# next\n")
	rolledBack := errors.New("an outbox write failed")
	err = fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
		if _, err := store.Register(ctx, newDraft(), changed); err != nil {
			return err
		}
		return rolledBack
	})
	if !errors.Is(err, rolledBack) {
		t.Fatalf("roll back the new registration: %v", err)
	}
	err = fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
		next, err := store.Register(ctx, newDraft(), changed)
		if err == nil && (!next.Created || next.Revision.Sequence != 2 || next.Revision.ID == revision.ID) {
			t.Errorf("unexpected second revision: %+v", next)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	owner := pgtest.Connect(t, database.Migrator)
	var original, manifest []byte
	var subject string
	err = owner.QueryRow(t.Context(), `
SELECT f.content, s.ref_closure_manifest, s.captured_by_subject
FROM contractregistry.source_file f
JOIN contractregistry.source_snapshot s USING (source_snapshot_id)
WHERE f.source_snapshot_id = $1 AND f.path = 'openapi.yaml'`, revision.SourceSnapshotID).Scan(&original, &manifest, &subject)
	if err != nil || string(original) != content || string(manifest) != source.Manifest() || subject != actor.Subject {
		t.Fatalf("original source evidence was not preserved: content %q, manifest %q, subject %q, error %v", original, manifest, subject, err)
	}
	var snapshots int
	if err := owner.QueryRow(t.Context(), "SELECT count(*) FROM contractregistry.source_snapshot").Scan(&snapshots); err != nil || snapshots != 2 {
		t.Fatalf("got %d snapshots, want two: %v", snapshots, err)
	}

	app := pgtest.Connect(t, database.Application)
	if _, err := app.Exec(t.Context(), "UPDATE contractregistry.contract_revision SET revision_sequence = 9"); err == nil {
		t.Fatal("the application role can rewrite revision history")
	}
}

// A revision says who recorded it: whoever submitted its content first. It
// says so however it is read, and it keeps saying so when someone else
// submits the same content again or its status moves.
func TestARevisionSaysWhoRecordedItWhoeverSubmitsItsContentAgain(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fabric.Close)
	store := postgres.NewRevisions(fabric.Framework)
	asset := domain.AssetRef{ProductID: ids.NewUUID(), WorkspaceID: ids.NewUUID(), AssetID: ids.NewUUID()}
	author := domain.Principal{Issuer: "https://issuer.example", Subject: "author"}
	coauthor := domain.Principal{Issuer: "https://other-issuer.example", Subject: "coauthor"}
	source, err := domain.CaptureSource("openapi.yaml", "3.1.2", []domain.SourceFile{
		{Path: "openapi.yaml", Content: "openapi: 3.1.2\ninfo: {title: Shipments, version: v1}\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	register := func(by domain.Principal) ports.Registration {
		t.Helper()
		var registered ports.Registration
		if err := fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
			var err error
			registered, err = store.Register(ctx, domain.Draft{
				ID: ids.NewUUID(), SourceSnapshotID: ids.NewUUID(), Asset: asset, CapturedBy: by, CapturedAt: time.Now(),
			}, source)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return registered
	}

	first := register(author)
	if !first.Created || first.Revision.CreatedBy != author {
		t.Fatalf("the revision as it was registered: %+v, want it recorded by %+v", first, author)
	}
	again := register(coauthor)
	if again.Created || again.Revision.ID != first.Revision.ID || again.Revision.CreatedBy != author {
		t.Errorf("the same content submitted by someone else: %+v, want the revision %s that %+v recorded", again, first.Revision.ID, author)
	}
	var moved domain.Revision
	if err := fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
		var err error
		moved, _, err = store.Move(ctx, asset, first.Revision.ID, domain.RevisionInReview)
		return err
	}); err != nil || moved.CreatedBy != author {
		t.Errorf("the revision once it is submitted: recorded by %+v, want %+v: %v", moved.CreatedBy, author, err)
	}
	found, _, err := store.Find(t.Context(), asset, first.Revision.ID)
	if err != nil || found.CreatedBy != author {
		t.Errorf("the revision as it is found: recorded by %+v, want %+v: %v", found.CreatedBy, author, err)
	}
	listed, err := store.List(t.Context(), asset, nil, 10)
	if err != nil || len(listed) != 1 || listed[0].CreatedBy != author {
		t.Errorf("the revision as it is listed: %+v, want it recorded by %+v: %v", listed, author, err)
	}
}

const (
	firstCommit  = "1111111111111111111111111111111111111111"
	secondCommit = "2222222222222222222222222222222222222222"
	thirdCommit  = "3333333333333333333333333333333333333333"
)

// The uploads of an asset are one source and each repository is another. A
// closure is recorded once from each, under the origin it was first submitted
// with, and an entrypoint of a commit is recorded with one closure: a commit
// never changes, so a second declaration with another closure is refused and
// leaves nothing behind.
func TestASourceIsRecordedOncePerOriginAndACommitWithOneClosure(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fabric.Close)
	store := postgres.NewRevisions(fabric.Framework)
	asset := domain.AssetRef{ProductID: ids.NewUUID(), WorkspaceID: ids.NewUUID(), AssetID: ids.NewUUID()}
	actor := domain.Principal{Issuer: "https://issuer.example", Subject: "ci"}
	const content = "openapi: 3.1.2\ninfo: {title: Shipments, version: v1}\n"
	capture := func(entrypoint, bytes string) domain.SourceSnapshot {
		t.Helper()
		snapshot, err := domain.CaptureSource(entrypoint, "3.1.2", []domain.SourceFile{{Path: entrypoint, Content: bytes}})
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	started := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)
	registrations := 0
	register := func(asset domain.AssetRef, origin domain.GitOrigin, source domain.SourceSnapshot) (ports.Registration, error) {
		t.Helper()
		registrations++
		draft := domain.Draft{
			ID: ids.NewUUID(), SourceSnapshotID: ids.NewUUID(), Asset: asset, Origin: origin,
			CapturedBy: actor, CapturedAt: started.Add(time.Duration(registrations) * time.Second),
		}
		var registered ports.Registration
		err := fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
			var err error
			registered, err = store.Register(ctx, draft, source)
			return err
		})
		return registered, err
	}

	const (
		contracts = "https://git.example/logistics/contracts"
		mirror    = "https://mirror.example/logistics/contracts"
	)
	var (
		uploaded   = domain.GitOrigin{}
		onMain     = domain.GitOrigin{Repository: contracts, Commit: firstCommit, Ref: "refs/heads/main"}
		tagged     = domain.GitOrigin{Repository: contracts, Commit: secondCommit, Ref: "refs/tags/v1.0.0"}
		mirrored   = domain.GitOrigin{Repository: mirror, Commit: firstCommit}
		atFirst    = domain.GitOrigin{Repository: contracts, Commit: firstCommit}
		atThird    = domain.GitOrigin{Repository: contracts, Commit: thirdCommit}
		forked     = domain.GitOrigin{Repository: mirror, Commit: thirdCommit}
		shipments  = capture("openapi.yaml", content)
		revised    = capture("openapi.yaml", content+"# revised\n")
		diverged   = capture("openapi.yaml", content+"# diverged\n")
		elsewhere  = capture("events/openapi.yaml", content)
		recordings = []struct {
			name    string
			origin  domain.GitOrigin
			source  domain.SourceSnapshot
			created bool
			// sequence and recorded are the revision that is answered: its
			// place in the history and the origin it was recorded under.
			sequence int64
			recorded domain.GitOrigin
			refused  error
		}{
			{name: "files uploaded by hand", origin: uploaded, source: shipments, created: true, sequence: 1, recorded: uploaded},
			{name: "the same files from a repository", origin: onMain, source: shipments, created: true, sequence: 2, recorded: onMain},
			{name: "the same commit again", origin: onMain, source: shipments, sequence: 2, recorded: onMain},
			{name: "a later commit that left the files alone, under a tag", origin: tagged, source: shipments, sequence: 2, recorded: onMain},
			{name: "the same files from another repository", origin: mirrored, source: shipments, created: true, sequence: 3, recorded: mirrored},
			{name: "the same files uploaded again", origin: uploaded, source: shipments, sequence: 1, recorded: uploaded},
			{name: "other files said to be of the first commit", origin: atFirst, source: revised, refused: ports.ErrOriginConflict},
			{name: "other files of a third commit", origin: atThird, source: revised, created: true, sequence: 4, recorded: atThird},
			{name: "the first files said to be of the third commit", origin: atThird, source: shipments, refused: ports.ErrOriginConflict},
			{name: "another entrypoint of the first commit", origin: atFirst, source: elsewhere, created: true, sequence: 5, recorded: atFirst},
			{name: "the other files uploaded", origin: uploaded, source: revised, created: true, sequence: 6, recorded: uploaded},
			// A commit is a commit of its repository: what one repository
			// holds at a name says nothing of another one.
			{name: "files of their own at the third commit of the other repository", origin: forked, source: diverged, created: true, sequence: 7, recorded: forked},
		}
	)
	var history []domain.Revision
	for _, recording := range recordings {
		got, err := register(asset, recording.origin, recording.source)
		if !errors.Is(err, recording.refused) {
			t.Fatalf("%s: got %v, want %v", recording.name, err, recording.refused)
		}
		if recording.refused != nil {
			continue
		}
		revision := got.Revision
		if got.Created != recording.created || revision.Sequence != recording.sequence || revision.Origin != recording.recorded ||
			revision.SourceKind != recording.recorded.SourceKind() || revision.RefClosureDigest != recording.source.RefClosureDigest ||
			revision.Entrypoint != recording.source.Entrypoint || revision.Asset != asset {
			t.Fatalf("%s: created %t, %+v; want created %t at sequence %d under %+v",
				recording.name, got.Created, revision, recording.created, recording.sequence, recording.recorded)
		}
		if got.Created {
			history = append(history, revision)
			continue
		}
		if revision != history[recording.sequence-1] {
			t.Fatalf("%s: answered %+v, want the revision as it was recorded %+v", recording.name, revision, history[recording.sequence-1])
		}
	}

	listed, err := store.List(t.Context(), asset, nil, 100)
	if err != nil || len(listed) != len(history) {
		t.Fatalf("listed %d revisions, want %d: %v", len(listed), len(history), err)
	}
	for index, revision := range history {
		found, present, err := store.Find(t.Context(), asset, revision.ID)
		if err != nil || !present || found != revision || listed[index] != revision {
			t.Errorf("revision %d is read as %+v and listed as %+v, want %+v (%v)", revision.Sequence, found, listed[index], revision, err)
		}
	}

	// What an asset holds says nothing about another one.
	other := domain.AssetRef{ProductID: asset.ProductID, WorkspaceID: asset.WorkspaceID, AssetID: ids.NewUUID()}
	if got, err := register(other, atFirst, revised); err != nil || !got.Created || got.Revision.Sequence != 1 {
		t.Fatalf("the first commit with other files, for another asset: %+v, %v", got, err)
	}

	owner := pgtest.Connect(t, database.Migrator)
	var snapshots, files, revisions int
	if err := owner.QueryRow(t.Context(), `
SELECT (SELECT count(*) FROM contractregistry.source_snapshot),
       (SELECT count(*) FROM contractregistry.source_file),
       (SELECT count(*) FROM contractregistry.contract_revision)`).Scan(&snapshots, &files, &revisions); err != nil {
		t.Fatal(err)
	}
	if want := len(history) + 1; snapshots != want || files != want || revisions != want {
		t.Fatalf("the tables hold %d snapshots, %d files and %d revisions, want %d of each: a refusal leaves nothing", snapshots, files, revisions, want)
	}
	var kind string
	var repository, commit, ref *string
	if err := owner.QueryRow(t.Context(), `
SELECT source_kind, origin_repository, origin_commit, origin_ref
FROM contractregistry.source_snapshot WHERE source_snapshot_id = $1`, history[0].SourceSnapshotID).Scan(&kind, &repository, &commit, &ref); err != nil ||
		kind != domain.SourceUpload || repository != nil || commit != nil || ref != nil {
		t.Fatalf("an upload is stored as %s with origin %v %v %v, want UPLOAD and nothing said of an origin: %v", kind, repository, commit, ref, err)
	}
	if err := owner.QueryRow(t.Context(), `
SELECT source_kind, origin_repository, origin_commit, origin_ref
FROM contractregistry.source_snapshot WHERE source_snapshot_id = $1`, history[2].SourceSnapshotID).Scan(&kind, &repository, &commit, &ref); err != nil ||
		kind != domain.SourceGit || repository == nil || *repository != mirror || commit == nil || *commit != firstCommit || ref != nil {
		t.Fatalf("a Git source without a reference is stored as %s with origin %v %v %v: %v", kind, repository, commit, ref, err)
	}
}

// Declarations that arrive together are decided one after the other: of
// several closures said to be of one commit, one is recorded and the others
// are told of the conflict, none of them by a failed statement.
func TestConcurrentDeclarationsOfOneCommitRecordOneClosure(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fabric.Close)
	store := postgres.NewRevisions(fabric.Framework)
	asset := domain.AssetRef{ProductID: ids.NewUUID(), WorkspaceID: ids.NewUUID(), AssetID: ids.NewUUID()}
	origin := domain.GitOrigin{Repository: "https://git.example/logistics/contracts", Commit: firstCommit}

	const writers = 6
	results := make(chan error, writers)
	for writer := range writers {
		source, err := domain.CaptureSource("openapi.yaml", "3.1.2", []domain.SourceFile{{
			Path: "openapi.yaml", Content: "openapi: 3.1.2\ninfo: {title: Shipments, version: v" + string(rune('1'+writer)) + "}\n",
		}})
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			results <- fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
				registered, err := store.Register(ctx, domain.Draft{
					ID: ids.NewUUID(), SourceSnapshotID: ids.NewUUID(), Asset: asset, Origin: origin,
					CapturedBy: domain.Principal{Issuer: "https://issuer.example", Subject: "ci"}, CapturedAt: time.Now(),
				}, source)
				if err == nil && !registered.Created {
					return errors.New("a closure nobody recorded was answered as recorded")
				}
				return err
			})
		}()
	}
	recorded := 0
	for range writers {
		switch err := <-results; {
		case err == nil:
			recorded++
		case !errors.Is(err, ports.ErrOriginConflict):
			t.Errorf("a declaration failed with %v, want it recorded or told of the conflict", err)
		}
	}
	listed, err := store.List(t.Context(), asset, nil, 100)
	if err != nil || recorded != 1 || len(listed) != 1 || listed[0].Origin != origin || listed[0].Sequence != 1 {
		t.Fatalf("%d declarations were recorded and the history is %+v, want one revision of the commit: %v", recorded, listed, err)
	}
}

// The table holds every writer to what an origin is, whatever the model let
// through: a GIT snapshot names a repository and a full commit, an UPLOAD
// names neither, and no URL with credentials is stored. Each row below breaks
// one rule and is refused by the constraint that states it.
func TestTheTableHoldsASnapshotToItsOrigin(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	app := pgtest.Connect(t, database.Application)
	product, asset := ids.NewUUID(), ids.NewUUID()
	const (
		insert = `
INSERT INTO contractregistry.source_snapshot (
    source_snapshot_id, product_id, api_asset_id, source_kind, entrypoint, source_digest,
    ref_closure_digest, ref_closure_manifest, captured_at, captured_by_issuer, captured_by_subject,
    origin_repository, origin_commit, origin_ref
) VALUES ($1,$2,$3,$4,'openapi.yaml',$5,$5,'{}',now(),'https://issuer.example','author',$6,$7,$8)`
		contracts = "https://git.example/logistics/contracts"
		mirror    = "https://mirror.example/logistics/contracts"

		checkViolation  = "23514"
		uniqueViolation = "23505"
	)
	digest := func(of string) string { return "sha256:" + strings.Repeat(of, 64) }
	type row struct {
		name                    string
		kind, digest            string
		repository, commit, ref any
		state, constraint       string
	}
	rows := []row{
		{name: "an upload", kind: "UPLOAD", digest: digest("a")},
		{name: "the same closure from a repository", kind: "GIT", digest: digest("a"), repository: contracts, commit: firstCommit, ref: "refs/heads/main"},
		{name: "the same closure from another repository", kind: "GIT", digest: digest("a"), repository: mirror, commit: firstCommit},
		{name: "another closure of another commit", kind: "GIT", digest: digest("b"), repository: contracts, commit: strings.Repeat("4", 64)},

		{name: "an uploaded closure twice", kind: "UPLOAD", digest: digest("a"),
			state: uniqueViolation, constraint: "source_snapshot_upload_closure_key"},
		{name: "a closure of a repository twice", kind: "GIT", digest: digest("a"), repository: contracts, commit: thirdCommit,
			state: uniqueViolation, constraint: "source_snapshot_git_closure_key"},
		{name: "a commit with a second closure", kind: "GIT", digest: digest("c"), repository: contracts, commit: firstCommit,
			state: uniqueViolation, constraint: "source_snapshot_git_commit_key"},

		{name: "a kind nobody defined", kind: "EDITOR", digest: digest("d"),
			state: checkViolation, constraint: "source_snapshot_source_kind_check"},
		{name: "a GIT snapshot without a repository", kind: "GIT", digest: digest("d"), commit: thirdCommit,
			state: checkViolation, constraint: "source_snapshot_origin_check"},
		{name: "a GIT snapshot without a commit", kind: "GIT", digest: digest("d"), repository: contracts,
			state: checkViolation, constraint: "source_snapshot_origin_check"},
		{name: "an upload with an origin", kind: "UPLOAD", digest: digest("d"), repository: contracts, commit: thirdCommit,
			state: checkViolation, constraint: "source_snapshot_origin_check"},
		{name: "an upload with a reference", kind: "UPLOAD", digest: digest("d"), ref: "refs/heads/main",
			state: checkViolation, constraint: "source_snapshot_origin_check"},

		{name: "a repository over plain http", kind: "GIT", digest: digest("d"), repository: "http://git.example/contracts", commit: thirdCommit,
			state: checkViolation, constraint: "source_snapshot_origin_repository_check"},
		{name: "a repository with credentials", kind: "GIT", digest: digest("d"), repository: "https://ci:glpat-secret@git.example/contracts", commit: thirdCommit,
			state: checkViolation, constraint: "source_snapshot_origin_repository_check"},
		{name: "a repository with a query", kind: "GIT", digest: digest("d"), repository: contracts + "?private_token=glpat-secret", commit: thirdCommit,
			state: checkViolation, constraint: "source_snapshot_origin_repository_check"},
		{name: "a repository with a fragment", kind: "GIT", digest: digest("d"), repository: contracts + "#readme", commit: thirdCommit,
			state: checkViolation, constraint: "source_snapshot_origin_repository_check"},
		{name: "a repository with a trailing slash", kind: "GIT", digest: digest("d"), repository: contracts + "/", commit: thirdCommit,
			state: checkViolation, constraint: "source_snapshot_origin_repository_check"},
		{name: "a host alone", kind: "GIT", digest: digest("d"), repository: "https://git.example", commit: thirdCommit,
			state: checkViolation, constraint: "source_snapshot_origin_repository_check"},
		{name: "a repository longer than allowed", kind: "GIT", digest: digest("d"), repository: "https://git.example/" + strings.Repeat("r", 281), commit: thirdCommit,
			state: checkViolation, constraint: "source_snapshot_origin_repository_check"},

		{name: "an abbreviated commit", kind: "GIT", digest: digest("d"), repository: contracts, commit: thirdCommit[:7],
			state: checkViolation, constraint: "source_snapshot_origin_commit_check"},
		{name: "a commit in upper case", kind: "GIT", digest: digest("d"), repository: contracts, commit: strings.Repeat("A", 40),
			state: checkViolation, constraint: "source_snapshot_origin_commit_check"},
		{name: "a branch for a commit", kind: "GIT", digest: digest("d"), repository: contracts, commit: "main",
			state: checkViolation, constraint: "source_snapshot_origin_commit_check"},

		{name: "a short branch name", kind: "GIT", digest: digest("d"), repository: contracts, commit: thirdCommit, ref: "main",
			state: checkViolation, constraint: "source_snapshot_origin_ref_check"},
		{name: "a reference with a space", kind: "GIT", digest: digest("d"), repository: contracts, commit: thirdCommit, ref: "refs/heads/my branch",
			state: checkViolation, constraint: "source_snapshot_origin_ref_check"},
		{name: "a reference longer than allowed", kind: "GIT", digest: digest("d"), repository: contracts, commit: thirdCommit, ref: "refs/heads/" + strings.Repeat("b", 190),
			state: checkViolation, constraint: "source_snapshot_origin_ref_check"},

		// What was refused left the closure free.
		{name: "the closure the refused rows named", kind: "GIT", digest: digest("d"), repository: contracts, commit: thirdCommit, ref: "refs/tags/v2.0.0"},
	}
	accepted := 0
	for _, row := range rows {
		_, err := app.Exec(t.Context(), insert, ids.NewUUID(), product, asset, row.kind, row.digest, row.repository, row.commit, row.ref)
		var refused *pgconn.PgError
		errors.As(err, &refused)
		switch {
		case row.state == "" && err == nil:
			accepted++
		case row.state == "":
			t.Errorf("%s: refused: %v", row.name, err)
		case refused == nil || refused.Code != row.state || refused.ConstraintName != row.constraint:
			t.Errorf("%s: got %v, want it refused by %s", row.name, err, row.constraint)
		}
	}
	var stored int
	if err := app.QueryRow(t.Context(), "SELECT count(*) FROM contractregistry.source_snapshot").Scan(&stored); err != nil || stored != accepted || accepted != 5 {
		t.Fatalf("the table holds %d snapshots and %d were accepted, want 5 of both: %v", stored, accepted, err)
	}
}
