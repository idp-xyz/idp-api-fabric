package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

var (
	ErrReleaseNotFound = errors.New("no such release of the API asset")
	ErrReleaseConflict = errors.New("the release version already names other evidence")
	// ErrReleaseBlocked refuses the download of a release by its identifier
	// while an access block of the release stands on a target of its
	// product.
	ErrReleaseBlocked = errors.New("an access block of the release stands")
)

type ReleaseDependencies struct {
	Transactions ports.Transactor
	Scopes       ports.ReleaseScopes
	Revisions    ports.Revisions
	Releases     ports.Releases
	Audit        func(context.Context, ports.AuditEntry) error
	Events       ports.ReleaseEvents
	Clock        ports.Clock
	Identifiers  ports.Identifiers
}

type Releases struct{ deps ReleaseDependencies }

func NewReleases(deps ReleaseDependencies) (*Releases, error) {
	if deps.Transactions == nil || deps.Scopes == nil || deps.Revisions == nil || deps.Releases == nil ||
		deps.Audit == nil || deps.Events == nil || deps.Clock == nil || deps.Identifiers == nil {
		return nil, errors.New("release preparation needs transactions, scopes, revision and release storage, audit, events, clock and identifiers")
	}
	return &Releases{deps: deps}, nil
}

// PrepareRelease names the revision a release is prepared from, the check of
// it the release rests on and the waivers of the rules that did not pass in
// that check.
type PrepareRelease struct {
	ports.ScopeQuery
	RevisionID string
	CheckRunID string
	Waivers    []domain.Waiver
	Version    string
}

type Preparation struct {
	Release domain.Release
	Created bool
}

// Prepare makes a release of a revision, on a check of it. Whatever status
// the revision has in the review it may have had is not asked: the check,
// with its waivers, is what Policy & Review holds the release to. Asking
// again under the same version answers the release that is there when it
// was prepared from the same revision, check and waivers, and is refused
// otherwise (ErrReleaseConflict).
func (r *Releases) Prepare(ctx context.Context, command PrepareRelease) (Preparation, error) {
	if err := domain.ValidateReleaseVersion(command.Version); err != nil {
		return Preparation{}, err
	}
	var result Preparation
	err := r.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		asset, err := r.authorizedAsset(tx, command.ScopeQuery, releasePublisher)
		if err != nil {
			return err
		}
		existing, found, err := r.deps.Releases.Version(tx, asset, command.Version)
		if err != nil {
			return err
		}
		if found {
			result, err = reusedRelease(existing, command)
			return err
		}

		revision, err := r.revision(tx, asset, command.RevisionID)
		if err != nil {
			return err
		}
		evidence, waivers, err := r.deps.Scopes.Check(tx, revision, command.CheckRunID, command.Waivers)
		if err != nil {
			return err
		}
		files, err := r.deps.Revisions.Files(tx, asset, revision.ID)
		if err != nil {
			return err
		}
		payload, digest, err := domain.BuildArtifact(revision, files)
		if err != nil {
			return err
		}
		release := domain.Release{
			ID: r.deps.Identifiers.NewID(), Asset: asset, RevisionID: revision.ID, Specification: revision.Specification,
			Evidence: evidence, Waivers: waivers,
			Version: command.Version, InputDigest: revision.RefClosureDigest, ArtifactDigest: digest,
			PreparedAt: r.deps.Clock.Now().UTC().Truncate(time.Microsecond), PreparedBy: command.Caller,
		}
		created, err := r.deps.Releases.Record(tx, release, payload)
		if err != nil {
			return err
		}
		if !created {
			existing, found, err := r.deps.Releases.Version(tx, asset, command.Version)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("the reserved release version disappeared")
			}
			result, err = reusedRelease(existing, command)
			return err
		}
		if err := r.deps.Audit(tx, ports.AuditEntry{
			Query: command.ScopeQuery, ReleaseID: release.ID, Action: "release.prepare", Rule: "AC-04", Allowed: true,
		}); err != nil {
			return err
		}
		result = Preparation{Release: release, Created: true}
		return r.deps.Events.Prepared(tx, release, command.CorrelationID)
	})
	return result, r.refused(ctx, command.ScopeQuery, "release.prepare", err)
}

// reusedRelease is the release a version names already, when it is the one
// the command asks for: of the same revision, on the same check, with the
// same waivers in whatever order they are given. A release prepared under a
// review is never the one a command asks for, which names no review.
func reusedRelease(existing domain.Release, command PrepareRelease) (Preparation, error) {
	waivers := slices.Clone(command.Waivers)
	slices.SortFunc(waivers, func(a, b domain.Waiver) int { return strings.Compare(a.RuleID, b.RuleID) })
	if existing.RevisionID != command.RevisionID || existing.ReviewID != "" ||
		existing.Evidence.CheckRunID != command.CheckRunID || !slices.Equal(existing.Waivers, waivers) {
		return Preparation{}, ErrReleaseConflict
	}
	return Preparation{Release: existing}, nil
}

func (r *Releases) revision(ctx context.Context, asset domain.AssetRef, id string) (domain.Revision, error) {
	revision, found, err := r.deps.Revisions.Find(ctx, asset, id)
	if err != nil {
		return domain.Revision{}, err
	}
	if !found {
		return domain.Revision{}, ErrRevisionNotFound
	}
	return revision, nil
}

func (r *Releases) GetRelease(ctx context.Context, query ports.ScopeQuery, id string) (domain.Release, error) {
	var release domain.Release
	err := r.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		asset, err := r.authorizedAsset(tx, query, releaseMetadata)
		if err != nil {
			return err
		}
		release, err = r.FindRelease(tx, asset, id)
		return err
	})
	return release, r.refused(ctx, query, "release.read", err)
}

type ReleasePage struct {
	Items []domain.Release
	Next  *ports.Position
}

func (r *Releases) ListReleases(ctx context.Context, query ports.ScopeQuery, after *ports.Position, size int) (ReleasePage, error) {
	if size == 0 {
		size = 50
	}
	if size < 1 || size > 100 {
		return ReleasePage{}, ErrInvalidPage
	}
	var items []domain.Release
	err := r.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		asset, err := r.authorizedAsset(tx, query, releaseMetadata)
		if err != nil {
			return err
		}
		items, err = r.deps.Releases.List(tx, asset, after, size+1)
		return err
	})
	if err != nil {
		return ReleasePage{}, r.refused(ctx, query, "release.read", err)
	}
	if len(items) <= size {
		return ReleasePage{Items: items}, nil
	}
	last := items[size-1]
	return ReleasePage{Items: items[:size], Next: &ports.Position{At: last.PreparedAt, ID: last.ID}}, nil
}

type releaseGrant int

const (
	releaseMetadata releaseGrant = iota
	releaseMaterial
	releasePublisher
)

func (r *Releases) authorizedAsset(ctx context.Context, query ports.ScopeQuery, required releaseGrant) (domain.AssetRef, error) {
	scope, err := r.deps.Scopes.Asset(ctx, query)
	if err != nil {
		return domain.AssetRef{}, err
	}
	access, err := r.deps.Scopes.ReleaseAccess(ctx, query.Caller, scope.Asset)
	if err != nil {
		return domain.AssetRef{}, err
	}
	var allowed bool
	switch required {
	case releaseMetadata:
		allowed = access.Metadata
	case releaseMaterial:
		allowed = access.Material
	case releasePublisher:
		allowed = access.Publisher
	}
	if !allowed {
		return domain.AssetRef{}, ErrForbidden
	}
	if required == releasePublisher && !access.Active {
		return domain.AssetRef{}, ErrProductSuspended
	}
	return scope.Asset, nil
}

// refused puts a refusal on record under DENY-03: one of a caller without the
// grant, and one of a download that an access block of the release stands
// in the way of, as Publication puts on record a read through a slot that a
// block refuses.
func (r *Releases) refused(ctx context.Context, query ports.ScopeQuery, action string, cause error) error {
	if !errors.Is(cause, ErrForbidden) && !errors.Is(cause, ErrReleaseBlocked) {
		return cause
	}
	if err := r.deps.Audit(ctx, ports.AuditEntry{Query: query, Action: action, Rule: "DENY-03"}); err != nil {
		return fmt.Errorf("%w: %w: %v", cause, ErrRefusalNotAudited, err)
	}
	return cause
}

// FindRelease and ReleaseMaterial are trusted queries: they ask nothing of the
// caller. Publication uses them for what a slot serves, and its use case
// checks caller, target, current pointer and grants before serving any
// release material. GetReleaseArtifact uses them too, to download a release by
// its identifier after checking the caller's release evidence grant and the
// access blocks of the release.
func (r *Releases) FindRelease(ctx context.Context, asset domain.AssetRef, id string) (domain.Release, error) {
	release, found, err := r.deps.Releases.Find(ctx, asset, id)
	if err != nil {
		return domain.Release{}, err
	}
	if !found {
		return domain.Release{}, ErrReleaseNotFound
	}
	return release, nil
}

func (r *Releases) ReleaseMaterial(ctx context.Context, release domain.Release) (domain.Revision, []byte, error) {
	revision, err := r.revision(ctx, release.Asset, release.RevisionID)
	if err != nil {
		return domain.Revision{}, nil, err
	}
	if revision.RefClosureDigest != release.InputDigest {
		return domain.Revision{}, nil, domain.ErrArtifactDamaged
	}
	payload, err := r.deps.Releases.Artifact(ctx, release.ArtifactDigest)
	if err != nil {
		return domain.Revision{}, nil, err
	}
	return revision, payload, domain.VerifyArtifact(release.ArtifactDigest, payload)
}
