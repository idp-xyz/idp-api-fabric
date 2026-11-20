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

type Releases struct{ database *bentopostgres.DB }

func NewReleases(database *bentopostgres.DB) *Releases { return &Releases{database: database} }

const selectRelease = `
SELECT release_id, product_id, workspace_id, api_asset_id, revision_id, review_id,
       check_run_id, policy_snapshot_id, policy_digest, input_digest, artifact_digest,
       release_version, prepared_at, prepared_by_issuer, prepared_by_subject
FROM contractregistry.contract_release
WHERE product_id = $1 AND api_asset_id = $2 AND workspace_id = $3`

func scanRelease(row pgx.Row) (domain.Release, error) {
	var r domain.Release
	err := row.Scan(&r.ID, &r.Asset.ProductID, &r.Asset.WorkspaceID, &r.Asset.AssetID, &r.RevisionID, &r.ReviewID,
		&r.Approval.CheckRunID, &r.Approval.PolicySnapshotID, &r.Approval.PolicyDigest, &r.InputDigest, &r.ArtifactDigest,
		&r.Version, &r.PreparedAt, &r.PreparedBy.Issuer, &r.PreparedBy.Subject)
	return r, err
}

func (s *Releases) Find(ctx context.Context, asset domain.AssetRef, id string) (domain.Release, bool, error) {
	return s.find(ctx, asset, selectRelease+` AND release_id = $4`, id)
}

func (s *Releases) Version(ctx context.Context, asset domain.AssetRef, version string) (domain.Release, bool, error) {
	return s.find(ctx, asset, selectRelease+` AND release_version = $4`, version)
}

func (s *Releases) find(ctx context.Context, asset domain.AssetRef, query, value string) (domain.Release, bool, error) {
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return domain.Release{}, false, err
	}
	release, err := scanRelease(reader.QueryRow(ctx, query, asset.ProductID, asset.AssetID, asset.WorkspaceID, value))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Release{}, false, nil
	}
	if err != nil {
		return domain.Release{}, false, fmt.Errorf("find release: %w", err)
	}
	return release, true, nil
}

func (s *Releases) List(ctx context.Context, asset domain.AssetRef, after *ports.Position, limit int) ([]domain.Release, error) {
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	query := selectRelease + ` AND ($5::timestamptz IS NULL OR (prepared_at, release_id) > ($5, $6::uuid))
ORDER BY prepared_at, release_id LIMIT $4`
	var at *time.Time
	var id *string
	if after != nil {
		at, id = &after.At, &after.ID
	}
	rows, err := reader.Query(ctx, query, asset.ProductID, asset.AssetID, asset.WorkspaceID, limit, at, id)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.Release, error) { return scanRelease(row) })
}

func (s *Releases) Record(ctx context.Context, release domain.Release, payload []byte) (bool, error) {
	writer, err := s.database.RequireExecutor(ctx)
	if err != nil {
		return false, err
	}
	const putArtifact = `INSERT INTO contractregistry.release_artifact (artifact_digest, payload)
VALUES ($1, $2) ON CONFLICT (artifact_digest) DO NOTHING`
	if _, err := writer.Exec(ctx, putArtifact, release.ArtifactDigest, payload); err != nil {
		return false, fmt.Errorf("record release artifact: %w", err)
	}
	stored, err := s.Artifact(ctx, release.ArtifactDigest)
	if err != nil {
		return false, err
	}
	if err := domain.VerifyArtifact(release.ArtifactDigest, stored); err != nil {
		return false, err
	}
	const insert = `INSERT INTO contractregistry.contract_release
    (release_id, product_id, workspace_id, api_asset_id, revision_id, review_id, check_run_id,
     policy_snapshot_id, policy_digest, input_digest, artifact_digest, release_version,
     prepared_at, prepared_by_issuer, prepared_by_subject)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (product_id, api_asset_id, release_version) DO NOTHING`
	result, err := writer.Exec(ctx, insert, release.ID, release.Asset.ProductID, release.Asset.WorkspaceID, release.Asset.AssetID,
		release.RevisionID, release.ReviewID, release.Approval.CheckRunID, release.Approval.PolicySnapshotID, release.Approval.PolicyDigest,
		release.InputDigest, release.ArtifactDigest, release.Version, release.PreparedAt, release.PreparedBy.Issuer, release.PreparedBy.Subject)
	if err != nil {
		return false, fmt.Errorf("record release: %w", err)
	}
	return result.RowsAffected() == 1, nil
}

func (s *Releases) Artifact(ctx context.Context, digest string) ([]byte, error) {
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	var payload []byte
	if err := reader.QueryRow(ctx, `SELECT payload FROM contractregistry.release_artifact WHERE artifact_digest = $1`, digest).Scan(&payload); err != nil {
		return nil, fmt.Errorf("read release artifact: %w", err)
	}
	return payload, nil
}
