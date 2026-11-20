package main

import (
	"context"
	"errors"
	"time"

	catalog "go.idp.xyz/idp-api-fabric/internal/apicatalog/application"
	registry "go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	registryports "go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	checkdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	governancedomain "go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
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
