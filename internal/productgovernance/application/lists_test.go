package application

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

func (s team) listProducts(caller domain.Principal, size int, after *ports.Position) (Page[domain.Product], error) {
	return s.service.ListProducts(ctx, ListProducts{Caller: caller, CorrelationID: "correlation-11", PageSize: size, After: after})
}

func (s team) listWorkspaces(caller domain.Principal, product domain.Product, size int, after *ports.Position) (Page[domain.Workspace], error) {
	return s.service.ListWorkspaces(ctx, ListWorkspaces{
		Caller: caller, CorrelationID: "correlation-12", ProductID: string(product.ID), PageSize: size, After: after,
	})
}

func (s team) listMemberships(caller domain.Principal, workspace domain.Workspace, size int, after *ports.Position) (Page[domain.Membership], error) {
	return s.service.ListMemberships(ctx, ListMemberships{
		Caller: caller, CorrelationID: "correlation-13", ProductID: string(workspace.ProductID), WorkspaceID: string(workspace.ID),
		PageSize: size, After: after,
	})
}

func (s team) listRoles(caller domain.Principal, product domain.Product, size int, after *ports.Position) (Page[domain.RoleAssignment], error) {
	return s.service.ListProductRoles(ctx, ListProductRoles{
		Caller: caller, CorrelationID: "correlation-14", ProductID: string(product.ID), PageSize: size, After: after,
	})
}

// whole reads a list to its end, a page of size items at a time, and returns
// its pages.
func whole[T any](t *testing.T, size int, read func(size int, after *ports.Position) (Page[T], error)) [][]T {
	t.Helper()

	var pages [][]T
	var after *ports.Position
	for len(pages) < 100 {
		page, err := read(size, after)
		if err != nil {
			t.Fatalf("read a page: %v", err)
		}
		pages = append(pages, page.Items)
		if page.Next == nil {
			return pages
		}
		after = page.Next
	}
	t.Fatal("the list does not end")
	return nil
}

// untouched checks that what a test did left no audit record, no event and
// no transaction: a list that is answered changes nothing and records
// nothing.
func (s team) untouched(t *testing.T) {
	t.Helper()

	if len(s.w.audited) != 0 || len(s.w.events) != 0 || s.w.transactions != 0 {
		t.Errorf("the lists left the audit entries %+v, the events %v and %d transactions, want none",
			s.w.audited, s.w.events, s.w.transactions)
	}
}

// Every caller is answered: a platform administrator with every product,
// anybody else with the products it holds something in force in.
func TestListProductsGivesEachCallerTheProductsItMayRead(t *testing.T) {
	s := newTeam(t)

	for name, c := range map[string]struct {
		caller domain.Principal
		want   []domain.Product
	}{
		"the platform administrator":               {admin, []domain.Product{s.cargopai, s.parcel}},
		"an owner":                                 {alice, []domain.Product{s.cargopai}},
		"the owner of the other product":           {bob, []domain.Product{s.parcel}},
		"a member of a workspace":                  {carol, []domain.Product{s.cargopai}},
		"a member of another workspace":            {dave, []domain.Product{s.cargopai}},
		"whoever was a member and is not any more": {erin, nil},
		"whoever holds nothing":                    {mallory, nil},
	} {
		page, err := s.listProducts(c.caller, 0, nil)
		if err != nil || !slices.Equal(page.Items, c.want) || page.Next != nil {
			t.Errorf("%s: got %+v and %v, want %+v and nothing to follow", name, page, err, c.want)
		}
	}
	s.untouched(t)

	// What a caller holds in two products lists both, and an administrator
	// that owns a product is still answered as one.
	joined(t, s.service, s.sorting, bob, carol, domain.RoleInternalAuthor)
	named(t, s.service, s.parcel, bob, admin)
	s.w.quiet()
	if page, err := s.listProducts(carol, 0, nil); err != nil || !slices.Equal(page.Items, []domain.Product{s.cargopai, s.parcel}) {
		t.Errorf("a member in two products: got %+v and %v, want both", page, err)
	}
	if page, err := s.listProducts(admin, 0, nil); err != nil || !slices.Equal(page.Items, []domain.Product{s.cargopai, s.parcel}) {
		t.Errorf("an administrator that owns a product: got %+v and %v, want every product", page, err)
	}

	// What the caller holds is read with every page: what was revoked
	// lists nothing any more.
	if err := s.revoke(alice, s.engineering, s.maintainer.ID); err != nil {
		t.Fatalf("revoke the membership of carol: %v", err)
	}
	s.w.quiet()
	if page, err := s.listProducts(carol, 0, nil); err != nil || !slices.Equal(page.Items, []domain.Product{s.parcel}) {
		t.Errorf("once her membership in CargoPai is revoked: got %+v and %v, want Parcel alone", page, err)
	}
	s.untouched(t)
}

// A list is read a page at a time, oldest first. Every page but the last is
// full and says where the next one starts; the last one says nothing, also
// when it is full. The store is asked for one item more than the page
// holds, which is how a page knows whether another follows.
func TestAListIsReadAPageAtATimeAndSaysWhetherAnotherFollows(t *testing.T) {
	s := newTeam(t)
	want := []domain.Product{s.cargopai, s.parcel}
	for index := range 5 {
		s.w.now = started.Add(time.Duration(index+1) * time.Minute)
		want = append(want, registered(t, s.service, fmt.Sprintf("product-%d", index), alice))
	}
	s.w.quiet()
	read := func(size int, after *ports.Position) (Page[domain.Product], error) {
		return s.listProducts(admin, size, after)
	}

	for _, size := range []int{1, 2, 3, 6, 7, 8, MaxPageSize} {
		s.w.asked = nil
		pages := whole(t, size, read)
		if got := slices.Concat(pages...); !slices.Equal(got, want) {
			t.Errorf("in pages of %d the products are\n %+v\nwant\n %+v", size, got, want)
		}
		if wantPages := (len(want) + size - 1) / size; len(pages) != wantPages {
			t.Errorf("in pages of %d the list has %d pages, want %d", size, len(pages), wantPages)
		}
		for index, page := range pages[:len(pages)-1] {
			if len(page) != size {
				t.Errorf("in pages of %d, page %d holds %d products and is not the last", size, index+1, len(page))
			}
		}
		// Each page was read behind the last product of the one before,
		// one product more than it holds.
		if len(s.w.asked) != len(pages) {
			t.Fatalf("in pages of %d the store was asked %d times for %d pages", size, len(s.w.asked), len(pages))
		}
		for index, asked := range s.w.asked {
			var behind *ports.Position
			if index > 0 {
				last := ports.PositionOfProduct(pages[index-1][len(pages[index-1])-1])
				behind = &last
			}
			if asked.Limit != size+1 || (asked.After == nil) != (behind == nil) || (behind != nil && *asked.After != *behind) {
				t.Errorf("in pages of %d, page %d was asked as %+v (behind %+v), want %d items behind %+v",
					size, index+1, asked, asked.After, size+1, behind)
			}
		}
	}

	// No size asks for the default one.
	s.w.asked = nil
	if page, err := read(0, nil); err != nil || len(page.Items) != len(want) || page.Next != nil {
		t.Errorf("without a size: got %+v and %v, want every product", page, err)
	}
	if len(s.w.asked) != 1 || s.w.asked[0].Limit != DefaultPageSize+1 {
		t.Errorf("without a size the store was asked %+v, want %d items", s.w.asked, DefaultPageSize+1)
	}
	s.untouched(t)
}

// What a request says of the page is checked before a store is asked: a
// size out of range and a position that is none of the list are refused as
// values that are not valid, whichever list is read.
func TestAListRefusesAPageItCannotBe(t *testing.T) {
	s := newBoard(t)
	position := map[string]ports.Position{
		"products": ports.PositionOfProduct(s.cargopai), "workspaces": ports.PositionOfWorkspace(s.engineering),
		"memberships": ports.PositionOfMembership(s.maintainer), "role assignments": ports.PositionOfRoleAssignment(s.first),
	}
	lists := map[string]func(size int, after *ports.Position) (int, error){
		"products": func(size int, after *ports.Position) (int, error) {
			page, err := s.listProducts(admin, size, after)
			return len(page.Items), err
		},
		"workspaces": func(size int, after *ports.Position) (int, error) {
			page, err := s.listWorkspaces(alice, s.cargopai, size, after)
			return len(page.Items), err
		},
		"memberships": func(size int, after *ports.Position) (int, error) {
			page, err := s.listMemberships(alice, s.engineering, size, after)
			return len(page.Items), err
		},
		"role assignments": func(size int, after *ports.Position) (int, error) {
			page, err := s.listRoles(alice, s.cargopai, size, after)
			return len(page.Items), err
		},
	}

	for name, list := range lists {
		valid := position[name]
		refused := map[string]struct {
			size  int
			after *ports.Position
		}{
			"a size below one":                              {-1, nil},
			"a size far below one":                          {-MaxPageSize, nil},
			"a size above the largest":                      {MaxPageSize + 1, nil},
			"a size far above the largest":                  {1 << 30, nil},
			"a size out of range behind a position":         {MaxPageSize + 1, &valid},
			"a position whose identifier is none":           {1, &ports.Position{At: valid.At, ID: "cargopai"}},
			"a position without an identifier":              {1, &ports.Position{At: valid.At}},
			"a position whose identifier is too long":       {1, &ports.Position{At: valid.At, ID: valid.ID + "0"}},
			"a position whose identifier is in upper case":  {1, &ports.Position{At: valid.At, ID: "00000000-0000-4000-8000-00000000000A"}},
			"a position without an instant":                 {1, &ports.Position{ID: valid.ID}},
			"a position that is empty, and no size":         {0, &ports.Position{}},
			"a position that is none, and the largest size": {MaxPageSize, &ports.Position{At: valid.At, ID: "x"}},
		}
		for what, c := range refused {
			s.w.asked = nil
			items, err := list(c.size, c.after)
			if !errors.Is(err, domain.ErrInvalid) || items != 0 || len(s.w.asked) != 0 {
				t.Errorf("%s, %s: got %d items and %v, and the store was asked %+v; want %v and no read",
					name, what, items, err, s.w.asked, domain.ErrInvalid)
			}
		}

		// The bounds themselves are sizes like any other, and a position of
		// the list is one wherever it is: behind the first item, or behind
		// one that is not there.
		for what, c := range map[string]struct {
			size  int
			after *ports.Position
		}{
			"one item":                         {1, nil},
			"the largest size":                 {MaxPageSize, nil},
			"behind an item":                   {1, &valid},
			"behind an item that is not there": {MaxPageSize, &ports.Position{At: valid.At.Add(time.Hour), ID: missingID}},
		} {
			s.w.asked = nil
			if _, err := list(c.size, c.after); err != nil || len(s.w.asked) != 1 || s.w.asked[0].Limit != c.size+1 {
				t.Errorf("%s, %s: got %v and the store was asked %+v, want one read of %d items", name, what, err, s.w.asked, c.size+1)
			}
		}
	}
	s.untouched(t)

	// The position a store is asked with is the one the caller gave, and
	// not the caller's own: changing that one afterwards changes nothing.
	given := ports.PositionOfWorkspace(s.engineering)
	s.w.asked = nil
	page, err := s.listWorkspaces(alice, s.cargopai, 1, &given)
	given.ID = "changed"
	if err != nil || !slices.Equal(page.Items, []domain.Workspace{s.operations}) || page.Next != nil ||
		*s.w.asked[0].After != ports.PositionOfWorkspace(s.engineering) {
		t.Errorf("behind the first workspace: got %+v and %v, asked %+v; want the second one, and nothing to follow", page, err, s.w.asked)
	}
}

// An owner of the product reads every workspace of it, and a member the
// workspaces it is a member of. It is not told that there are others.
func TestListWorkspacesGivesAnOwnerAllOfThemAndAMemberItsOwn(t *testing.T) {
	s := newTeam(t)

	check := func(what string, caller domain.Principal, product domain.Product, want ...domain.Workspace) {
		t.Helper()

		page, err := s.listWorkspaces(caller, product, 0, nil)
		if err != nil || !slices.Equal(page.Items, want) || page.Next != nil {
			t.Errorf("%s: got %+v and %v, want %+v and nothing to follow", what, page, err, want)
		}
	}
	check("an owner", alice, s.cargopai, s.engineering, s.operations)
	check("the owner of the other product", bob, s.parcel, s.sorting)
	check("a member of the first workspace", carol, s.cargopai, s.engineering)
	check("a member of the second workspace", dave, s.cargopai, s.operations)
	s.untouched(t)

	// A member of both reads both, in the order of the list, a page at a
	// time. A platform administrator that is a member of one reads that one:
	// what it holds counts, not what it administers.
	joined(t, s.service, s.operations, alice, carol, domain.RoleInternalAuthor)
	joined(t, s.service, s.operations, alice, admin, domain.RoleWorkspaceMaintainer)
	s.w.quiet()
	check("a member of both workspaces", carol, s.cargopai, s.engineering, s.operations)
	check("a platform administrator that is a member of one", admin, s.cargopai, s.operations)
	first, err := s.listWorkspaces(carol, s.cargopai, 1, nil)
	if err != nil || !slices.Equal(first.Items, []domain.Workspace{s.engineering}) || first.Next == nil ||
		*first.Next != ports.PositionOfWorkspace(s.engineering) {
		t.Fatalf("the first page of a member of both: got %+v and %v, want the first workspace and where the next page starts", first, err)
	}
	if second, err := s.listWorkspaces(carol, s.cargopai, 1, first.Next); err != nil ||
		!slices.Equal(second.Items, []domain.Workspace{s.operations}) || second.Next != nil {
		t.Errorf("the second page of a member of both: got %+v and %v, want the second workspace and nothing to follow", second, err)
	}

	// A product that is not there has no list, whoever asks, and that is
	// no refusal.
	for name, productID := range map[string]string{"a product that does not exist": missingID, "a product that is no identifier": "cargopai"} {
		for _, caller := range []domain.Principal{alice, admin, mallory} {
			page, err := s.service.ListWorkspaces(ctx, ListWorkspaces{Caller: caller, CorrelationID: "correlation-12", ProductID: productID})
			if err != ErrProductNotFound || page.Items != nil || page.Next != nil {
				t.Errorf("%s, asked by %s: got %+v and %v, want nothing and exactly %v", name, caller.Subject, page, err, ErrProductNotFound)
			}
		}
	}
	s.untouched(t)
}

// Whoever holds nothing in the product reads none of its workspaces. A
// stranger is told there is no such product; a platform administrator, who
// knows the product, that it may not. The answer is the same whatever the
// request says of the page, and no store is asked for a list.
func TestListWorkspacesIsRefusedToWhoeverHoldsNothingInTheProduct(t *testing.T) {
	cases := map[string]answered{
		"the platform administrator":               {admin, ErrForbidden},
		"a stranger":                               {mallory, ErrProductNotFound},
		"the owner of another product":             {bob, ErrProductNotFound},
		"whoever was a member and is not any more": {erin, ErrProductNotFound},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newTeam(t)

			for what, request := range map[string]struct {
				size  int
				after *ports.Position
			}{
				"the first page":             {0, nil},
				"a page it cannot be":        {MaxPageSize + 1, nil},
				"a position that is none":    {1, &ports.Position{ID: "x"}},
				"a page behind a workspace":  {1, &ports.Position{At: started, ID: string(s.engineering.ID)}},
				"a page of the largest size": {MaxPageSize, nil},
			} {
				s.w.quiet()
				s.w.asked = nil
				page, err := s.listWorkspaces(c.caller, s.cargopai, request.size, request.after)
				if err != c.want || page.Items != nil || page.Next != nil {
					t.Fatalf("%s: got %+v and %v, want nothing and exactly %v", what, page, err, c.want)
				}
				want := []audited{{ports.AuditEntry{
					Actor: c.caller, Action: "workspace.read", Rule: "DENY-03",
					ProductID: s.cargopai.ID, ResourceType: "Workspace", CorrelationID: "correlation-12",
				}, false}}
				if !slices.Equal(s.w.audited, want) || len(s.w.asked) != 0 {
					t.Errorf("%s: audited\n %+v\nand the store was asked %+v; want\n %+v\nand no read", what, s.w.audited, s.w.asked, want)
				}
			}
		})
	}
}

// The members of a workspace are the owners' to read.
func TestListMembershipsAnswersAnOwnerOfTheProduct(t *testing.T) {
	s := newTeam(t)
	// bob joins engineering later, and erin, whose first membership was
	// revoked, again after him.
	s.w.now = started.Add(time.Hour)
	later := joined(t, s.service, s.engineering, alice, bob, domain.RoleInternalAuthor)
	s.w.now = started.Add(2 * time.Hour)
	again := joined(t, s.service, s.engineering, alice, erin, domain.RoleWorkspaceMaintainer)
	s.w.quiet()

	check := func(what string, caller domain.Principal, workspace domain.Workspace, want ...domain.Membership) {
		t.Helper()

		page, err := s.listMemberships(caller, workspace, 0, nil)
		if err != nil || !slices.Equal(page.Items, want) || page.Next != nil {
			t.Errorf("%s: got %+v and %v, want %+v and nothing to follow", what, page, err, want)
		}
	}
	// The membership erin held first was revoked: it is in no list.
	check("the first workspace", alice, s.engineering, s.maintainer, later, again)
	check("the second workspace", alice, s.operations, s.author)
	check("the workspace of the other product", bob, s.sorting)
	pages := whole(t, 2, func(size int, after *ports.Position) (Page[domain.Membership], error) {
		return s.listMemberships(alice, s.engineering, size, after)
	})
	if len(pages) != 2 || !slices.Equal(slices.Concat(pages...), []domain.Membership{s.maintainer, later, again}) {
		t.Errorf("in pages of two the members are %+v, want the three of them in two pages", pages)
	}

	// A workspace that is not there, or not one of the product, has no
	// list. That is no refusal.
	list := func(productID, workspaceID string) error {
		page, err := s.service.ListMemberships(ctx, ListMemberships{
			Caller: alice, CorrelationID: "correlation-13", ProductID: productID, WorkspaceID: workspaceID,
		})
		if page.Items != nil || page.Next != nil {
			t.Errorf("a list that is not there was returned: %+v", page)
		}
		return err
	}
	cargopai := string(s.cargopai.ID)
	for name, c := range map[string]struct {
		err  error
		want error
	}{
		"a workspace that does not exist":   {list(cargopai, missingID), ErrWorkspaceNotFound},
		"a workspace of another product":    {list(cargopai, string(s.sorting.ID)), ErrWorkspaceNotFound},
		"a workspace that is no identifier": {list(cargopai, "api-engineering"), ErrWorkspaceNotFound},
		"a product that does not exist":     {list(missingID, string(s.engineering.ID)), ErrProductNotFound},
		"a product that is no identifier":   {list("cargopai", string(s.engineering.ID)), ErrProductNotFound},
	} {
		if c.err != c.want {
			t.Errorf("%s: got %v, want exactly %v", name, c.err, c.want)
		}
	}
	s.untouched(t)
}

// Whoever does not own the product reads no list of members, and is answered
// as far as it may know. A member of the workspace knows it has members, and
// is told it may not read them. Whoever may not read the workspace is
// answered the same whether or not the workspace exists.
func TestListMembershipsIsWithheldFromWhoeverDoesNotOwnTheProduct(t *testing.T) {
	cases := map[string]struct {
		caller domain.Principal
		// want is the answer about the first workspace of CargoPai, and
		// elsewhere the one about every other workspace named under it.
		want, elsewhere error
	}{
		"a stranger":                               {mallory, ErrProductNotFound, ErrProductNotFound},
		"the owner of another product":             {bob, ErrProductNotFound, ErrProductNotFound},
		"whoever was a member and is not any more": {erin, ErrProductNotFound, ErrProductNotFound},
		// They know the product and may not read this workspace.
		"the platform administrator":    {admin, ErrWorkspaceNotFound, ErrWorkspaceNotFound},
		"a member of another workspace": {dave, ErrWorkspaceNotFound, ErrWorkspaceNotFound},
		// carol is a member of the workspace, and of no other.
		"a member of the workspace": {carol, ErrForbidden, ErrWorkspaceNotFound},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newTeam(t)

			for what, asked := range map[string]struct {
				workspaceID string
				want        error
			}{
				"the workspace":                     {string(s.engineering.ID), c.want},
				"a workspace that does not exist":   {missingID, c.elsewhere},
				"a workspace of another product":    {string(s.sorting.ID), c.elsewhere},
				"a workspace that is no identifier": {"api-engineering", c.elsewhere},
			} {
				s.w.quiet()
				s.w.asked = nil
				page, err := s.service.ListMemberships(ctx, ListMemberships{
					Caller: c.caller, CorrelationID: "correlation-13", ProductID: string(s.cargopai.ID), WorkspaceID: asked.workspaceID,
					PageSize: MaxPageSize + 1,
				})
				if err != asked.want || page.Items != nil || page.Next != nil {
					t.Fatalf("%s: got %+v and %v, want nothing and exactly %v", what, page, err, asked.want)
				}
				want := []audited{{ports.AuditEntry{
					Actor: c.caller, Action: "member.read", Rule: "DENY-03",
					ProductID: s.cargopai.ID, ResourceType: "Member", CorrelationID: "correlation-13",
				}, false}}
				if !slices.Equal(s.w.audited, want) || len(s.w.asked) != 0 {
					t.Errorf("%s: audited\n %+v\nand the store was asked %+v; want\n %+v\nand no read", what, s.w.audited, s.w.asked, want)
				}
			}
		})
	}

	// dave, a member of the second workspace, is told he may not read the
	// members of that one.
	s := newTeam(t)
	if _, err := s.listMemberships(dave, s.operations, 0, nil); err != ErrForbidden {
		t.Errorf("a member lists the members of its own workspace: got %v, want exactly %v", err, ErrForbidden)
	}
}

// Who owns a product is the owners' to read. It is how an owner finds the
// assignment of another one: that of an owner named when the product was
// registered is in no other answer.
func TestListProductRolesAnswersAnOwnerOfTheProduct(t *testing.T) {
	s := newBoard(t)

	check := func(what string, caller domain.Principal, product domain.Product, want ...domain.RoleAssignment) {
		t.Helper()

		page, err := s.listRoles(caller, product, 0, nil)
		if err != nil || !slices.Equal(page.Items, want) || page.Next != nil {
			t.Errorf("%s: got %+v and %v, want %+v and nothing to follow", what, page, err, want)
		}
	}
	// The assignment grace held was revoked: it is in no list.
	check("the first owner", alice, s.cargopai, s.first, s.second)
	check("the owner she named", frank, s.cargopai, s.first, s.second)
	check("the owner of the other product", bob, s.parcel, s.elsewhere)
	first, err := s.listRoles(alice, s.cargopai, 1, nil)
	if err != nil || !slices.Equal(first.Items, []domain.RoleAssignment{s.first}) || first.Next == nil ||
		*first.Next != ports.PositionOfRoleAssignment(s.first) {
		t.Fatalf("the first page: got %+v and %v, want the first owner and where the next page starts", first, err)
	}
	if second, err := s.listRoles(alice, s.cargopai, 1, first.Next); err != nil ||
		!slices.Equal(second.Items, []domain.RoleAssignment{s.second}) || second.Next != nil {
		t.Errorf("the second page: got %+v and %v, want the second owner and nothing to follow", second, err)
	}
	for name, productID := range map[string]string{"a product that does not exist": missingID, "a product that is no identifier": "cargopai"} {
		page, err := s.service.ListProductRoles(ctx, ListProductRoles{Caller: alice, CorrelationID: "correlation-14", ProductID: productID})
		if err != ErrProductNotFound || page.Items != nil || page.Next != nil {
			t.Errorf("%s: got %+v and %v, want nothing and exactly %v", name, page, err, ErrProductNotFound)
		}
	}
	s.untouched(t)

	// The owner the product was registered with is removed by the one who
	// was named since, who finds her assignment in the list and nowhere
	// else.
	page, err := s.listRoles(frank, s.cargopai, 0, nil)
	if err != nil {
		t.Fatalf("list the owners: %v", err)
	}
	found := slices.IndexFunc(page.Items, func(assignment domain.RoleAssignment) bool { return assignment.Principal == alice })
	if found < 0 {
		t.Fatalf("the first owner is not among the owners: %+v", page.Items)
	}
	if err := s.revokeRole(frank, s.cargopai, page.Items[found].ID); err != nil {
		t.Fatalf("remove the owner found in the list: %v", err)
	}
	s.w.quiet()
	check("once the first owner is removed", frank, s.cargopai, s.second)
}

// Whoever does not own the product reads no list of its owners, and is
// answered as far as it may know: there is no such product to a stranger,
// and whoever knows the product may not. A platform administrator names an
// owner and reads none.
func TestListProductRolesIsWithheldFromWhoeverDoesNotOwnTheProduct(t *testing.T) {
	cases := map[string]answered{
		"a stranger":                               {mallory, ErrProductNotFound},
		"the owner of another product":             {bob, ErrProductNotFound},
		"whoever was a member and is not any more": {erin, ErrProductNotFound},
		"whoever was an owner and is not any more": {grace, ErrProductNotFound},
		"the platform administrator":               {admin, ErrForbidden},
		"a member of a workspace":                  {carol, ErrForbidden},
		"a member of another workspace":            {dave, ErrForbidden},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newBoard(t)

			for what, request := range map[string]struct {
				size  int
				after *ports.Position
			}{
				"the first page":          {0, nil},
				"a page it cannot be":     {MaxPageSize + 1, nil},
				"a position that is none": {1, &ports.Position{ID: "x"}},
				"a page behind an owner":  {1, &ports.Position{At: started, ID: string(s.first.ID)}},
			} {
				s.w.quiet()
				s.w.asked = nil
				page, err := s.listRoles(c.caller, s.cargopai, request.size, request.after)
				if err != c.want || page.Items != nil || page.Next != nil {
					t.Fatalf("%s: got %+v and %v, want nothing and exactly %v", what, page, err, c.want)
				}
				want := []audited{{ports.AuditEntry{
					Actor: c.caller, Action: "product_role.read", Rule: "DENY-03",
					ProductID: s.cargopai.ID, ResourceType: "RoleAssignment", CorrelationID: "correlation-14",
				}, false}}
				if !slices.Equal(s.w.audited, want) || len(s.w.asked) != 0 {
					t.Errorf("%s: audited\n %+v\nand the store was asked %+v; want\n %+v\nand no read", what, s.w.audited, s.w.asked, want)
				}
			}
		})
	}
}

// What is revoked lists nothing from that moment on: every list reads what
// the caller holds when it is asked. An owner whose role is revoked is a
// stranger to the product if it holds nothing else there, and a member, no
// more, if it is still one.
func TestWhatIsRevokedLeavesTheListsAtOnce(t *testing.T) {
	s := newBoard(t)
	// frank owns CargoPai and is a member of nothing; grace is named again
	// and is a member of the first workspace as well.
	named(t, s.service, s.cargopai, alice, grace)
	joined(t, s.service, s.engineering, alice, grace, domain.RoleInternalAuthor)

	lists := func(caller domain.Principal) (products []domain.Product, workspaces []domain.Workspace, errs map[string]error) {
		t.Helper()

		listed, errProducts := s.listProducts(caller, 0, nil)
		spaces, errWorkspaces := s.listWorkspaces(caller, s.cargopai, 0, nil)
		_, errMembers := s.listMemberships(caller, s.engineering, 0, nil)
		_, errOwners := s.listRoles(caller, s.cargopai, 0, nil)
		return listed.Items, spaces.Items, map[string]error{
			"ListProducts": errProducts, "ListWorkspaces": errWorkspaces, "ListMemberships": errMembers, "ListProductRoles": errOwners,
		}
	}
	for _, owner := range []domain.Principal{frank, grace} {
		products, workspaces, errs := lists(owner)
		if !slices.Equal(products, []domain.Product{s.cargopai}) || !slices.Equal(workspaces, []domain.Workspace{s.engineering, s.operations}) ||
			!maps.Equal(errs, map[string]error{"ListProducts": nil, "ListWorkspaces": nil, "ListMemberships": nil, "ListProductRoles": nil}) {
			t.Fatalf("%s, an owner, lists the products %+v and the workspaces %+v, and is answered %v", owner.Subject, products, workspaces, errs)
		}
	}

	for _, owner := range []domain.Principal{frank, grace} {
		if err := s.revokeRole(alice, s.cargopai, s.w.ownerOf(t, s.cargopai, owner).ID); err != nil {
			t.Fatalf("revoke the role of %s: %v", owner.Subject, err)
		}
	}

	// frank holds nothing else: no product is listed, and the product is
	// not there for the lists of what is inside it.
	products, workspaces, errs := lists(frank)
	if len(products) != 0 || workspaces != nil || !maps.Equal(errs, map[string]error{
		"ListProducts": nil, "ListWorkspaces": ErrProductNotFound, "ListMemberships": ErrProductNotFound, "ListProductRoles": ErrProductNotFound,
	}) {
		t.Errorf("frank, once revoked, lists the products %+v and the workspaces %+v, and is answered %v", products, workspaces, errs)
	}
	// grace is still a member of the first workspace: she lists the
	// product and that workspace, and no member and no owner.
	products, workspaces, errs = lists(grace)
	if !slices.Equal(products, []domain.Product{s.cargopai}) || !slices.Equal(workspaces, []domain.Workspace{s.engineering}) || !maps.Equal(errs, map[string]error{
		"ListProducts": nil, "ListWorkspaces": nil, "ListMemberships": ErrForbidden, "ListProductRoles": ErrForbidden,
	}) {
		t.Errorf("grace, a member once revoked as an owner, lists the products %+v and the workspaces %+v, and is answered %v", products, workspaces, errs)
	}

	// Named again, frank lists as an owner again: what counts is what is
	// in force.
	named(t, s.service, s.cargopai, alice, frank)
	if products, workspaces, errs := lists(frank); len(products) != 1 || len(workspaces) != 2 || errs["ListMemberships"] != nil || errs["ListProductRoles"] != nil {
		t.Errorf("frank, named again, lists the products %+v and the workspaces %+v, and is answered %v", products, workspaces, errs)
	}
}

// A refusal stands whether or not it could be recorded.
func TestARefusedListStandsWhetherOrNotItCouldBeAudited(t *testing.T) {
	s := newTeam(t)
	s.w.failing["audit"] = errors.New("the audit trail is down")

	_, errWorkspaces := s.listWorkspaces(admin, s.cargopai, 0, nil)
	_, errMembers := s.listMemberships(carol, s.engineering, 0, nil)
	_, errOwners := s.listRoles(mallory, s.cargopai, 0, nil)
	for list, c := range map[string]struct {
		err  error
		want error
	}{
		"the workspaces": {errWorkspaces, ErrForbidden},
		"the members":    {errMembers, ErrForbidden},
		"the owners":     {errOwners, ErrProductNotFound},
	} {
		if !errors.Is(c.err, c.want) || !errors.Is(c.err, ErrRefusalNotAudited) {
			t.Errorf("%s: got %v, want %v and %v", list, c.err, c.want, ErrRefusalNotAudited)
		}
	}
}

// A list that cannot be read is a failure, not an empty list, and a list
// asked for by nobody is refused.
func TestAListThatCannotBeReadIsAFailure(t *testing.T) {
	s := newTeam(t)
	down := errors.New("the database is down")

	attempts := func(caller domain.Principal) map[string]error {
		products, errProducts := s.listProducts(caller, 0, nil)
		workspaces, errWorkspaces := s.listWorkspaces(caller, s.cargopai, 0, nil)
		members, errMembers := s.listMemberships(caller, s.engineering, 0, nil)
		owners, errOwners := s.listRoles(caller, s.cargopai, 0, nil)
		if (errProducts != nil && products.Items != nil) || (errWorkspaces != nil && workspaces.Items != nil) ||
			(errMembers != nil && members.Items != nil) || (errOwners != nil && owners.Items != nil) {
			t.Errorf("a list that failed returned %+v, %+v, %+v, %+v", products, workspaces, members, owners)
		}
		return map[string]error{
			"ListProducts": errProducts, "ListWorkspaces": errWorkspaces, "ListMemberships": errMembers, "ListProductRoles": errOwners,
		}
	}

	// The list itself cannot be read, as an owner, a member and an
	// administrator would read it.
	s.w.failing["list"] = down
	for name, err := range attempts(alice) {
		if !errors.Is(err, down) {
			t.Errorf("%s, as an owner, with the list out of reach: got %v, want %v", name, err, down)
		}
	}
	if _, err := s.listProducts(admin, 0, nil); !errors.Is(err, down) {
		t.Errorf("ListProducts, as an administrator, with the list out of reach: got %v, want %v", err, down)
	}
	if _, err := s.listWorkspaces(carol, s.cargopai, 0, nil); !errors.Is(err, down) {
		t.Errorf("ListWorkspaces, as a member, with the list out of reach: got %v, want %v", err, down)
	}
	delete(s.w.failing, "list")

	// What the caller holds cannot be read: nothing is decided, so nobody
	// is answered and nobody refused.
	for _, step := range []string{"read", "roles", "held"} {
		s.w.failing[step] = down
		for name, err := range attempts(alice) {
			if name == "ListProducts" {
				// It reads no product by itself, and decides nothing from
				// what the caller holds in one.
				continue
			}
			if !errors.Is(err, down) {
				t.Errorf("%s, with the step %q failing: got %v, want %v", name, step, err, down)
			}
		}
		delete(s.w.failing, step)
	}
	s.untouched(t)

	for name, err := range attempts(domain.Principal{}) {
		if !errors.Is(err, ErrForbidden) {
			t.Errorf("%s, asked by nobody: got %v, want %v", name, err, ErrForbidden)
		}
	}
	s.untouched(t)
}
