package application

import (
	"context"
	"slices"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// ReviewScope is what Product Governance knows of a caller to one workspace,
// for the reading of the reviews in it. Policy & Review owns the reviews and
// decides from these facts; knowing that a product exists, or that a workspace
// does, conveys no right to read what is reviewed there.
//
// The facts are read whoever asks, so that what is withheld costs what is
// missing costs. Whoever reads them tells a caller of a workspace no more
// than the caller may know of it.
type ReviewScope struct {
	// Found is whether there is such a product.
	Found bool
	// Known is whether the caller may know that there is: it holds something
	// in the product, or it administers the platform.
	Known bool
	// Sees is whether the caller may know the workspace: it owns the product,
	// or it is a member of the workspace in whichever role. That is who sees
	// an asset of the workspace. An administrator of the platform knows the
	// product and sees nothing inside it.
	Sees bool
	// WorkspaceFound is whether the product has such a workspace. A workspace
	// of another product is not one of this.
	WorkspaceFound bool
	// Reads is whether the caller currently has a governance read grant in
	// the workspace or owns the product.
	Reads bool
}

// ReadReviewScope reads what the caller is to the workspace of the product,
// for the reading of its reviews. It is the question RevisionReader answers
// of one workspace, with what the caller is told when it may not read: that
// the product is not there, that the workspace is not, or that it may not.
//
// A workspace that is none is one that is not there, and is looked up no more
// than an empty identifier is. A product that is not there is not there for
// anyone, an administrator included: no facts, and no failure, which the
// caller reads as the product not being known. A read takes no lock: the
// grants are read again with every request, as RevisionReader reads them.
func (s *ProductGovernance) ReadReviewScope(ctx context.Context, caller domain.Principal,
	product domain.ProductID, workspace domain.WorkspaceID) (ReviewScope, error) {
	if err := known(caller); err != nil {
		return ReviewScope{}, err
	}
	productID, err := domain.ParseProductID(string(product))
	if err != nil {
		return ReviewScope{}, ErrProductNotFound
	}
	workspaceID, _ := domain.ParseWorkspaceID(string(workspace))
	seen, err := s.survey(ctx, caller, productID, workspaceID, "")
	if err != nil || !seen.productFound {
		return ReviewScope{}, err
	}
	return ReviewScope{
		Found:          true,
		Known:          s.knows(seen.standing, caller),
		Sees:           seen.standing.Owns() || seen.standing.MemberOf(workspaceID),
		WorkspaceFound: seen.workspaceFound,
		Reads: seen.workspaceFound && (seen.standing.Owns() ||
			slices.ContainsFunc(seen.standing.Memberships, func(membership domain.Membership) bool {
				return membership.WorkspaceID == workspaceID && slices.Contains(revisionReaders, membership.Role)
			})),
	}, nil
}
