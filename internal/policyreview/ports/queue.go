package ports

import (
	"context"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// ReviewFilter keeps the reviews of a workspace to some of them. Every member
// narrows the list and none widens it: a review is let through when it is
// what each member that is set says. A member that is not set, the empty
// string, lets every review through.
type ReviewFilter struct {
	// Status is where a review stands: domain.ReviewOpen until it is
	// concluded, then the way it ended.
	Status string
	// RequestedByIssuer and RequestedBySubject are the parts of the
	// principal that asked for the review. Either may be asked for alone:
	// the subject then matches under any issuer.
	RequestedByIssuer  string
	RequestedBySubject string
}

// ReviewQueue reads the reviews of one workspace, of every revision of every
// asset it owns, in the order they were asked for.
//
// What it reads back it reads as Reviews does, through domain.RecordedReview:
// a review whose rows are not ones the model recorded is an error
// (domain.ErrReviewDamaged) and not a review.
type ReviewQueue interface {
	// ListInWorkspace returns the reviews of the workspace of the product
	// that the filter lets through, behind the position, in the order they
	// were asked for, at most limit of them. The product and the workspace
	// narrow the list before the position and the limit do: a review of
	// another workspace, or of another product that has a workspace of the
	// same identifier, is in no page.
	ListInWorkspace(ctx context.Context, productID, workspaceID string, filter ReviewFilter, after *Position, limit int) ([]domain.Review, error)
}

// WorkspaceScope is what Product Governance says of a caller to one
// workspace, for the reading of the reviews in it. The reviews are Policy &
// Review's and it decides from these facts; knowing that a product exists
// conveys no right to read what is reviewed in it.
type WorkspaceScope struct {
	// Found is whether there is such a product.
	Found bool
	// Known is whether the caller may know that there is: it holds something
	// in the product, or it administers the platform.
	Known bool
	// Sees is whether the caller may know the workspace: it owns the product,
	// or it is a member of the workspace in whichever role. A platform
	// administrator knows the product and sees nothing inside it.
	Sees bool
	// WorkspaceFound is whether the product has such a workspace.
	WorkspaceFound bool
	// Reads is whether the caller holds the grant of an author, a reviewer or
	// a publisher in the workspace now, the grants that read what is reviewed
	// in it. A maintainer, and an owner that holds none of them, do not.
	Reads bool
}

// WorkspaceScopes says what a caller is to a workspace, as the grants it
// holds in the product say now. The grants are read again with every request,
// and no lock keeps them: a read is no point where a revocation has to be
// ordered with what is read.
type WorkspaceScopes interface {
	ReviewScope(ctx context.Context, caller domain.Principal, productID, workspaceID string) (WorkspaceScope, error)
}
