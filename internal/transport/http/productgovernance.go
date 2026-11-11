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
	case errors.Is(err, ports.ErrProductKeyTaken):
		return problem(http.StatusConflict, CodeProductKeyTaken, "A product with this key is registered already."), true
	case errors.Is(err, ports.ErrWorkspaceKeyTaken):
		return problem(http.StatusConflict, CodeWorkspaceKeyTaken, "The product has a workspace with this key already."), true
	case errors.Is(err, domain.ErrProductNotActive):
		return problem(http.StatusConflict, CodeProductNotActive, "The product is suspended and takes no new work."), true
	case errors.Is(err, application.ErrOwnerIssuerNotTrusted):
		return problem(http.StatusUnprocessableEntity, CodeOwnerIssuerNotTrusted,
			"An owner is named under an issuer this deployment does not trust."), true
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
