package main

import (
	"context"
	"errors"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	checkdb "go.idp.xyz/idp-api-fabric/internal/policyreview/adapters/postgres"
	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	checkdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	checkports "go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
	governance "go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	governancedomain "go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// reviewScopes is what Product Governance says of a caller to a workspace, for
// the reading of its reviews. Policy & Review owns the reviews and decides
// from these facts; the two contexts do not know each other, and this is where
// the port of one meets the use case of the other (F03 §3.2).
type reviewScopes struct{ governance *governance.ProductGovernance }

func (s reviewScopes) ReviewScope(ctx context.Context, caller checkdomain.Principal, productID, workspaceID string) (checkports.WorkspaceScope, error) {
	scope, err := s.governance.ReadReviewScope(ctx, governancedomain.Principal(caller),
		governancedomain.ProductID(productID), governancedomain.WorkspaceID(workspaceID))
	switch {
	case errors.Is(err, governance.ErrProductNotFound):
		// An identifier that names no product is no product to know.
		return checkports.WorkspaceScope{}, nil
	case errors.Is(err, governance.ErrForbidden):
		// A request that names nobody is refused, and not failed: there is
		// nobody to read for, or to name in the record of a refusal.
		return checkports.WorkspaceScope{}, errors.Join(err, checks.ErrReviewsForbidden)
	case err != nil:
		return checkports.WorkspaceScope{}, err
	}
	return checkports.WorkspaceScope{
		Found: scope.Found, Known: scope.Known, Sees: scope.Sees, WorkspaceFound: scope.WorkspaceFound, Reads: scope.Reads,
	}, nil
}

// newReviewQueue is the use case of reading the reviews of a workspace. It
// reads the reviews that Policy & Review stored, as the use cases of a review
// read them, for whoever Product Governance says reads them, and records what
// it refuses as the use cases of a review do.
func newReviewQueue(database *db.Database, scopes *governance.ProductGovernance, audit auditTrail) (*checks.ReviewQueue, error) {
	return checks.NewReviewQueue(checks.QueueDependencies{
		Reviews: checkdb.NewReviews(database.Framework),
		Scopes:  reviewScopes{governance: scopes},
		Audit:   audit.recordCheck,
	})
}
