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
// it and move them.
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

func (f *statusFixture) move(asset domain.AssetRef, id, to string) (domain.Revision, bool, error) {
	f.t.Helper()
	var (
		moved domain.Revision
		found bool
	)
	err := f.fabric.WithinTransaction(f.t.Context(), func(ctx context.Context) error {
		var err error
		moved, found, err = f.store.Move(ctx, asset, id, to)
		return err
	})
	return moved, found, err
}

// A revision is recorded as a draft at version 1 of its status. Each move
// along its review takes the next version and is read back with the
// revision, which is otherwise what it was. A move the review does not have
// is refused and changes nothing; so is one of a revision the asset does not
// have, which is not found.
func TestARevisionMovesAlongItsReviewOneVersionAtATime(t *testing.T) {
	f, _ := newStatusFixture(t)
	revision := f.record()
	if revision.Status != domain.RevisionDraft || revision.StatusVersion != 1 {
		t.Fatalf("recorded as %s at version %d, want a DRAFT at version 1", revision.Status, revision.StatusVersion)
	}
	if found, present, err := f.store.Find(t.Context(), f.asset, revision.ID); err != nil || !present || found != revision {
		t.Fatalf("read back %+v, want %+v: %v", found, revision, err)
	}

	steps := []string{domain.RevisionInReview, domain.RevisionWithdrawn, domain.RevisionInReview, domain.RevisionApproved}
	want := revision
	for _, to := range steps {
		moved, found, err := f.move(f.asset, revision.ID, to)
		want.Status, want.StatusVersion = to, want.StatusVersion+1
		if err != nil || !found || moved != want {
			t.Fatalf("to %s: %+v found %t, want %+v: %v", to, moved, found, want, err)
		}
		if read, _, err := f.store.Find(t.Context(), f.asset, revision.ID); err != nil || read != want {
			t.Fatalf("after the move to %s the revision reads %+v, want %+v: %v", to, read, want, err)
		}
	}
	for _, to := range []string{domain.RevisionInReview, domain.RevisionRejected, domain.RevisionWithdrawn, domain.RevisionDraft, domain.RevisionApproved} {
		if _, found, err := f.move(f.asset, revision.ID, to); !found || !errors.Is(err, domain.ErrStatusConflict) {
			t.Errorf("an approved revision to %s: found %t, %v; want %v", to, found, err, domain.ErrStatusConflict)
		}
	}
	if read, _, err := f.store.Find(t.Context(), f.asset, revision.ID); err != nil || read != want {
		t.Errorf("after the refused moves the revision reads %+v, want %+v: %v", read, want, err)
	}

	rejected := f.record()
	if _, _, err := f.move(f.asset, rejected.ID, domain.RevisionRejected); !errors.Is(err, domain.ErrStatusConflict) {
		t.Errorf("a draft to REJECTED: %v, want %v", err, domain.ErrStatusConflict)
	}
	f.move(f.asset, rejected.ID, domain.RevisionInReview)
	if moved, _, err := f.move(f.asset, rejected.ID, domain.RevisionRejected); err != nil || moved.Status != domain.RevisionRejected || moved.StatusVersion != 3 {
		t.Errorf("a revision in review to REJECTED: %+v, %v", moved, err)
	}
	if _, _, err := f.move(f.asset, rejected.ID, domain.RevisionInReview); !errors.Is(err, domain.ErrStatusConflict) {
		t.Errorf("a rejected revision submitted again: %v, want %v", err, domain.ErrStatusConflict)
	}

	// A revision is moved under its asset, its workspace and its product.
	elsewhere := map[string]domain.AssetRef{
		"another asset":     {ProductID: f.asset.ProductID, WorkspaceID: f.asset.WorkspaceID, AssetID: ids.NewUUID()},
		"another workspace": {ProductID: f.asset.ProductID, WorkspaceID: ids.NewUUID(), AssetID: f.asset.AssetID},
		"another product":   {ProductID: ids.NewUUID(), WorkspaceID: f.asset.WorkspaceID, AssetID: f.asset.AssetID},
	}
	draft := f.record()
	for name, asset := range elsewhere {
		if _, found, err := f.move(asset, draft.ID, domain.RevisionInReview); found || err != nil {
			t.Errorf("under %s: found %t, %v; want it not found", name, found, err)
		}
	}
	if _, found, err := f.move(f.asset, ids.NewUUID(), domain.RevisionInReview); found || err != nil {
		t.Errorf("a revision nobody recorded: found %t, %v; want it not found", found, err)
	}
	if read, _, _ := f.store.Find(t.Context(), f.asset, draft.ID); read != draft {
		t.Errorf("the draft reads %+v after moves under other scopes, want %+v", read, draft)
	}
	// A move belongs to the transaction of what it is made for.
	if _, _, err := f.store.Move(t.Context(), f.asset, draft.ID, domain.RevisionInReview); err == nil {
		t.Error("a move outside a transaction was made")
	}
}

// Moves that arrive together are made one after the other: of several
// submissions of one draft one is made, and the others find the revision in
// review.
func TestConcurrentMovesOfOneRevisionMakeOne(t *testing.T) {
	f, _ := newStatusFixture(t)
	revision := f.record()

	const submitters = 6
	results := make(chan error, submitters)
	for range submitters {
		go func() {
			results <- f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
				_, _, err := f.store.Move(ctx, f.asset, revision.ID, domain.RevisionInReview)
				return err
			})
		}()
	}
	made := 0
	for range submitters {
		switch err := <-results; {
		case err == nil:
			made++
		case !errors.Is(err, domain.ErrStatusConflict):
			t.Errorf("a submission failed with %v, want it made or told of the conflict", err)
		}
	}
	read, _, err := f.store.Find(t.Context(), f.asset, revision.ID)
	if err != nil || made != 1 || read.Status != domain.RevisionInReview || read.StatusVersion != 2 {
		t.Fatalf("%d submissions were made and the revision is %s at version %d, want one and IN_REVIEW at 2: %v",
			made, read.Status, read.StatusVersion, err)
	}
}

// A revision is held as it is: the hold answers the revision and changes
// nothing of it, under its asset and inside a transaction. Until that
// transaction ends a move of the revision waits, and so does another hold,
// so what is decided behind a hold is decided by one caller at a time.
func TestARevisionIsHeldAsItIsUntilTheTransactionEnds(t *testing.T) {
	f, database := newStatusFixture(t)
	revision := f.record()
	hold := func(asset domain.AssetRef, id string) (held domain.Revision, found bool, err error) {
		err = f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
			held, found, err = f.store.Hold(ctx, asset, id)
			return err
		})
		return held, found, err
	}

	if held, found, err := hold(f.asset, revision.ID); err != nil || !found || held != revision {
		t.Fatalf("held %+v found %t, want %+v: %v", held, found, revision, err)
	}
	if read, _, err := f.store.Find(t.Context(), f.asset, revision.ID); err != nil || read != revision {
		t.Fatalf("after the hold the revision reads %+v, want %+v: %v", read, revision, err)
	}
	elsewhere := map[string]domain.AssetRef{
		"another asset":     {ProductID: f.asset.ProductID, WorkspaceID: f.asset.WorkspaceID, AssetID: ids.NewUUID()},
		"another workspace": {ProductID: f.asset.ProductID, WorkspaceID: ids.NewUUID(), AssetID: f.asset.AssetID},
		"another product":   {ProductID: ids.NewUUID(), WorkspaceID: f.asset.WorkspaceID, AssetID: f.asset.AssetID},
	}
	for name, asset := range elsewhere {
		if _, found, err := hold(asset, revision.ID); found || err != nil {
			t.Errorf("under %s: found %t, %v; want it not found", name, found, err)
		}
	}
	if _, found, err := hold(f.asset, ids.NewUUID()); found || err != nil {
		t.Errorf("a revision nobody recorded: found %t, %v; want it not found", found, err)
	}
	// A hold lasts as long as the transaction it is made in.
	if _, _, err := f.store.Hold(t.Context(), f.asset, revision.ID); err == nil {
		t.Error("a hold outside a transaction was made")
	}

	// The first hold stays open until the test lets it end. The pool is
	// closed with the test and waits for that transaction, so a failure
	// that left it open would wait out the timeout of the package. Finish
	// it first, and say which of the two did not wait.
	owner := pgtest.Connect(t, database.Migrator)
	held, release := make(chan struct{}), make(chan struct{})
	first := make(chan error, 1)
	go func() {
		first <- f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
			_, _, err := f.store.Hold(ctx, f.asset, revision.ID)
			close(held)
			<-release
			return err
		})
	}()
	<-held
	ended := false
	finishHold := func() {
		t.Helper()
		if ended {
			return
		}
		ended = true
		close(release)
		if err := <-first; err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() {
		if !ended {
			close(release)
		}
	})
	type outcome struct {
		revision domain.Revision
		err      error
	}
	moved, second := make(chan outcome, 1), make(chan outcome, 1)
	go func() {
		after, _, err := f.move(f.asset, revision.ID, domain.RevisionInReview)
		moved <- outcome{after, err}
	}()
	go func() {
		seen, _, err := hold(f.asset, revision.ID)
		second <- outcome{seen, err}
	}()
	// Both reach the row and wait there. One that finishes while the first
	// transaction is open did not wait for it.
	deadline := time.Now().Add(15 * time.Second)
	var blocked int
	for {
		select {
		case got := <-moved:
			finishHold()
			t.Fatalf("the move did not wait for the hold: %+v, %v", got.revision, got.err)
		case got := <-second:
			finishHold()
			t.Fatalf("the second hold did not wait for the first: %+v, %v", got.revision, got.err)
		default:
		}
		if err := owner.QueryRow(t.Context(),
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`,
		).Scan(&blocked); err != nil {
			finishHold()
			t.Fatal(err)
		}
		if blocked == 2 {
			break
		}
		if time.Now().After(deadline) {
			finishHold()
			t.Fatalf("%d sessions wait for a lock, want 2", blocked)
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case got := <-moved:
		finishHold()
		t.Fatalf("the move was made behind the hold: %+v, %v", got.revision, got.err)
	case got := <-second:
		finishHold()
		t.Fatalf("a second hold was given behind the first: %+v, %v", got.revision, got.err)
	default:
	}
	if read, _, err := f.store.Find(t.Context(), f.asset, revision.ID); err != nil || read != revision {
		finishHold()
		t.Fatalf("while it is held the revision reads %+v, want %+v: %v", read, revision, err)
	}
	finishHold()
	if got := <-moved; got.err != nil || got.revision.Status != domain.RevisionInReview || got.revision.StatusVersion != 2 {
		t.Errorf("the move that waited: %+v, %v; want the revision in review at version 2", got.revision, got.err)
	}
	// The hold that waited sees the revision as the move left it or as it
	// was before, whichever of the two went first.
	if got := <-second; got.err != nil || (got.revision.StatusVersion != 1 && got.revision.StatusVersion != 2) {
		t.Errorf("the hold that waited: %+v, %v", got.revision, got.err)
	}
}

// The table holds every writer to what a revision is, whatever the model
// let through: it begins as a draft, its status moves one step and one
// version at a time, nothing else of it changes, and it is never removed.
func TestTheTableLetsTheStatusOfARevisionMoveAndNothingElse(t *testing.T) {
	f, database := newStatusFixture(t)
	draft, reviewed := f.record(), f.record()
	f.move(f.asset, reviewed.ID, domain.RevisionInReview)

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
			want.Status, want.StatusVersion = domain.RevisionInReview, 2
		}
		if read, _, err := f.store.Find(t.Context(), f.asset, kept.ID); err != nil || read != want {
			t.Errorf("after the refused statements a revision reads %+v, want %+v: %v", read, want, err)
		}
	}
	if stored := countRevisions(t, f); stored != 2 {
		t.Errorf("the table holds %d revisions, want the two that were recorded", stored)
	}
	// The move the model makes is one the table takes, by hand as well.
	if _, err := app.Exec(t.Context(), update+`status = 'IN_REVIEW', status_version = 2 WHERE revision_id = $1`, draft.ID); err != nil {
		t.Errorf("a submission by hand: %v", err)
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
