package application

import (
	"context"
	"slices"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// PublicationScope records the grants for release records, evidence, slots,
// artifact delivery and publication. An internal reader downloads through
// the active slot so that the origin applies AccessBlock.
type PublicationScope struct {
	ReleaseMetadata bool
	ReleaseMaterial bool
	Slots           bool
	Artifact        bool
	Publisher       bool
	Active          bool
}

// HoldPublicationScope holds the grants for this asset's workspace and product
// while Registry or Publication handles the request.
func (s *ProductGovernance) HoldPublicationScope(ctx context.Context, actor domain.Principal,
	product domain.ProductID, workspace domain.WorkspaceID) (PublicationScope, error) {
	members, err := s.deps.Memberships.LockHeldInWorkspace(ctx, product, workspace, actor)
	if err != nil {
		return PublicationScope{}, err
	}
	var scope PublicationScope
	var internalReader bool
	for _, member := range members {
		scope.Publisher = scope.Publisher || member.Role == domain.RoleInternalPublisher
		scope.ReleaseMaterial = scope.ReleaseMaterial || slices.Contains(revisionReaders, member.Role)
		internalReader = internalReader || member.Role == domain.RoleInternalReader
	}
	var security bool
	if !scope.ReleaseMaterial {
		roles, err := s.deps.Roles.LockHeldRoles(ctx, product, actor)
		if err != nil {
			return PublicationScope{}, err
		}
		scope.ReleaseMaterial = slices.Contains(roles, domain.RoleProductOwner)
		security = holdsSecurityRole(roles)
	}
	scope.ReleaseMetadata = internalReader || scope.ReleaseMaterial
	scope.Artifact = scope.ReleaseMetadata
	scope.Slots = scope.Artifact || security
	current, found, err := s.deps.Products.Find(ctx, product)
	scope.Active = found && current.Status == domain.ProductActive
	return scope, err
}

func holdsSecurityRole(roles []domain.Role) bool {
	return slices.Contains(roles, domain.RoleSecurityAdmin) || slices.Contains(roles, domain.RoleSecurityReviewer)
}

// ActiveProductKey identifies a product that can serve a publication.
func (s *ProductGovernance) ActiveProductKey(ctx context.Context, id domain.ProductID) (string, error) {
	product, found, err := s.deps.Products.Find(ctx, id)
	if err != nil || !found || product.Status != domain.ProductActive {
		return "", err
	}
	return product.Key, nil
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
