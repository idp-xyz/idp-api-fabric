package application

import (
	"context"
	"slices"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// revisionReaders are the roles of a workspace that read its contract
// revisions. A product owner's read grant is checked separately.
var revisionReaders = []domain.Role{
	domain.RoleWorkspaceMaintainer, domain.RoleInternalAuthor, domain.RoleInternalReviewer, domain.RoleInternalPublisher,
}

// RevisionReader reports whether actor may read the contract revisions of
// the workspace now: it owns the product or holds a governance reading role
// there. A reviewer reads what it is to decide on, and a publisher
// what it is to release: the revision, its checks and the reviews that
// approved it, which a release names. Neither writes a revision. A read
// takes no lock: the grant is read again with every request.
func (s *ProductGovernance) RevisionReader(ctx context.Context, actor domain.Principal,
	productID domain.ProductID, workspaceID domain.WorkspaceID) (bool, error) {
	roles, err := s.deps.Roles.RolesOf(ctx, productID, actor)
	if err != nil {
		return false, err
	}
	if slices.Contains(roles, domain.RoleProductOwner) {
		if _, err := domain.ParseWorkspaceID(string(workspaceID)); err != nil {
			return false, nil
		}
		_, found, err := s.deps.Workspaces.Find(ctx, productID, workspaceID)
		return found, err
	}
	memberships, err := s.deps.Memberships.HeldBy(ctx, productID, actor)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(memberships, func(membership domain.Membership) bool {
		return membership.WorkspaceID == workspaceID && slices.Contains(revisionReaders, membership.Role)
	}), nil
}

// RevisionReviewer holds the current reviewer grant in the asset's workspace
// while a decision on a review is recorded: the membership is locked and the
// status of the product is read in the transaction of the decision, so that
// a revocation and a decision take effect in the order they commit.
func (s *ProductGovernance) RevisionReviewer(ctx context.Context, actor domain.Principal,
	productID domain.ProductID, workspaceID domain.WorkspaceID) (reviewer, productActive bool, err error) {
	return s.lockedRole(ctx, actor, productID, workspaceID, domain.RoleInternalReviewer, true)
}

// RevisionAuthor holds the current author grant in the asset's workspace.
// For an import the membership is locked and the product's status is read
// in the same transaction as registration.
func (s *ProductGovernance) RevisionAuthor(ctx context.Context, actor domain.Principal,
	productID domain.ProductID, workspaceID domain.WorkspaceID, importing bool) (author, productActive bool, err error) {
	return s.lockedRole(ctx, actor, productID, workspaceID, domain.RoleInternalAuthor, importing)
}

// lockedRole reports whether actor holds role in the workspace. With writing
// it locks the membership it finds and reads whether the product takes new
// work; without, it reads the grant and says nothing of the product.
func (s *ProductGovernance) lockedRole(ctx context.Context, actor domain.Principal, productID domain.ProductID,
	workspaceID domain.WorkspaceID, role domain.Role, writing bool) (held, productActive bool, err error) {
	var memberships []domain.Membership
	if writing {
		memberships, err = s.deps.Memberships.LockHeldInWorkspace(ctx, productID, workspaceID, actor)
	} else {
		memberships, err = s.deps.Memberships.HeldBy(ctx, productID, actor)
	}
	if err != nil {
		return false, false, err
	}
	held = slices.ContainsFunc(memberships, func(membership domain.Membership) bool {
		return membership.WorkspaceID == workspaceID && membership.Role == role
	})
	if !held || !writing {
		return held, false, nil
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
