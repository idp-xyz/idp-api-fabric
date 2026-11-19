package main

import (
	"context"

	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	registryports "go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
	checkdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	checkports "go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
	governancedomain "go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

type checkRevisions struct {
	registry *registry.Registry
	scopes   revisionScopes
}

func checkScope(target checkports.Target) registryports.ScopeQuery {
	return registryports.ScopeQuery{
		Caller: registrydomain.Principal(target.Caller), ProductID: target.ProductID,
		AssetID: target.AssetID, CorrelationID: target.CorrelationID,
	}
}

func checkRevision(revision registrydomain.Revision) checkdomain.RevisionRef {
	return checkdomain.RevisionRef{
		ProductID: revision.Asset.ProductID, WorkspaceID: revision.Asset.WorkspaceID,
		AssetID: revision.Asset.AssetID, RevisionID: revision.ID,
	}
}

func (r checkRevisions) Source(ctx context.Context, target checkports.Target) (checkdomain.RevisionRef, checkdomain.Source, error) {
	recorded, err := r.registry.GetRevisionSource(ctx, checkScope(target), target.RevisionID)
	if err != nil {
		return checkdomain.RevisionRef{}, checkdomain.Source{}, err
	}
	source := checkdomain.Source{
		Entrypoint: recorded.Revision.Entrypoint, Digest: recorded.Revision.RefClosureDigest,
		Files: make([]checkdomain.SourceFile, 0, len(recorded.Files)),
	}
	for _, file := range recorded.Files {
		source.Files = append(source.Files, checkdomain.SourceFile{Path: file.Path, Content: file.Content})
	}
	return checkRevision(recorded.Revision), source, nil
}

func (r checkRevisions) LockAuthor(ctx context.Context, caller checkdomain.Principal, revision checkdomain.RevisionRef) (bool, bool, error) {
	return r.scopes.Author(ctx, registrydomain.Principal(caller), registrydomain.AssetRef{
		ProductID: revision.ProductID, WorkspaceID: revision.WorkspaceID, AssetID: revision.AssetID,
	}, true)
}

func (a auditTrail) recordCheck(ctx context.Context, entry checkports.AuditEntry) error {
	return a.Record(ctx, governanceports.AuditEntry{
		Actor: governancedomain.Principal(entry.Target.Caller), ProductID: governancedomain.ProductID(entry.Target.ProductID),
		ResourceType: "CheckRun", ResourceID: entry.CheckRunID, CorrelationID: entry.Target.CorrelationID,
		Action: entry.Action, Rule: entry.Rule, Allowed: entry.Allowed,
	})
}
