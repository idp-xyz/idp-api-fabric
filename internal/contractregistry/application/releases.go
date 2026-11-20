package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

var (
	ErrReleaseNotFound    = errors.New("no such release of the API asset")
	ErrReleaseConflict    = errors.New("the release version already names other evidence")
	ErrRevisionUnapproved = errors.New("the revision has not been approved")
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

type PrepareRelease struct {
	ports.ScopeQuery
	RevisionID string
	ReviewID   string
	Version    string
}

type Preparation struct {
	Release domain.Release
	Created bool
}

func (r *Releases) Prepare(ctx context.Context, command PrepareRelease) (Preparation, error) {
	if err := domain.ValidateReleaseVersion(command.Version); err != nil {
		return Preparation{}, err
	}
	var result Preparation
	err := r.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		asset, err := r.authorizedAsset(tx, command.ScopeQuery, true)
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

		revision, err := r.approvedRevision(tx, asset, command.RevisionID)
		if err != nil {
			return err
		}
		evidence, err := r.deps.Scopes.Approval(tx, revision, command.ReviewID, command.Caller)
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
			ID: r.deps.Identifiers.NewID(), Asset: asset, RevisionID: revision.ID, ReviewID: command.ReviewID,
			Approval: evidence, Version: command.Version, InputDigest: revision.RefClosureDigest, ArtifactDigest: digest,
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

func reusedRelease(existing domain.Release, command PrepareRelease) (Preparation, error) {
	if existing.RevisionID != command.RevisionID || existing.ReviewID != command.ReviewID {
		return Preparation{}, ErrReleaseConflict
	}
	return Preparation{Release: existing}, nil
}

func (r *Releases) approvedRevision(ctx context.Context, asset domain.AssetRef, id string) (domain.Revision, error) {
	revision, found, err := r.deps.Revisions.Find(ctx, asset, id)
	if err != nil {
		return domain.Revision{}, err
	}
	if !found {
		return domain.Revision{}, ErrRevisionNotFound
	}
	if revision.Status != domain.RevisionApproved {
		return domain.Revision{}, ErrRevisionUnapproved
	}
	return revision, nil
}

func (r *Releases) GetRelease(ctx context.Context, query ports.ScopeQuery, id string) (domain.Release, error) {
	var release domain.Release
	err := r.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		asset, err := r.authorizedAsset(tx, query, false)
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
		asset, err := r.authorizedAsset(tx, query, false)
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

func (r *Releases) authorizedAsset(ctx context.Context, query ports.ScopeQuery, publishing bool) (domain.AssetRef, error) {
	scope, err := r.deps.Scopes.Asset(ctx, query)
	if err != nil {
		return domain.AssetRef{}, err
	}
	read, publish, active, err := r.deps.Scopes.ReleaseMembership(ctx, query.Caller, scope.Asset)
	if err != nil {
		return domain.AssetRef{}, err
	}
	if !read || (publishing && !publish) {
		return domain.AssetRef{}, ErrForbidden
	}
	if publishing && !active {
		return domain.AssetRef{}, ErrProductSuspended
	}
	return scope.Asset, nil
}

func (r *Releases) refused(ctx context.Context, query ports.ScopeQuery, action string, cause error) error {
	if !errors.Is(cause, ErrForbidden) {
		return cause
	}
	if err := r.deps.Audit(ctx, ports.AuditEntry{Query: query, Action: action, Rule: "DENY-03"}); err != nil {
		return fmt.Errorf("%w: %w: %v", cause, ErrRefusalNotAudited, err)
	}
	return cause
}

// FindRelease and ReleaseMaterial are trusted queries for Publication. Its
// use case checks caller, target, current pointer and grants before serving
// any release material.
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
	revision, err := r.approvedRevision(ctx, release.Asset, release.RevisionID)
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
