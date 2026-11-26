package ports

import (
	"context"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// Reviews reads the reviews of revisions. A request and a conclusion were
// each written once and are never changed; no review is written any more.
//
// What it reads back it reads through domain.RecordedReview: a review whose
// rows are not ones the model recorded is an error (domain.ErrReviewDamaged)
// and not a review.
type Reviews interface {
	// Find returns the review of the revision with the identifier, and none
	// for a review of another revision.
	Find(ctx context.Context, revision domain.RevisionRef, id string) (domain.Review, bool, error)
	// List returns the reviews of the revision behind the position, in the
	// order they were asked for, at most limit of them.
	List(ctx context.Context, revision domain.RevisionRef, after *Position, limit int) ([]domain.Review, error)
}
