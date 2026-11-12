package domain

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	membershipID = MembershipID("3f8b1c2d-4e5a-4b6c-9d7e-8f9a0b1c2d3e")
	otherID      = WorkspaceID("9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d")
)

var mallory = Principal{Issuer: "https://issuer.example", Subject: "mallory"}

func engineering(t *testing.T) (Product, Workspace) {
	t.Helper()

	product := registered(t)
	workspace, err := NewWorkspace(workspaceID, product, "api-engineering", "API engineering", alice, created)
	if err != nil {
		t.Fatalf("create the workspace: %v", err)
	}
	return product, workspace
}

func granted(t *testing.T, member Principal, role Role) Membership {
	t.Helper()

	product, workspace := engineering(t)
	membership, err := GrantMembership(membershipID, product, workspace, member, role, alice, created)
	if err != nil {
		t.Fatalf("grant the membership: %v", err)
	}
	return membership
}

// INV-02: a membership names its product, its workspace, its principal, its
// role and the interval it is in force for.
func TestAMembershipIsGrantedInForceAtVersionOne(t *testing.T) {
	at := time.Date(2026, 11, 11, 17, 0, 0, 123456789, time.FixedZone("UTC+8", 8*60*60))
	product, workspace := engineering(t)

	membership, err := GrantMembership(membershipID, product, workspace, bob, RoleWorkspaceMaintainer, alice, at)
	if err != nil {
		t.Fatalf("grant the membership: %v", err)
	}
	want := Membership{
		ID: membershipID, ProductID: productID, WorkspaceID: workspaceID, Principal: bob, Role: RoleWorkspaceMaintainer,
		Version: 1, GrantedAt: time.Date(2026, 11, 11, 9, 0, 0, 123456000, time.UTC), GrantedBy: alice,
	}
	if membership != want {
		t.Errorf("membership\n %+v\nwant\n %+v", membership, want)
	}
	if membership.GrantedAt.Location() != time.UTC {
		t.Errorf("the membership is timed in %s, want UTC", membership.GrantedAt.Location())
	}
	if !membership.InForce() {
		t.Error("a membership that was just granted is not in force")
	}
}

// A membership grants a role of a workspace. The role that owns a product is
// not one, and neither is a role nobody defined.
func TestAMembershipGrantsOneOfTheRolesOfAWorkspace(t *testing.T) {
	product, workspace := engineering(t)

	if want := []Role{"workspace_maintainer", "internal_author"}; !slices.Equal(WorkspaceRoles(), want) {
		t.Fatalf("the roles of a workspace are %v, want %v", WorkspaceRoles(), want)
	}
	for _, role := range WorkspaceRoles() {
		if _, err := GrantMembership(membershipID, product, workspace, bob, role, alice, created); err != nil {
			t.Errorf("the role %s was refused: %v", role, err)
		}
	}

	refused := map[string]Role{
		"no role":                         "",
		"the role that owns a product":    RoleProductOwner,
		"a role of the platform":          "platform_admin",
		"a role nobody defined":           "internal_reader",
		"a role of a workspace, in upper": "WORKSPACE_MAINTAINER",
		"a role with a space after it":    "internal_author ",
	}
	for name, role := range refused {
		t.Run(name, func(t *testing.T) {
			if membership, err := GrantMembership(membershipID, product, workspace, bob, role, alice, created); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %+v and %v, want %v", membership, err, ErrInvalid)
			}
		})
	}
}

func TestAMembershipNeedsAnIdentifierAWorkspaceOfItsProductAMemberAGranterAndATime(t *testing.T) {
	product, workspace := engineering(t)
	elsewhere := workspace
	elsewhere.ProductID = ProductID("0f1e2d3c-4b5a-4968-8776-655443322110")

	cases := map[string]func() (Membership, error){
		"an identifier that is not a UUID": func() (Membership, error) {
			return GrantMembership("member-1", product, workspace, bob, RoleInternalAuthor, alice, created)
		},
		"a workspace of another product": func() (Membership, error) {
			return GrantMembership(membershipID, product, elsewhere, bob, RoleInternalAuthor, alice, created)
		},
		"no member": func() (Membership, error) {
			return GrantMembership(membershipID, product, workspace, Principal{}, RoleInternalAuthor, alice, created)
		},
		"a member without an issuer": func() (Membership, error) {
			return GrantMembership(membershipID, product, workspace, Principal{Subject: "bob"}, RoleInternalAuthor, alice, created)
		},
		"a member with a control letter": func() (Membership, error) {
			return GrantMembership(membershipID, product, workspace, Principal{Issuer: bob.Issuer, Subject: "bo\x00b"}, RoleInternalAuthor, alice, created)
		},
		"no granter": func() (Membership, error) {
			return GrantMembership(membershipID, product, workspace, bob, RoleInternalAuthor, Principal{}, created)
		},
		"no time": func() (Membership, error) {
			return GrantMembership(membershipID, product, workspace, bob, RoleInternalAuthor, alice, time.Time{})
		},
	}
	for name, grant := range cases {
		t.Run(name, func(t *testing.T) {
			if membership, err := grant(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %+v and %v, want %v", membership, err, ErrInvalid)
			}
		})
	}

	// An owner may be a member of a workspace of its own product, and may
	// grant itself the membership: owning a product is not maintaining its
	// assets.
	if _, err := GrantMembership(membershipID, product, workspace, alice, RoleWorkspaceMaintainer, alice, created); err != nil {
		t.Errorf("an owner who makes itself a member was refused: %v", err)
	}
}

// INV-01: a suspended product takes no new member. What it granted before
// can still be revoked.
func TestASuspendedProductTakesNoMember(t *testing.T) {
	before := granted(t, bob, RoleInternalAuthor)

	product, workspace := engineering(t)
	product.Status = ProductSuspended
	if membership, err := GrantMembership(membershipID, product, workspace, mallory, RoleInternalAuthor, alice, created); !errors.Is(err, ErrProductNotActive) {
		t.Fatalf("got %+v and %v, want %v", membership, err, ErrProductNotActive)
	}
	if _, err := before.Revoke(alice, created.Add(time.Hour)); err != nil {
		t.Errorf("a membership of a suspended product could not be revoked: %v", err)
	}
}

func TestARevokedMembershipGrantsNothingAndSaysWhoRevokedIt(t *testing.T) {
	membership := granted(t, bob, RoleWorkspaceMaintainer)
	at := time.Date(2026, 11, 12, 1, 30, 0, 987654321, time.FixedZone("UTC+8", 8*60*60))

	revoked, err := membership.Revoke(admin, at)
	if err != nil {
		t.Fatalf("revoke the membership: %v", err)
	}
	want := membership
	want.Version, want.RevokedAt, want.RevokedBy = 2, time.Date(2026, 11, 11, 17, 30, 0, 987654000, time.UTC), admin
	if revoked != want {
		t.Errorf("revoked\n %+v\nwant\n %+v", revoked, want)
	}
	if revoked.InForce() {
		t.Error("a revoked membership is in force")
	}
	if revoked.RevokedAt.Location() != time.UTC {
		t.Errorf("the revocation is timed in %s, want UTC", revoked.RevokedAt.Location())
	}
	// Revoking returns the membership as it is from then on. The one it was
	// called on is a value and stays what it was.
	if !membership.InForce() || membership.Version != 1 {
		t.Errorf("the membership that was revoked changed in place: %+v", membership)
	}
}

// A revoked membership is a record. It is not revoked again, by anyone.
func TestAMembershipIsRevokedOnce(t *testing.T) {
	revoked, err := granted(t, bob, RoleInternalAuthor).Revoke(alice, created.Add(time.Hour))
	if err != nil {
		t.Fatalf("revoke the membership: %v", err)
	}
	again, err := revoked.Revoke(admin, created.Add(2*time.Hour))
	if !errors.Is(err, ErrMembershipRevoked) || again != (Membership{}) {
		t.Fatalf("got %+v and %v, want nothing and %v", again, err, ErrMembershipRevoked)
	}
}

func TestARevocationNeedsARevokerAndATime(t *testing.T) {
	membership := granted(t, bob, RoleInternalAuthor)

	if revoked, err := membership.Revoke(Principal{}, created.Add(time.Hour)); !errors.Is(err, ErrInvalid) {
		t.Errorf("no revoker: got %+v and %v, want %v", revoked, err, ErrInvalid)
	}
	if revoked, err := membership.Revoke(alice, time.Time{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("no time: got %+v and %v, want %v", revoked, err, ErrInvalid)
	}
}

// A revocation is never refused for the time it carries. A process whose
// clock runs behind the one that granted the membership ends it the instant
// it began.
func TestAClockThatRunsBehindDoesNotKeepAMembershipFromBeingRevoked(t *testing.T) {
	membership := granted(t, bob, RoleInternalAuthor)

	revoked, err := membership.Revoke(alice, membership.GrantedAt.Add(-time.Minute))
	if err != nil {
		t.Fatalf("revoke the membership: %v", err)
	}
	if !revoked.RevokedAt.Equal(membership.GrantedAt) || revoked.InForce() {
		t.Errorf("revoked at %s and in force %t, want at %s and not in force", revoked.RevokedAt, revoked.InForce(), membership.GrantedAt)
	}
}

func TestAStandingSaysWhatAPrincipalHoldsInAProduct(t *testing.T) {
	maintainer := granted(t, bob, RoleWorkspaceMaintainer)
	revoked, err := maintainer.Revoke(alice, created.Add(time.Hour))
	if err != nil {
		t.Fatalf("revoke the membership: %v", err)
	}

	cases := map[string]struct {
		standing Standing
		owns     bool
		member   bool
		stranger bool
	}{
		"nothing":                         {Standing{}, false, false, true},
		"the role of an owner":            {Standing{Roles: []Role{RoleProductOwner}}, true, false, false},
		"a membership of the workspace":   {Standing{Memberships: []Membership{maintainer}}, false, true, false},
		"an owner who is a member too":    {Standing{Roles: []Role{RoleProductOwner}, Memberships: []Membership{maintainer}}, true, true, false},
		"a membership that was revoked":   {Standing{Memberships: []Membership{revoked}}, false, false, true},
		"a role that is not the owner's":  {Standing{Roles: []Role{RoleWorkspaceMaintainer}}, false, false, false},
		"a revoked and a live membership": {Standing{Memberships: []Membership{revoked, maintainer}}, false, true, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := c.standing.Owns(); got != c.owns {
				t.Errorf("owns the product: %t, want %t", got, c.owns)
			}
			if got := c.standing.MemberOf(workspaceID); got != c.member {
				t.Errorf("member of the workspace: %t, want %t", got, c.member)
			}
			if got := c.standing.Stranger(); got != c.stranger {
				t.Errorf("a stranger to the product: %t, want %t", got, c.stranger)
			}
		})
	}
}

// Being a member of one workspace of a product is not being a member of its
// other workspaces (F03 §2).
func TestAMemberOfOneWorkspaceIsNotAMemberOfAnother(t *testing.T) {
	standing := Standing{Memberships: []Membership{granted(t, bob, RoleInternalAuthor)}}

	if !standing.MemberOf(workspaceID) {
		t.Error("the member is not a member of its own workspace")
	}
	if standing.MemberOf(otherID) {
		t.Error("the member of one workspace is a member of another")
	}
	if standing.Stranger() || standing.Owns() {
		t.Errorf("a member is a stranger %t and an owner %t, want neither", standing.Stranger(), standing.Owns())
	}
}

func TestAMembershipIdentifierIsACanonicalUUID(t *testing.T) {
	if id, err := ParseMembershipID(string(membershipID)); err != nil || id != membershipID {
		t.Errorf("got %q and %v, want the identifier back", id, err)
	}
	for _, value := range []string{"", "member-1", strings.ToUpper(string(membershipID)), string(membershipID) + "0", "{" + string(membershipID) + "}"} {
		if _, err := ParseMembershipID(value); !errors.Is(err, ErrInvalid) {
			t.Errorf("membership identifier %q: got %v, want %v", value, err, ErrInvalid)
		}
	}
}

// The errors are written for a log and for a caller. They repeat nothing of
// the value they refuse.
func TestAnErrorOfAMembershipRepeatsNothingOfTheValue(t *testing.T) {
	product, workspace := engineering(t)

	_, err := GrantMembership(membershipID, product, workspace, bob, "canary_role", alice, created)
	if err == nil || strings.Contains(err.Error(), "canary") {
		t.Errorf("the error of a refused role is %v", err)
	}
	_, err = GrantMembership(membershipID, product, workspace, Principal{Issuer: bob.Issuer, Subject: "canary\nsubject"}, RoleInternalAuthor, alice, created)
	if err == nil || strings.Contains(err.Error(), "canary") {
		t.Errorf("the error of a refused member is %v", err)
	}
}
