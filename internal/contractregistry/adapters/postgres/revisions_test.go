package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

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
