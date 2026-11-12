package application

import (
	"context"
	"fmt"
	"maps"
	"slices"
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
	roles      []domain.RoleAssignment
	// memberships holds every membership, in force or revoked, as the table
	// does.
	memberships map[domain.MembershipID]domain.Membership
	audited     []audited
	events      []string

	administrators []domain.Principal
	issuers        []string
	now            time.Time
	nextID         int

	transactions int
	// failing makes the named step fail, to see what a failure leaves behind.
	failing map[string]error
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
	products, workspaces, memberships := maps.Clone(w.products), maps.Clone(w.workspaces), maps.Clone(w.memberships)
	roles, audit, events := slices.Clone(w.roles), slices.Clone(w.audited), slices.Clone(w.events)

	err := fn(context.WithValue(ctx, inTransaction{}, true))
	if err == nil {
		err = w.fail("commit")
	}
	if err != nil {
		w.products, w.workspaces, w.memberships = products, workspaces, memberships
		w.roles, w.audited, w.events = roles, audit, events
	}
	return err
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

type roleStore struct{ *world }

func (s roleStore) Add(ctx context.Context, assignments []domain.RoleAssignment) error {
	if !transacted(ctx) {
		return fmt.Errorf("a role was written outside a transaction")
	}
	s.roles = append(s.roles, assignments...)
	return nil
}

func (s roleStore) RolesOf(_ context.Context, productID domain.ProductID, principal domain.Principal) ([]domain.Role, error) {
	var roles []domain.Role
	for _, assignment := range s.roles {
		if assignment.ProductID == productID && assignment.Principal == principal {
			roles = append(roles, assignment.Role)
		}
	}
	return roles, nil
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
