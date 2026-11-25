package application

import (
	"context"
	"fmt"
	"slices"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// CatalogScope is a snapshot of the Product Governance facts used by the
// Catalog. Knowing that a product exists conveys no Workspace membership.
type CatalogScope struct {
	Found      bool
	Active     bool
	Known      bool
	Owner      bool
	Security   bool
	Workspaces []domain.WorkspaceID
	// ContractReaders are the workspaces where the principal reads governance
	// evidence. Owner covers the product-wide grant separately.
	ContractReaders []domain.WorkspaceID
}

func (s *ProductGovernance) ReadCatalogScope(ctx context.Context, caller domain.Principal, productID domain.ProductID) (CatalogScope, error) {
	if err := known(caller); err != nil {
		return CatalogScope{}, err
	}
	if _, err := domain.ParseProductID(string(productID)); err != nil {
		return CatalogScope{}, ErrProductNotFound
	}
	seen, err := s.survey(ctx, caller, productID, "", "")
	if err != nil || !seen.productFound {
		return CatalogScope{}, err
	}
	workspaces := make([]domain.WorkspaceID, 0, len(seen.standing.Memberships))
	readers := make([]domain.WorkspaceID, 0, len(seen.standing.Memberships))
	for _, membership := range seen.standing.Memberships {
		workspaces = append(workspaces, membership.WorkspaceID)
		if slices.Contains(revisionReaders, membership.Role) {
			readers = append(readers, membership.WorkspaceID)
		}
	}
	slices.Sort(workspaces)
	slices.Sort(readers)
	return CatalogScope{
		Found: true, Active: seen.product.Status == domain.ProductActive,
		Known: s.knows(seen.standing, caller), Owner: seen.standing.Owns(), Workspaces: slices.Compact(workspaces),
		Security: holdsSecurityRole(seen.standing.Roles), ContractReaders: slices.Compact(readers),
	}, nil
}

type CatalogRegistrationScope struct {
	CatalogScope
	WorkspaceFound bool
	Maintainer     bool
}

// LockCatalogRegistration resolves the target by Product and Workspace
// together, then reads the authorizing memberships under a shared row lock.
// The Catalog owns the transaction that also writes its asset.
func (s *ProductGovernance) LockCatalogRegistration(ctx context.Context, caller domain.Principal,
	productID domain.ProductID, workspaceID domain.WorkspaceID) (CatalogRegistrationScope, error) {
	if err := known(caller); err != nil {
		return CatalogRegistrationScope{}, err
	}
	if _, err := domain.ParseProductID(string(productID)); err != nil {
		return CatalogRegistrationScope{}, ErrProductNotFound
	}
	if _, err := domain.ParseWorkspaceID(string(workspaceID)); err != nil {
		return CatalogRegistrationScope{}, ErrWorkspaceNotFound
	}
	workspace, found, err := s.deps.Workspaces.Find(ctx, productID, workspaceID)
	if err != nil {
		return CatalogRegistrationScope{}, fmt.Errorf("resolve the Catalog Workspace: %w", err)
	}
	var maintainer bool
	if found && workspace.Status == domain.WorkspaceActive {
		held, err := s.deps.Memberships.LockHeldInWorkspace(ctx, productID, workspaceID, caller)
		if err != nil {
			return CatalogRegistrationScope{}, err
		}
		maintainer = slices.ContainsFunc(held, func(membership domain.Membership) bool {
			return membership.Role == domain.RoleWorkspaceMaintainer
		})
	}
	scope, err := s.ReadCatalogScope(ctx, caller, productID)
	return CatalogRegistrationScope{CatalogScope: scope, WorkspaceFound: found, Maintainer: maintainer}, err
}
