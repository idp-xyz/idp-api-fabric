package application

import (
	"context"
	"errors"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

var (
	// ErrProductNotFound reports that there is no such product, or that the
	// caller may not know whether there is. The two are answered alike, so
	// that an identifier cannot be probed (F05 §5.2).
	ErrProductNotFound = errors.New("no such product")
	// ErrWorkspaceNotFound reports that the product has no such workspace, or
	// that the caller may not know whether it has: it is neither an owner of
	// the product nor a member of the workspace. The two are answered alike.
	ErrWorkspaceNotFound = errors.New("no such workspace in the product")
	// ErrReviewsForbidden reports a caller that may know the workspace and
	// holds none of the grants that read the reviews in it.
	ErrReviewsForbidden = errors.New("the caller may not read the reviews of the workspace")
)

const (
	// actionReadReviews is what the access matrix of F05 calls the reading of
	// reviews (AC-52). A refusal to read the reviews of a workspace is
	// recorded under it, against the product, for the kind of resource a
	// review is and for no resource of its own: a queue names none.
	actionReadReviews = "review.read"

	// DefaultQueuePage is how many reviews a page of the queue holds when the
	// caller names no size, and MaxQueuePage the most it gets. The contract of
	// the API states the same bounds, and a test holds the two together.
	DefaultQueuePage = 50
	MaxQueuePage     = 100
)

// QueueDependencies are what the queue of the reviews of a workspace reaches.
// All are required.
type QueueDependencies struct {
	Reviews ports.ReviewQueue
	Scopes  ports.WorkspaceScopes
	Audit   func(context.Context, ports.AuditEntry) error
}

// ReviewQueue is the use case of reading the reviews of a workspace, in the
// order they were asked for: the record of what was asked there and how it
// ended. No review is asked for any more, so none waits for a decision.
type ReviewQueue struct {
	deps QueueDependencies
}

func NewReviewQueue(deps QueueDependencies) (*ReviewQueue, error) {
	if deps.Reviews == nil || deps.Scopes == nil || deps.Audit == nil {
		return nil, errors.New("the queue of reviews needs review storage, the scope of the callers and audit")
	}
	return &ReviewQueue{deps: deps}, nil
}

// ListWorkspaceReviews asks for a page of the reviews of one workspace.
type ListWorkspaceReviews struct {
	Caller        domain.Principal
	CorrelationID string
	ProductID     string
	WorkspaceID   string
	// Status keeps the queue to the reviews that stand there: IN_REVIEW, or
	// the way they ended. RequestedByIssuer and RequestedBySubject keep it to
	// the reviews that a principal asked for. A member that is empty keeps
	// nothing out.
	Status             string
	RequestedByIssuer  string
	RequestedBySubject string
	// PageSize is how many reviews the page holds at most: 1 to MaxQueuePage.
	// A caller that names none is given DefaultQueuePage before it comes here.
	PageSize int
	// After is the position the page starts behind: Next of the page before.
	// Nil starts the queue.
	After *ports.Position
}

// ListWorkspaceReviews returns a page of the reviews of a workspace to whoever
// reads the reviews of it: a current author, reviewer or publisher of the
// workspace, who reads every review of it, as it reads each of them alone
// (GetReview). They are the reviews of every revision of every asset of the
// workspace, oldest first, by the instant they were asked for and then by
// their identifier, so that a review keeps its place for as long as it is
// listed. Each is the review GetReview returns, but for one check: that the
// review rests on the content the revision has now, which needs the revision
// and is made when one review is read.
//
// Whoever reads none is answered as far as it may know. To a caller that may
// not know the product there is no such product; to one that knows it and may
// not know the workspace, an owner or a member of it, there is no such
// workspace. An owner or a member that holds none of the three grants is told
// that it may not read; and an owner that asks for a workspace the product
// has not is told that there is none. Having none to read is a refusal and
// not an empty list, which would say that the workspace has no review. The
// refusal is recorded, except where there is nothing to record it under: a
// product that is not there. A read that is answered leaves no record.
//
// What the caller holds is read with every page, and no lock keeps it: a
// page token grants nothing, and a grant that was revoked reads no page after
// it. What the request says of the reviews is looked at once the caller is
// known to read them: whoever is refused is told nothing else.
func (q *ReviewQueue) ListWorkspaceReviews(ctx context.Context, query ListWorkspaceReviews) (ReviewPage, error) {
	if query.Caller.Issuer == "" || query.Caller.Subject == "" {
		return ReviewPage{}, fmt.Errorf("%w: the request carries no principal", ErrReviewsForbidden)
	}
	if query.PageSize < 1 || query.PageSize > MaxQueuePage {
		return ReviewPage{}, fmt.Errorf("%w: a page holds 1 to %d reviews", ErrInvalidReviewPage, MaxQueuePage)
	}
	if !domain.ValidIdentifier(query.ProductID) {
		return ReviewPage{}, ErrProductNotFound
	}

	scope, err := q.deps.Scopes.ReviewScope(ctx, query.Caller, query.ProductID, query.WorkspaceID)
	if err != nil {
		return ReviewPage{}, fmt.Errorf("read what the caller is to the workspace: %w", err)
	}
	var refusal error
	switch {
	case !scope.Found:
		return ReviewPage{}, ErrProductNotFound
	case !scope.Known:
		refusal = ErrProductNotFound
	case !scope.Sees:
		refusal = ErrWorkspaceNotFound
	case !scope.WorkspaceFound:
		return ReviewPage{}, ErrWorkspaceNotFound
	case !scope.Reads:
		refusal = ErrReviewsForbidden
	}
	if refusal != nil {
		return ReviewPage{}, q.refuse(ctx, query, refusal)
	}

	filter := ports.ReviewFilter{
		Status: query.Status, RequestedByIssuer: query.RequestedByIssuer, RequestedBySubject: query.RequestedBySubject,
	}
	if err := checkQueue(filter, query.After); err != nil {
		return ReviewPage{}, err
	}
	// The store is asked for one review more than the page holds: whether it
	// is there says whether a page follows, without counting what is behind.
	items, err := q.deps.Reviews.ListInWorkspace(ctx, query.ProductID, query.WorkspaceID, filter, query.After, query.PageSize+1)
	if err != nil {
		return ReviewPage{}, err
	}
	if len(items) <= query.PageSize {
		return ReviewPage{Items: items}, nil
	}
	last := items[query.PageSize-1].Request
	return ReviewPage{Items: items[:query.PageSize], Next: &ports.Position{At: last.RequestedAt, ID: last.ID}}, nil
}

// checkQueue holds what a queue is asked for to what the queue can answer: a
// status a review has, principals that are references, and a position that is
// one a page of the queue ended at, an instant and the identifier of a review.
func checkQueue(filter ports.ReviewFilter, after *ports.Position) error {
	switch {
	case filter.Status != "" && !domain.ValidReviewStatus(filter.Status):
		return fmt.Errorf("%w: a review is IN_REVIEW, WITHDRAWN, APPROVED or REJECTED", ErrInvalidReviewPage)
	case filter.RequestedByIssuer != "" && !domain.ValidReference(filter.RequestedByIssuer):
		return fmt.Errorf("%w: the issuer of a principal is a reference of 1 to %d characters", ErrInvalidReviewPage, domain.MaxReference)
	case filter.RequestedBySubject != "" && !domain.ValidReference(filter.RequestedBySubject):
		return fmt.Errorf("%w: the subject of a principal is a reference of 1 to %d characters", ErrInvalidReviewPage, domain.MaxReference)
	case after != nil && (after.At.IsZero() || !domain.ValidIdentifier(after.ID)):
		return fmt.Errorf("%w: a page starts behind a position of the queue", ErrInvalidReviewPage)
	}
	return nil
}

// refuse puts a refusal for what the caller is on record, outside any
// transaction: a refusal changes nothing, so its record commits on its own.
// The caller is refused whether or not the record could be written. A record
// that could not is reported with the refusal, as ErrReviewNotAudited, for
// whoever operates the service: it never changes what the caller is told.
func (q *ReviewQueue) refuse(ctx context.Context, query ListWorkspaceReviews, cause error) error {
	if err := q.deps.Audit(ctx, ports.AuditEntry{
		Target: ports.Target{Caller: query.Caller, ProductID: query.ProductID, CorrelationID: query.CorrelationID},
		Action: actionReadReviews, Rule: ruleRefused,
	}); err != nil {
		return fmt.Errorf("%w: %w: %v", cause, ErrReviewNotAudited, err)
	}
	return cause
}
