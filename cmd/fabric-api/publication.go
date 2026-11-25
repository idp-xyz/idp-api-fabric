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

func (s releaseAuthority) ReleaseAccess(ctx context.Context, actor registrydomain.Principal, asset registrydomain.AssetRef) (registryports.ReleaseAccess, error) {
	grants, err := s.governance.HoldPublicationScope(ctx, governancedomain.Principal(actor),
		governancedomain.ProductID(asset.ProductID), governancedomain.WorkspaceID(asset.WorkspaceID))
	return registryports.ReleaseAccess{
		Metadata: grants.ReleaseMetadata, Material: grants.ReleaseMaterial, Publisher: grants.Publisher, Active: grants.Active,
	}, err
}

func (s releaseAuthority) Approval(ctx context.Context, revision registrydomain.Revision, id string, actor registrydomain.Principal) (registrydomain.ApprovalEvidence, error) {
	evidence, err := s.approval(ctx, revision, id, actor, nil, nil, false)
	if errors.Is(err, checks.ErrPublisherInvolved) {
		err = errors.Join(err, registry.ErrForbidden)
	}
	return evidence, err
}

// approval verifies the review a release names as the approval of the
// revision: for the target and against the baseline if there is one, and
// for a return to a release (restoring) only as far as it was given.
func (s releaseAuthority) approval(ctx context.Context, revision registrydomain.Revision, id string, actor registrydomain.Principal,
	target *checkdomain.Target, baseline *checkdomain.ComparisonBaseline, restoring bool) (registrydomain.ApprovalEvidence, error) {
	approved, err := s.reviews.VerifyReleaseApproval(ctx, checks.ReleaseApproval{
		Revision: checkRevision(revision), ReviewID: id, InputDigest: revision.RefClosureDigest,
		Author: checkdomain.Principal(revision.CreatedBy), Publisher: checkdomain.Principal(actor), Target: target, Baseline: baseline,
		Restoring: restoring,
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
	grants, err := s.governance.HoldPublicationScope(ctx, governancedomain.Principal(request.Caller),
		governancedomain.ProductID(scope.Asset.ProductID), governancedomain.WorkspaceID(scope.Asset.WorkspaceID))
	return publicationports.AssetAccess{
		WorkspaceID: scope.Asset.WorkspaceID, Reader: grants.Slots, ArtifactReader: grants.Artifact,
		Publisher: grants.Publisher, Active: grants.Active,
	}, err
}

// ReadableAssets returns the asset placements whose slots the caller can
// read under the current product and workspace grants.
func (s publicationAuthority) ReadableAssets(ctx context.Context, request publicationdomain.Request) ([]publicationports.ReadableAsset, error) {
	readers, err := s.governance.ReleaseReaderScope(ctx, governancedomain.Principal(request.Caller), governancedomain.ProductID(request.ProductID))
	if err != nil {
		if errors.Is(err, governance.ErrForbidden) || errors.Is(err, governance.ErrProductNotFound) {
			err = errors.Join(err, publication.ErrForbidden)
		}
		return nil, err
	}
	allWorkspaces := readers.Owner || readers.Security
	switch {
	case !readers.Known:
		return nil, errors.Join(governance.ErrProductNotFound, publication.ErrForbidden)
	case !allWorkspaces && len(readers.Workspaces) == 0:
		return nil, publication.ErrForbidden
	}
	workspaces := make([]catalogdomain.WorkspaceID, 0, len(readers.Workspaces))
	for _, workspace := range readers.Workspaces {
		workspaces = append(workspaces, catalogdomain.WorkspaceID(workspace))
	}
	placed, err := s.catalog.AssetPlaces(ctx, catalogdomain.ProductID(request.ProductID), workspaces, allWorkspaces)
	if err != nil {
		return nil, err
	}
	readable := make([]publicationports.ReadableAsset, 0, len(placed))
	for _, asset := range placed {
		readable = append(readable, publicationports.ReadableAsset{AssetID: string(asset.ID), WorkspaceID: string(asset.WorkspaceID)})
	}
	return readable, nil
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

// Artifact reads what a slot serves: the release and its artifact, which is
// verified against the digest the release names. It does not read the
// review the release rests on. A release is put on a slot under an approval
// that holds; once it is there it is served until it is withdrawn or
// blocked, and what became of the approval since is for the slot to say.
func (s publicationAuthority) Artifact(ctx context.Context, request publicationdomain.Request, workspaceID, releaseID string) (publicationports.Artifact, error) {
	_, _, artifact, err := s.release(ctx, assetOf(request, workspaceID), releaseID)
	return artifact, err
}

// Admission verifies a release for the caller who puts it on a slot of the
// target. A publisher who had a part in the revision is refused as one who
// may not publish it, whether the release is new to the slot or returns to
// it.
func (s publicationAuthority) Admission(ctx context.Context, request publicationdomain.Request, workspaceID string,
	target publicationdomain.Target, releaseID, previousID string, returning bool) error {
	err := s.verify(ctx, assetOf(request, workspaceID), target, releaseID, previousID, registrydomain.Principal(request.Caller), returning)
	if errors.Is(err, checks.ErrPublisherInvolved) {
		err = errors.Join(err, publication.ErrForbidden)
	}
	return err
}

// Approval verifies the approval a release rests on as an activation of the
// release would, the duties of whoever publishes and the comparison with
// what the slot served aside, and answers why it does not hold. The slot
// says it, and serves the release all the same.
func (s publicationAuthority) Approval(ctx context.Context, request publicationdomain.Request, workspaceID string,
	target publicationdomain.Target, releaseID string) (error, error) {
	err := s.verify(ctx, assetOf(request, workspaceID), target, releaseID, "", registrydomain.Principal{}, false)
	if unapproved(err) {
		return err, nil
	}
	return nil, err
}

// unapproved reports whether err says that the approval a release rests on
// does not hold now: it is not one, it ran out or a waiver of it did, it
// does not reach the target, its reviewer lost the grant, or the policy and
// the tools moved on from its check. These are the reasons a slot names
// for a release that would not be put on it today.
func unapproved(err error) bool {
	return errors.Is(err, checks.ErrApprovalExpired) || errors.Is(err, checks.ErrApprovalRequired) ||
		errors.Is(err, checks.ErrApprovalScope) || errors.Is(err, checks.ErrWaiverExpired) ||
		errors.Is(err, checks.ErrReviewerGrantRevoked) || errors.Is(err, checkdomain.ErrCheckStale)
}

// assetOf names the asset of a request in the workspace that owns it.
func assetOf(request publicationdomain.Request, workspaceID string) registrydomain.AssetRef {
	return registrydomain.AssetRef{ProductID: request.ProductID, WorkspaceID: workspaceID, AssetID: request.AssetID}
}

// release reads a release of the asset, the revision it was prepared from
// and its artifact, verified against the digest of the release. It hands on
// what it finds as it is.
func (s publicationAuthority) release(ctx context.Context, asset registrydomain.AssetRef, releaseID string) (
	registrydomain.Release, registrydomain.Revision, publicationports.Artifact, error) {
	release, err := s.releases.FindRelease(ctx, asset, releaseID)
	if err != nil {
		return registrydomain.Release{}, registrydomain.Revision{}, publicationports.Artifact{}, err
	}
	revision, payload, err := s.releases.ReleaseMaterial(ctx, release)
	if err != nil {
		return registrydomain.Release{}, registrydomain.Revision{}, publicationports.Artifact{}, err
	}
	return release, revision, publicationports.Artifact{Payload: payload, Digest: release.ArtifactDigest}, nil
}

// verify reads a release and verifies the approval it rests on for the
// target: for the publisher, if one is named, and against the release the
// slot served last, if previousID names one of another revision. returning
// verifies the approval as it was given and not whether it still holds. It
// hands on what it finds as it is.
func (s publicationAuthority) verify(ctx context.Context, asset registrydomain.AssetRef, target publicationdomain.Target,
	releaseID, previousID string, publisher registrydomain.Principal, returning bool) error {
	release, revision, _, err := s.release(ctx, asset, releaseID)
	if err != nil {
		return err
	}
	var baseline *checkdomain.ComparisonBaseline
	if previousID != "" && previousID != release.ID {
		previous, err := s.releases.FindRelease(ctx, asset, previousID)
		if err != nil {
			return err
		}
		if previous.RevisionID != release.RevisionID {
			baseline = &checkdomain.ComparisonBaseline{RevisionID: previous.RevisionID, InputDigest: previous.InputDigest}
		}
	}
	evidence, err := s.approval(ctx, revision, release.ReviewID, publisher,
		&checkdomain.Target{Audience: target.Audience, Environment: target.Environment}, baseline, returning)
	if err != nil {
		return err
	}
	if release.Approval != evidence {
		return checkdomain.ErrReviewDamaged
	}
	return nil
}

func (a auditTrail) recordPublication(ctx context.Context, entry publicationports.AuditEntry) error {
	return a.Record(ctx, governanceports.AuditEntry{
		Actor: governancedomain.Principal(entry.Request.Caller), ProductID: governancedomain.ProductID(entry.Request.ProductID),
		ResourceType: entry.Resource, ResourceID: entry.ResourceID, CorrelationID: entry.Request.CorrelationID,
		Action: entry.Action, Rule: entry.Rule, Allowed: entry.Allowed,
	})
}
