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

// listOfWorkspaceReviews is the list a page token of the reviews of one
// workspace is of. The token names a position in the queue of that workspace
// of that product and in no other: the one of another workspace, of another
// product, or of another list, is none. It names no filter: the position is
// in the order of the whole queue, whatever the page is kept to.
func listOfWorkspaceReviews(productID, workspaceID string) string {
	return "workspace-reviews/" + productID + "/" + workspaceID
}

func (o operations) ListWorkspaceReviews(ctx context.Context, request ListWorkspaceReviewsRequestObject) (ListWorkspaceReviewsResponseObject, error) {
	principal, correlationID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	list := listOfWorkspaceReviews(request.ProductID, request.WorkspaceID)
	params := request.Params
	size, position, err := paging(list, params.PageSize, params.PageToken)
	if err != nil {
		return nil, err
	}
	// The validator has filled in the size the contract declares as the
	// default. A request that reaches here without one is given it all the
	// same: the use case does not choose.
	if size == 0 {
		size = checks.DefaultQueuePage
	}
	var after *ports.Position
	if position != nil {
		after = &ports.Position{At: position.At, ID: position.ID}
	}
	page, err := o.policyReview.ListWorkspaceReviews(ctx, checks.ListWorkspaceReviews{
		Caller: domain.Principal(principal), CorrelationID: correlationID,
		ProductID: request.ProductID, WorkspaceID: request.WorkspaceID,
		Status:            string(valueOf(params.Status)),
		RequestedByIssuer: valueOf(params.RequestedByIssuer), RequestedBySubject: valueOf(params.RequestedBySubject),
		PageSize: size, After: after,
	})
	if err != nil {
		return nil, err
	}
	// Each review is shown as the read of one shows it.
	items := make([]Review, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, showReview(item))
	}
	var next *NextPageToken
	if page.Next != nil {
		next = nextPageToken(list, &governanceports.Position{At: page.Next.At, ID: page.Next.ID})
	}
	return ListWorkspaceReviews200JSONResponse{
		Body: ReviewList{Items: items, NextPageToken: next}, Headers: ListWorkspaceReviews200ResponseHeaders{CacheControl: noStore},
	}, nil
}

// queueProblem is the problem a failed reading of the reviews of a workspace
// is answered with, and false for a failure it does not name. A page that
// cannot be asked for is answered as one of the reviews of a revision is
// (reviewProblem).
func queueProblem(err error) (Problem, bool) {
	switch {
	case errors.Is(err, checks.ErrProductNotFound):
		return problem(http.StatusNotFound, CodeProductNotFound, ""), true
	case errors.Is(err, checks.ErrWorkspaceNotFound):
		return problem(http.StatusNotFound, CodeWorkspaceNotFound, ""), true
	case errors.Is(err, checks.ErrReviewsForbidden):
		return problem(http.StatusForbidden, CodeForbidden, "The caller holds no role in the workspace that allows this."), true
	default:
		return Problem{}, false
	}
}
