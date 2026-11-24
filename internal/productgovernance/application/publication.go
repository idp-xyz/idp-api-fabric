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
	if !reader {
		roles, err := s.deps.Roles.LockHeldRoles(ctx, product, actor)
		if err != nil {
			return false, false, false, err
		}
		reader = slices.Contains(roles, domain.RoleProductOwner)
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

// RequirePublicationRole holds the specific product grant for a target or
// security operation. Platform administration alone conveys no such grant.
func (s *ProductGovernance) RequirePublicationRole(ctx context.Context, actor domain.Principal, id domain.ProductID, allowed ...domain.Role) (bool, error) {
	scope, err := s.ReadCatalogScope(ctx, actor, id)
	if err != nil {
		return false, err
	}
	if !scope.Known {
		return false, ErrProductNotFound
	}
	roles, err := s.deps.Roles.LockHeldRoles(ctx, id, actor)
	if err != nil {
		return false, err
	}
	if !slices.ContainsFunc(roles, func(role domain.Role) bool { return slices.Contains(allowed, role) }) {
		return false, ErrForbidden
	}
	return scope.Active, nil
}

// HoldPublicationConfig reads the audience and environment of the active
// configuration, keeping a concurrent removal ordered with this transaction.
func (s *ProductGovernance) HoldPublicationConfig(ctx context.Context, product domain.ProductID, id domain.AudienceConfigID) (domain.AudienceConfig, error) {
	config, found, err := s.deps.AudienceConfigs.Hold(ctx, product, id)
	if err != nil {
		return domain.AudienceConfig{}, err
	}
	if !found {
		return domain.AudienceConfig{}, ErrAudienceConfigNotFound
	}
	return config, nil
}
