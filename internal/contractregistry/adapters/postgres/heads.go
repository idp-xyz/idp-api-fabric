package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

// Heads reads where assets stand: their last revision and their last
// release.
type Heads struct {
	database *bentopostgres.DB
}

var _ ports.Heads = (*Heads)(nil)

func NewHeads(database *bentopostgres.DB) *Heads { return &Heads{database: database} }

// The heads of a set of assets are read with one statement, however many
// there are: the assets are the rows of a list and each of them is looked up
// by the index that leads with the product and the asset, as the read of one
// would, the last revision by the greatest sequence and the last release by
// the instant it was prepared and its identifier. Both ways round, the index
// finds the one row without reading the others.
//
// The columns of the revision are those of selectRevision and are read the
// same way, so that the revision a head names is the revision a read of it
// gives. An asset with no revision has no row: the revision is joined, and
// the release, which cannot be there without it, is joined after it. The
// asset is looked up under its Workspace, as every read of it is, so that a
// revision that the registry holds under another Workspace is not its own.
const selectHeads = `
SELECT r.revision_id, r.source_snapshot_id, r.product_id, r.workspace_id,
       r.api_asset_id, r.revision_sequence, r.status, r.status_version, r.source_kind,
       r.origin_repository, r.origin_commit, r.origin_ref,
       r.entrypoint, r.spec_version, r.source_digest, r.ref_closure_digest, r.captured_at,
       r.captured_by_issuer, r.captured_by_subject,
       l.release_id, l.revision_id, l.review_id, l.check_run_id, l.policy_snapshot_id, l.policy_digest,
       l.input_digest, l.artifact_digest, l.release_version, l.prepared_at, l.prepared_by_issuer, l.prepared_by_subject,
       EXISTS (SELECT 1 FROM contractregistry.contract_release x
               WHERE x.product_id = $1 AND x.api_asset_id = t.api_asset_id AND x.revision_id = r.revision_id)
FROM unnest($2::text[]::uuid[], $3::text[]::uuid[]) AS t(api_asset_id, workspace_id)
JOIN LATERAL (
    SELECT v.revision_id, v.source_snapshot_id, v.product_id, v.workspace_id, v.api_asset_id,
           v.revision_sequence, v.status, v.status_version, s.source_kind,
           coalesce(s.origin_repository, '') AS origin_repository, coalesce(s.origin_commit, '') AS origin_commit,
           coalesce(s.origin_ref, '') AS origin_ref,
           s.entrypoint, v.spec_version, s.source_digest, s.ref_closure_digest, s.captured_at,
           s.captured_by_issuer, s.captured_by_subject
    FROM contractregistry.contract_revision v
    JOIN contractregistry.source_snapshot s USING (source_snapshot_id)
    WHERE v.product_id = $1 AND v.api_asset_id = t.api_asset_id AND v.workspace_id = t.workspace_id
    ORDER BY v.revision_sequence DESC
    LIMIT 1) r ON true
LEFT JOIN LATERAL (
    SELECT release_id, revision_id, review_id, check_run_id, policy_snapshot_id, policy_digest, input_digest,
           artifact_digest, release_version, prepared_at, prepared_by_issuer, prepared_by_subject
    FROM contractregistry.contract_release
    WHERE product_id = $1 AND api_asset_id = t.api_asset_id AND workspace_id = t.workspace_id
    ORDER BY prepared_at DESC, release_id DESC
    LIMIT 1) l ON true`

func scanHead(row pgx.CollectableRow) (ports.Head, error) {
	var head ports.Head
	revision := &head.Revision
	// The columns of a release are all there or all absent: the statement
	// joins one row or none.
	var (
		releaseID, releaseRevision, review, check, snapshot, policy, input, artifact *string
		version, issuer, subject                                                     *string
		preparedAt                                                                   *time.Time
	)
	if err := row.Scan(&revision.ID, &revision.SourceSnapshotID, &revision.Asset.ProductID, &revision.Asset.WorkspaceID,
		&revision.Asset.AssetID, &revision.Sequence, &revision.Status, &revision.StatusVersion, &revision.SourceKind,
		&revision.Origin.Repository, &revision.Origin.Commit, &revision.Origin.Ref,
		&revision.Entrypoint, &revision.SpecVersion, &revision.SourceDigest, &revision.RefClosureDigest, &revision.CreatedAt,
		&revision.CreatedBy.Issuer, &revision.CreatedBy.Subject,
		&releaseID, &releaseRevision, &review, &check, &snapshot, &policy, &input, &artifact,
		&version, &preparedAt, &issuer, &subject, &head.Released); err != nil {
		return ports.Head{}, err
	}
	revision.CreatedAt = revision.CreatedAt.UTC()
	if releaseID == nil {
		return head, nil
	}
	for _, member := range []*string{releaseRevision, review, check, snapshot, policy, input, artifact, version, issuer, subject} {
		if member == nil {
			return ports.Head{}, fmt.Errorf("the last release %s of asset %s is not whole", *releaseID, revision.Asset.AssetID)
		}
	}
	if preparedAt == nil {
		return ports.Head{}, fmt.Errorf("the last release %s of asset %s is not whole", *releaseID, revision.Asset.AssetID)
	}
	head.Release = &domain.Release{
		ID: *releaseID, Asset: revision.Asset, RevisionID: *releaseRevision, ReviewID: *review,
		Approval:       domain.ApprovalEvidence{CheckRunID: *check, PolicySnapshotID: *snapshot, PolicyDigest: *policy},
		Version:        *version,
		InputDigest:    *input,
		ArtifactDigest: *artifact,
		PreparedAt:     preparedAt.UTC(),
		PreparedBy:     domain.Principal{Issuer: *issuer, Subject: *subject},
	}
	return head, nil
}

func (s *Heads) Heads(ctx context.Context, product string, assets []domain.AssetRef) ([]ports.Head, error) {
	if len(assets) == 0 {
		return nil, nil
	}
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	names, workspaces := make([]string, 0, len(assets)), make([]string, 0, len(assets))
	for _, asset := range assets {
		names, workspaces = append(names, asset.AssetID), append(workspaces, asset.WorkspaceID)
	}
	rows, err := reader.Query(ctx, selectHeads, product, names, workspaces)
	if err != nil {
		return nil, fmt.Errorf("read the heads of assets: %w", err)
	}
	return pgx.CollectRows(rows, scanHead)
}
