package domain

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	assignmentID      = RoleAssignmentID("5a6b7c8d-9e0f-4a1b-8c2d-3e4f5a6b7c8d")
	otherAssignmentID = RoleAssignmentID("6b7c8d9e-0f1a-4b2c-9d3e-4f5a6b7c8d9e")
)

// owning makes principal an owner of the product, granted by the
// administrator of the platform.
func owning(t *testing.T, principal Principal) RoleAssignment {
	t.Helper()

	assignment, err := GrantProductRole(assignmentID, registered(t), principal, RoleProductOwner, admin, created)
	if err != nil {
		t.Fatalf("grant the role: %v", err)
	}
	return assignment
}

// A role assignment names its product, its principal and its role, and says
// who granted it and since when it is in force.
func TestARoleAssignmentIsGrantedInForceAtVersionOne(t *testing.T) {
	at := time.Date(2026, 11, 11, 17, 0, 0, 123456789, time.FixedZone("UTC+8", 8*60*60))

	assignment, err := GrantProductRole(assignmentID, registered(t), bob, RoleProductOwner, alice, at)
	if err != nil {
		t.Fatalf("grant the role: %v", err)
	}
	want := RoleAssignment{
		ID: assignmentID, ProductID: productID, Principal: bob, Role: RoleProductOwner,
		Version: 1, GrantedAt: time.Date(2026, 11, 11, 9, 0, 0, 123456000, time.UTC), GrantedBy: alice,
	}
	if assignment != want {
		t.Errorf("assignment\n %+v\nwant\n %+v", assignment, want)
	}
	if assignment.GrantedAt.Location() != time.UTC {
		t.Errorf("the assignment is timed in %s, want UTC", assignment.GrantedAt.Location())
	}
	if !assignment.InForce() {
		t.Error("an assignment that was just granted is not in force")
	}
}

// A role assignment grants a role of a product. A role of a workspace is not
// one, and neither is a role nobody defined.
func TestARoleAssignmentGrantsOneOfTheRolesOfAProduct(t *testing.T) {
	product := registered(t)

	if want := []Role{"product_owner"}; !slices.Equal(ProductRoles(), want) {
		t.Fatalf("the roles of a product are %v, want %v", ProductRoles(), want)
	}
	for _, role := range ProductRoles() {
		if _, err := GrantProductRole(assignmentID, product, bob, role, alice, created); err != nil {
			t.Errorf("the role %s was refused: %v", role, err)
		}
	}
	// The roles of a product and the roles of a workspace are two sets.
	for _, role := range WorkspaceRoles() {
		if slices.Contains(ProductRoles(), role) {
			t.Errorf("%s is a role of a product and of a workspace", role)
		}
	}

	refused := map[string]Role{
		"no role":                        "",
		"a role of a workspace":          RoleWorkspaceMaintainer,
		"the other role of a workspace":  RoleInternalAuthor,
		"a role of the platform":         "platform_admin",
		"a role nobody defined":          "product_reader",
		"the role of an owner, in upper": "PRODUCT_OWNER",
		"the role with a space after it": "product_owner ",
	}
	for name, role := range refused {
		t.Run(name, func(t *testing.T) {
			if assignment, err := GrantProductRole(assignmentID, product, bob, role, alice, created); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %+v and %v, want %v", assignment, err, ErrInvalid)
			}
		})
	}
}

func TestARoleAssignmentNeedsAnIdentifierAPrincipalAGranterAndATime(t *testing.T) {
	product := registered(t)

	cases := map[string]func() (RoleAssignment, error){
		"an identifier that is not a UUID": func() (RoleAssignment, error) {
			return GrantProductRole("owner-1", product, bob, RoleProductOwner, alice, created)
		},
		"an identifier in upper case": func() (RoleAssignment, error) {
			return GrantProductRole(RoleAssignmentID(strings.ToUpper(string(assignmentID))), product, bob, RoleProductOwner, alice, created)
		},
		"no principal": func() (RoleAssignment, error) {
			return GrantProductRole(assignmentID, product, Principal{}, RoleProductOwner, alice, created)
		},
		"a principal without an issuer": func() (RoleAssignment, error) {
			return GrantProductRole(assignmentID, product, Principal{Subject: "bob"}, RoleProductOwner, alice, created)
		},
		"a principal with a control letter": func() (RoleAssignment, error) {
			return GrantProductRole(assignmentID, product, Principal{Issuer: bob.Issuer, Subject: "bo\x00b"}, RoleProductOwner, alice, created)
		},
		"no granter": func() (RoleAssignment, error) {
			return GrantProductRole(assignmentID, product, bob, RoleProductOwner, Principal{}, created)
		},
		"no time": func() (RoleAssignment, error) {
			return GrantProductRole(assignmentID, product, bob, RoleProductOwner, alice, time.Time{})
		},
	}
	for name, grant := range cases {
		t.Run(name, func(t *testing.T) {
			if assignment, err := grant(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %+v and %v, want %v", assignment, err, ErrInvalid)
			}
		})
	}

	// Whoever grants the role need not own the product: the administrator of
	// the platform names owners, and may be named one.
	if _, err := GrantProductRole(assignmentID, product, admin, RoleProductOwner, admin, created); err != nil {
		t.Errorf("the administrator who makes itself an owner was refused: %v", err)
	}
}

// INV-01: a suspended product takes no new owner. What it granted before can
// still be revoked.
func TestASuspendedProductTakesNoRoleAssignment(t *testing.T) {
	before := owning(t, bob)

	product := registered(t)
	product.Status = ProductSuspended
	if assignment, err := GrantProductRole(otherAssignmentID, product, mallory, RoleProductOwner, alice, created); !errors.Is(err, ErrProductNotActive) {
		t.Fatalf("got %+v and %v, want %v", assignment, err, ErrProductNotActive)
	}
	if _, err := before.Revoke(alice, created.Add(time.Hour), 2); err != nil {
		t.Errorf("a role assignment of a suspended product could not be revoked: %v", err)
	}
}

func TestARevokedRoleAssignmentGrantsNothingAndSaysWhoRevokedIt(t *testing.T) {
	assignment := owning(t, bob)
	at := time.Date(2026, 11, 12, 1, 30, 0, 987654321, time.FixedZone("UTC+8", 8*60*60))

	revoked, err := assignment.Revoke(alice, at, 2)
	if err != nil {
		t.Fatalf("revoke the assignment: %v", err)
	}
	want := assignment
	want.Version, want.RevokedAt, want.RevokedBy = 2, time.Date(2026, 11, 11, 17, 30, 0, 987654000, time.UTC), alice
	if revoked != want {
		t.Errorf("revoked\n %+v\nwant\n %+v", revoked, want)
	}
	if revoked.InForce() {
		t.Error("a revoked assignment is in force")
	}
	if revoked.RevokedAt.Location() != time.UTC {
		t.Errorf("the revocation is timed in %s, want UTC", revoked.RevokedAt.Location())
	}
	// Revoking returns the assignment as it is from then on. The one it was
	// called on is a value and stays what it was.
	if !assignment.InForce() || assignment.Version != 1 {
		t.Errorf("the assignment that was revoked changed in place: %+v", assignment)
	}
}

// A revoked assignment is a record. It is not revoked again, by anyone,
// however many owners the product has.
func TestARoleAssignmentIsRevokedOnce(t *testing.T) {
	revoked, err := owning(t, bob).Revoke(alice, created.Add(time.Hour), 2)
	if err != nil {
		t.Fatalf("revoke the assignment: %v", err)
	}
	for _, owners := range []int{0, 1, 2, 5} {
		again, err := revoked.Revoke(admin, created.Add(2*time.Hour), owners)
		if !errors.Is(err, ErrRoleAssignmentRevoked) || again != (RoleAssignment{}) {
			t.Fatalf("among %d owners: got %+v and %v, want nothing and %v", owners, again, err, ErrRoleAssignmentRevoked)
		}
	}
}

func TestARevocationOfARoleNeedsARevokerAndATime(t *testing.T) {
	assignment := owning(t, bob)

	if revoked, err := assignment.Revoke(Principal{}, created.Add(time.Hour), 2); !errors.Is(err, ErrInvalid) {
		t.Errorf("no revoker: got %+v and %v, want %v", revoked, err, ErrInvalid)
	}
	if revoked, err := assignment.Revoke(alice, time.Time{}, 2); !errors.Is(err, ErrInvalid) {
		t.Errorf("no time: got %+v and %v, want %v", revoked, err, ErrInvalid)
	}
}

// A product always has an owner: the last assignment in force that makes
// somebody one is not revoked, by anybody, itself included.
func TestTheLastOwnerOfAProductIsNotRevoked(t *testing.T) {
	assignment := owning(t, bob)

	// Whatever is counted that is not more than this one assignment leaves
	// the product to nobody.
	for _, owners := range []int{1, 0, -1} {
		for name, by := range map[string]Principal{"another principal": alice, "the owner itself": bob, "the administrator": admin} {
			revoked, err := assignment.Revoke(by, created.Add(time.Hour), owners)
			if !errors.Is(err, ErrLastOwner) || revoked != (RoleAssignment{}) {
				t.Errorf("among %d owners, revoked by %s: got %+v and %v, want nothing and %v", owners, name, revoked, err, ErrLastOwner)
			}
		}
	}
	if !assignment.InForce() || assignment.Version != 1 {
		t.Errorf("a refused revocation changed the assignment: %+v", assignment)
	}

	// With another owner in force it is revoked, by that owner or by
	// itself.
	for _, owners := range []int{2, 3, 10} {
		for name, by := range map[string]Principal{"another owner": alice, "the owner itself": bob} {
			revoked, err := assignment.Revoke(by, created.Add(time.Hour), owners)
			if err != nil || revoked.InForce() || revoked.RevokedBy != by {
				t.Errorf("among %d owners, revoked by %s: got %+v and %v, want it revoked", owners, name, revoked, err)
			}
		}
	}
}

// A revocation is never refused for the time it carries. A process whose
// clock runs behind the one that granted the role ends the assignment the
// instant it began.
func TestAClockThatRunsBehindDoesNotKeepARoleAssignmentFromBeingRevoked(t *testing.T) {
	assignment := owning(t, bob)

	revoked, err := assignment.Revoke(alice, assignment.GrantedAt.Add(-time.Minute), 2)
	if err != nil {
		t.Fatalf("revoke the assignment: %v", err)
	}
	if !revoked.RevokedAt.Equal(assignment.GrantedAt) || revoked.InForce() {
		t.Errorf("revoked at %s and in force %t, want at %s and not in force", revoked.RevokedAt, revoked.InForce(), assignment.GrantedAt)
	}
}

func TestARoleAssignmentIdentifierIsACanonicalUUID(t *testing.T) {
	if id, err := ParseRoleAssignmentID(string(assignmentID)); err != nil || id != assignmentID {
		t.Errorf("got %q and %v, want the identifier back", id, err)
	}
	for _, value := range []string{"", "owner-1", strings.ToUpper(string(assignmentID)), string(assignmentID) + "0", "{" + string(assignmentID) + "}"} {
		if _, err := ParseRoleAssignmentID(value); !errors.Is(err, ErrInvalid) {
			t.Errorf("role assignment identifier %q: got %v, want %v", value, err, ErrInvalid)
		}
	}
}

// The errors are written for a log and for a caller. They repeat nothing of
// the value they refuse.
func TestAnErrorOfARoleAssignmentRepeatsNothingOfTheValue(t *testing.T) {
	product := registered(t)

	_, err := GrantProductRole(assignmentID, product, bob, "canary_role", alice, created)
	if err == nil || strings.Contains(err.Error(), "canary") {
		t.Errorf("the error of a refused role is %v", err)
	}
	_, err = GrantProductRole(assignmentID, product, Principal{Issuer: bob.Issuer, Subject: "canary\nsubject"}, RoleProductOwner, alice, created)
	if err == nil || strings.Contains(err.Error(), "canary") {
		t.Errorf("the error of a refused principal is %v", err)
	}
	_, err = GrantProductRole("canary-identifier", product, bob, RoleProductOwner, alice, created)
	if err == nil || strings.Contains(err.Error(), "canary") {
		t.Errorf("the error of a refused identifier is %v", err)
	}
}
