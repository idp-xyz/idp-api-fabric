package main

import (
	"context"

	catalogdomain "go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	catalogports "go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
	governance "go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	governancedomain "go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// Product Governance exposes scope facts and the authorizing membership
// lock. The Catalog consumes neither its adapters nor its database schema.
type catalogScopes struct{ governance *governance.ProductGovernance }

func (s catalogScopes) Product(ctx context.Context, product catalogdomain.ProductID, actor catalogdomain.Principal) (catalogports.ProductScope, error) {
	scope, err := s.governance.ReadCatalogScope(ctx, governancedomain.Principal(actor), governancedomain.ProductID(product))
	return catalogScopeOf(scope), err
}

func (s catalogScopes) Registration(ctx context.Context, product catalogdomain.ProductID, workspace catalogdomain.WorkspaceID, actor catalogdomain.Principal) (catalogports.RegistrationScope, error) {
	scope, err := s.governance.LockCatalogRegistration(ctx, governancedomain.Principal(actor),
		governancedomain.ProductID(product), governancedomain.WorkspaceID(workspace))
	return catalogports.RegistrationScope{
		ProductScope: catalogScopeOf(scope.CatalogScope), WorkspaceExists: scope.WorkspaceFound, Maintainer: scope.Maintainer,
	}, err
}

func catalogScopeOf(scope governance.CatalogScope) catalogports.ProductScope {
	workspaces := make([]catalogdomain.WorkspaceID, 0, len(scope.Workspaces))
	for _, workspace := range scope.Workspaces {
		workspaces = append(workspaces, catalogdomain.WorkspaceID(workspace))
	}
	readers := make([]catalogdomain.WorkspaceID, 0, len(scope.ContractReaders))
	for _, workspace := range scope.ContractReaders {
		readers = append(readers, catalogdomain.WorkspaceID(workspace))
	}
	return catalogports.ProductScope{
		Exists: scope.Found, Active: scope.Active, Known: scope.Known, Owner: scope.Owner, Security: scope.Security, Workspaces: workspaces,
		Readers: readers,
	}
}

func (a auditTrail) recordCatalog(ctx context.Context, entry catalogports.AuditEntry) error {
	return a.Record(ctx, governanceports.AuditEntry{
		Actor: governancedomain.Principal(entry.Actor), ProductID: governancedomain.ProductID(entry.ProductID),
		ResourceType: "APIAsset", ResourceID: entry.ResourceID, CorrelationID: entry.CorrelationID,
		Action: entry.Action, Rule: entry.Rule, Allowed: entry.Allowed,
	})
}
