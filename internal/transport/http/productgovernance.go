package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// ProductGovernance are the use cases of the context of that name, as the
// transport calls them. Each decides for itself whether the caller may do
// what it asks: the transport says who the caller is and nothing more.
type ProductGovernance interface {
	RegisterProduct(ctx context.Context, command application.RegisterProduct) (domain.Product, error)
	GetProduct(ctx context.Context, query application.GetProduct) (domain.Product, error)
	CreateWorkspace(ctx context.Context, command application.CreateWorkspace) (domain.Workspace, error)
	GetWorkspace(ctx context.Context, query application.GetWorkspace) (domain.Workspace, error)
	GrantMembership(ctx context.Context, command application.GrantMembership) (domain.Membership, error)
	GetMembership(ctx context.Context, query application.GetMembership) (domain.Membership, error)
	RevokeMembership(ctx context.Context, command application.RevokeMembership) error
	GrantProductRole(ctx context.Context, command application.GrantProductRole) (domain.RoleAssignment, error)
	GetProductRole(ctx context.Context, query application.GetProductRole) (domain.RoleAssignment, error)
	RevokeProductRole(ctx context.Context, command application.RevokeProductRole) error
}

// noStore is the cache directive of everything private: a product and what
// is inside it belong to its members (F05 §8).
const noStore = "no-store"

// caller returns who the request acts as and the identifier of the request.
//
// An operation of Product Governance declares a security requirement, so the
// validator has authenticated the request before it gets here. A request
// without a principal means the operation was wired around the validator;
// it is failed rather than served.
func caller(ctx context.Context) (domain.Principal, string, error) {
	state := stateOf(ctx)
	if state == nil || !state.authenticated {
		return domain.Principal{}, "", errors.New("the operation was reached without an authenticated principal")
	}
	return domain.Principal{Issuer: state.principal.Issuer, Subject: state.principal.Subject}, state.correlationID, nil
}

func (o operations) RegisterProduct(ctx context.Context, request RegisterProductRequestObject) (RegisterProductResponseObject, error) {
	principal, correlationID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	owners := make([]domain.Principal, 0, len(request.Body.Owners))
	for _, owner := range request.Body.Owners {
		owners = append(owners, domain.Principal{Issuer: owner.Issuer, Subject: owner.Subject})
	}
	product, err := o.governance.RegisterProduct(ctx, application.RegisterProduct{
		Caller: principal, CorrelationID: correlationID,
		Key: request.Body.ProductKey, DisplayName: request.Body.DisplayName,
		Owner: domain.OrganizationRef{
			IdentityProvider: request.Body.OwnerOrganization.IdentityProvider,
			Organization:     request.Body.OwnerOrganization.Organization,
		},
		Owners: owners,
	})
	if err != nil {
		return nil, err
	}
	body, err := productOf(product)
	if err != nil {
		return nil, err
	}
	return RegisterProduct201JSONResponse{
		Body:    body,
		Headers: RegisterProduct201ResponseHeaders{Location: "/v1/products/" + body.ProductID, CacheControl: noStore},
	}, nil
}

func (o operations) GetProduct(ctx context.Context, request GetProductRequestObject) (GetProductResponseObject, error) {
	principal, correlationID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	product, err := o.governance.GetProduct(ctx, application.GetProduct{
		Caller: principal, CorrelationID: correlationID, ProductID: request.ProductID,
	})
	if err != nil {
		return nil, err
	}
	body, err := productOf(product)
	if err != nil {
		return nil, err
	}
	return GetProduct200JSONResponse{Body: body, Headers: GetProduct200ResponseHeaders{CacheControl: noStore}}, nil
}

func (o operations) CreateWorkspace(ctx context.Context, request CreateWorkspaceRequestObject) (CreateWorkspaceResponseObject, error) {
	principal, correlationID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	workspace, err := o.governance.CreateWorkspace(ctx, application.CreateWorkspace{
		Caller: principal, CorrelationID: correlationID, ProductID: request.ProductID,
		Key: request.Body.WorkspaceKey, DisplayName: request.Body.DisplayName,
	})
	if err != nil {
		return nil, err
	}
	body, err := workspaceOf(workspace)
	if err != nil {
		return nil, err
	}
	return CreateWorkspace201JSONResponse{
		Body: body,
		Headers: CreateWorkspace201ResponseHeaders{
			Location:     "/v1/products/" + body.ProductID + "/workspaces/" + body.WorkspaceID,
			CacheControl: noStore,
		},
	}, nil
}

func (o operations) GetWorkspace(ctx context.Context, request GetWorkspaceRequestObject) (GetWorkspaceResponseObject, error) {
	principal, correlationID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	workspace, err := o.governance.GetWorkspace(ctx, application.GetWorkspace{
		Caller: principal, CorrelationID: correlationID, ProductID: request.ProductID, WorkspaceID: request.WorkspaceID,
	})
	if err != nil {
		return nil, err
	}
	body, err := workspaceOf(workspace)
	if err != nil {
		return nil, err
	}
	return GetWorkspace200JSONResponse{Body: body, Headers: GetWorkspace200ResponseHeaders{CacheControl: noStore}}, nil
}

func (o operations) GrantMembership(ctx context.Context, request GrantMembershipRequestObject) (GrantMembershipResponseObject, error) {
	principal, correlationID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	membership, err := o.governance.GrantMembership(ctx, application.GrantMembership{
		Caller: principal, CorrelationID: correlationID, ProductID: request.ProductID, WorkspaceID: request.WorkspaceID,
		Member: domain.Principal{Issuer: request.Body.Member.Issuer, Subject: request.Body.Member.Subject},
		Role:   string(request.Body.Role),
	})
	if err != nil {
		return nil, err
	}
	body, err := membershipOf(membership)
	if err != nil {
		return nil, err
	}
	return GrantMembership201JSONResponse{
		Body: body,
		Headers: GrantMembership201ResponseHeaders{
			Location:     "/v1/products/" + body.ProductID + "/workspaces/" + body.WorkspaceID + "/memberships/" + body.MembershipID,
			CacheControl: noStore,
		},
	}, nil
}

func (o operations) GetMembership(ctx context.Context, request GetMembershipRequestObject) (GetMembershipResponseObject, error) {
	principal, correlationID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	membership, err := o.governance.GetMembership(ctx, application.GetMembership{
		Caller: principal, CorrelationID: correlationID,
		ProductID: request.ProductID, WorkspaceID: request.WorkspaceID, MembershipID: request.MembershipID,
	})
	if err != nil {
		return nil, err
	}
	body, err := membershipOf(membership)
	if err != nil {
		return nil, err
	}
	return GetMembership200JSONResponse{Body: body, Headers: GetMembership200ResponseHeaders{CacheControl: noStore}}, nil
}

func (o operations) RevokeMembership(ctx context.Context, request RevokeMembershipRequestObject) (RevokeMembershipResponseObject, error) {
	principal, correlationID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := o.governance.RevokeMembership(ctx, application.RevokeMembership{
		Caller: principal, CorrelationID: correlationID,
		ProductID: request.ProductID, WorkspaceID: request.WorkspaceID, MembershipID: request.MembershipID,
	}); err != nil {
		return nil, err
	}
	return RevokeMembership204Response{}, nil
}

func (o operations) GrantProductRole(ctx context.Context, request GrantProductRoleRequestObject) (GrantProductRoleResponseObject, error) {
	principal, correlationID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	assignment, err := o.governance.GrantProductRole(ctx, application.GrantProductRole{
		Caller: principal, CorrelationID: correlationID, ProductID: request.ProductID,
		Principal: domain.Principal{Issuer: request.Body.Principal.Issuer, Subject: request.Body.Principal.Subject},
		Role:      string(request.Body.Role),
	})
	if err != nil {
		return nil, err
	}
	body, err := roleAssignmentOf(assignment)
	if err != nil {
		return nil, err
	}
	return GrantProductRole201JSONResponse{
		Body: body,
		Headers: GrantProductRole201ResponseHeaders{
			Location:     "/v1/products/" + body.ProductID + "/role-assignments/" + body.RoleAssignmentID,
			CacheControl: noStore,
		},
	}, nil
}

func (o operations) GetProductRole(ctx context.Context, request GetProductRoleRequestObject) (GetProductRoleResponseObject, error) {
	principal, correlationID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	assignment, err := o.governance.GetProductRole(ctx, application.GetProductRole{
		Caller: principal, CorrelationID: correlationID,
		ProductID: request.ProductID, RoleAssignmentID: request.RoleAssignmentID,
	})
	if err != nil {
		return nil, err
	}
	body, err := roleAssignmentOf(assignment)
	if err != nil {
		return nil, err
	}
	return GetProductRole200JSONResponse{Body: body, Headers: GetProductRole200ResponseHeaders{CacheControl: noStore}}, nil
}

func (o operations) RevokeProductRole(ctx context.Context, request RevokeProductRoleRequestObject) (RevokeProductRoleResponseObject, error) {
	principal, correlationID, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	if err := o.governance.RevokeProductRole(ctx, application.RevokeProductRole{
		Caller: principal, CorrelationID: correlationID,
		ProductID: request.ProductID, RoleAssignmentID: request.RoleAssignmentID,
	}); err != nil {
		return nil, err
	}
	return RevokeProductRole204Response{}, nil
}

// productOf is the product as the contract shows it. The DTO is not the
// model: what the contract does not declare, such as who registered the
// product or its version, stays inside (F07 §3.1).
//
// A status the contract does not declare is not sent: a caller would branch
// on a value it was never told of.
func productOf(product domain.Product) (Product, error) {
	status := ProductStatus(product.Status)
	if !status.Valid() {
		return Product{}, fmt.Errorf("the product is in the status %q, which the contract does not declare", product.Status)
	}
	return Product{
		ProductID:   string(product.ID),
		ProductKey:  product.Key,
		DisplayName: product.DisplayName,
		Status:      status,
		OwnerOrganization: OrganizationRef{
			IdentityProvider: product.Owner.IdentityProvider,
			Organization:     product.Owner.Organization,
		},
		CreatedAt: product.CreatedAt,
	}, nil
}

// workspaceOf is the workspace as the contract shows it.
func workspaceOf(workspace domain.Workspace) (Workspace, error) {
	status := WorkspaceStatus(workspace.Status)
	if !status.Valid() {
		return Workspace{}, fmt.Errorf("the workspace is in the status %q, which the contract does not declare", workspace.Status)
	}
	return Workspace{
		WorkspaceID:  string(workspace.ID),
		ProductID:    string(workspace.ProductID),
		WorkspaceKey: workspace.Key,
		DisplayName:  workspace.DisplayName,
		Status:       status,
		CreatedAt:    workspace.CreatedAt,
	}, nil
}

// membershipOf is the membership as the contract shows it: who holds which
// role in which workspace, since when. Who granted it and its version stay
// inside.
//
// The contract shows a membership in force and has no member that says
// otherwise, so a revoked one is not sent, and neither is a role the
// contract does not declare.
func membershipOf(membership domain.Membership) (Membership, error) {
	if !membership.InForce() {
		return Membership{}, errors.New("the membership was revoked, and the contract shows none that is")
	}
	role := WorkspaceRole(membership.Role)
	if !role.Valid() {
		return Membership{}, fmt.Errorf("the membership grants the role %q, which the contract does not declare", membership.Role)
	}
	return Membership{
		MembershipID: string(membership.ID),
		ProductID:    string(membership.ProductID),
		WorkspaceID:  string(membership.WorkspaceID),
		Member:       Principal{Issuer: membership.Principal.Issuer, Subject: membership.Principal.Subject},
		Role:         role,
		GrantedAt:    membership.GrantedAt,
	}, nil
}

// roleAssignmentOf is the role assignment as the contract shows it: who
// holds which role in which product, since when. Who granted it and its
// version stay inside.
//
// As with a membership, the contract shows an assignment in force and has no
// member that says otherwise: a revoked one is not sent, and neither is a
// role the contract does not declare.
func roleAssignmentOf(assignment domain.RoleAssignment) (ProductRoleAssignment, error) {
	if !assignment.InForce() {
		return ProductRoleAssignment{}, errors.New("the role assignment was revoked, and the contract shows none that is")
	}
	role := ProductRole(assignment.Role)
	if !role.Valid() {
		return ProductRoleAssignment{}, fmt.Errorf("the role assignment grants the role %q, which the contract does not declare", assignment.Role)
	}
	return ProductRoleAssignment{
		RoleAssignmentID: string(assignment.ID),
		ProductID:        string(assignment.ProductID),
		Principal:        Principal{Issuer: assignment.Principal.Issuer, Subject: assignment.Principal.Subject},
		Role:             role,
		GrantedAt:        assignment.GrantedAt,
	}, nil
}

// problemOf is the problem a failed use case is answered with. The second
// result is false for a failure nobody expected, which is an internal error.
//
// A problem says what the caller needs to act on and no more. That a product
// exists and is withheld, and that it does not exist, are one answer.
func problemOf(err error) (Problem, bool) {
	switch {
	case errors.Is(err, application.ErrForbidden):
		return problem(http.StatusForbidden, CodeForbidden, "The caller may not perform this action."), true
	case errors.Is(err, application.ErrProductNotFound):
		return problem(http.StatusNotFound, CodeProductNotFound, ""), true
	case errors.Is(err, application.ErrWorkspaceNotFound):
		return problem(http.StatusNotFound, CodeWorkspaceNotFound, ""), true
	case errors.Is(err, application.ErrMembershipNotFound):
		return problem(http.StatusNotFound, CodeMembershipNotFound, ""), true
	case errors.Is(err, application.ErrRoleAssignmentNotFound):
		return problem(http.StatusNotFound, CodeRoleAssignmentNotFound, ""), true
	case errors.Is(err, ports.ErrProductKeyTaken):
		return problem(http.StatusConflict, CodeProductKeyTaken, "A product with this key is registered already."), true
	case errors.Is(err, ports.ErrWorkspaceKeyTaken):
		return problem(http.StatusConflict, CodeWorkspaceKeyTaken, "The product has a workspace with this key already."), true
	case errors.Is(err, ports.ErrMembershipExists):
		return problem(http.StatusConflict, CodeMembershipExists, "The principal holds this role in the workspace already."), true
	case errors.Is(err, ports.ErrRoleAssignmentExists):
		return problem(http.StatusConflict, CodeRoleAssignmentExists, "The principal holds this role in the product already."), true
	case errors.Is(err, domain.ErrLastOwner):
		return problem(http.StatusConflict, CodeLastOwner, "The product has no other owner, and always has one."), true
	case errors.Is(err, domain.ErrProductNotActive):
		return problem(http.StatusConflict, CodeProductNotActive, "The product is suspended and takes no new work."), true
	case errors.Is(err, application.ErrOwnerIssuerNotTrusted):
		return problem(http.StatusUnprocessableEntity, CodeOwnerIssuerNotTrusted,
			"An owner is named under an issuer this deployment does not trust."), true
	case errors.Is(err, application.ErrMemberIssuerNotTrusted):
		return problem(http.StatusUnprocessableEntity, CodeMemberIssuerNotTrusted,
			"A member is named under an issuer this deployment does not trust."), true
	case errors.Is(err, domain.ErrInvalid):
		// The contract states the rules of the model, so the validator has
		// refused such a request already. This is for the day the two differ.
		return problem(http.StatusUnprocessableEntity, CodeRequestUnprocessable, ""), true
	case errors.Is(err, ports.ErrOutcomeUncertain):
		return problem(http.StatusServiceUnavailable, CodeOutcomeUncertain,
			"It is not known whether the change was committed. Read before retrying."), true
	}
	return Problem{}, false
}
