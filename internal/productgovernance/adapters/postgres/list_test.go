package postgres_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

var (
	carol = domain.Principal{Issuer: "https://issuer.example", Subject: "carol"}
	// The subject alice names somebody else under another issuer.
	otherAlice = domain.Principal{Issuer: "https://other.example", Subject: "alice"}
)

// later is an instant so many minutes after the one everything else in these
// tests is created at.
func later(minutes int) time.Time { return created.Add(time.Duration(minutes) * time.Minute) }

func productAt(t *testing.T, key string, at time.Time) domain.Product {
	t.Helper()

	product, err := domain.NewProduct(domain.ProductID(ids.NewUUID()), key, "CargoPai 货代", cargo, admin, at)
	if err != nil {
		t.Fatalf("build the product: %v", err)
	}
	return product
}

func workspaceAt(t *testing.T, of domain.Product, key string, at time.Time) domain.Workspace {
	t.Helper()

	workspace, err := domain.NewWorkspace(domain.WorkspaceID(ids.NewUUID()), of, key, "API engineering", alice, at)
	if err != nil {
		t.Fatalf("build the workspace: %v", err)
	}
	return workspace
}

func membershipAt(t *testing.T, of domain.Product, in domain.Workspace, member domain.Principal, role domain.Role, at time.Time) domain.Membership {
	t.Helper()

	granted, err := domain.GrantMembership(domain.MembershipID(ids.NewUUID()), of, in, member, role, alice, at)
	if err != nil {
		t.Fatalf("build the membership: %v", err)
	}
	return granted
}

func owningAt(t *testing.T, of domain.Product, principal domain.Principal, at time.Time) domain.RoleAssignment {
	t.Helper()

	granted, err := domain.GrantProductRole(domain.RoleAssignmentID(ids.NewUUID()), of, principal, domain.RoleProductOwner, admin, at)
	if err != nil {
		t.Fatalf("build the role assignment: %v", err)
	}
	return granted
}

// walk reads a list to its end, a page of limit items at a time, each page
// behind the last item of the one before. It returns the pages that held
// something.
func walk[T any](t *testing.T, limit int, position func(T) ports.Position, read func(ports.Page) ([]T, error)) [][]T {
	t.Helper()

	var pages [][]T
	page := ports.Page{Limit: limit}
	for len(pages) < 1000 {
		items, err := read(page)
		if err != nil {
			t.Fatalf("read a page: %v", err)
		}
		if len(items) > limit {
			t.Fatalf("a page of at most %d items holds %d", limit, len(items))
		}
		if len(items) == 0 {
			return pages
		}
		pages = append(pages, items)
		last := position(items[len(items)-1])
		page.After = &last
	}
	t.Fatal("the list does not end")
	return nil
}

// inOrder checks that a list read in pages of every size is want, each item
// once, in pages that are full but for the last.
func inOrder[T comparable](t *testing.T, what string, want []T, position func(T) ports.Position, read func(ports.Page) ([]T, error)) {
	t.Helper()

	for _, limit := range []int{1, 2, 3, len(want), len(want) + 1, 100} {
		if limit < 1 {
			continue
		}
		pages := walk(t, limit, position, read)
		if got := slices.Concat(pages...); !slices.Equal(got, want) {
			t.Errorf("%s, in pages of %d:\n %+v\nwant\n %+v", what, limit, got, want)
		}
		if wantPages := (len(want) + limit - 1) / limit; len(pages) != wantPages {
			t.Errorf("%s, in pages of %d: %d pages hold something, want %d", what, limit, len(pages), wantPages)
		}
		for index, page := range pages {
			if index < len(pages)-1 && len(page) != limit {
				t.Errorf("%s, in pages of %d: page %d holds %d items and is not the last", what, limit, index+1, len(page))
			}
		}
	}
}

// A list is read a page at a time, oldest first, and what was created in one
// instant in the order of its identifiers. Whatever the size of the page,
// every product is read once.
func TestProductsAreListedOldestFirstAPageAtATime(t *testing.T) {
	f := newFixture(t)

	// Seven products, three of them created in the same microsecond.
	var want []domain.Product
	for index, minute := range []int{0, 1, 2, 2, 2, 3, 4} {
		want = append(want, productAt(t, fmt.Sprintf("product-%d", index), later(minute)))
	}
	slices.SortFunc(want[2:5], func(a, b domain.Product) int { return strings.Compare(string(a.ID), string(b.ID)) })
	// They are stored in another order than they are listed in.
	for _, index := range []int{4, 0, 6, 2, 5, 1, 3} {
		f.add(t, want[index])
	}

	inOrder(t, "the products", want, ports.PositionOfProduct, func(page ports.Page) ([]domain.Product, error) {
		return f.products.List(t.Context(), page)
	})

	// What is read is what was stored, timed in UTC to the microsecond: the
	// position of a product is the one the next page is asked with.
	first, err := f.products.List(t.Context(), ports.Page{Limit: 1})
	if err != nil || len(first) != 1 || first[0] != want[0] {
		t.Fatalf("the first product is %+v (%v), want %+v", first, err, want[0])
	}
	if at := ports.PositionOfProduct(first[0]).At; at.Location() != time.UTC || at.Nanosecond() != 123456000 {
		t.Errorf("the product is listed at %s, want UTC to the microsecond", at)
	}
}

// The products of a principal are those it holds something in force in: a
// role in the product, or a membership of one of its workspaces. A product
// in which it holds several things is listed once, and one in which it
// holds only what was revoked is not listed. A principal is its issuer and
// its subject together.
func TestTheProductsOfAPrincipalAreThoseItHoldsSomethingIn(t *testing.T) {
	f := newFixture(t)

	owned, joined, both := productAt(t, "owned", later(1)), productAt(t, "joined", later(2)), productAt(t, "both", later(3))
	disownedOne, left := productAt(t, "disowned", later(4)), productAt(t, "left", later(5))
	namesake, foreign := productAt(t, "namesake", later(6)), productAt(t, "foreign", later(7))
	room := func(of domain.Product, key string) domain.Workspace { return workspaceAt(t, of, key, later(10)) }
	joinedRoom, bothRoom, bothHall, leftRoom := room(joined, "room"), room(both, "room"), room(both, "hall"), room(left, "room")
	f.add(t, owned)
	f.add(t, joined, joinedRoom)
	f.add(t, both, bothRoom, bothHall)
	f.add(t, disownedOne)
	f.add(t, left, leftRoom)
	f.add(t, namesake)
	f.add(t, foreign)

	// alice owns one, is a member in another, and is both in a third, where
	// she holds two roles in one workspace and one in another.
	revokedRole, revokedMembership := owning(t, disownedOne, alice), membership(t, left, leftRoom, alice, domain.RoleInternalAuthor)
	f.assign(t,
		owning(t, owned, alice), owning(t, both, alice), revokedRole,
		// Every product has an owner: bob owns the ones alice does not.
		owning(t, joined, bob), owning(t, disownedOne, bob), owning(t, left, bob), owning(t, foreign, bob),
		// Somebody else is called alice under another issuer.
		owning(t, namesake, otherAlice))
	f.grant(t,
		membership(t, joined, joinedRoom, alice, domain.RoleWorkspaceMaintainer),
		membership(t, both, bothRoom, alice, domain.RoleWorkspaceMaintainer),
		membership(t, both, bothRoom, alice, domain.RoleInternalAuthor),
		membership(t, both, bothHall, alice, domain.RoleInternalAuthor),
		revokedMembership)
	f.unassign(t, disowned(t, revokedRole, bob))
	f.revoke(t, ended(t, revokedMembership, bob))

	listFor := func(principal domain.Principal) func(ports.Page) ([]domain.Product, error) {
		return func(page ports.Page) ([]domain.Product, error) {
			return f.products.ListFor(t.Context(), principal, page)
		}
	}
	inOrder(t, "the products of alice", []domain.Product{owned, joined, both}, ports.PositionOfProduct, listFor(alice))
	inOrder(t, "the products of bob", []domain.Product{joined, disownedOne, left, foreign}, ports.PositionOfProduct, listFor(bob))
	inOrder(t, "the products of the other alice", []domain.Product{namesake}, ports.PositionOfProduct, listFor(otherAlice))
	inOrder(t, "the products of carol", nil, ports.PositionOfProduct, listFor(carol))
	// Every product is still in the list of them all.
	inOrder(t, "the products", []domain.Product{owned, joined, both, disownedOne, left, namesake, foreign},
		ports.PositionOfProduct, func(page ports.Page) ([]domain.Product, error) { return f.products.List(t.Context(), page) })
}

// The workspaces of a product are listed oldest first, and no workspace of
// another product among them. The workspaces of a principal are those of the
// product it is a member of now, each once.
func TestTheWorkspacesOfAProductAreListedAndThoseOfAMemberAmongThem(t *testing.T) {
	f := newFixture(t)

	cargopai, parcel := product(t, "cargopai"), product(t, "parcel")
	var spaces []domain.Workspace
	for index, minute := range []int{1, 2, 2, 3, 4} {
		spaces = append(spaces, workspaceAt(t, cargopai, fmt.Sprintf("space-%d", index), later(minute)))
	}
	slices.SortFunc(spaces[1:3], func(a, b domain.Workspace) int { return strings.Compare(string(a.ID), string(b.ID)) })
	sorting := workspaceAt(t, parcel, "space-0", later(2))
	f.add(t, cargopai, spaces[3], spaces[0], spaces[2], spaces[4], spaces[1])
	f.add(t, parcel, sorting)

	list := func(of domain.Product) func(ports.Page) ([]domain.Workspace, error) {
		return func(page ports.Page) ([]domain.Workspace, error) { return f.workspaces.List(t.Context(), of.ID, page) }
	}
	inOrder(t, "the workspaces of CargoPai", spaces, ports.PositionOfWorkspace, list(cargopai))
	inOrder(t, "the workspaces of Parcel", []domain.Workspace{sorting}, ports.PositionOfWorkspace, list(parcel))
	inOrder(t, "the workspaces of a product that is missing", nil, ports.PositionOfWorkspace, list(product(t, "missing")))

	// bob is a member of two workspaces of CargoPai, in one of them in two
	// roles, was a member of a third, and is a member of the one of Parcel.
	former := membership(t, cargopai, spaces[4], bob, domain.RoleInternalAuthor)
	f.grant(t,
		membership(t, cargopai, spaces[3], bob, domain.RoleWorkspaceMaintainer),
		membership(t, cargopai, spaces[1], bob, domain.RoleWorkspaceMaintainer),
		membership(t, cargopai, spaces[1], bob, domain.RoleInternalAuthor),
		former,
		membership(t, parcel, sorting, bob, domain.RoleInternalAuthor),
		// Others are members where bob is not, and where he is.
		membership(t, cargopai, spaces[0], carol, domain.RoleInternalAuthor),
		membership(t, cargopai, spaces[1], carol, domain.RoleInternalAuthor),
		membership(t, cargopai, spaces[2], domain.Principal{Issuer: otherAlice.Issuer, Subject: bob.Subject}, domain.RoleInternalAuthor))
	f.revoke(t, ended(t, former, alice))

	listFor := func(of domain.Product, principal domain.Principal) func(ports.Page) ([]domain.Workspace, error) {
		return func(page ports.Page) ([]domain.Workspace, error) {
			return f.workspaces.ListFor(t.Context(), of.ID, principal, page)
		}
	}
	inOrder(t, "the workspaces of bob in CargoPai", []domain.Workspace{spaces[1], spaces[3]}, ports.PositionOfWorkspace, listFor(cargopai, bob))
	inOrder(t, "the workspaces of bob in Parcel", []domain.Workspace{sorting}, ports.PositionOfWorkspace, listFor(parcel, bob))
	inOrder(t, "the workspaces of carol in CargoPai", []domain.Workspace{spaces[0], spaces[1]}, ports.PositionOfWorkspace, listFor(cargopai, carol))
	inOrder(t, "the workspaces of carol in Parcel", nil, ports.PositionOfWorkspace, listFor(parcel, carol))
	inOrder(t, "the workspaces of alice in CargoPai", nil, ports.PositionOfWorkspace, listFor(cargopai, alice))
}

// The members of a workspace are listed oldest first: the memberships in
// force of that workspace of that product, and no other. One that was
// revoked is in no list.
func TestTheMembershipsInForceOfAWorkspaceAreListed(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)

	var want []domain.Membership
	for index, minute := range []int{1, 2, 2, 2, 3} {
		member := domain.Principal{Issuer: alice.Issuer, Subject: fmt.Sprintf("member-%d", index)}
		want = append(want, membershipAt(t, s.cargopai, s.engineering, member, domain.RoleInternalAuthor, later(minute)))
	}
	slices.SortFunc(want[1:4], func(a, b domain.Membership) int { return strings.Compare(string(a.ID), string(b.ID)) })
	// bob held a role here and holds another now; mallory holds one in the
	// other workspace of the product, and carol one in another product.
	former := membershipAt(t, s.cargopai, s.engineering, bob, domain.RoleInternalAuthor, later(2))
	current := membershipAt(t, s.cargopai, s.engineering, bob, domain.RoleWorkspaceMaintainer, later(5))
	elsewhere := membershipAt(t, s.cargopai, s.operations, mallory, domain.RoleInternalAuthor, later(2))
	abroad := membershipAt(t, s.parcel, s.sorting, carol, domain.RoleInternalAuthor, later(2))
	f.grant(t, want[4], want[1], former, want[0], elsewhere, want[3], abroad, want[2], current)
	f.revoke(t, ended(t, former, alice))
	want = append(want, current)

	list := func(of domain.Product, in domain.Workspace) func(ports.Page) ([]domain.Membership, error) {
		return func(page ports.Page) ([]domain.Membership, error) {
			return f.memberships.List(t.Context(), of.ID, in.ID, page)
		}
	}
	inOrder(t, "the members of the workspace", want, ports.PositionOfMembership, list(s.cargopai, s.engineering))
	inOrder(t, "the members of the other workspace", []domain.Membership{elsewhere}, ports.PositionOfMembership, list(s.cargopai, s.operations))
	inOrder(t, "the members of the workspace of the other product", []domain.Membership{abroad}, ports.PositionOfMembership, list(s.parcel, s.sorting))
	// A workspace is listed under its own product only.
	inOrder(t, "the members of a workspace under another product", nil, ports.PositionOfMembership, list(s.parcel, s.engineering))
}

// The owners of a product are listed oldest first: the role assignments in
// force of that product, and no other. One that was revoked is in no list.
func TestTheRoleAssignmentsInForceOfAProductAreListed(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)

	var want []domain.RoleAssignment
	for index, minute := range []int{1, 2, 2, 2, 3} {
		owner := domain.Principal{Issuer: alice.Issuer, Subject: fmt.Sprintf("owner-%d", index)}
		want = append(want, owningAt(t, s.cargopai, owner, later(minute)))
	}
	slices.SortFunc(want[1:4], func(a, b domain.RoleAssignment) int { return strings.Compare(string(a.ID), string(b.ID)) })
	// bob owned the product, was removed and was named again; carol owns
	// the other product.
	former, again := owningAt(t, s.cargopai, bob, later(2)), owningAt(t, s.cargopai, bob, later(5))
	abroad := owningAt(t, s.parcel, carol, later(2))
	f.assign(t, want[3], former, want[0], abroad, want[4], want[2], want[1])
	f.unassign(t, disowned(t, former, alice))
	f.assign(t, again)
	want = append(want, again)

	list := func(of domain.Product) func(ports.Page) ([]domain.RoleAssignment, error) {
		return func(page ports.Page) ([]domain.RoleAssignment, error) { return f.roles.List(t.Context(), of.ID, page) }
	}
	inOrder(t, "the owners of the product", want, ports.PositionOfRoleAssignment, list(s.cargopai))
	inOrder(t, "the owners of the other product", []domain.RoleAssignment{abroad}, ports.PositionOfRoleAssignment, list(s.parcel))
	inOrder(t, "the owners of a product that is missing", nil, ports.PositionOfRoleAssignment, list(product(t, "missing")))
}

// A page starts behind a position, wherever the rows around that position
// are by then. What is added behind it is read by a later page; what is
// added before it is not; and a row that ended since, the one at the
// position included, leaves the rest in place.
func TestAPageStartsBehindAPositionWhateverChangedAroundIt(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)

	var granted []domain.Membership
	for index := range 5 {
		member := domain.Principal{Issuer: alice.Issuer, Subject: fmt.Sprintf("member-%d", index)}
		granted = append(granted, membershipAt(t, s.cargopai, s.engineering, member, domain.RoleInternalAuthor, later(10*(index+1))))
	}
	f.grant(t, granted...)
	read := func(page ports.Page) []domain.Membership {
		t.Helper()

		memberships, err := f.memberships.List(t.Context(), s.cargopai.ID, s.engineering.ID, page)
		if err != nil {
			t.Fatalf("list the memberships: %v", err)
		}
		return memberships
	}

	first := read(ports.Page{Limit: 2})
	if !slices.Equal(first, granted[:2]) {
		t.Fatalf("the first page is %+v, want %+v", first, granted[:2])
	}
	position := ports.PositionOfMembership(first[1])

	// Between the two pages: the membership at the position ends, and so
	// does the one behind it; one is granted before the position, one right
	// behind it and one at the end.
	early := membershipAt(t, s.cargopai, s.engineering, bob, domain.RoleInternalAuthor, later(15))
	next := membershipAt(t, s.cargopai, s.engineering, carol, domain.RoleInternalAuthor, later(25))
	last := membershipAt(t, s.cargopai, s.engineering, mallory, domain.RoleInternalAuthor, later(60))
	f.revoke(t, ended(t, granted[1], alice))
	f.revoke(t, ended(t, granted[2], alice))
	f.grant(t, early, next, last)

	want := []domain.Membership{next, granted[3], granted[4], last}
	if rest := read(ports.Page{After: &position, Limit: 10}); !slices.Equal(rest, want) {
		t.Errorf("behind the position the list holds\n %+v\nwant\n %+v", rest, want)
	}
	// Read again from the start, the list is what is in force now.
	if all := read(ports.Page{Limit: 10}); !slices.Equal(all, []domain.Membership{granted[0], early, next, granted[3], granted[4], last}) {
		t.Errorf("from the start the list holds %+v", all)
	}

	// Behind the last item there is nothing, and behind a position that
	// precedes them all there is everything.
	end := ports.PositionOfMembership(last)
	if beyond := read(ports.Page{After: &end, Limit: 10}); len(beyond) != 0 {
		t.Errorf("behind the last membership the list holds %+v, want nothing", beyond)
	}
	if all := read(ports.Page{After: &ports.Position{At: created.Add(-time.Hour), ID: "ffffffff-ffff-4fff-bfff-ffffffffffff"}, Limit: 10}); len(all) != 6 {
		t.Errorf("behind a position before them all the list holds %d memberships, want 6", len(all))
	}
	// Of two rows of one instant, the position names one: the identifier
	// decides which side of it the other is on.
	same := ports.Position{At: end.At, ID: "00000000-0000-4000-8000-000000000000"}
	if behind := read(ports.Page{After: &same, Limit: 10}); !slices.Equal(behind, []domain.Membership{last}) {
		t.Errorf("behind a smaller identifier of the same instant the list holds %+v, want the last membership", behind)
	}
	same.ID = "ffffffff-ffff-4fff-bfff-ffffffffffff"
	if behind := read(ports.Page{After: &same, Limit: 10}); len(behind) != 0 {
		t.Errorf("behind a greater identifier of the same instant the list holds %+v, want nothing", behind)
	}
}

// A page takes at least one row: a store asked for less sends nothing to the
// database. Inside a transaction a list holds what that transaction wrote.
func TestAListTakesAPageOfAtLeastOneRowAndReadsInsideATransaction(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)

	for _, limit := range []int{0, -1} {
		page := ports.Page{Limit: limit}
		_, errProducts := f.products.List(t.Context(), page)
		_, errProductsFor := f.products.ListFor(t.Context(), alice, page)
		_, errWorkspaces := f.workspaces.List(t.Context(), s.cargopai.ID, page)
		_, errWorkspacesFor := f.workspaces.ListFor(t.Context(), s.cargopai.ID, alice, page)
		_, errMemberships := f.memberships.List(t.Context(), s.cargopai.ID, s.engineering.ID, page)
		_, errRoles := f.roles.List(t.Context(), s.cargopai.ID, page)
		for list, err := range map[string]error{
			"products": errProducts, "products of a principal": errProductsFor,
			"workspaces": errWorkspaces, "workspaces of a principal": errWorkspacesFor,
			"memberships": errMemberships, "role assignments": errRoles,
		} {
			if err == nil || !strings.Contains(err.Error(), "at least one row") {
				t.Errorf("a page of %d %s: got %v, want it refused", limit, list, err)
			}
		}
	}

	express := product(t, "express")
	hub := workspace(t, express, "hub")
	f.within(t, func(ctx context.Context) error {
		if err := f.products.Add(ctx, express); err != nil {
			return err
		}
		if err := f.workspaces.Add(ctx, hub); err != nil {
			return err
		}
		if err := f.roles.Add(ctx, owning(t, express, alice)); err != nil {
			return err
		}
		if err := f.memberships.Add(ctx, membership(t, express, hub, alice, domain.RoleInternalAuthor)); err != nil {
			return err
		}
		page := ports.Page{Limit: 10}
		products, errProducts := f.products.ListFor(ctx, alice, page)
		workspaces, errWorkspaces := f.workspaces.ListFor(ctx, express.ID, alice, page)
		memberships, errMemberships := f.memberships.List(ctx, express.ID, hub.ID, page)
		roles, errRoles := f.roles.List(ctx, express.ID, page)
		if errProducts != nil || errWorkspaces != nil || errMemberships != nil || errRoles != nil ||
			!slices.Equal(products, []domain.Product{express}) || !slices.Equal(workspaces, []domain.Workspace{hub}) ||
			len(memberships) != 1 || len(roles) != 1 {
			t.Errorf("inside its transaction the lists hold %v, %v, %v and %v (%v, %v, %v, %v)",
				products, workspaces, memberships, roles, errProducts, errWorkspaces, errMemberships, errRoles)
		}
		return nil
	})
}
