package main

import (
	"context"
	"errors"

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
	checks  *checks.Checks
	reviews *checks.Reviews
	blocks  *publication.ReleaseBlocks
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

// Check verifies the check a release of the revision is prepared on, with
// the waivers whoever prepares it gives, and hands over what the release
// records of them in the words of the registry.
func (s releaseAuthority) Check(ctx context.Context, revision registrydomain.Revision, checkRunID string,
	waivers []registrydomain.Waiver) (registrydomain.CheckEvidence, []registrydomain.Waiver, error) {
	given := make([]checkdomain.ReleaseWaiver, 0, len(waivers))
	for _, waiver := range waivers {
		given = append(given, checkdomain.ReleaseWaiver(waiver))
	}
	verified, err := s.check(ctx, revision, checkRunID, given, true, nil)
	if err != nil {
		return registrydomain.CheckEvidence{}, nil, err
	}
	var recorded []registrydomain.Waiver
	for _, waiver := range verified.Waivers {
		recorded = append(recorded, registrydomain.Waiver(waiver))
	}
	return evidenceOf(verified), recorded, nil
}

// check verifies the check a release of the revision rests on: as a release
// is prepared on it (preparing), or as the check a release recorded, against
// the baseline if there is one.
func (s releaseAuthority) check(ctx context.Context, revision registrydomain.Revision, checkRunID string,
	waivers []checkdomain.ReleaseWaiver, preparing bool, baseline *checkdomain.ComparisonBaseline) (checks.VerifiedCheck, error) {
	return s.checks.VerifyReleaseCheck(ctx, checks.ReleaseCheck{
		Revision: checkRevision(revision), CheckRunID: checkRunID, InputDigest: revision.RefClosureDigest,
		Waivers: waivers, Preparing: preparing, Baseline: baseline,
	})
}

// ReleaseBlocked asks Publication, which keeps the access blocks, whether one
// names the release on a target of its product, for the registry to refuse
// the download of the release by its identifier (CR-0002). The registry has
// checked the grant of the caller already; Publication holds the targets of
// the product in the transaction of ctx before it reads their blocks.
func (s releaseAuthority) ReleaseBlocked(ctx context.Context, release registrydomain.Release) (bool, error) {
	return s.blocks.Blocked(ctx, release.Asset.ProductID, release.Asset.AssetID, release.ID)
}

func evidenceOf(verified checks.VerifiedCheck) registrydomain.CheckEvidence {
	return registrydomain.CheckEvidence{
		CheckRunID: verified.CheckRunID, PolicySnapshotID: verified.PolicySnapshotID, PolicyDigest: verified.PolicyDigest,
	}
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
// verified against the digest the release names. It does not read the check
// the release rests on: a release is put on a slot on its check, and once it
// is there it is served until it is withdrawn or blocked.
func (s publicationAuthority) Artifact(ctx context.Context, request publicationdomain.Request, workspaceID, releaseID string) (publicationports.Artifact, error) {
	_, _, artifact, err := s.release(ctx, assetOf(request, workspaceID), releaseID)
	return artifact, err
}

// Admission verifies a release that is put on a slot: the release and its
// artifact are what was recorded, and its check is the one it was prepared
// on. Where previousID names a release of another revision, the check
// compared the revision with that one.
func (s publicationAuthority) Admission(ctx context.Context, request publicationdomain.Request, workspaceID, releaseID, previousID string) error {
	_, err := s.verify(ctx, assetOf(request, workspaceID), releaseID, previousID)
	return err
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

// verify reads a release and verifies that the check it rests on is the one
// it recorded, and that it compared the revision with the one of the release
// the slot served last, if previousID names one of another revision. Whether
// the check would be made the same way under the policy and the tools of
// today is not asked: that was asked when the release was prepared. It
// hands on what it finds as it is.
func (s publicationAuthority) verify(ctx context.Context, asset registrydomain.AssetRef, releaseID, previousID string) (publicationports.Artifact, error) {
	release, revision, artifact, err := s.release(ctx, asset, releaseID)
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
	verified, err := s.check(ctx, revision, release.Evidence.CheckRunID, nil, false, baseline)
	if err != nil {
		return publicationports.Artifact{}, err
	}
	if release.Evidence != evidenceOf(verified) {
		return publicationports.Artifact{}, checkdomain.ErrCheckRunDamaged
	}
	return artifact, nil
}

func (a auditTrail) recordPublication(ctx context.Context, entry publicationports.AuditEntry) error {
	return a.Record(ctx, governanceports.AuditEntry{
		Actor: governancedomain.Principal(entry.Request.Caller), ProductID: governancedomain.ProductID(entry.Request.ProductID),
		ResourceType: entry.Resource, ResourceID: entry.ResourceID, CorrelationID: entry.Request.CorrelationID,
		Action: entry.Action, Rule: entry.Rule, Allowed: entry.Allowed,
	})
}
