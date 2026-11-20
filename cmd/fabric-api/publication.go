package main

import (
	"context"
	"errors"
	"time"

	catalog "go.idp.xyz/idp-api-fabric/internal/apicatalog/application"
	catalogdomain "go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	registryports "go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	checkdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	governance "go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	governancedomain "go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
	publication "go.idp.xyz/idp-api-fabric/internal/publication/application"
	publicationdomain "go.idp.xyz/idp-api-fabric/internal/publication/domain"
	publicationports "go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

type releaseAuthority struct {
	revisionScopes
	reviews *checks.Reviews
}

func (s releaseAuthority) Asset(ctx context.Context, query registryports.ScopeQuery) (registryports.AssetScope, error) {
	asset, err := s.revisionScopes.Asset(ctx, query)
	if errors.Is(err, catalog.ErrAssetNotFound) || errors.Is(err, catalog.ErrProductNotFound) {
		err = errors.Join(err, registry.ErrForbidden)
	}
	return asset, err
}

func (s releaseAuthority) ReleaseMembership(ctx context.Context, actor registrydomain.Principal, asset registrydomain.AssetRef) (bool, bool, bool, error) {
	return s.governance.ReleaseMembership(ctx, governancedomain.Principal(actor),
		governancedomain.ProductID(asset.ProductID), governancedomain.WorkspaceID(asset.WorkspaceID))
}

func (s releaseAuthority) Approval(ctx context.Context, revision registrydomain.Revision, id string, actor registrydomain.Principal) (registrydomain.ApprovalEvidence, error) {
	evidence, err := s.approval(ctx, revision, id, actor, nil, nil)
	if errors.Is(err, checks.ErrPublisherInvolved) {
		err = errors.Join(err, registry.ErrForbidden)
	}
	return evidence, err
}

func (s releaseAuthority) approval(ctx context.Context, revision registrydomain.Revision, id string, actor registrydomain.Principal,
	target *checkdomain.Target, baseline *checkdomain.ComparisonBaseline) (registrydomain.ApprovalEvidence, error) {
	approved, err := s.reviews.VerifyReleaseApproval(ctx, checks.ReleaseApproval{
		Revision: checkRevision(revision), ReviewID: id, InputDigest: revision.RefClosureDigest,
		Author: checkdomain.Principal(revision.CreatedBy), Publisher: checkdomain.Principal(actor), Target: target, Baseline: baseline,
	}, func(ctx context.Context, reviewer checkdomain.Principal, approvedAt time.Time) (bool, error) {
		return s.governance.HoldsApprovalGrant(ctx, governancedomain.Principal(reviewer),
			governancedomain.ProductID(revision.Asset.ProductID), governancedomain.WorkspaceID(revision.Asset.WorkspaceID), approvedAt)
	})
	if err != nil {
		return registrydomain.ApprovalEvidence{}, err
	}
	return registrydomain.ApprovalEvidence{
		CheckRunID: approved.CheckRunID, PolicySnapshotID: approved.PolicySnapshotID, PolicyDigest: approved.PolicyDigest,
	}, nil
}

type publicationAuthority struct {
	releaseAuthority
	releases *registry.Releases
}

func (s publicationAuthority) ProductKnown(ctx context.Context, request publicationdomain.Request) error {
	scope, err := s.governance.ReadCatalogScope(ctx, governancedomain.Principal(request.Caller), governancedomain.ProductID(request.ProductID))
	if err == nil && !scope.Known {
		err = errors.Join(governance.ErrProductNotFound, publication.ErrForbidden)
	}
	return err
}

func (s publicationAuthority) role(ctx context.Context, request publicationdomain.Request, roles ...governancedomain.Role) (bool, error) {
	active, err := s.governance.RequirePublicationRole(ctx, governancedomain.Principal(request.Caller), governancedomain.ProductID(request.ProductID), roles...)
	if errors.Is(err, governance.ErrForbidden) || errors.Is(err, governance.ErrProductNotFound) {
		err = errors.Join(err, publication.ErrForbidden)
	}
	return active, err
}

func (s publicationAuthority) TargetManager(ctx context.Context, request publicationdomain.Request) (bool, error) {
	return s.role(ctx, request, governancedomain.RoleProductOwner)
}

func (s publicationAuthority) Security(ctx context.Context, request publicationdomain.Request, reviewing bool) error {
	role := governancedomain.RoleSecurityAdmin
	if reviewing {
		role = governancedomain.RoleSecurityReviewer
	}
	_, err := s.role(ctx, request, role)
	return err
}

func (s publicationAuthority) ReadBlocks(ctx context.Context, request publicationdomain.Request) error {
	_, err := s.role(ctx, request, governancedomain.RoleSecurityAdmin, governancedomain.RoleSecurityReviewer)
	return err
}

func (s publicationAuthority) Config(ctx context.Context, productID, configID string) (string, string, error) {
	config, err := s.governance.HoldPublicationConfig(ctx, governancedomain.ProductID(productID), governancedomain.AudienceConfigID(configID))
	return string(config.Audience), string(config.Environment), err
}

func (s publicationAuthority) Asset(ctx context.Context, request publicationdomain.Request) (publicationports.AssetAccess, error) {
	scope, err := s.revisionScopes.Asset(ctx, registryports.ScopeQuery{
		Caller: registrydomain.Principal(request.Caller), ProductID: request.ProductID, AssetID: request.AssetID,
		CorrelationID: request.CorrelationID,
	})
	if err != nil {
		if errors.Is(err, catalog.ErrAssetNotFound) || errors.Is(err, catalog.ErrProductNotFound) {
			err = errors.Join(err, publication.ErrForbidden)
		}
		return publicationports.AssetAccess{}, err
	}
	read, publish, active, err := s.ReleaseMembership(ctx, registrydomain.Principal(request.Caller), scope.Asset)
	return publicationports.AssetAccess{WorkspaceID: scope.Asset.WorkspaceID, Reader: read, Publisher: publish, Active: active}, err
}

func (s publicationAuthority) BlockScope(ctx context.Context, block publicationdomain.AccessBlock) error {
	if block.AssetID == "" {
		return nil
	}
	asset, err := s.catalog.AssetInProduct(ctx, catalogdomain.ProductID(block.ProductID), catalogdomain.AssetID(block.AssetID))
	if err != nil || block.ReleaseID == "" {
		return err
	}
	_, err = s.releases.FindRelease(ctx, registrydomain.AssetRef{
		ProductID: block.ProductID, AssetID: block.AssetID, WorkspaceID: string(asset.WorkspaceID),
	}, block.ReleaseID)
	return err
}

func (s publicationAuthority) Material(ctx context.Context, request publicationdomain.Request, workspaceID string,
	target publicationdomain.Target, releaseID, previousID string, reading bool) (publicationports.Artifact, error) {
	asset := registrydomain.AssetRef{ProductID: request.ProductID, WorkspaceID: workspaceID, AssetID: request.AssetID}
	release, err := s.releases.FindRelease(ctx, asset, releaseID)
	if err != nil {
		return publicationports.Artifact{}, err
	}
	revision, payload, err := s.releases.ReleaseMaterial(ctx, release)
	if err != nil {
		return publicationports.Artifact{}, err
	}
	var baseline *checkdomain.ComparisonBaseline
	if previousID != "" && previousID != release.ID {
		previous, err := s.releases.FindRelease(ctx, asset, previousID)
		if err != nil {
			return publicationports.Artifact{}, err
		}
		if previous.RevisionID != release.RevisionID {
			baseline = &checkdomain.ComparisonBaseline{RevisionID: previous.RevisionID, InputDigest: previous.InputDigest}
		}
	}
	actor := registrydomain.Principal(request.Caller)
	if reading {
		actor = registrydomain.Principal{}
	}
	evidence, err := s.approval(ctx, revision, release.ReviewID, actor,
		&checkdomain.Target{Audience: target.Audience, Environment: target.Environment}, baseline)
	if err != nil {
		if errors.Is(err, checks.ErrPublisherInvolved) {
			err = errors.Join(err, publication.ErrForbidden)
		}
		if reading && (errors.Is(err, checks.ErrApprovalExpired) || errors.Is(err, checks.ErrApprovalRequired) ||
			errors.Is(err, checks.ErrApprovalScope) || errors.Is(err, checks.ErrWaiverExpired) ||
			errors.Is(err, checks.ErrReviewerGrantRevoked) || errors.Is(err, checkdomain.ErrCheckStale)) {
			err = publication.ErrNoVisibleArtifact
		}
		return publicationports.Artifact{}, err
	}
	if release.Approval != evidence {
		return publicationports.Artifact{}, checkdomain.ErrReviewDamaged
	}
	return publicationports.Artifact{Payload: payload, Digest: release.ArtifactDigest}, nil
}

func (a auditTrail) recordPublication(ctx context.Context, entry publicationports.AuditEntry) error {
	return a.Record(ctx, governanceports.AuditEntry{
		Actor: governancedomain.Principal(entry.Request.Caller), ProductID: governancedomain.ProductID(entry.Request.ProductID),
		ResourceType: entry.Resource, ResourceID: entry.ResourceID, CorrelationID: entry.Request.CorrelationID,
		Action: entry.Action, Rule: entry.Rule, Allowed: entry.Allowed,
	})
}
