package ports

import (
	"context"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// Reviews keeps the reviews of revisions. A request and a conclusion are
// each written once and never changed.
//
// What it reads back it reads through domain.RecordedReview: a review whose
// rows are not ones the model recorded is an error (domain.ErrReviewDamaged)
// and not a review.
type Reviews interface {
	// Request stores a request. statusVersion is the version the status of
	// the revision took when the request put it in review: a revision is in
	// review under one request at a time, and storage holds it to that. For
	// a supplementary request, which moves nothing, it is the version the
	// status of the approved revision has, and storage takes any number of
	// requests at it.
	Request(ctx context.Context, request domain.ReviewRequest, statusVersion int64) error
	// Open reports whether a review of the revision has no conclusion yet.
	Open(ctx context.Context, revision domain.RevisionRef) (bool, error)
	// Conclude stores the conclusion of a review. A review has one.
	Conclude(context.Context, domain.Conclusion) error
	// Find returns the review of the revision with the identifier, and none
	// for a review of another revision.
	Find(ctx context.Context, revision domain.RevisionRef, id string) (domain.Review, bool, error)
	// List returns the reviews of the revision behind the position, in the
	// order they were asked for, at most limit of them.
	List(ctx context.Context, revision domain.RevisionRef, after *Position, limit int) ([]domain.Review, error)
}

// ReviewEvents announces what happened to a review, in the transaction that
// records it.
type ReviewEvents interface {
	Requested(ctx context.Context, request domain.ReviewRequest, correlationID string) error
	Concluded(ctx context.Context, review domain.Review, correlationID string) error
}
