package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
)

// statusFixture is a store with one asset and a way to record revisions of
// it.
type statusFixture struct {
	t      *testing.T
	fabric *db.Database
	store  *postgres.Revisions
	asset  domain.AssetRef
	count  int
}

func newStatusFixture(t *testing.T) (*statusFixture, pgtest.Database) {
	t.Helper()
	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fabric.Close)
	return &statusFixture{
		t: t, fabric: fabric, store: postgres.NewRevisions(fabric.Framework),
		asset: domain.AssetRef{ProductID: ids.NewUUID(), WorkspaceID: ids.NewUUID(), AssetID: ids.NewUUID()},
	}, database
}

func (f *statusFixture) record() domain.Revision {
	f.t.Helper()
	f.count++
	content := "openapi: 3.1.2\ninfo: {title: Shipments, version: v" + string(rune('0'+f.count)) + "}\n"
	snapshot, err := domain.CaptureSource("openapi.yaml", "3.1.2", []domain.SourceFile{{Path: "openapi.yaml", Content: content}})
	if err != nil {
		f.t.Fatal(err)
	}
	var revision domain.Revision
	err = f.fabric.WithinTransaction(f.t.Context(), func(ctx context.Context) error {
		registered, err := f.store.Register(ctx, domain.Draft{
			ID: ids.NewUUID(), SourceSnapshotID: ids.NewUUID(), Asset: f.asset,
			CapturedBy: domain.Principal{Issuer: "https://issuer.example", Subject: "author"}, CapturedAt: time.Now(),
		}, snapshot)
		revision = registered.Revision
		return err
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return revision
}

// moveBySQL moves the status of a revision of the fixture one step, as the
// review of a build before this one did and as a migration does: no use case
// of this build moves a status (CR-0001), and the table still decides which
// moves there are.
func (f *statusFixture) moveBySQL(database pgtest.Database, id, to string) {
	f.t.Helper()
	owner := pgtest.Connect(f.t, database.Migrator)
	tag, err := owner.Exec(f.t.Context(), `UPDATE contractregistry.contract_revision
SET status = $2, status_version = status_version + 1 WHERE revision_id = $1`, id, to)
	if err != nil || tag.RowsAffected() != 1 {
		f.t.Fatalf("move revision %s to %s: %v, %d rows", id, to, err, tag.RowsAffected())
	}
}

// The table holds every writer to what a revision is: it begins as a draft,
// its status moves one step and one version at a time, nothing else of it
// changes, and it is never removed. No use case of this build moves a status
// any more; the table still holds what a build before this one and the
// migration that withdraws open reviews write to it.
func TestTheTableLetsTheStatusOfARevisionMoveAndNothingElse(t *testing.T) {
	f, database := newStatusFixture(t)
	draft, reviewed, withdrawn := f.record(), f.record(), f.record()
	f.moveBySQL(database, reviewed.ID, "IN_REVIEW")
	f.moveBySQL(database, withdrawn.ID, "IN_REVIEW")

	const (
		integrityViolation    = "23000"
		checkViolation        = "23514"
		insufficientPrivilege = "42501"
		update                = `UPDATE contractregistry.contract_revision SET `
	)
	statements := []struct {
		name      string
		statement string
		id        string
		state     string
	}{
		{"skip the review", update + `status = 'APPROVED', status_version = 2 WHERE revision_id = $1`, draft.ID, integrityViolation},
		{"reject a draft", update + `status = 'REJECTED', status_version = 2 WHERE revision_id = $1`, draft.ID, integrityViolation},
		{"move without the next version", update + `status = 'IN_REVIEW' WHERE revision_id = $1`, draft.ID, integrityViolation},
		{"move two versions on", update + `status = 'IN_REVIEW', status_version = 3 WHERE revision_id = $1`, draft.ID, integrityViolation},
		{"take a version and stay", update + `status_version = 2 WHERE revision_id = $1`, draft.ID, integrityViolation},
		{"return to a draft", update + `status = 'DRAFT', status_version = 3 WHERE revision_id = $1`, reviewed.ID, integrityViolation},
		{"a status nobody defined", update + `status = 'PUBLISHED', status_version = 3 WHERE revision_id = $1`, reviewed.ID, integrityViolation},
		{"change the specification version with the move", update + `status = 'APPROVED', status_version = 3, spec_version = '3.2.0' WHERE revision_id = $1`, reviewed.ID, integrityViolation},
		{"renumber it with the move", update + `status = 'APPROVED', status_version = 3, revision_sequence = 9 WHERE revision_id = $1`, reviewed.ID, integrityViolation},
		{"give it to another asset with the move", update + `status = 'APPROVED', status_version = 3, api_asset_id = gen_random_uuid() WHERE revision_id = $1`, reviewed.ID, integrityViolation},
		{"record one that is in review", `
			INSERT INTO contractregistry.contract_revision
			    (revision_id, product_id, workspace_id, api_asset_id, source_snapshot_id, revision_sequence, spec_version, status, status_version)
			SELECT gen_random_uuid(), product_id, workspace_id, api_asset_id, source_snapshot_id, 90, spec_version, 'IN_REVIEW', 1
			FROM contractregistry.contract_revision WHERE revision_id = $1`, draft.ID, integrityViolation},
		{"record one at a later version", `
			INSERT INTO contractregistry.contract_revision
			    (revision_id, product_id, workspace_id, api_asset_id, source_snapshot_id, revision_sequence, spec_version, status, status_version)
			SELECT gen_random_uuid(), product_id, workspace_id, api_asset_id, source_snapshot_id, 91, spec_version, 'DRAFT', 2
			FROM contractregistry.contract_revision WHERE revision_id = $1`, draft.ID, integrityViolation},
		{"remove it", `DELETE FROM contractregistry.contract_revision WHERE revision_id = $1`, draft.ID, insufficientPrivilege},
	}
	state := func(err error) string {
		var refused *pgconn.PgError
		if errors.As(err, &refused) {
			return refused.Code
		}
		return ""
	}
	// The role a process connects with, and the owner of the table: the
	// trigger is no privilege, so it holds both.
	app, owner := pgtest.Connect(t, database.Application), pgtest.Connect(t, database.Migrator)
	for _, s := range statements {
		if _, err := app.Exec(t.Context(), s.statement, s.id); state(err) != s.state {
			t.Errorf("%s, as the application role: got %v, want SQLSTATE %s", s.name, err, s.state)
		}
		if s.state == insufficientPrivilege {
			continue
		}
		if _, err := owner.Exec(t.Context(), s.statement, s.id); state(err) != s.state {
			t.Errorf("%s, as the owner of the table: got %v, want SQLSTATE %s", s.name, err, s.state)
		}
	}
	for _, kept := range []domain.Revision{draft, reviewed} {
		want := kept
		if kept.ID == reviewed.ID {
			want.Status, want.StatusVersion = "IN_REVIEW", 2
		}
		if read, _, err := f.store.Find(t.Context(), f.asset, kept.ID); err != nil || read != want {
			t.Errorf("after the refused statements a revision reads %+v, want %+v: %v", read, want, err)
		}
	}
	if stored := countRevisions(t, f); stored != 3 {
		t.Errorf("the table holds %d revisions, want the three that were recorded", stored)
	}
	// The moves a review made are ones the table takes, by hand as well:
	// a submission, and the withdrawal of a revision in review, which is the
	// move the migration of CR-0001 makes.
	if _, err := app.Exec(t.Context(), update+`status = 'IN_REVIEW', status_version = 2 WHERE revision_id = $1`, draft.ID); err != nil {
		t.Errorf("a submission by hand: %v", err)
	}
	if _, err := owner.Exec(t.Context(), update+`status = 'WITHDRAWN', status_version = 3 WHERE revision_id = $1`, withdrawn.ID); err != nil {
		t.Errorf("a withdrawal by hand: %v", err)
	}
	want := withdrawn
	want.Status, want.StatusVersion = "WITHDRAWN", 3
	if read, _, err := f.store.Find(t.Context(), f.asset, withdrawn.ID); err != nil || read != want {
		t.Errorf("the withdrawn revision reads %+v, want %+v: %v", read, want, err)
	}
}

func countRevisions(t *testing.T, f *statusFixture) int {
	t.Helper()
	listed, err := f.store.List(t.Context(), f.asset, nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	return len(listed)
}
