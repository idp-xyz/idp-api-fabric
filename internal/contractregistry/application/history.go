package application

import (
	"context"
	"errors"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

var (
	ErrRevisionNotFound = errors.New("no such revision of the API asset")
	ErrInvalidPage      = errors.New("the revision page is invalid")
)

const readAction = "revision.read"

func (r *Registry) GetRevision(ctx context.Context, query ports.ScopeQuery, id string) (domain.Revision, error) {
	asset, err := r.readAsset(ctx, query)
	if err != nil {
		return domain.Revision{}, err
	}
	revision, found, err := r.deps.Revisions.Find(ctx, asset, id)
	if err != nil {
		return domain.Revision{}, err
	}
	if !found {
		return domain.Revision{}, ErrRevisionNotFound
	}
	return revision, nil
}

// RevisionSource is a revision with the files it was recorded from.
type RevisionSource struct {
	Revision domain.Revision
	Files    []domain.RecordedFile
}

// GetRevisionSource reads the files a revision was recorded from, for whoever
// may read the revision. The files are checked against the digests of the
// revision before they are returned: it fails with domain.ErrSourceDamaged
// rather than hand out files that are not the recorded ones.
//
// A revision and its files are written once, in one transaction, and never
// changed, so the two reads here need no transaction of their own.
func (r *Registry) GetRevisionSource(ctx context.Context, query ports.ScopeQuery, id string) (RevisionSource, error) {
	asset, err := r.readAsset(ctx, query)
	if err != nil {
		return RevisionSource{}, err
	}
	revision, found, err := r.deps.Revisions.Find(ctx, asset, id)
	if err != nil {
		return RevisionSource{}, err
	}
	if !found {
		return RevisionSource{}, ErrRevisionNotFound
	}
	stored, err := r.deps.Revisions.Files(ctx, asset, id)
	if err != nil {
		return RevisionSource{}, err
	}
	files, err := domain.RecordedSource(revision, stored)
	if err != nil {
		return RevisionSource{}, fmt.Errorf("the source of revision %s: %w", revision.ID, err)
	}
	return RevisionSource{Revision: revision, Files: files}, nil
}

type ListRevisions struct {
	ports.ScopeQuery
	PageSize int
	After    *ports.Position
}

type Page struct {
	Items []domain.Revision
	Next  *ports.Position
}

func (r *Registry) ListRevisions(ctx context.Context, query ListRevisions) (Page, error) {
	size := query.PageSize
	if size == 0 {
		size = 50
	}
	if size < 1 || size > 100 {
		return Page{}, ErrInvalidPage
	}
	asset, err := r.readAsset(ctx, query.ScopeQuery)
	if err != nil {
		return Page{}, err
	}
	revisions, err := r.deps.Revisions.List(ctx, asset, query.After, size+1)
	if err != nil {
		return Page{}, err
	}
	if len(revisions) <= size {
		return Page{Items: revisions}, nil
	}
	last := revisions[size-1]
	return Page{Items: revisions[:size], Next: &ports.Position{At: last.CreatedAt, ID: last.ID}}, nil
}

func (r *Registry) readAsset(ctx context.Context, query ports.ScopeQuery) (domain.AssetRef, error) {
	scope, err := r.deps.Scopes.Asset(ctx, query)
	if err != nil {
		return domain.AssetRef{}, err
	}
	// Whoever writes the revisions of the workspace reads them, and so does
	// whoever reviews them.
	reader, err := r.deps.Scopes.Reader(ctx, query.Caller, scope.Asset)
	if err != nil {
		return domain.AssetRef{}, err
	}
	if !reader {
		return domain.AssetRef{}, r.auditRefusal(ctx, query, readAction, ErrForbidden)
	}
	return scope.Asset, nil
}
