package application

import (
	"context"
	"errors"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

var (
	ErrCheckRunNotFound = errors.New("no such check run of the revision")
	ErrInvalidPage      = errors.New("the check run page is invalid")
)

func (c *Checks) Get(ctx context.Context, target ports.Target, id string) (domain.CheckRun, error) {
	revision, digest, err := c.deps.Revisions.Revision(ctx, target)
	if err != nil {
		return domain.CheckRun{}, err
	}
	run, found, err := c.deps.Checks.Find(ctx, revision, id)
	if err != nil {
		return domain.CheckRun{}, err
	}
	if !found {
		return domain.CheckRun{}, ErrCheckRunNotFound
	}
	if run.InputDigest != digest {
		return domain.CheckRun{}, domain.ErrCheckRunDamaged
	}
	return run, nil
}

type ListChecks struct {
	ports.Target
	PageSize int
	After    *ports.Position
}

type Page struct {
	Items []domain.Overview
	Next  *ports.Position
}

func (c *Checks) List(ctx context.Context, query ListChecks) (Page, error) {
	size := query.PageSize
	if size == 0 {
		size = 50
	}
	if size < 1 || size > 100 {
		return Page{}, ErrInvalidPage
	}
	revision, digest, err := c.deps.Revisions.Revision(ctx, query.Target)
	if err != nil {
		return Page{}, err
	}
	items, err := c.deps.Checks.List(ctx, revision, query.After, size+1)
	if err != nil {
		return Page{}, err
	}
	for _, item := range items {
		if item.InputDigest != digest {
			return Page{}, domain.ErrCheckRunDamaged
		}
	}
	if len(items) <= size {
		return Page{Items: items}, nil
	}
	last := items[size-1]
	return Page{Items: items[:size], Next: &ports.Position{At: last.CompletedAt, ID: last.ID}}, nil
}
