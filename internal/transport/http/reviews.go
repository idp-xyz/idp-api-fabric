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

// ContractReviews is the use cases of the review of a revision, as the
// transport reaches them.
type ContractReviews interface {
	RequestReview(context.Context, ports.Target, checks.RequestReview) (domain.Review, error)
	WithdrawReview(ctx context.Context, target ports.Target, id, reason string) (domain.Review, error)
	DecideReview(ctx context.Context, target ports.Target, id string, decision domain.Decision) (domain.Review, error)
	GetReview(ctx context.Context, target ports.Target, id string) (domain.Review, error)
	ListReviews(context.Context, checks.ListReviews) (checks.ReviewPage, error)
}

func reviewsOf(target ports.Target) string {
	return "/v1/products/" + target.ProductID + "/assets/" + target.AssetID + "/revisions/" + target.RevisionID + "/reviews"
}

func (o operations) RequestRevisionReview(ctx context.Context, request RequestRevisionReviewRequestObject) (RequestRevisionReviewResponseObject, error) {
	target, err := checkTarget(ctx, request.ProductID, request.APIAssetID, request.RevisionID)
	if err != nil {
		return nil, err
	}
	command := checks.RequestReview{CheckRunID: request.Body.CheckRunID}
	for _, item := range request.Body.Scope {
		command.Scope = append(command.Scope, domain.Target{Audience: string(item.Audience), Environment: string(item.Environment)})
	}
	if request.Body.Note != nil {
		command.Note = *request.Body.Note
	}
	review, err := o.policyReview.RequestReview(ctx, target, command)
	if err != nil {
		return nil, err
	}
	return RequestRevisionReview201JSONResponse{Body: showReview(review), Headers: RequestRevisionReview201ResponseHeaders{
		CacheControl: noStore, Location: reviewsOf(target) + "/" + review.Request.ID,
	}}, nil
}

func (o operations) WithdrawRevisionReview(ctx context.Context, request WithdrawRevisionReviewRequestObject) (WithdrawRevisionReviewResponseObject, error) {
	target, err := checkTarget(ctx, request.ProductID, request.APIAssetID, request.RevisionID)
	if err != nil {
		return nil, err
	}
	var reason string
	if request.Body.Reason != nil {
		reason = *request.Body.Reason
	}
	review, err := o.policyReview.WithdrawReview(ctx, target, request.ReviewID, reason)
	if err != nil {
		return nil, err
	}
	return WithdrawRevisionReview200JSONResponse{
		Body: showReview(review), Headers: WithdrawRevisionReview200ResponseHeaders{CacheControl: noStore},
	}, nil
}

func (o operations) DecideRevisionReview(ctx context.Context, request DecideRevisionReviewRequestObject) (DecideRevisionReviewResponseObject, error) {
	target, err := checkTarget(ctx, request.ProductID, request.APIAssetID, request.RevisionID)
	if err != nil {
		return nil, err
	}
	decision := domain.Decision{Outcome: domain.ReviewOutcome(request.Body.Outcome)}
	if request.Body.Reason != nil {
		decision.Reason = *request.Body.Reason
	}
	if request.Body.ValidUntil != nil {
		decision.ValidUntil = *request.Body.ValidUntil
	}
	if request.Body.Waivers != nil {
		for _, item := range *request.Body.Waivers {
			waiver := domain.Waiver{RuleID: item.RuleID, Reason: item.Reason, ExpiresAt: item.ExpiresAt}
			if item.CompensatingControl != nil {
				waiver.CompensatingControl = *item.CompensatingControl
			}
			decision.Waivers = append(decision.Waivers, waiver)
		}
	}
	review, err := o.policyReview.DecideReview(ctx, target, request.ReviewID, decision)
	if err != nil {
		return nil, err
	}
	return DecideRevisionReview200JSONResponse{
		Body: showReview(review), Headers: DecideRevisionReview200ResponseHeaders{CacheControl: noStore},
	}, nil
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
		Status: ReviewStatus(review.Status()), Scope: make([]ReviewTarget, 0, len(request.Scope)),
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
	case errors.Is(err, domain.ErrNotRequester):
		return problem(http.StatusForbidden, CodeForbidden, "A review is withdrawn by whoever asked for it."), true
	case errors.Is(err, domain.ErrNotIndependent):
		return problem(http.StatusForbidden, "REVIEWER_NOT_INDEPENDENT",
			"Whoever recorded the revision, ran its check or asked for the review does not decide on it."), true
	case errors.Is(err, domain.ErrWaiverForbidden):
		return problem(http.StatusUnprocessableEntity, "WAIVER_FORBIDDEN", "A rule that nothing waives cannot be waived."), true
	case errors.Is(err, domain.ErrWaiverMissing):
		return problem(http.StatusConflict, "WAIVER_REQUIRED",
			"Rules failed or could not be judged in the check of the review. An approval waives each of them by name."), true
	case errors.Is(err, domain.ErrCheckStale):
		return problem(http.StatusConflict, "POLICY_STALE",
			"The check was not made under the policy and by the tool versions a check is made with now. Run the check again."), true
	case errors.Is(err, domain.ErrReviewBlocked):
		return problem(http.StatusConflict, "REVIEW_BLOCKED",
			"In the check, rules that nothing waives failed or could not be judged. No decision approves such a revision."), true
	case errors.Is(err, domain.ErrReviewConcluded):
		return problem(http.StatusConflict, "REVIEW_CONCLUDED", "The review was concluded already."), true
	case errors.Is(err, domain.ErrInvalidReview):
		return problem(http.StatusUnprocessableEntity, CodeRequestUnprocessable, ""), true
	default:
		return Problem{}, false
	}
}
