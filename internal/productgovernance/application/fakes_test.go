package application

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// world stands in for everything outside the use cases: the stores, the
// audit trail, the events, the clock and the identifiers. The real adapters
// are tested against PostgreSQL where they live.
//
// It keeps the one property of a database the use cases rely on: what is
// written inside a transaction that fails is gone, and what is written
// outside one stays.
type world struct {
	products   map[domain.ProductID]domain.Product
	workspaces map[domain.WorkspaceID]domain.Workspace
	// roles and memberships hold every role assignment and every
	// membership, in force or revoked, as the tables do.
	roles       map[domain.RoleAssignmentID]domain.RoleAssignment
	memberships map[domain.MembershipID]domain.Membership
	audited     []audited
	events      []string

	administrators []domain.Principal
	issuers        []string
	now            time.Time
	nextID         int

	transactions int
	// asked are the pages the stores were asked for, in the order they
	// were asked.
	asked []ports.Page
	// failing makes the named step fail, to see what a failure leaves behind.
	failing map[string]error
	// meanwhile runs when the owners of a product are locked, before they
	// are counted. It stands for what another transaction committed while
	// this one waited for the lock: this one reads it, and does not take it
	// back when it fails.
	meanwhile func()
	// undo puts the world back as the transaction in progress found it.
	undo func()
}

// audited is one audit entry and whether a transaction was open when it was
// recorded.
type audited struct {
	entry      ports.AuditEntry
	transacted bool
}

type inTransaction struct{}

func transacted(ctx context.Context) bool { return ctx.Value(inTransaction{}) == true }

const trustedIssuer = "https://issuer.example"

var (
	admin   = domain.Principal{Issuer: trustedIssuer, Subject: "platform-admin"}
	alice   = domain.Principal{Issuer: trustedIssuer, Subject: "alice"}
	bob     = domain.Principal{Issuer: trustedIssuer, Subject: "bob"}
	carol   = domain.Principal{Issuer: trustedIssuer, Subject: "carol"}
	mallory = domain.Principal{Issuer: trustedIssuer, Subject: "mallory"}
	cargo   = domain.OrganizationRef{IdentityProvider: trustedIssuer, Organization: "org-cargopai"}
	started = time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
)

// wiring is the world as the ports of the use cases.
func (w *world) wiring() Dependencies {
	return Dependencies{
		Transactor: w, Products: productStore{w}, Workspaces: workspaceStore{w}, Roles: roleStore{w},
		Memberships: membershipStore{w}, Administrators: w, Issuers: w, Audit: w, Events: w, Clock: w, Identifiers: w,
	}
}

func newWorld(t *testing.T) (*world, *ProductGovernance) {
	t.Helper()

	w := &world{
		products:       map[domain.ProductID]domain.Product{},
		workspaces:     map[domain.WorkspaceID]domain.Workspace{},
		roles:          map[domain.RoleAssignmentID]domain.RoleAssignment{},
		memberships:    map[domain.MembershipID]domain.Membership{},
		administrators: []domain.Principal{admin},
		issuers:        []string{trustedIssuer},
		now:            started,
		failing:        map[string]error{},
	}
	service, err := New(w.wiring())
	if err != nil {
		t.Fatalf("wire the use cases: %v", err)
	}
	return w, service
}

func (w *world) fail(step string) error { return w.failing[step] }

// WithinTransaction runs fn and undoes what it wrote if it fails.
func (w *world) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	if transacted(ctx) {
		return fn(ctx)
	}
	w.transactions++
	w.undo = w.checkpoint()

	err := fn(context.WithValue(ctx, inTransaction{}, true))
	if err == nil {
		err = w.fail("commit")
	}
	if err != nil {
		w.undo()
	}
	return err
}

// checkpoint returns what puts the world back as it is now.
func (w *world) checkpoint() func() {
	products, workspaces := maps.Clone(w.products), maps.Clone(w.workspaces)
	roles, memberships := maps.Clone(w.roles), maps.Clone(w.memberships)
	audit, events := slices.Clone(w.audited), slices.Clone(w.events)
	return func() {
		w.products, w.workspaces = products, workspaces
		w.roles, w.memberships = roles, memberships
		w.audited, w.events = audit, events
	}
}

func (w *world) Includes(principal domain.Principal) bool {
	return slices.Contains(w.administrators, principal)
}

func (w *world) Trusts(issuer string) bool { return slices.Contains(w.issuers, issuer) }

func (w *world) Now() time.Time { return w.now }

// NewID returns identifiers that are told apart at a glance.
func (w *world) NewID() string {
	w.nextID++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", w.nextID)
}

func (w *world) Record(ctx context.Context, entry ports.AuditEntry) error {
	if err := w.fail("audit"); err != nil {
		return err
	}
	w.audited = append(w.audited, audited{entry, transacted(ctx)})
	return nil
}

func (w *world) ProductRegistered(ctx context.Context, product domain.Product, correlationID string) error {
	if err := w.fail("events"); err != nil {
		return err
	}
	if !transacted(ctx) {
		return fmt.Errorf("an event was announced outside a transaction")
	}
	w.events = append(w.events, "ProductRegistered "+string(product.ID)+" "+correlationID)
	return nil
}

func (w *world) WorkspaceCreated(ctx context.Context, workspace domain.Workspace, correlationID string) error {
	if err := w.fail("events"); err != nil {
		return err
	}
	if !transacted(ctx) {
		return fmt.Errorf("an event was announced outside a transaction")
	}
	w.events = append(w.events, "WorkspaceCreated "+string(workspace.ID)+" "+correlationID)
	return nil
}

func (w *world) MembershipChanged(ctx context.Context, membership domain.Membership, correlationID string) error {
	if err := w.fail("events"); err != nil {
		return err
	}
	if !transacted(ctx) {
		return fmt.Errorf("an event was announced outside a transaction")
	}
	change := "granted"
	if !membership.InForce() {
		change = "revoked"
	}
	w.events = append(w.events, "MembershipChanged "+string(membership.ID)+" "+change+" "+correlationID)
	return nil
}

func (w *world) ProductRoleChanged(ctx context.Context, assignment domain.RoleAssignment, correlationID string) error {
	if err := w.fail("events"); err != nil {
		return err
	}
	if !transacted(ctx) {
		return fmt.Errorf("an event was announced outside a transaction")
	}
	change := "granted"
	if !assignment.InForce() {
		change = "revoked"
	}
	w.events = append(w.events, "ProductRoleChanged "+string(assignment.ID)+" "+change+" "+correlationID)
	return nil
}

type productStore struct{ *world }

func (s productStore) Add(ctx context.Context, product domain.Product) error {
	if !transacted(ctx) {
		return fmt.Errorf("a product was written outside a transaction")
	}
	for _, existing := range s.products {
		if existing.Key == product.Key {
			return ports.ErrProductKeyTaken
		}
	}
	s.products[product.ID] = product
	return nil
}

func (s productStore) Find(_ context.Context, id domain.ProductID) (domain.Product, bool, error) {
	if err := s.fail("read"); err != nil {
		return domain.Product{}, false, err
	}
	product, found := s.products[id]
	return product, found, nil
}

// paged is the part of a list a page asks for: of items, in the order of the
// list, those behind the position the page starts after, as many as the page
// takes. Like the stores, it takes no page of less than one item.
func paged[T any](items []T, position func(T) ports.Position, page ports.Page) ([]T, error) {
	if page.Limit < 1 {
		return nil, fmt.Errorf("a page of %d items was asked for", page.Limit)
	}
	order := func(a, b ports.Position) int {
		if instants := a.At.Compare(b.At); instants != 0 {
			return instants
		}
		return strings.Compare(a.ID, b.ID)
	}
	slices.SortFunc(items, func(a, b T) int { return order(position(a), position(b)) })
	taken := []T{}
	for _, item := range items {
		if page.After != nil && order(position(item), *page.After) <= 0 {
			continue
		}
		if len(taken) == page.Limit {
			break
		}
		taken = append(taken, item)
	}
	return taken, nil
}

// holds reports whether the principal holds something in force in the
// product: a role, or a membership of one of its workspaces.
func (w *world) holds(principal domain.Principal, productID domain.ProductID) bool {
	for _, assignment := range w.roles {
		if assignment.InForce() && assignment.ProductID == productID && assignment.Principal == principal {
			return true
		}
	}
	for _, membership := range w.memberships {
		if membership.InForce() && membership.ProductID == productID && membership.Principal == principal {
			return true
		}
	}
	return false
}

// List returns a page of every product.
func (s productStore) List(_ context.Context, page ports.Page) ([]domain.Product, error) {
	if err := s.fail("list"); err != nil {
		return nil, err
	}
	s.asked = append(s.asked, page)
	return paged(slices.Collect(maps.Values(s.products)), ports.PositionOfProduct, page)
}

// ListFor returns a page of the products the principal holds something in.
func (s productStore) ListFor(_ context.Context, principal domain.Principal, page ports.Page) ([]domain.Product, error) {
	if err := s.fail("list"); err != nil {
		return nil, err
	}
	s.asked = append(s.asked, page)
	var held []domain.Product
	for _, product := range s.products {
		if s.holds(principal, product.ID) {
			held = append(held, product)
		}
	}
	return paged(held, ports.PositionOfProduct, page)
}

type workspaceStore struct{ *world }

func (s workspaceStore) Add(ctx context.Context, workspace domain.Workspace) error {
	if !transacted(ctx) {
		return fmt.Errorf("a workspace was written outside a transaction")
	}
	for _, existing := range s.workspaces {
		if existing.ProductID == workspace.ProductID && existing.Key == workspace.Key {
			return ports.ErrWorkspaceKeyTaken
		}
	}
	s.workspaces[workspace.ID] = workspace
	return nil
}

func (s workspaceStore) Find(_ context.Context, productID domain.ProductID, id domain.WorkspaceID) (domain.Workspace, bool, error) {
	workspace, found := s.workspaces[id]
	if !found || workspace.ProductID != productID {
		return domain.Workspace{}, false, nil
	}
	return workspace, true, nil
}

// List returns a page of the workspaces of the product.
func (s workspaceStore) List(_ context.Context, productID domain.ProductID, page ports.Page) ([]domain.Workspace, error) {
	if err := s.fail("list"); err != nil {
		return nil, err
	}
	s.asked = append(s.asked, page)
	var of []domain.Workspace
	for _, workspace := range s.workspaces {
		if workspace.ProductID == productID {
			of = append(of, workspace)
		}
	}
	return paged(of, ports.PositionOfWorkspace, page)
}

// ListFor returns a page of the workspaces of the product in which the
// principal holds a membership in force.
func (s workspaceStore) ListFor(_ context.Context, productID domain.ProductID, principal domain.Principal, page ports.Page) ([]domain.Workspace, error) {
	if err := s.fail("list"); err != nil {
		return nil, err
	}
	s.asked = append(s.asked, page)
	var of []domain.Workspace
	for _, workspace := range s.workspaces {
		member := slices.ContainsFunc(slices.Collect(maps.Values(s.memberships)), func(membership domain.Membership) bool {
			return membership.InForce() && membership.WorkspaceID == workspace.ID && membership.Principal == principal
		})
		if workspace.ProductID == productID && member {
			of = append(of, workspace)
		}
	}
	return paged(of, ports.PositionOfWorkspace, page)
}

type roleStore struct{ *world }

func (s roleStore) Add(ctx context.Context, assignment domain.RoleAssignment) error {
	if !transacted(ctx) {
		return fmt.Errorf("a role was written outside a transaction")
	}
	for _, existing := range s.roles {
		if existing.InForce() && existing.ProductID == assignment.ProductID &&
			existing.Principal == assignment.Principal && existing.Role == assignment.Role {
			return ports.ErrRoleAssignmentExists
		}
	}
	s.roles[assignment.ID] = assignment
	return nil
}

// Find finds a role assignment in force, under its own product.
func (s roleStore) Find(_ context.Context, productID domain.ProductID, id domain.RoleAssignmentID) (domain.RoleAssignment, bool, error) {
	assignment, found := s.roles[id]
	if !found || !assignment.InForce() || assignment.ProductID != productID {
		return domain.RoleAssignment{}, false, nil
	}
	return assignment, true, nil
}

func (s roleStore) Revoke(ctx context.Context, revoked domain.RoleAssignment) error {
	if !transacted(ctx) {
		return fmt.Errorf("a role was revoked outside a transaction")
	}
	// "unassign" stands for a revocation that lost the race against another.
	if err := s.fail("unassign"); err != nil {
		return err
	}
	if _, found, _ := s.Find(ctx, revoked.ProductID, revoked.ID); !found {
		return ports.ErrRoleAssignmentGone
	}
	s.roles[revoked.ID] = revoked
	return nil
}

// RolesOf returns the roles of the assignments in force of the principal in
// the product.
func (s roleStore) RolesOf(_ context.Context, productID domain.ProductID, principal domain.Principal) ([]domain.Role, error) {
	if err := s.fail("roles"); err != nil {
		return nil, err
	}
	var roles []domain.Role
	for _, assignment := range s.roles {
		if assignment.InForce() && assignment.ProductID == productID && assignment.Principal == principal {
			roles = append(roles, assignment.Role)
		}
	}
	return roles, nil
}

// List returns a page of the role assignments in force of the product.
func (s roleStore) List(_ context.Context, productID domain.ProductID, page ports.Page) ([]domain.RoleAssignment, error) {
	if err := s.fail("list"); err != nil {
		return nil, err
	}
	s.asked = append(s.asked, page)
	var of []domain.RoleAssignment
	for _, assignment := range s.roles {
		if assignment.InForce() && assignment.ProductID == productID {
			of = append(of, assignment)
		}
	}
	return paged(of, ports.PositionOfRoleAssignment, page)
}

// LockOwners counts the owners in force of the product. Nothing runs beside
// a test, so there is nobody to wait for: what another transaction would
// have committed in the meantime is what the test says.
func (s roleStore) LockOwners(ctx context.Context, productID domain.ProductID) (int, error) {
	if !transacted(ctx) {
		return 0, fmt.Errorf("the owners of a product were locked outside a transaction")
	}
	if err := s.fail("lock"); err != nil {
		return 0, err
	}
	if s.meanwhile != nil {
		// What the other transaction committed stays, whatever becomes of
		// this one. The lock is the first thing a revocation asks for, so
		// nothing of its own is kept with it.
		s.meanwhile()
		s.undo = s.checkpoint()
	}
	owners := 0
	for _, assignment := range s.roles {
		if assignment.InForce() && assignment.ProductID == productID && assignment.Role == domain.RoleProductOwner {
			owners++
		}
	}
	return owners, nil
}

type membershipStore struct{ *world }

func (s membershipStore) Add(ctx context.Context, membership domain.Membership) error {
	if !transacted(ctx) {
		return fmt.Errorf("a membership was written outside a transaction")
	}
	for _, existing := range s.memberships {
		if existing.InForce() && existing.WorkspaceID == membership.WorkspaceID &&
			existing.Principal == membership.Principal && existing.Role == membership.Role {
			return ports.ErrMembershipExists
		}
	}
	s.memberships[membership.ID] = membership
	return nil
}

// Find finds a membership in force, under its own product and workspace.
func (s membershipStore) Find(_ context.Context, productID domain.ProductID, workspaceID domain.WorkspaceID, id domain.MembershipID) (domain.Membership, bool, error) {
	membership, found := s.memberships[id]
	if !found || !membership.InForce() || membership.ProductID != productID || membership.WorkspaceID != workspaceID {
		return domain.Membership{}, false, nil
	}
	return membership, true, nil
}

func (s membershipStore) Revoke(ctx context.Context, revoked domain.Membership) error {
	if !transacted(ctx) {
		return fmt.Errorf("a membership was revoked outside a transaction")
	}
	// "revoke" stands for a revocation that lost the race against another.
	if err := s.fail("revoke"); err != nil {
		return err
	}
	if _, found, _ := s.Find(ctx, revoked.ProductID, revoked.WorkspaceID, revoked.ID); !found {
		return ports.ErrMembershipGone
	}
	s.memberships[revoked.ID] = revoked
	return nil
}

// HeldBy returns the memberships in force of the principal in the product.
func (s membershipStore) HeldBy(_ context.Context, productID domain.ProductID, principal domain.Principal) ([]domain.Membership, error) {
	if err := s.fail("held"); err != nil {
		return nil, err
	}
	var held []domain.Membership
	for _, membership := range s.memberships {
		if membership.InForce() && membership.ProductID == productID && membership.Principal == principal {
			held = append(held, membership)
		}
	}
	return held, nil
}

// List returns a page of the memberships in force of the workspace of the
// product.
func (s membershipStore) List(_ context.Context, productID domain.ProductID, workspaceID domain.WorkspaceID, page ports.Page) ([]domain.Membership, error) {
	if err := s.fail("list"); err != nil {
		return nil, err
	}
	s.asked = append(s.asked, page)
	var of []domain.Membership
	for _, membership := range s.memberships {
		if membership.InForce() && membership.ProductID == productID && membership.WorkspaceID == workspaceID {
			of = append(of, membership)
		}
	}
	return paged(of, ports.PositionOfMembership, page)
}

// refusals are the audit entries of refused attempts.
func (w *world) refusals() []audited {
	var refused []audited
	for _, record := range w.audited {
		if !record.entry.Allowed {
			refused = append(refused, record)
		}
	}
	return refused
}
