package application

import (
	"context"
	"errors"

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
	if _, err := r.requireAuthor(ctx, query.Caller, scope.Asset, false); err != nil {
		return domain.AssetRef{}, r.auditRefusal(ctx, query, readAction, err)
	}
	return scope.Asset, nil
}
