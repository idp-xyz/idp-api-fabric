package application

import (
	"context"
	"errors"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

var (
	ErrReviewNotFound      = errors.New("no such review of the revision")
	ErrInvalidReviewPage   = errors.New("the review page is invalid")
	ErrReviewNotAudited    = errors.New("a review refusal could not be audited")
	errReviewWithoutStatus = errors.New("the registry moved a revision to no version of its status")
)

// ReviewDependencies is what the use cases of a review reach.
type ReviewDependencies struct {
	Transactions ports.Transactions
	Revisions    ports.Revisions
	Checks       ports.CheckRuns
	Reviews      ports.Reviews
	// Evaluator and Comparator say which tools a check is made with now: a
	// review rests on a check made with them.
	Evaluator   ports.Evaluator
	Comparator  ports.Comparator
	Audit       func(context.Context, ports.AuditEntry) error
	Events      ports.ReviewEvents
	Clock       ports.Clock
	Identifiers ports.Identifiers
}

// Reviews are the use cases of the review of a revision: asking for a
// decision, taking the request back, and reading what was asked and how it
// ended.
type Reviews struct {
	deps   ReviewDependencies
	policy domain.Policy
}

func NewReviews(deps ReviewDependencies) (*Reviews, error) {
	if deps.Transactions == nil || deps.Revisions == nil || deps.Checks == nil || deps.Reviews == nil ||
		deps.Evaluator == nil || deps.Comparator == nil || deps.Audit == nil || deps.Events == nil ||
		deps.Clock == nil || deps.Identifiers == nil {
		return nil, errors.New("the reviews of Policy & Review need transactions, revisions, check storage, review storage, the tools of a check, audit, events, clock and identifiers")
	}
	return &Reviews{deps: deps, policy: domain.Baseline()}, nil
}

// The rules of the access matrix a review is recorded under (F05 §6).
const (
	ruleRequestReview  = "AC-50"
	ruleWithdrawReview = "AC-51"
	ruleRefused        = "DENY-03"

	actionRequestReview  = "review.request"
	actionWithdrawReview = "review.withdraw"
)

// tools are the tools a check is made with now.
func (r *Reviews) tools() []domain.Tool {
	return []domain.Tool{r.deps.Evaluator.Tool(), r.deps.Comparator.Tool()}
}

// RequestReview is what an author asks a review with.
type RequestReview struct {
	CheckRunID string
	Scope      []domain.Target
	Note       string
}

// RequestReview asks for a decision on a revision, on the evidence of one of
// its checks and for the scope it names. A current author of the workspace
// asks, in a product that takes new work, for a revision that is a draft or
// was withdrawn; the revision is in review from then on.
//
// The request, the move of the revision, the audit record and the event are
// written together or not at all.
func (r *Reviews) RequestReview(ctx context.Context, target ports.Target, command RequestReview) (domain.Review, error) {
	revision, digest, err := r.deps.Revisions.Revision(ctx, target)
	if err != nil {
		return domain.Review{}, err
	}
	run, found, err := r.deps.Checks.Find(ctx, revision, command.CheckRunID)
	if err != nil {
		return domain.Review{}, err
	}
	if !found {
		return domain.Review{}, ErrCheckRunNotFound
	}
	if run.InputDigest != digest {
		return domain.Review{}, fmt.Errorf("check run %s: %w", run.ID, domain.ErrCheckRunDamaged)
	}

	var review domain.Review
	err = r.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		author, active, err := r.deps.Revisions.LockAuthor(tx, target.Caller, revision)
		if err != nil {
			return err
		}
		if !author {
			return ErrForbidden
		}
		if !active {
			return ErrProductSuspended
		}
		request, err := domain.RequestReview(r.deps.Identifiers.NewID(), run, r.policy, r.tools(),
			command.Scope, command.Note, target.Caller, r.deps.Clock.Now())
		if err != nil {
			return err
		}
		version, err := r.deps.Revisions.Move(tx, revision, domain.RevisionInReview)
		if err != nil {
			return err
		}
		if version < 2 {
			return errReviewWithoutStatus
		}
		if err := r.deps.Reviews.Request(tx, request, version); err != nil {
			return err
		}
		if err := r.deps.Audit(tx, ports.AuditEntry{
			Target: target, ReviewID: request.ID, Action: actionRequestReview, Rule: ruleRequestReview, Allowed: true,
		}); err != nil {
			return err
		}
		if err := r.deps.Events.Requested(tx, request, target.CorrelationID); err != nil {
			return err
		}
		review = domain.Review{Request: request}
		return nil
	})
	if err != nil {
		return domain.Review{}, r.refused(ctx, target, actionRequestReview, err)
	}
	return review, nil
}

// WithdrawReview takes a request back. Whoever asked for the review does,
// while still an author of the workspace and while the review is open; the
// revision is withdrawn from then on and can be submitted again.
func (r *Reviews) WithdrawReview(ctx context.Context, target ports.Target, id, reason string) (domain.Review, error) {
	revision, _, err := r.deps.Revisions.Revision(ctx, target)
	if err != nil {
		return domain.Review{}, err
	}
	asked, found, err := r.deps.Reviews.Find(ctx, revision, id)
	if err != nil {
		return domain.Review{}, err
	}
	if !found {
		return domain.Review{}, ErrReviewNotFound
	}

	var review domain.Review
	err = r.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		author, _, err := r.deps.Revisions.LockAuthor(tx, target.Caller, revision)
		if err != nil {
			return err
		}
		if !author {
			return ErrForbidden
		}
		// Who asks and whether the review is open is said before anything
		// moves, so the caller is told that and not where the revision is.
		if _, err := domain.Withdraw(asked, target.Caller, reason, r.deps.Clock.Now()); err != nil {
			return err
		}
		// The move locks the revision, so the review is read behind it as
		// it stands: of two conclusions that arrive together the second
		// finds the first.
		if _, err := r.deps.Revisions.Move(tx, revision, domain.RevisionWithdrawn); err != nil {
			return err
		}
		open, found, err := r.deps.Reviews.Find(tx, revision, id)
		if err != nil {
			return err
		}
		if !found {
			return ErrReviewNotFound
		}
		conclusion, err := domain.Withdraw(open, target.Caller, reason, r.deps.Clock.Now())
		if err != nil {
			return err
		}
		if err := r.deps.Reviews.Conclude(tx, conclusion); err != nil {
			return err
		}
		review = domain.Review{Request: open.Request, Conclusion: &conclusion}
		if err := r.deps.Audit(tx, ports.AuditEntry{
			Target: target, ReviewID: id, Action: actionWithdrawReview, Rule: ruleWithdrawReview, Allowed: true,
		}); err != nil {
			return err
		}
		return r.deps.Events.Concluded(tx, review, target.CorrelationID)
	})
	if err != nil {
		return domain.Review{}, r.refused(ctx, target, actionWithdrawReview, err)
	}
	return review, nil
}

// refused puts a refusal for who the caller is on record, outside the
// transaction that was rolled back, and hands the cause on.
func (r *Reviews) refused(ctx context.Context, target ports.Target, action string, cause error) error {
	if !errors.Is(cause, ErrForbidden) && !errors.Is(cause, domain.ErrNotRequester) {
		return cause
	}
	if err := r.deps.Audit(ctx, ports.AuditEntry{Target: target, Action: action, Rule: ruleRefused}); err != nil {
		return fmt.Errorf("%w: %w: %v", cause, ErrReviewNotAudited, err)
	}
	return cause
}

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
