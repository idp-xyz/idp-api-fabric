package application

import (
	"context"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// The bounds of a page. The contract of the API states the same ones, and a
// test holds the two together.
const (
	// DefaultPageSize is how many items a page holds when the caller names
	// no size.
	DefaultPageSize = 50
	// MaxPageSize is the most a caller gets in one page. A list is read to
	// its end by asking for the next page, never by asking for a larger one.
	MaxPageSize = 100
)

// Page is one page of a list, in the order of the list (ports.Position).
//
// A list is read page by page, each page from what is there when it is read.
// What was in the list before the first page was read and is still in it
// after the last is returned once. What is added, or ends, in between may or
// may not be returned.
type Page[T any] struct {
	Items []T
	// Next is the position the page after this one starts behind: that of
	// the last item here. It is nil when nothing follows.
	Next *ports.Position
}

// paging is the part of a list a query asks for, checked.
//
// size is how many items the page holds at most, and what the store is asked
// for is one more: whether that one is there says whether a page follows,
// without counting what the caller may read.
func paging(pageSize int, after *ports.Position, identifies func(id string) error) (page ports.Page, size int, err error) {
	switch {
	case pageSize == 0:
		size = DefaultPageSize
	case pageSize < 0 || pageSize > MaxPageSize:
		return ports.Page{}, 0, fmt.Errorf("%w: a page holds 1 to %d items", domain.ErrInvalid, MaxPageSize)
	default:
		size = pageSize
	}
	if after != nil {
		// A position is one a page of this list ended at: the identifier
		// of an item, and the instant the list is ordered by.
		if err := identifies(after.ID); err != nil || after.At.IsZero() {
			return ports.Page{}, 0, fmt.Errorf("%w: a page starts behind a position of the list", domain.ErrInvalid)
		}
		behind := *after
		page.After = &behind
	}
	page.Limit = size + 1
	return page, size, nil
}

// pageOf is the page of at most size items in what a store returned when it
// was asked for one more.
func pageOf[T any](items []T, size int, position func(T) ports.Position) Page[T] {
	if len(items) <= size {
		return Page[T]{Items: items}
	}
	next := position(items[size-1])
	return Page[T]{Items: items[:size], Next: &next}
}

// ListProducts asks for a page of the products the caller may read.
type ListProducts struct {
	Caller        domain.Principal
	CorrelationID string
	// PageSize is how many products the page holds at most. Zero asks for
	// DefaultPageSize.
	PageSize int
	// After is the position the page starts behind: Next of the page
	// before. Nil starts the list.
	After *ports.Position
}

// ListProducts returns a page of the products the caller may read, the way
// GetProduct decides it for one: every product to a platform administrator,
// and to anybody else the products it holds something in force in, a role in
// the product or a membership of one of its workspaces. Whoever holds
// nothing anywhere gets an empty list.
//
// Nobody is refused, so nothing is recorded. What the caller holds is read
// with every page: a product in which it holds nothing any more is in no
// page that is read afterwards.
func (s *ProductGovernance) ListProducts(ctx context.Context, query ListProducts) (Page[domain.Product], error) {
	if err := known(query.Caller); err != nil {
		return Page[domain.Product]{}, err
	}
	page, size, err := paging(query.PageSize, query.After, func(id string) error {
		_, err := domain.ParseProductID(id)
		return err
	})
	if err != nil {
		return Page[domain.Product]{}, err
	}

	var products []domain.Product
	if s.deps.Administrators.Includes(query.Caller) {
		products, err = s.deps.Products.List(ctx, page)
	} else {
		products, err = s.deps.Products.ListFor(ctx, query.Caller, page)
	}
	if err != nil {
		return Page[domain.Product]{}, fmt.Errorf("list the products: %w", err)
	}
	return pageOf(products, size, ports.PositionOfProduct), nil
}

// ListWorkspaces asks for a page of the workspaces of one product.
type ListWorkspaces struct {
	Caller        domain.Principal
	CorrelationID string
	ProductID     string
	PageSize      int
	After         *ports.Position
}

// ListWorkspaces returns a page of the workspaces of a product the caller
// may read, the way GetWorkspace decides it for one: every workspace to an
// owner of the product, and to a member the workspaces it is a member of. A
// member is not told that there are others (F03 §2).
//
// Whoever holds nothing in the product reads none of its workspaces, and is
// answered as far as it may know: no such product to a stranger, and that it
// may not to a platform administrator, who knows the product exists. An
// empty list would say that the product has no workspace.
func (s *ProductGovernance) ListWorkspaces(ctx context.Context, query ListWorkspaces) (Page[domain.Workspace], error) {
	if err := known(query.Caller); err != nil {
		return Page[domain.Workspace]{}, err
	}
	productID, err := domain.ParseProductID(query.ProductID)
	if err != nil {
		return Page[domain.Workspace]{}, ErrProductNotFound
	}
	seen, err := s.survey(ctx, query.Caller, productID, "", "")
	if err != nil {
		return Page[domain.Workspace]{}, err
	}

	if !seen.productFound {
		return Page[domain.Workspace]{}, ErrProductNotFound
	}
	if seen.standing.Stranger() {
		refused := refusal{ErrProductNotFound, ports.AuditEntry{
			Actor: query.Caller, Action: ActionWorkspaceRead, Rule: ruleNoRole,
			ProductID: productID, ResourceType: resourceWorkspace, CorrelationID: query.CorrelationID,
		}}
		if s.knows(seen.standing, query.Caller) {
			refused.err = ErrForbidden
		}
		return Page[domain.Workspace]{}, s.refuse(ctx, refused)
	}

	// What the request says of the page is looked at once the caller is
	// known to read the list: whoever is refused is told nothing else.
	page, size, err := paging(query.PageSize, query.After, func(id string) error {
		_, err := domain.ParseWorkspaceID(id)
		return err
	})
	if err != nil {
		return Page[domain.Workspace]{}, err
	}
	var workspaces []domain.Workspace
	if seen.standing.Owns() {
		workspaces, err = s.deps.Workspaces.List(ctx, productID, page)
	} else {
		workspaces, err = s.deps.Workspaces.ListFor(ctx, productID, query.Caller, page)
	}
	if err != nil {
		return Page[domain.Workspace]{}, fmt.Errorf("list the workspaces: %w", err)
	}
	return pageOf(workspaces, size, ports.PositionOfWorkspace), nil
}

// ListMemberships asks for a page of the memberships of one workspace.
type ListMemberships struct {
	Caller        domain.Principal
	CorrelationID string
	ProductID     string
	WorkspaceID   string
	PageSize      int
	After         *ports.Position
}

// ListMemberships returns a page of the memberships in force of a workspace
// to an owner of its product. As with one membership, the members of a
// workspace are the owner's to manage and nobody else's to read.
//
// Whoever does not own the product is answered as far as it may know: no
// such product to a stranger, no such workspace to whoever may not read the
// workspace, and that it may not to a member of it, who knows the workspace
// has members: it is one.
func (s *ProductGovernance) ListMemberships(ctx context.Context, query ListMemberships) (Page[domain.Membership], error) {
	if err := known(query.Caller); err != nil {
		return Page[domain.Membership]{}, err
	}
	productID, err := domain.ParseProductID(query.ProductID)
	if err != nil {
		return Page[domain.Membership]{}, ErrProductNotFound
	}
	workspaceID, _ := domain.ParseWorkspaceID(query.WorkspaceID)
	seen, err := s.survey(ctx, query.Caller, productID, workspaceID, "")
	if err != nil {
		return Page[domain.Membership]{}, err
	}

	if !seen.productFound {
		return Page[domain.Membership]{}, ErrProductNotFound
	}
	if !seen.standing.Owns() {
		refused := refusal{ErrForbidden, ports.AuditEntry{
			Actor: query.Caller, Action: ActionMemberRead, Rule: ruleNoRole,
			ProductID: productID, ResourceType: resourceMember, CorrelationID: query.CorrelationID,
		}}
		switch {
		case !s.knows(seen.standing, query.Caller):
			refused.err = ErrProductNotFound
		case !seen.standing.MemberOf(workspaceID):
			refused.err = ErrWorkspaceNotFound
		}
		return Page[domain.Membership]{}, s.refuse(ctx, refused)
	}
	if !seen.workspaceFound {
		return Page[domain.Membership]{}, ErrWorkspaceNotFound
	}

	page, size, err := paging(query.PageSize, query.After, func(id string) error {
		_, err := domain.ParseMembershipID(id)
		return err
	})
	if err != nil {
		return Page[domain.Membership]{}, err
	}
	memberships, err := s.deps.Memberships.List(ctx, productID, workspaceID, page)
	if err != nil {
		return Page[domain.Membership]{}, fmt.Errorf("list the memberships: %w", err)
	}
	return pageOf(memberships, size, ports.PositionOfMembership), nil
}

// ListProductRoles asks for a page of the role assignments of one product.
type ListProductRoles struct {
	Caller        domain.Principal
	CorrelationID string
	ProductID     string
	PageSize      int
	After         *ports.Position
}

// ListProductRoles returns a page of the role assignments in force of a
// product to an owner of it. As with one assignment, who owns a product is
// the owners' to manage and nobody else's to read: a platform administrator
// names an owner and reads none.
//
// It is how an owner finds the assignment of another: that of an owner who
// was named when the product was registered is in no other answer.
//
// Whoever does not own the product is answered as far as it may know: no
// such product to a stranger, and that it may not to whoever knows the
// product.
func (s *ProductGovernance) ListProductRoles(ctx context.Context, query ListProductRoles) (Page[domain.RoleAssignment], error) {
	if err := known(query.Caller); err != nil {
		return Page[domain.RoleAssignment]{}, err
	}
	productID, err := domain.ParseProductID(query.ProductID)
	if err != nil {
		return Page[domain.RoleAssignment]{}, ErrProductNotFound
	}
	seen, err := s.survey(ctx, query.Caller, productID, "", "")
	if err != nil {
		return Page[domain.RoleAssignment]{}, err
	}

	if !seen.productFound {
		return Page[domain.RoleAssignment]{}, ErrProductNotFound
	}
	if !seen.standing.Owns() {
		return Page[domain.RoleAssignment]{}, s.refuse(ctx, *s.notOwner(seen.standing, ports.AuditEntry{
			Actor: query.Caller, Action: ActionProductRoleRead, Rule: ruleNoRole,
			ProductID: productID, ResourceType: resourceRoleAssignment, CorrelationID: query.CorrelationID,
		}))
	}

	page, size, err := paging(query.PageSize, query.After, func(id string) error {
		_, err := domain.ParseRoleAssignmentID(id)
		return err
	})
	if err != nil {
		return Page[domain.RoleAssignment]{}, err
	}
	assignments, err := s.deps.Roles.List(ctx, productID, page)
	if err != nil {
		return Page[domain.RoleAssignment]{}, fmt.Errorf("list the role assignments: %w", err)
	}
	return pageOf(assignments, size, ports.PositionOfRoleAssignment), nil
}
