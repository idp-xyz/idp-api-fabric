package application

import (
	"context"

	"go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	"go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
)

// AssetsInWorkspaces places the assets of the product that the Workspaces
// own. It is for a use case of another context that has decided which
// Workspaces its caller reads in already, such as the list of the slots of a
// publication target, and like AssetInProduct it does not grant Catalog read
// access to its caller.
func (c *Catalog) AssetsInWorkspaces(ctx context.Context, product domain.ProductID, workspaces []domain.WorkspaceID) ([]ports.AssetPlace, error) {
	if !domain.ValidIdentifier(product) {
		return nil, ErrProductNotFound
	}
	for _, workspace := range workspaces {
		if !domain.ValidIdentifier(workspace) {
			return nil, domain.ErrInvalid
		}
	}
	return c.deps.Assets.InWorkspaces(ctx, product, workspaces)
}
