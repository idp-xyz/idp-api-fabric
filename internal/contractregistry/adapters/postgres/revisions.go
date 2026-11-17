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

const selectRevision = `
SELECT r.revision_id, r.source_snapshot_id, r.product_id, r.workspace_id,
       r.api_asset_id, r.revision_sequence, r.status, s.source_kind,
       s.entrypoint, r.spec_version, s.source_digest, s.ref_closure_digest, s.captured_at
FROM contractregistry.contract_revision r
JOIN contractregistry.source_snapshot s USING (source_snapshot_id)
WHERE r.product_id = $1 AND r.api_asset_id = $2`

func scanRevision(row pgx.CollectableRow) (domain.Revision, error) {
	var revision domain.Revision
	err := row.Scan(&revision.ID, &revision.SourceSnapshotID, &revision.Asset.ProductID, &revision.Asset.WorkspaceID,
		&revision.Asset.AssetID, &revision.Sequence, &revision.Status, &revision.SourceKind,
		&revision.Entrypoint, &revision.SpecVersion, &revision.SourceDigest, &revision.RefClosureDigest, &revision.CreatedAt)
	revision.CreatedAt = revision.CreatedAt.UTC()
	return revision, err
}

func (s *Revisions) Register(ctx context.Context, draft domain.Draft, source domain.SourceSnapshot) (ports.Registration, error) {
	writer, err := s.database.RequireExecutor(ctx)
	if err != nil {
		return ports.Registration{}, err
	}
	asset := draft.Asset

	// Registration is serialized per asset until commit, so content reuse
	// and sequence allocation both observe the preceding committed draft.
	const lock = `SELECT pg_advisory_xact_lock(hashtextextended('contractregistry/revision/' || $1::uuid::text, 0))`
	if _, err := writer.Exec(ctx, lock, asset.AssetID); err != nil {
		return ports.Registration{}, fmt.Errorf("lock revision registration: %w", err)
	}
	rows, err := writer.Query(ctx, selectRevision+" AND s.ref_closure_digest = $3",
		asset.ProductID, asset.AssetID, source.RefClosureDigest)
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
    ref_closure_digest, ref_closure_manifest, captured_at, captured_by_issuer, captured_by_subject
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`
	if _, err := writer.Exec(ctx, insertSnapshot, draft.SourceSnapshotID, asset.ProductID, asset.AssetID,
		domain.SourceUpload, source.Entrypoint, source.SourceDigest, source.RefClosureDigest, []byte(source.Manifest()),
		captured, draft.CapturedBy.Issuer, draft.CapturedBy.Subject); err != nil {
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
		Status: domain.RevisionDraft, SourceKind: domain.SourceUpload, Entrypoint: source.Entrypoint, SpecVersion: source.SpecVersion,
		SourceDigest: source.SourceDigest, RefClosureDigest: source.RefClosureDigest, CreatedAt: captured,
	}
	return ports.Registration{Revision: revision, Created: true}, nil
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
