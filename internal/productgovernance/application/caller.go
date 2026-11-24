package application

import (
	"context"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// GetCaller asks who the caller is. It names nobody else: there is no
// parameter that could, so nobody is asked about but the caller.
type GetCaller struct {
	Caller domain.Principal
}

// CallerIdentity is who the caller is: the principal its token is about, and
// whether the deployment names it an administrator of the platform.
type CallerIdentity struct {
	Principal             domain.Principal
	PlatformAdministrator bool
}

// GetCaller says who the caller is and whether it administers the platform
// (AC-08). It reads no store: the principal is the one the token of the
// request is about, and the administrators are those the deployment names,
// which nothing a request carries can change. Nobody is refused, so nothing is
// recorded.
//
// Administering the platform holds nothing in a product. What the caller holds
// in one is for GetCallerInProduct.
func (s *ProductGovernance) GetCaller(_ context.Context, query GetCaller) (CallerIdentity, error) {
	if err := known(query.Caller); err != nil {
		return CallerIdentity{}, err
	}
	return CallerIdentity{
		Principal:             query.Caller,
		PlatformAdministrator: s.deps.Administrators.Includes(query.Caller),
	}, nil
}

// GetCallerInProduct asks what the caller holds in one product. Like
// GetCaller it names nobody else.
type GetCallerInProduct struct {
	Caller        domain.Principal
	CorrelationID string
	ProductID     string
}

// CallerStanding is what the caller holds in one product now: the roles of the
// product, and the roles in each workspace of it the caller is a member of.
// It says what is held and nothing of what that lets the caller do, and
// nothing of who granted it or when.
type CallerStanding struct {
	ProductID  domain.ProductID
	Roles      []domain.Role
	Workspaces []domain.HeldInWorkspace
}

// GetCallerInProduct returns what the caller holds in a product, to whoever
// GetProduct returns the product to: a platform administrator, and whoever
// holds something in force in it, a role in the product or a membership of
// one of its workspaces. An administrator that holds nothing in it is told so.
// To anyone else the product does not exist, the way GetProduct answers: a
// stranger, and whoever held something in it that was revoked since, are
// refused as for a product that is missing, and the refusal is recorded unless
// there is no such product.
//
// What the caller holds is read when the request is made, from the state the
// server holds, and never kept from one request to the next: a role or a
// membership that was revoked is not in the answer from the moment the
// revocation commits (F05 §5.3). A product that is suspended still tells what
// is held in it; asking is not new work. Nothing is locked: the answer is the
// roles themselves, and every decision that follows reads them again.
func (s *ProductGovernance) GetCallerInProduct(ctx context.Context, query GetCallerInProduct) (CallerStanding, error) {
	if err := known(query.Caller); err != nil {
		return CallerStanding{}, err
	}
	id, err := domain.ParseProductID(query.ProductID)
	if err != nil {
		return CallerStanding{}, ErrProductNotFound
	}
	seen, err := s.survey(ctx, query.Caller, id, "", "")
	if err != nil {
		return CallerStanding{}, err
	}

	if !seen.productFound {
		return CallerStanding{}, ErrProductNotFound
	}
	if !s.knows(seen.standing, query.Caller) {
		return CallerStanding{}, s.refuse(ctx, refusal{ErrProductNotFound, ports.AuditEntry{
			Actor: query.Caller, Action: ActionProductRead, Rule: ruleNoRole,
			ProductID: id, ResourceType: resourceProduct, ResourceID: string(id),
			CorrelationID: query.CorrelationID,
		}})
	}
	return CallerStanding{
		ProductID:  id,
		Roles:      seen.standing.HeldRoles(),
		Workspaces: seen.standing.HeldInWorkspaces(),
	}, nil
}
