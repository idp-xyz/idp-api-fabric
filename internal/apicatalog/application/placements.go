package application

import (
	"context"

	"go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	"go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
)

// AssetPlaces returns placements in a product for a use case that has
// already authorized the whole product or a set of workspaces within it.
func (c *Catalog) AssetPlaces(ctx context.Context, product domain.ProductID, workspaces []domain.WorkspaceID, all bool) ([]ports.AssetPlace, error) {
	if !domain.ValidIdentifier(product) {
		return nil, ErrProductNotFound
	}
	for _, workspace := range workspaces {
		if !domain.ValidIdentifier(workspace) {
			return nil, domain.ErrInvalid
		}
	}
	return c.deps.Assets.Places(ctx, product, workspaces, all)
}
