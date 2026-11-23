package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	"go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
)

// The assets that some Workspaces own, for a use case of another context
// that decides for itself who reads what: the Workspaces are the ones it
// names. The index api_asset_workspace_order leads with the product and the
// Workspace, so the statement reads the assets of those Workspaces and of no
// other.
const inWorkspaces = `
SELECT api_asset_id, workspace_id FROM apicatalog.api_asset
WHERE product_id = $1 AND workspace_id = ANY($2::text[]::uuid[])`

func (s *Assets) InWorkspaces(ctx context.Context, product domain.ProductID, workspaces []domain.WorkspaceID) ([]ports.AssetPlace, error) {
	if len(workspaces) == 0 {
		return nil, nil
	}
	reader, err := s.database.ReadExecutor(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		names = append(names, string(workspace))
	}
	rows, err := reader.Query(ctx, inWorkspaces, string(product), names)
	if err != nil {
		return nil, fmt.Errorf("place API assets: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ports.AssetPlace, error) {
		var id, workspace string
		if err := row.Scan(&id, &workspace); err != nil {
			return ports.AssetPlace{}, err
		}
		return ports.AssetPlace{ID: domain.AssetID(id), WorkspaceID: domain.WorkspaceID(workspace)}, nil
	})
}
