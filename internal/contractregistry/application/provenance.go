package application

import (
	"context"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

// The actions a refusal to read a release is put on record under, beside
// release.read of the metadata of a release: what was asked for is told
// apart in the trail, as publication.download is from publication.read.
const (
	actionDownloadRelease = "release.download"
	actionReadProvenance  = "release.read"
)

// ReleaseDownload is a release with the bytes of its artifact, which are the
// bytes the release was prepared with.
type ReleaseDownload struct {
	Release domain.Release
	Payload []byte
}

// GetReleaseArtifact reads the artifact by release identifier when the caller
// holds a release evidence grant for the asset.
//
// The release is read the way GetRelease reads it, so a release of another
// asset or product is no release here. A release that an access block names,
// on any publication target of the product and whatever the target serves,
// is not downloaded until the block is cleared (ErrReleaseBlocked, CR-0002):
// a block of one release is a block of its bytes, wherever they are read.
// A block of a whole target or of an asset on it does not refuse
// the download: it is about what the target serves, and a release read by
// its identifier is served to no target. Its bytes are read and verified as
// ReleaseMaterial does for a slot: the closure of its revision is the one
// the release records and the bytes are those of its digest. A product that
// is suspended still shows what it has released.
//
// The blocks are read after the grants of the caller and in the same
// transaction, which is the order a read through a slot takes them in: the
// targets of the product are held as that read holds its target, so a block
// made or cleared at the same time is ordered with the download (see
// ReleaseScopes.ReleaseBlocked).
func (r *Releases) GetReleaseArtifact(ctx context.Context, query ports.ScopeQuery, id string) (ReleaseDownload, error) {
	var download ReleaseDownload
	err := r.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		asset, err := r.authorizedAsset(tx, query, releaseMaterial)
		if err != nil {
			return err
		}
		release, err := r.FindRelease(tx, asset, id)
		if err != nil {
			return err
		}
		blocked, err := r.deps.Scopes.ReleaseBlocked(tx, release)
		if err != nil {
			return err
		}
		if blocked {
			return ErrReleaseBlocked
		}
		_, payload, err := r.ReleaseMaterial(tx, release)
		if err != nil {
			return err
		}
		download = ReleaseDownload{Release: release, Payload: payload}
		return nil
	})
	if err != nil {
		return ReleaseDownload{}, r.refused(ctx, query, actionDownloadRelease, err)
	}
	return download, nil
}

// GetReleaseProvenance returns the release, revision, check and approval it
// names when the caller holds a release evidence grant for the asset. A
// release prepared on its check alone names no approval.
//
// Every part is read by what the release recorded when it was prepared: the
// revision, the check and the review it names. None of it is the latest of
// anything, so the answer is the same after the revision is checked again
// or its repository moves on. Nothing is verified against the policy of
// today.
//
// A release whose revision or evidence is not what it recorded is a failure
// of the storage and is answered as one, without saying what is wrong.
func (r *Releases) GetReleaseProvenance(ctx context.Context, query ports.ScopeQuery, id string) (domain.ReleaseProvenance, error) {
	var provenance domain.ReleaseProvenance
	err := r.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		asset, err := r.authorizedAsset(tx, query, releaseMaterial)
		if err != nil {
			return err
		}
		release, err := r.FindRelease(tx, asset, id)
		if err != nil {
			return err
		}
		revision, found, err := r.deps.Revisions.Find(tx, asset, release.RevisionID)
		if err != nil {
			return err
		}
		// As ReleaseMaterial holds them to each other: the release was
		// prepared from the closure its revision has.
		if !found || revision.RefClosureDigest != release.InputDigest {
			return fmt.Errorf("release %s is not of the closure of its revision: %w", release.ID, domain.ErrArtifactDamaged)
		}
		evidence, err := r.deps.Scopes.Evidence(tx, release)
		if err != nil {
			return err
		}
		provenance = domain.ReleaseProvenance{Release: release, Revision: revision, Evidence: evidence}
		return nil
	})
	if err != nil {
		return domain.ReleaseProvenance{}, r.refused(ctx, query, actionReadProvenance, err)
	}
	return provenance, nil
}
