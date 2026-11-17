package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	"go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
)

type Assets struct {
	database *bentopostgres.DB
}

func NewAssets(database *bentopostgres.DB) *Assets { return &Assets{database: database} }

const addAsset = `
INSERT INTO apicatalog.api_asset (
    api_asset_id, product_id, workspace_id, asset_key, display_name, domain_key,
    api_kind, lifecycle, aggregate_version, created_at, created_by_issuer, created_by_subject
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (product_id, asset_key) DO NOTHING`

func (s *Assets) Add(ctx context.Context, asset domain.Asset) error {
	writer, err := s.database.RequireExecutor(ctx)
	if err != nil {
		return err
	}
	written, err := writer.Exec(ctx, addAsset,
		string(asset.ID), string(asset.ProductID), string(asset.WorkspaceID), asset.Key, asset.DisplayName, asset.DomainKey,
		string(asset.Kind), string(asset.Lifecycle), asset.Version, asset.CreatedAt, asset.CreatedBy.Issuer, asset.CreatedBy.Subject)
	if err != nil {
		return fmt.Errorf("store an API asset: %w", err)
	}
	if written.RowsAffected() == 0 {
		return ports.ErrAssetKeyTaken
	}
	return nil
}

const selectAsset = `
SELECT api_asset_id, product_id, workspace_id, asset_key, display_name, domain_key,
       api_kind, lifecycle, aggregate_version, created_at, created_by_issuer, created_by_subject
FROM apicatalog.api_asset
WHERE product_id = $1`

func scanAsset(row pgx.CollectableRow) (domain.Asset, error) {
	var asset domain.Asset
	var id, product, workspace, kind, lifecycle string
	if err := row.Scan(&id, &product, &workspace, &asset.Key, &asset.DisplayName, &asset.DomainKey, &kind, &lifecycle,
		&asset.Version, &asset.CreatedAt, &asset.CreatedBy.Issuer, &asset.CreatedBy.Subject); err != nil {
		return domain.Asset{}, err
	}
	asset.ID, asset.ProductID, asset.WorkspaceID = domain.AssetID(id), domain.ProductID(product), domain.WorkspaceID(workspace)
	asset.Kind, asset.Lifecycle = domain.Kind(kind), domain.Lifecycle(lifecycle)
	return asset, nil
}

func (s *Assets) Find(ctx context.Context, product domain.ProductID, id domain.AssetID) (domain.Asset, bool, error) {
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return domain.Asset{}, false, err
	}
	rows, err := reader.Query(ctx, selectAsset+" AND api_asset_id = $2", string(product), string(id))
	if err != nil {
		return domain.Asset{}, false, err
	}
	asset, err := pgx.CollectOneRow(rows, scanAsset)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Asset{}, false, nil
	}
	return asset, err == nil, err
}

// The authorization predicate is part of the query, ahead of the cursor
// and LIMIT. Search is on scoped metadata and treats wildcard characters
// as literal text.
const listAssets = selectAsset + `
  AND ($2::boolean OR workspace_id = ANY($3::text[]::uuid[]))
  AND ($4 = '' OR workspace_id = NULLIF($4, '')::uuid)
  AND ($5 = '' OR api_kind = $5)
  AND ($6 = '' OR domain_key = $6)
  AND ($7 = '' OR strpos(lower(display_name), lower($7)) > 0
               OR strpos(asset_key, lower($7)) > 0
               OR strpos(domain_key, lower($7)) > 0)
  AND (created_at, api_asset_id) > (coalesce($8, '-infinity'::timestamptz),
                                   coalesce($9, '00000000-0000-0000-0000-000000000000'::uuid))
ORDER BY created_at, api_asset_id
LIMIT $10`

func (s *Assets) List(ctx context.Context, product domain.ProductID, filter ports.Selection) ([]domain.Asset, error) {
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	workspaces := make([]string, 0, len(filter.Workspaces))
	for _, workspace := range filter.Workspaces {
		workspaces = append(workspaces, string(workspace))
	}
	var at, id any
	if filter.After != nil {
		at, id = filter.After.At, string(filter.After.ID)
	}
	rows, err := reader.Query(ctx, listAssets, string(product), filter.All, workspaces, string(filter.Workspace),
		string(filter.Kind), filter.DomainKey, filter.Query, at, id, filter.Limit)
	if err != nil {
		return nil, fmt.Errorf("read API assets: %w", err)
	}
	return pgx.CollectRows(rows, scanAsset)
}
