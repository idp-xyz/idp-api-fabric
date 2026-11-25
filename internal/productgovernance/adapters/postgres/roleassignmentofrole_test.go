package postgres_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// holdingAt makes principal hold the role in the product from the instant,
// granted by the administrator of the platform.
func holdingAt(t *testing.T, of domain.Product, principal domain.Principal, role domain.Role, at time.Time) domain.RoleAssignment {
	t.Helper()

	granted, err := domain.GrantProductRole(domain.RoleAssignmentID(ids.NewUUID()), of, principal, role, admin, at)
	if err != nil {
		t.Fatalf("build the role assignment: %v", err)
	}
	return granted
}

// The assignments of one role are the list of every role kept to that role:
// those in force of the product, oldest first, and of one instant in the
// order of their identifiers. The role is a condition of the statement, so a
// page is cut from the assignments of the role and is full while there are
// that many, however many assignments of other roles lie between them. One
// that was revoked is in no list, and one of another product is not either.
func TestTheAssignmentsOfOneRoleAreListedInTheOrderOfTheListInPagesThatAreFull(t *testing.T) {
	f := newFixture(t)
	s := f.scene(t)
	named := func(subject string) domain.Principal { return domain.Principal{Issuer: alice.Issuer, Subject: subject} }

	// Five auditors, three of them appointed in one instant, with the
	// owners and a security administrator granted between them.
	var auditors, owners []domain.RoleAssignment
	for index, minute := range []int{1, 3, 3, 3, 5} {
		auditors = append(auditors, holdingAt(t, s.cargopai, named(fmt.Sprintf("auditor-%d", index)), domain.RoleInternalAuditor, later(minute)))
		owners = append(owners, holdingAt(t, s.cargopai, named(fmt.Sprintf("owner-%d", index)), domain.RoleProductOwner, later(minute+1)))
	}
	byIdentifier := func(a, b domain.RoleAssignment) int { return strings.Compare(string(a.ID), string(b.ID)) }
	slices.SortFunc(auditors[1:4], byIdentifier)
	slices.SortFunc(owners[1:4], byIdentifier)
	security := holdingAt(t, s.cargopai, carol, domain.RoleSecurityAdmin, later(3))
	// bob audited the product, was dismissed and was appointed again. An
	// owner of the product audits it too, as one could before. carol audits
	// the other product.
	former, again := holdingAt(t, s.cargopai, bob, domain.RoleInternalAuditor, later(3)), holdingAt(t, s.cargopai, bob, domain.RoleInternalAuditor, later(7))
	both := holdingAt(t, s.cargopai, owners[0].Principal, domain.RoleInternalAuditor, later(6))
	abroad := holdingAt(t, s.parcel, carol, domain.RoleInternalAuditor, later(3))

	f.assign(t, auditors[3], owners[1], former, auditors[0], security, abroad, owners[4], auditors[4], owners[0], auditors[2], owners[3], auditors[1], owners[2], both)
	f.unassign(t, disowned(t, former, admin))
	f.assign(t, again)
	auditors = append(auditors, both, again)

	of := func(product domain.Product, role domain.Role) func(ports.Page) ([]domain.RoleAssignment, error) {
		return func(page ports.Page) ([]domain.RoleAssignment, error) {
			return f.roles.ListOfRole(t.Context(), product.ID, role, page)
		}
	}
	inOrder(t, "the auditors of the product", auditors, ports.PositionOfRoleAssignment, of(s.cargopai, domain.RoleInternalAuditor))
	inOrder(t, "the owners of the product", owners, ports.PositionOfRoleAssignment, of(s.cargopai, domain.RoleProductOwner))
	inOrder(t, "the security administrators of the product", []domain.RoleAssignment{security}, ports.PositionOfRoleAssignment, of(s.cargopai, domain.RoleSecurityAdmin))
	inOrder(t, "the security reviewers of the product", nil, ports.PositionOfRoleAssignment, of(s.cargopai, domain.RoleSecurityReviewer))
	inOrder(t, "the auditors of the other product", []domain.RoleAssignment{abroad}, ports.PositionOfRoleAssignment, of(s.parcel, domain.RoleInternalAuditor))
	inOrder(t, "the auditors of a product that is missing", nil, ports.PositionOfRoleAssignment, of(product(t, "missing"), domain.RoleInternalAuditor))
	inOrder(t, "the holders of a role nobody defined", nil, ports.PositionOfRoleAssignment, of(s.cargopai, "superuser"))

	// The lists of the roles are the list of every role, taken apart: each
	// assignment of that list is in the list of its role and in no other.
	all, err := f.roles.List(t.Context(), s.cargopai.ID, ports.Page{Limit: 100})
	if err != nil {
		t.Fatalf("list every role: %v", err)
	}
	byRole := map[domain.Role][]domain.RoleAssignment{}
	for _, assignment := range all {
		byRole[assignment.Role] = append(byRole[assignment.Role], assignment)
	}
	if len(all) != len(auditors)+len(owners)+1 || !slices.Equal(byRole[domain.RoleInternalAuditor], auditors) ||
		!slices.Equal(byRole[domain.RoleProductOwner], owners) || !slices.Equal(byRole[domain.RoleSecurityAdmin], []domain.RoleAssignment{security}) {
		t.Errorf("the list of every role holds %d assignments, by role\n %+v\nwant the %d auditors, the %d owners and the security administrator as their lists gave them",
			len(all), byRole, len(auditors), len(owners))
	}

	// A position of the list of every role is a position of the list of one
	// role: behind an owner are the auditors that follow it.
	behind := ports.PositionOfRoleAssignment(owners[1])
	rest, err := f.roles.ListOfRole(t.Context(), s.cargopai.ID, domain.RoleInternalAuditor, ports.Page{After: &behind, Limit: 100})
	if want := auditors[4:]; err != nil || !slices.Equal(rest, want) {
		t.Errorf("behind an owner the auditors are\n %+v (%v)\nwant\n %+v", rest, err, want)
	}

	// A page takes at least one row, as with every list.
	for _, limit := range []int{0, -1} {
		if _, err := f.roles.ListOfRole(t.Context(), s.cargopai.ID, domain.RoleInternalAuditor, ports.Page{Limit: limit}); err == nil ||
			!strings.Contains(err.Error(), "at least one row") {
			t.Errorf("a page of %d auditors: got %v, want it refused", limit, err)
		}
	}
}
