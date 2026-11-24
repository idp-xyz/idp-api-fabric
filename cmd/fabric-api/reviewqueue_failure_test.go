package main

import (
	"context"
	"errors"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	checks "go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	checkdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	checkports "go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
	governancestatic "go.idp.xyz/idp-api-fabric/internal/productgovernance/adapters/static"
	governance "go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	governancedomain "go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// The stores Product Governance is wired with in the test below, which asks
// the products and nothing else: any other that is asked fails the test with
// the panic of a store that is not there.
type (
	failureTransactor struct{ governanceports.Transactor }
	failureWorkspaces struct{ governanceports.Workspaces }
	failureRoles      struct {
		governanceports.RoleAssignments
	}
	failureMemberships     struct{ governanceports.Memberships }
	failureAudienceConfigs struct {
		governanceports.AudienceConfigs
	}
	failureAudit  struct{ governanceports.AuditTrail }
	failureEvents struct{ governanceports.Events }
)

// unreadableProducts are the products of a database that does not answer.
type unreadableProducts struct {
	governanceports.Products
	failure error
}

func (p unreadableProducts) Find(context.Context, governancedomain.ProductID) (governancedomain.Product, bool, error) {
	return governancedomain.Product{}, false, p.failure
}

// What Product Governance fails to read of what a caller is to a workspace is a
// failure of the queue, and is not told as a refusal or as a product that is
// not there: a database that does not answer must not make a product vanish
// for whoever asks, nor turn a failure into a 404 that nobody looks into. The
// scope hands the failure on as it is, the queue answers with it, and a failure
// is no refusal, so nothing is recorded for it.
func TestAFailureOfProductGovernanceToReadWhatACallerIsToAWorkspaceIsAFailureAndNotAProductThatIsNotThere(t *testing.T) {
	failure := errors.New("the products cannot be read")
	administrators, err := governancestatic.NewAdministrators(nil)
	if err != nil {
		t.Fatal(err)
	}
	governed, err := governance.New(governance.Dependencies{
		Transactor: failureTransactor{}, Products: unreadableProducts{failure: failure}, Workspaces: failureWorkspaces{},
		Roles: failureRoles{}, Memberships: failureMemberships{}, AudienceConfigs: failureAudienceConfigs{},
		Administrators: administrators, Issuers: governancestatic.NewIssuers("https://issuer.example"),
		Audit: failureAudit{}, Events: failureEvents{}, Clock: systemClock{}, Identifiers: ids.UUIDs{},
	})
	if err != nil {
		t.Fatal(err)
	}
	scopes := reviewScopes{governance: governed}
	caller := checkdomain.Principal{Issuer: "https://issuer.example", Subject: "reader"}
	const product, workspace = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6", "4f2d7b0e-85a3-4c1b-a9d4-c3f6a5b7d8e9"

	scope, err := scopes.ReviewScope(t.Context(), caller, product, workspace)
	if !errors.Is(err, failure) {
		t.Errorf("the scope of a caller whose products cannot be read: %v, want the failure %v", err, failure)
	}
	if scope != (checkports.WorkspaceScope{}) {
		t.Errorf("the scope of a caller whose products cannot be read is %+v, want no facts", scope)
	}
	for name, refusal := range map[string]error{
		"a product that is not there":     governance.ErrProductNotFound,
		"a caller that may not be told":   governance.ErrForbidden,
		"a caller that may not read them": checks.ErrReviewsForbidden,
	} {
		if errors.Is(err, refusal) {
			t.Errorf("a failure to read the products is told as %s: %v", name, err)
		}
	}

	// Through the use case it is a failure all the same, and nothing is
	// recorded of it.
	recorded := 0
	queue, err := checks.NewReviewQueue(checks.QueueDependencies{
		Reviews: nobodyReads{}, Scopes: scopes,
		Audit: func(context.Context, checkports.AuditEntry) error { recorded++; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := queue.ListWorkspaceReviews(t.Context(), checks.ListWorkspaceReviews{
		Caller: caller, ProductID: product, WorkspaceID: workspace, PageSize: 10,
	})
	if !errors.Is(err, failure) || len(page.Items) != 0 {
		t.Errorf("the queue of a workspace whose products cannot be read: %d reviews and %v, want none and the failure %v", len(page.Items), err, failure)
	}
	for name, refusal := range map[string]error{
		"no such product":                checks.ErrProductNotFound,
		"no such workspace":              checks.ErrWorkspaceNotFound,
		"a caller that may not read":     checks.ErrReviewsForbidden,
		"a request that cannot be asked": checks.ErrInvalidReviewPage,
	} {
		if errors.Is(err, refusal) {
			t.Errorf("a failure to read the products is told by the queue as %s: %v", name, err)
		}
	}
	if recorded != 0 {
		t.Errorf("a failure to read the products left %d audit records, want none: a failure is no refusal", recorded)
	}
}
