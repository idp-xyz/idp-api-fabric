package main

import (
	"context"

	catalog "go.idp.xyz/idp-api-fabric/internal/apicatalog/application"
	catalogdomain "go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	registryports "go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
	governance "go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	governancedomain "go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

type revisionScopes struct {
	catalog    *catalog.Catalog
	governance *governance.ProductGovernance
}

func (s revisionScopes) Asset(ctx context.Context, query registryports.ScopeQuery) (registryports.AssetScope, error) {
	asset, err := s.catalog.GetAsset(ctx, catalog.GetAsset{
		Request: catalog.Request{
			Caller: catalogdomain.Principal(query.Caller), ProductID: catalogdomain.ProductID(query.ProductID),
			CorrelationID: query.CorrelationID,
		},
		AssetID: catalogdomain.AssetID(query.AssetID),
	})
	if err != nil {
		return registryports.AssetScope{}, err
	}
	return registryports.AssetScope{
		Asset: registrydomain.AssetRef{ProductID: string(asset.ProductID), WorkspaceID: string(asset.WorkspaceID), AssetID: string(asset.ID)},
		HTTP:  asset.Kind == catalogdomain.KindHTTP,
	}, nil
}

func (s revisionScopes) Author(ctx context.Context, caller registrydomain.Principal, asset registrydomain.AssetRef, importing bool) (bool, bool, error) {
	return s.governance.RevisionAuthor(ctx, governancedomain.Principal(caller),
		governancedomain.ProductID(asset.ProductID), governancedomain.WorkspaceID(asset.WorkspaceID), importing)
}

func (s revisionScopes) Reader(ctx context.Context, caller registrydomain.Principal, asset registrydomain.AssetRef) (bool, error) {
	return s.governance.RevisionReader(ctx, governancedomain.Principal(caller),
		governancedomain.ProductID(asset.ProductID), governancedomain.WorkspaceID(asset.WorkspaceID))
}

func (a auditTrail) recordRevision(ctx context.Context, entry registryports.AuditEntry) error {
	return a.Record(ctx, governanceports.AuditEntry{
		Actor: governancedomain.Principal(entry.Query.Caller), ProductID: governancedomain.ProductID(entry.Query.ProductID),
		ResourceType: "ContractRevision", ResourceID: entry.RevisionID, CorrelationID: entry.Query.CorrelationID,
		Action: entry.Action, Rule: entry.Rule, Allowed: entry.Allowed,
	})
}
