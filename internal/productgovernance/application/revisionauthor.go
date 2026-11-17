package application

import (
	"context"
	"slices"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// RevisionAuthor holds the current author grant in the asset's workspace.
// For an import the membership is locked and the product's status is read
// in the same transaction as registration.
func (s *ProductGovernance) RevisionAuthor(ctx context.Context, actor domain.Principal,
	productID domain.ProductID, workspaceID domain.WorkspaceID, importing bool) (author, productActive bool, err error) {
	var memberships []domain.Membership
	if importing {
		memberships, err = s.deps.Memberships.LockHeldInWorkspace(ctx, productID, workspaceID, actor)
	} else {
		memberships, err = s.deps.Memberships.HeldBy(ctx, productID, actor)
	}
	if err != nil {
		return false, false, err
	}
	author = slices.ContainsFunc(memberships, func(membership domain.Membership) bool {
		return membership.WorkspaceID == workspaceID && membership.Role == domain.RoleInternalAuthor
	})
	if !author || !importing {
		return author, false, nil
	}
	product, found, err := s.deps.Products.Find(ctx, productID)
	if err != nil {
		return false, false, err
	}
	if !found {
		return false, false, ErrProductNotFound
	}
	return true, product.Status == domain.ProductActive, nil
}
