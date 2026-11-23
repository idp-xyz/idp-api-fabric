package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

type Revisions struct {
	database *bentopostgres.DB
}

func NewRevisions(database *bentopostgres.DB) *Revisions {
	return &Revisions{database: database}
}

// A snapshot without an origin holds NULL in the three columns: it is read
// as the zero GitOrigin.
const selectRevision = `
SELECT r.revision_id, r.source_snapshot_id, r.product_id, r.workspace_id,
       r.api_asset_id, r.revision_sequence, r.status, r.status_version, s.source_kind,
       coalesce(s.origin_repository, ''), coalesce(s.origin_commit, ''), coalesce(s.origin_ref, ''),
       s.entrypoint, r.spec_version, s.source_digest, s.ref_closure_digest, s.captured_at,
       s.captured_by_issuer, s.captured_by_subject
FROM contractregistry.contract_revision r
JOIN contractregistry.source_snapshot s USING (source_snapshot_id)
WHERE r.product_id = $1 AND r.api_asset_id = $2`

func scanRevision(row pgx.CollectableRow) (domain.Revision, error) {
	var revision domain.Revision
	err := row.Scan(&revision.ID, &revision.SourceSnapshotID, &revision.Asset.ProductID, &revision.Asset.WorkspaceID,
		&revision.Asset.AssetID, &revision.Sequence, &revision.Status, &revision.StatusVersion, &revision.SourceKind,
		&revision.Origin.Repository, &revision.Origin.Commit, &revision.Origin.Ref,
		&revision.Entrypoint, &revision.SpecVersion, &revision.SourceDigest, &revision.RefClosureDigest, &revision.CreatedAt,
		&revision.CreatedBy.Issuer, &revision.CreatedBy.Subject)
	revision.CreatedAt = revision.CreatedAt.UTC()
	return revision, err
}

// The closure a source is already recorded with, among the uploads of the
// asset and from one of its repositories.
const (
	recordedUpload = selectRevision + `
  AND s.source_kind = 'UPLOAD' AND s.ref_closure_digest = $3`
	recordedFromRepository = selectRevision + `
  AND s.source_kind = 'GIT' AND s.origin_repository = $3 AND s.ref_closure_digest = $4`
)

// The closure an entrypoint of a commit is recorded with.
const recordedAtCommit = `
SELECT ref_closure_digest
FROM contractregistry.source_snapshot
WHERE product_id = $1 AND api_asset_id = $2 AND source_kind = 'GIT'
  AND origin_repository = $3 AND origin_commit = $4 AND entrypoint = $5`

func (s *Revisions) Register(ctx context.Context, draft domain.Draft, source domain.SourceSnapshot) (ports.Registration, error) {
	writer, err := s.database.RequireExecutor(ctx)
	if err != nil {
		return ports.Registration{}, err
	}
	asset, origin := draft.Asset, draft.Origin

	// Registration is serialized per asset until commit, so content reuse
	// and sequence allocation both observe the preceding committed draft.
	const lock = `SELECT pg_advisory_xact_lock(hashtextextended('contractregistry/revision/' || $1::uuid::text, 0))`
	if _, err := writer.Exec(ctx, lock, asset.AssetID); err != nil {
		return ports.Registration{}, fmt.Errorf("lock revision registration: %w", err)
	}

	var rows pgx.Rows
	if origin.Declared() {
		// The commit is looked at before the content: a commit recorded
		// with one closure is refused with another, also when that other
		// closure is already recorded from the repository.
		var recorded string
		switch err := writer.QueryRow(ctx, recordedAtCommit,
			asset.ProductID, asset.AssetID, origin.Repository, origin.Commit, source.Entrypoint).Scan(&recorded); {
		case err == nil && recorded != source.RefClosureDigest:
			return ports.Registration{}, ports.ErrOriginConflict
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return ports.Registration{}, fmt.Errorf("read what the commit is recorded with: %w", err)
		}
		rows, err = writer.Query(ctx, recordedFromRepository,
			asset.ProductID, asset.AssetID, origin.Repository, source.RefClosureDigest)
	} else {
		rows, err = writer.Query(ctx, recordedUpload, asset.ProductID, asset.AssetID, source.RefClosureDigest)
	}
	if err != nil {
		return ports.Registration{}, fmt.Errorf("find a revision for the source: %w", err)
	}
	existing, err := pgx.CollectOneRow(rows, scanRevision)
	if err == nil {
		return ports.Registration{Revision: existing}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ports.Registration{}, fmt.Errorf("read a revision for the source: %w", err)
	}

	const nextSequence = `
SELECT COALESCE(MAX(revision_sequence), 0) + 1
FROM contractregistry.contract_revision
WHERE product_id = $1 AND api_asset_id = $2`
	var sequence int64
	if err := writer.QueryRow(ctx, nextSequence, asset.ProductID, asset.AssetID).Scan(&sequence); err != nil {
		return ports.Registration{}, fmt.Errorf("allocate a revision sequence: %w", err)
	}

	captured := draft.CapturedAt.UTC().Truncate(time.Microsecond)
	const insertSnapshot = `
INSERT INTO contractregistry.source_snapshot (
    source_snapshot_id, product_id, api_asset_id, source_kind, entrypoint, source_digest,
    ref_closure_digest, ref_closure_manifest, captured_at, captured_by_issuer, captured_by_subject,
    origin_repository, origin_commit, origin_ref
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`
	if _, err := writer.Exec(ctx, insertSnapshot, draft.SourceSnapshotID, asset.ProductID, asset.AssetID,
		origin.SourceKind(), source.Entrypoint, source.SourceDigest, source.RefClosureDigest, []byte(source.Manifest()),
		captured, draft.CapturedBy.Issuer, draft.CapturedBy.Subject,
		nullable(origin.Repository), nullable(origin.Commit), nullable(origin.Ref)); err != nil {
		return ports.Registration{}, fmt.Errorf("store the source snapshot: %w", err)
	}
	for _, file := range source.Files() {
		const insertFile = `
INSERT INTO contractregistry.source_file (source_snapshot_id, path, content) VALUES ($1,$2,$3)`
		if _, err := writer.Exec(ctx, insertFile, draft.SourceSnapshotID, file.Path, []byte(file.Content)); err != nil {
			return ports.Registration{}, fmt.Errorf("store the source bytes: %w", err)
		}
	}

	const insertRevision = `
INSERT INTO contractregistry.contract_revision (
    revision_id, product_id, workspace_id, api_asset_id, source_snapshot_id, revision_sequence, spec_version, status
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`
	if _, err := writer.Exec(ctx, insertRevision, draft.ID, asset.ProductID, asset.WorkspaceID,
		asset.AssetID, draft.SourceSnapshotID, sequence, source.SpecVersion, domain.RevisionDraft); err != nil {
		return ports.Registration{}, fmt.Errorf("store the contract revision: %w", err)
	}
	revision := domain.Revision{
		ID: draft.ID, SourceSnapshotID: draft.SourceSnapshotID, Asset: asset, Sequence: sequence,
		Status: domain.RevisionDraft, StatusVersion: 1, SourceKind: origin.SourceKind(), Origin: origin,
		Entrypoint: source.Entrypoint, SpecVersion: source.SpecVersion,
		SourceDigest: source.SourceDigest, RefClosureDigest: source.RefClosureDigest, CreatedAt: captured,
		CreatedBy: draft.CapturedBy,
	}
	return ports.Registration{Revision: revision, Created: true}, nil
}

// Move changes the status of a revision of the asset inside the transaction
// it is called in. The row is locked first, so that of several moves that
// arrive together one is made and the others find the revision where the
// first one left it. The table decides as well: it lets a status move along
// the review and nothing else of a revision change.
func (s *Revisions) Move(ctx context.Context, asset domain.AssetRef, id, to string) (domain.Revision, bool, error) {
	revision, found, err := s.Hold(ctx, asset, id)
	if err != nil || !found {
		return domain.Revision{}, false, err
	}
	if err := domain.Move(revision.Status, to); err != nil {
		return domain.Revision{}, true, err
	}
	writer, err := s.database.RequireExecutor(ctx)
	if err != nil {
		return domain.Revision{}, false, err
	}
	const move = `
UPDATE contractregistry.contract_revision SET status = $2, status_version = status_version + 1
WHERE revision_id = $1 AND status_version = $3`
	moved, err := writer.Exec(ctx, move, revision.ID, to, revision.StatusVersion)
	if err != nil {
		return domain.Revision{}, true, fmt.Errorf("move a contract revision: %w", err)
	}
	if moved.RowsAffected() != 1 {
		return domain.Revision{}, true, fmt.Errorf("move a contract revision: %d rows changed", moved.RowsAffected())
	}
	revision.Status, revision.StatusVersion = to, revision.StatusVersion+1
	return revision, true, nil
}

// Hold locks the row of a revision of the asset inside the transaction it
// is called in and returns the revision as it is. Until that transaction
// ends, whatever else asks for the row this way waits: a move, or another
// hold. What is decided behind a hold is decided on the status the revision
// has, and one caller at a time.
func (s *Revisions) Hold(ctx context.Context, asset domain.AssetRef, id string) (domain.Revision, bool, error) {
	writer, err := s.database.RequireExecutor(ctx)
	if err != nil {
		return domain.Revision{}, false, err
	}
	rows, err := writer.Query(ctx, selectRevision+" AND r.workspace_id = $3 AND r.revision_id = $4 FOR UPDATE OF r",
		asset.ProductID, asset.AssetID, asset.WorkspaceID, id)
	if err != nil {
		return domain.Revision{}, false, fmt.Errorf("lock a contract revision: %w", err)
	}
	revision, err := pgx.CollectOneRow(rows, scanRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Revision{}, false, nil
	}
	if err != nil {
		return domain.Revision{}, false, fmt.Errorf("lock a contract revision: %w", err)
	}
	return revision, true, nil
}

// nullable stores what a snapshot does not say as NULL.
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (s *Revisions) Find(ctx context.Context, asset domain.AssetRef, id string) (domain.Revision, bool, error) {
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return domain.Revision{}, false, err
	}
	rows, err := reader.Query(ctx, selectRevision+" AND r.workspace_id = $3 AND r.revision_id = $4",
		asset.ProductID, asset.AssetID, asset.WorkspaceID, id)
	if err != nil {
		return domain.Revision{}, false, fmt.Errorf("find a contract revision: %w", err)
	}
	revision, err := pgx.CollectOneRow(rows, scanRevision)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Revision{}, false, nil
	}
	return revision, err == nil, err
}

// Files reads the files through the revision, so that the scope of the asset
// decides what is read and not an identifier of a snapshot alone.
func (s *Revisions) Files(ctx context.Context, asset domain.AssetRef, id string) ([]domain.SourceFile, error) {
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	const files = `
SELECT f.path, f.content
FROM contractregistry.contract_revision r
JOIN contractregistry.source_file f USING (source_snapshot_id)
WHERE r.product_id = $1 AND r.api_asset_id = $2 AND r.workspace_id = $3 AND r.revision_id = $4
ORDER BY f.path COLLATE "C"`
	rows, err := reader.Query(ctx, files, asset.ProductID, asset.AssetID, asset.WorkspaceID, id)
	if err != nil {
		return nil, fmt.Errorf("read the files of a contract revision: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.SourceFile, error) {
		var file domain.SourceFile
		var content []byte
		err := row.Scan(&file.Path, &content)
		file.Content = string(content)
		return file, err
	})
}

func (s *Revisions) List(ctx context.Context, asset domain.AssetRef, after *ports.Position, limit int) ([]domain.Revision, error) {
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	const list = selectRevision + `
  AND r.workspace_id = $3
  AND (s.captured_at, r.revision_id) >
      (COALESCE($4::timestamptz, '-infinity'), COALESCE($5::uuid, '00000000-0000-0000-0000-000000000000'))
ORDER BY s.captured_at, r.revision_id
LIMIT $6`
	var at, id any
	if after != nil {
		at, id = after.At, after.ID
	}
	rows, err := reader.Query(ctx, list, asset.ProductID, asset.AssetID, asset.WorkspaceID, at, id, limit)
	if err != nil {
		return nil, fmt.Errorf("list contract revisions: %w", err)
	}
	return pgx.CollectRows(rows, scanRevision)
}
