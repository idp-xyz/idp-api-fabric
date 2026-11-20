package application

import (
	"context"
	"slices"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// ReleaseMembership holds the caller's grants while a release is prepared,
// published or read. A publisher can read the releases of the workspace.
func (s *ProductGovernance) ReleaseMembership(ctx context.Context, actor domain.Principal,
	product domain.ProductID, workspace domain.WorkspaceID) (reader, publisher, active bool, err error) {
	members, err := s.deps.Memberships.LockHeldInWorkspace(ctx, product, workspace, actor)
	if err != nil {
		return false, false, false, err
	}
	for _, member := range members {
		switch member.Role {
		case domain.RoleInternalPublisher:
			publisher, reader = true, true
		case domain.RoleInternalAuthor, domain.RoleInternalReviewer:
			reader = true
		}
	}
	current, found, err := s.deps.Products.Find(ctx, product)
	return reader, publisher, found && current.Status == domain.ProductActive, err
}

// HoldsApprovalGrant checks that the grant under which a reviewer approved
// is still in force. A later regrant does not revive that approval.
func (s *ProductGovernance) HoldsApprovalGrant(ctx context.Context, reviewer domain.Principal,
	product domain.ProductID, workspace domain.WorkspaceID, approvedAt time.Time) (bool, error) {
	members, err := s.deps.Memberships.LockHeldInWorkspace(ctx, product, workspace, reviewer)
	return slices.ContainsFunc(members, func(m domain.Membership) bool {
		return m.Role == domain.RoleInternalReviewer && !m.GrantedAt.After(approvedAt)
	}), err
}
