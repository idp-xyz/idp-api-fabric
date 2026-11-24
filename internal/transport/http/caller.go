package http

import (
	"context"
	"errors"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
)

// GetCaller says who the token of the request is about. Nothing the request
// says takes part: the operation declares no parameter, so a request that
// sends one never gets here, and the principal is the one the validator
// verified.
func (o operations) GetCaller(ctx context.Context, _ GetCallerRequestObject) (GetCallerResponseObject, error) {
	principal, _, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	who, err := o.governance.GetCaller(ctx, application.GetCaller{Caller: principal})
	if err != nil {
		return nil, err
	}
	return GetCaller200JSONResponse{
		Body: Caller{
			Principal:             Principal{Issuer: who.Principal.Issuer, Subject: who.Principal.Subject},
			PlatformAdministrator: who.PlatformAdministrator,
		},
		Headers: GetCaller200ResponseHeaders{CacheControl: noStore},
	}, nil
}

// GetCallerInProduct says what the caller holds in the product. The answer is
// private to the caller, as the other answers about a product are to its
// members (F05 §8), and carries no version: nothing is changed from it.
func (o operations) GetCallerInProduct(ctx context.Context, request GetCallerInProductRequestObject) (GetCallerInProductResponseObject, error) {
	principal, correlationID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	standing, err := o.governance.GetCallerInProduct(ctx, application.GetCallerInProduct{
		Caller: principal, CorrelationID: correlationID, ProductID: request.ProductID,
	})
	if err != nil {
		return nil, err
	}
	body, err := callerInProductOf(standing)
	if err != nil {
		return nil, err
	}
	return GetCallerInProduct200JSONResponse{
		Body:    body,
		Headers: GetCallerInProduct200ResponseHeaders{CacheControl: noStore},
	}, nil
}

// callerInProductOf is what the caller holds as the contract shows it: the
// roles of the product and, for each workspace, the roles in it. Nothing of
// who granted a role or when, and no identifier of a role assignment or a
// membership, is shown: they are the owners' to read.
//
// A role the contract does not declare is not sent, and neither is a workspace
// in which nothing is held: a caller would branch on a value it was never
// told of, or take the workspace for one it holds something in. Empty lists
// are empty lists, never missing ones.
func callerInProductOf(standing application.CallerStanding) (CallerInProduct, error) {
	roles := make([]ProductRole, 0, len(standing.Roles))
	for _, held := range standing.Roles {
		role := ProductRole(held)
		if !role.Valid() {
			return CallerInProduct{}, fmt.Errorf("the caller holds the role %q in the product, which the contract does not declare", held)
		}
		roles = append(roles, role)
	}
	workspaces := make([]CallerInWorkspace, 0, len(standing.Workspaces))
	for _, in := range standing.Workspaces {
		if len(in.Roles) == 0 {
			return CallerInProduct{}, errors.New("the caller holds nothing in a workspace, and the contract shows none that it does")
		}
		held := make([]WorkspaceRole, 0, len(in.Roles))
		for _, name := range in.Roles {
			role := WorkspaceRole(name)
			if !role.Valid() {
				return CallerInProduct{}, fmt.Errorf("the caller holds the role %q in a workspace, which the contract does not declare", name)
			}
			held = append(held, role)
		}
		workspaces = append(workspaces, CallerInWorkspace{WorkspaceID: string(in.WorkspaceID), Roles: held})
	}
	return CallerInProduct{ProductID: string(standing.ProductID), Roles: roles, Workspaces: workspaces}, nil
}
