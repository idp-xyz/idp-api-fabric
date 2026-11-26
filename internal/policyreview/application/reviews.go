package application

import (
	"context"
	"errors"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

var (
	ErrReviewNotFound    = errors.New("no such review of the revision")
	ErrInvalidReviewPage = errors.New("the review page is invalid")
	ErrReviewNotAudited  = errors.New("a review refusal could not be audited")
)

// ReviewDependencies is what the reads of a review reach.
type ReviewDependencies struct {
	Revisions ports.Revisions
	Checks    ports.CheckRuns
	Reviews   ports.Reviews
}

// Reviews are the reads of the reviews of a revision: what was asked and
// how it ended. No review is asked for, withdrawn or decided any more
// (CR-0001); those that were recorded are read as they were, and so is the
// evidence of a release that was prepared under one.
type Reviews struct {
	deps ReviewDependencies
}

func NewReviews(deps ReviewDependencies) (*Reviews, error) {
	if deps.Revisions == nil || deps.Checks == nil || deps.Reviews == nil {
		return nil, errors.New("the reviews of Policy & Review need revisions, check storage and review storage")
	}
	return &Reviews{deps: deps}, nil
}

// DENY-03 refuses who holds no grant that allows what was asked.
const ruleRefused = "DENY-03"

// GetReview reads one review of the revision, for whoever may read the
// revision.
func (r *Reviews) GetReview(ctx context.Context, target ports.Target, id string) (domain.Review, error) {
	revision, digest, err := r.deps.Revisions.Revision(ctx, target)
	if err != nil {
		return domain.Review{}, err
	}
	review, found, err := r.deps.Reviews.Find(ctx, revision, id)
	if err != nil {
		return domain.Review{}, err
	}
	if !found {
		return domain.Review{}, ErrReviewNotFound
	}
	if review.Request.InputDigest != digest {
		return domain.Review{}, fmt.Errorf("review %s: %w", review.Request.ID, domain.ErrReviewDamaged)
	}
	return review, nil
}

type ListReviews struct {
	ports.Target
	PageSize int
	After    *ports.Position
}

type ReviewPage struct {
	Items []domain.Review
	Next  *ports.Position
}

// ListReviews reads the reviews of the revision in the order they were asked
// for, for whoever may read the revision.
func (r *Reviews) ListReviews(ctx context.Context, query ListReviews) (ReviewPage, error) {
	size := query.PageSize
	if size == 0 {
		size = 50
	}
	if size < 1 || size > 100 {
		return ReviewPage{}, ErrInvalidReviewPage
	}
	revision, digest, err := r.deps.Revisions.Revision(ctx, query.Target)
	if err != nil {
		return ReviewPage{}, err
	}
	items, err := r.deps.Reviews.List(ctx, revision, query.After, size+1)
	if err != nil {
		return ReviewPage{}, err
	}
	for _, item := range items {
		if item.Request.InputDigest != digest {
			return ReviewPage{}, fmt.Errorf("review %s: %w", item.Request.ID, domain.ErrReviewDamaged)
		}
	}
	if len(items) <= size {
		return ReviewPage{Items: items}, nil
	}
	last := items[size-1].Request
	return ReviewPage{Items: items[:size], Next: &ports.Position{At: last.RequestedAt, ID: last.ID}}, nil
}
