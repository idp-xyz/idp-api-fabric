package postgres

import (
	"context"
	"encoding/json"
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

// The specification of a release is the one of its revision, which the
// release does not repeat.
//
// A release prepared on its check alone names no review: review_id is NULL
// and is read as the empty identifier. Its waivers are a JSON array of rule
// and reason, in the order of their rules, and an empty one for a release
// that waives nothing.
const selectRelease = `
SELECT release_id, product_id, workspace_id, api_asset_id, revision_id, coalesce(review_id::text, ''),
       check_run_id, policy_snapshot_id, policy_digest, input_digest, artifact_digest,
       release_version, prepared_at, prepared_by_issuer, prepared_by_subject, waivers,
       (SELECT r.specification FROM contractregistry.contract_revision r WHERE r.revision_id = contract_release.revision_id)
FROM contractregistry.contract_release
WHERE product_id = $1 AND api_asset_id = $2 AND workspace_id = $3`

// storedWaiver is a waiver of a release as the table keeps it.
type storedWaiver struct {
	RuleID string `json:"rule_id"`
	Reason string `json:"reason"`
}

func scanRelease(row pgx.Row) (domain.Release, error) {
	var r domain.Release
	var waivers []byte
	err := row.Scan(&r.ID, &r.Asset.ProductID, &r.Asset.WorkspaceID, &r.Asset.AssetID, &r.RevisionID, &r.ReviewID,
		&r.Evidence.CheckRunID, &r.Evidence.PolicySnapshotID, &r.Evidence.PolicyDigest, &r.InputDigest, &r.ArtifactDigest,
		&r.Version, &r.PreparedAt, &r.PreparedBy.Issuer, &r.PreparedBy.Subject, &waivers, &r.Specification)
	if err != nil {
		return domain.Release{}, err
	}
	r.Waivers, err = readWaivers(waivers)
	return r, err
}

func readWaivers(stored []byte) ([]domain.Waiver, error) {
	var items []storedWaiver
	if err := json.Unmarshal(stored, &items); err != nil {
		return nil, fmt.Errorf("read the waivers of a release: %w", err)
	}
	var waivers []domain.Waiver
	for _, item := range items {
		waivers = append(waivers, domain.Waiver(item))
	}
	return waivers, nil
}

func writeWaivers(waivers []domain.Waiver) ([]byte, error) {
	items := make([]storedWaiver, 0, len(waivers))
	for _, waiver := range waivers {
		items = append(items, storedWaiver(waiver))
	}
	return json.Marshal(items)
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
	waivers, err := writeWaivers(release.Waivers)
	if err != nil {
		return false, err
	}
	const insert = `INSERT INTO contractregistry.contract_release
    (release_id, product_id, workspace_id, api_asset_id, revision_id, review_id, check_run_id,
     policy_snapshot_id, policy_digest, input_digest, artifact_digest, release_version,
     prepared_at, prepared_by_issuer, prepared_by_subject, waivers)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (product_id, api_asset_id, release_version) DO NOTHING`
	result, err := writer.Exec(ctx, insert, release.ID, release.Asset.ProductID, release.Asset.WorkspaceID, release.Asset.AssetID,
		release.RevisionID, nullable(release.ReviewID), release.Evidence.CheckRunID, release.Evidence.PolicySnapshotID,
		release.Evidence.PolicyDigest, release.InputDigest, release.ArtifactDigest, release.Version, release.PreparedAt,
		release.PreparedBy.Issuer, release.PreparedBy.Subject, string(waivers))
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
