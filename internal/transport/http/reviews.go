package http

import (
	"context"
	"errors"
	"net/http"

	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// ContractReviews is the reads of the reviews of a revision, as the
// transport reaches them. No review is asked for or decided any more.
type ContractReviews interface {
	GetReview(ctx context.Context, target ports.Target, id string) (domain.Review, error)
	ListReviews(context.Context, checks.ListReviews) (checks.ReviewPage, error)
	ListWorkspaceReviews(context.Context, checks.ListWorkspaceReviews) (checks.ReviewPage, error)
}

func (o operations) GetRevisionReview(ctx context.Context, request GetRevisionReviewRequestObject) (GetRevisionReviewResponseObject, error) {
	target, err := checkTarget(ctx, request.ProductID, request.APIAssetID, request.RevisionID)
	if err != nil {
		return nil, err
	}
	review, err := o.policyReview.GetReview(ctx, target, request.ReviewID)
	if err != nil {
		return nil, err
	}
	return GetRevisionReview200JSONResponse{
		Body: showReview(review), Headers: GetRevisionReview200ResponseHeaders{CacheControl: noStore},
	}, nil
}

func (o operations) ListRevisionReviews(ctx context.Context, request ListRevisionReviewsRequestObject) (ListRevisionReviewsResponseObject, error) {
	target, err := checkTarget(ctx, request.ProductID, request.APIAssetID, request.RevisionID)
	if err != nil {
		return nil, err
	}
	list := "reviews/" + target.ProductID + "/" + target.AssetID + "/" + target.RevisionID
	size, position, err := paging(list, request.Params.PageSize, request.Params.PageToken)
	if err != nil {
		return nil, err
	}
	var after *ports.Position
	if position != nil {
		after = &ports.Position{At: position.At, ID: position.ID}
	}
	page, err := o.policyReview.ListReviews(ctx, checks.ListReviews{Target: target, PageSize: size, After: after})
	if err != nil {
		return nil, err
	}
	items := make([]Review, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, showReview(item))
	}
	var next *NextPageToken
	if page.Next != nil {
		next = nextPageToken(list, &governanceports.Position{At: page.Next.At, ID: page.Next.ID})
	}
	return ListRevisionReviews200JSONResponse{
		Body: ReviewList{Items: items, NextPageToken: next}, Headers: ListRevisionReviews200ResponseHeaders{CacheControl: noStore},
	}, nil
}

func showReview(review domain.Review) Review {
	request := review.Request
	body := Review{
		ReviewID: request.ID, ProductID: request.Revision.ProductID, APIAssetID: request.Revision.AssetID,
		RevisionID: request.Revision.RevisionID, CheckRunID: request.CheckRunID, InputDigest: request.InputDigest,
		Status: ReviewStatus(review.Status()), Supplementary: request.Supplementary,
		Scope:       make([]ReviewTarget, 0, len(request.Scope)),
		RequestedBy: Principal{Issuer: request.RequestedBy.Issuer, Subject: request.RequestedBy.Subject},
		RequestedAt: request.RequestedAt,
	}
	for _, target := range request.Scope {
		body.Scope = append(body.Scope, ReviewTarget{Audience: Audience(target.Audience), Environment: Environment(target.Environment)})
	}
	if request.Note != "" {
		body.Note = &request.Note
	}
	if conclusion := review.Conclusion; conclusion != nil {
		shown := ReviewConclusion{
			Outcome:     ReviewConclusionOutcome(conclusion.Outcome),
			ConcludedBy: Principal{Issuer: conclusion.ConcludedBy.Issuer, Subject: conclusion.ConcludedBy.Subject},
			ConcludedAt: conclusion.ConcludedAt,
		}
		if conclusion.Reason != "" {
			shown.Reason = &conclusion.Reason
		}
		if !conclusion.ValidUntil.IsZero() {
			shown.ValidUntil = &conclusion.ValidUntil
		}
		if len(conclusion.Waivers) > 0 {
			waivers := make([]ReviewWaiver, 0, len(conclusion.Waivers))
			for _, waiver := range conclusion.Waivers {
				item := ReviewWaiver{RuleID: waiver.RuleID, Reason: waiver.Reason, ExpiresAt: waiver.ExpiresAt}
				if waiver.CompensatingControl != "" {
					item.CompensatingControl = &waiver.CompensatingControl
				}
				waivers = append(waivers, item)
			}
			shown.Waivers = &waivers
		}
		body.Conclusion = &shown
	}
	return body
}

func reviewProblem(err error) (Problem, bool) {
	switch {
	case errors.Is(err, checks.ErrReviewNotFound):
		return problem(http.StatusNotFound, "REVIEW_NOT_FOUND", ""), true
	case errors.Is(err, checks.ErrInvalidReviewPage):
		return problem(http.StatusBadRequest, CodeRequestInvalid, ""), true
	default:
		return queueProblem(err)
	}
}
