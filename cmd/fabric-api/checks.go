package main

import (
	"context"
	"errors"
	"strings"

	registryopenapi "go.idp.xyz/idp-api-fabric/internal/contractregistry/adapters/openapi"
	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	registryports "go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
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

func (r checkRevisions) Revision(ctx context.Context, target checkports.Target) (checkdomain.RevisionRef, string, error) {
	revision, err := r.registry.GetRevision(ctx, checkScope(target), target.RevisionID)
	if err != nil {
		return checkdomain.RevisionRef{}, "", err
	}
	return checkRevision(revision), revision.RefClosureDigest, nil
}

func (r checkRevisions) Source(ctx context.Context, target checkports.Target) (checkdomain.RevisionRef, checkdomain.Source, error) {
	recorded, err := r.registry.GetRevisionSource(ctx, checkScope(target), target.RevisionID)
	if err != nil {
		return checkdomain.RevisionRef{}, checkdomain.Source{}, err
	}
	source := checkdomain.Source{
		Specification: checkdomain.Specification(recorded.Revision.Specification),
		Entrypoint:    recorded.Revision.Entrypoint, Digest: recorded.Revision.RefClosureDigest,
		Files: make([]checkdomain.SourceFile, 0, len(recorded.Files)),
	}
	source.Closure = verifyReferenceClosure(source.Entrypoint, source.Digest, recorded.Files)
	for _, file := range recorded.Files {
		source.Files = append(source.Files, checkdomain.SourceFile{Path: file.Path, Content: file.Content})
	}
	return checkRevision(recorded.Revision), source, nil
}

func verifyReferenceClosure(entrypoint, digest string, files []registrydomain.RecordedFile) checkdomain.ReferenceClosure {
	submitted := make([]registrydomain.SourceFile, 0, len(files))
	for _, file := range files {
		submitted = append(submitted, registrydomain.SourceFile{Path: file.Path, Content: file.Content})
	}
	captured, err := registryopenapi.Capture(entrypoint, submitted)
	switch {
	case errors.Is(err, registrydomain.ErrInvalidSource):
		return checkdomain.ClosureInvalid
	case err != nil:
		return checkdomain.ClosureUndecided
	case captured.RefClosureDigest != digest:
		return checkdomain.ClosureInvalid
	default:
		return checkdomain.ClosureVerified
	}
}

func (r checkRevisions) LockAuthor(ctx context.Context, caller checkdomain.Principal, revision checkdomain.RevisionRef) (bool, bool, error) {
	return r.scopes.Author(ctx, registrydomain.Principal(caller), registrydomain.AssetRef{
		ProductID: revision.ProductID, WorkspaceID: revision.WorkspaceID, AssetID: revision.AssetID,
	}, true)
}

// policyReview is the use cases of Policy & Review as the transport reaches
// them: those of a check, the reads of a review and the queue of the reviews
// of a workspace.
type policyReview struct {
	*checks.Checks
	*checks.Reviews
	*checks.ReviewQueue
}

func (a auditTrail) recordCheck(ctx context.Context, entry checkports.AuditEntry) error {
	resource, id := "CheckRun", entry.CheckRunID
	if strings.HasPrefix(entry.Action, "review.") {
		// A refusal to read the reviews of a workspace names no review.
		resource, id = "Review", ""
	}
	return a.Record(ctx, governanceports.AuditEntry{
		Actor: governancedomain.Principal(entry.Target.Caller), ProductID: governancedomain.ProductID(entry.Target.ProductID),
		ResourceType: resource, ResourceID: id, CorrelationID: entry.Target.CorrelationID,
		Action: entry.Action, Rule: entry.Rule, Allowed: entry.Allowed,
	})
}
