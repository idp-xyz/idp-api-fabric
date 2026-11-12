package application

import (
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

var (
	frank = domain.Principal{Issuer: trustedIssuer, Subject: "frank"}
	grace = domain.Principal{Issuer: trustedIssuer, Subject: "grace"}
)

// ownerOf is the role assignment in force that makes principal an owner of
// the product.
func (w *world) ownerOf(t *testing.T, product domain.Product, principal domain.Principal) domain.RoleAssignment {
	t.Helper()

	for _, assignment := range w.roles {
		if assignment.InForce() && assignment.ProductID == product.ID &&
			assignment.Principal == principal && assignment.Role == domain.RoleProductOwner {
			return assignment
		}
	}
	t.Fatalf("%s does not own %s", principal.Subject, product.Key)
	return domain.RoleAssignment{}
}

// named makes principal an owner of the product, as one of its owners.
func named(t *testing.T, service *ProductGovernance, product domain.Product, owner, principal domain.Principal) domain.RoleAssignment {
	t.Helper()

	assignment, err := service.GrantProductRole(ctx, GrantProductRole{
		Caller: owner, CorrelationID: "correlation-r", ProductID: string(product.ID),
		Principal: principal, Role: string(domain.RoleProductOwner),
	})
	if err != nil {
		t.Fatalf("make %s an owner of %s: %v", principal.Subject, product.Key, err)
	}
	return assignment
}

// board is the team with the owners of CargoPai told apart. alice was named
// when the product was registered and has named frank since. grace owned it
// too, until alice revoked her role. Parcel is still bob's alone.
type board struct {
	team

	// first is alice's role in CargoPai, second frank's and former the one
	// grace held. elsewhere is bob's role in Parcel.
	first, second, former, elsewhere domain.RoleAssignment
}

func newBoard(t *testing.T) board {
	t.Helper()

	s := board{team: newTeam(t)}
	s.first, s.elsewhere = s.w.ownerOf(t, s.cargopai, alice), s.w.ownerOf(t, s.parcel, bob)
	s.second = named(t, s.service, s.cargopai, alice, frank)
	s.former = named(t, s.service, s.cargopai, alice, grace)
	if err := s.revokeRole(alice, s.cargopai, s.former.ID); err != nil {
		t.Fatalf("revoke the role of grace: %v", err)
	}
	s.w.quiet()
	return s
}

func (s team) grantRole(caller domain.Principal, product domain.Product, principal domain.Principal) (domain.RoleAssignment, error) {
	return s.service.GrantProductRole(ctx, GrantProductRole{
		Caller: caller, CorrelationID: "correlation-8", ProductID: string(product.ID),
		Principal: principal, Role: string(domain.RoleProductOwner),
	})
}

func (s team) revokeRole(caller domain.Principal, product domain.Product, id domain.RoleAssignmentID) error {
	return s.service.RevokeProductRole(ctx, RevokeProductRole{
		Caller: caller, CorrelationID: "correlation-9", ProductID: string(product.ID), RoleAssignmentID: string(id),
	})
}

func (s team) role(caller domain.Principal, product domain.Product, id domain.RoleAssignmentID) (domain.RoleAssignment, error) {
	return s.service.GetProductRole(ctx, GetProductRole{
		Caller: caller, CorrelationID: "correlation-10", ProductID: string(product.ID), RoleAssignmentID: string(id),
	})
}

// answered is a caller and what it is told.
type answered struct {
	caller domain.Principal
	want   error
}

// withoutSay are the callers that neither own CargoPai nor administer the
// platform, with what each is told when it asks for what an owner may do.
// Whoever may know the product exists is told it may not; to anyone else
// there is no such product.
var withoutSay = map[string]answered{
	// A member knows the product and does not manage it.
	"a member of a workspace":                  {carol, ErrForbidden},
	"a member of another workspace":            {dave, ErrForbidden},
	"a stranger":                               {mallory, ErrProductNotFound},
	"the owner of another product":             {bob, ErrProductNotFound},
	"whoever was a member and is not any more": {erin, ErrProductNotFound},
	// A role that was revoked leaves a stranger behind, as a membership
	// does.
	"whoever was an owner and is not any more": {grace, ErrProductNotFound},
}

func TestGrantProductRoleStoresTheAssignmentTheAuditFactAndTheEventTogether(t *testing.T) {
	s := newBoard(t)
	s.w.now = started.Add(time.Hour)
	before := len(s.w.roles)

	assignment, err := s.grantRole(alice, s.cargopai, carol)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}

	want := domain.RoleAssignment{
		ID: assignment.ID, ProductID: s.cargopai.ID, Principal: carol, Role: domain.RoleProductOwner,
		Version: 1, GrantedAt: s.w.now, GrantedBy: alice,
	}
	if assignment != want || s.w.roles[assignment.ID] != want {
		t.Errorf("assignment\n %+v\nstored\n %+v\nwant\n %+v", assignment, s.w.roles[assignment.ID], want)
	}
	if _, err := domain.ParseRoleAssignmentID(string(assignment.ID)); err != nil || len(s.w.roles) != before+1 {
		t.Errorf("the assignment has the identifier %q and there are %d assignments, want a new one", assignment.ID, len(s.w.roles))
	}
	// AC-10: an owner manages who holds what in its own product.
	wantAudit := []audited{{ports.AuditEntry{
		Actor: alice, Action: "product_role.grant", Allowed: true, Rule: "AC-10",
		ProductID: s.cargopai.ID, ResourceType: "RoleAssignment", ResourceID: string(assignment.ID), CorrelationID: "correlation-8",
	}, true}}
	if !slices.Equal(s.w.audited, wantAudit) {
		t.Errorf("audited\n %+v\nwant, inside the transaction,\n %+v", s.w.audited, wantAudit)
	}
	if want := []string{"ProductRoleChanged " + string(assignment.ID) + " granted correlation-8"}; !slices.Equal(s.w.events, want) {
		t.Errorf("events %v, want %v", s.w.events, want)
	}
	if s.w.transactions != 1 {
		t.Errorf("%d transactions, want 1", s.w.transactions)
	}

	// The role is in force from that moment. carol, a member until now,
	// manages the product.
	if _, err := s.service.CreateWorkspace(ctx, CreateWorkspace{Caller: carol, ProductID: string(s.cargopai.ID), Key: "hers", DisplayName: "Hers"}); err != nil {
		t.Errorf("the new owner creates a workspace: %v", err)
	}
}

// AC-08: a platform administrator names an owner of a product, as it does
// when it registers one. That is how a product whose owner was mistyped is
// repaired, and it is all the administrator does there.
func TestAPlatformAdministratorNamesAnOwnerAndGetsNothingInsideTheProduct(t *testing.T) {
	s := newTeam(t)
	s.w.now = started.Add(time.Hour)

	assignment, err := s.grantRole(admin, s.cargopai, frank)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	want := domain.RoleAssignment{
		ID: assignment.ID, ProductID: s.cargopai.ID, Principal: frank, Role: domain.RoleProductOwner,
		Version: 1, GrantedAt: s.w.now, GrantedBy: admin,
	}
	if assignment != want || s.w.roles[assignment.ID] != want {
		t.Errorf("assignment\n %+v\nstored\n %+v\nwant\n %+v", assignment, s.w.roles[assignment.ID], want)
	}
	wantAudit := []audited{{ports.AuditEntry{
		Actor: admin, Action: "product_role.grant", Allowed: true, Rule: "AC-08",
		ProductID: s.cargopai.ID, ResourceType: "RoleAssignment", ResourceID: string(assignment.ID), CorrelationID: "correlation-8",
	}, true}}
	if !slices.Equal(s.w.audited, wantAudit) {
		t.Errorf("audited\n %+v\nwant, inside the transaction,\n %+v", s.w.audited, wantAudit)
	}
	if want := []string{"ProductRoleChanged " + string(assignment.ID) + " granted correlation-8"}; !slices.Equal(s.w.events, want) {
		t.Errorf("events %v, want %v", s.w.events, want)
	}

	// It reads no role assignment, the one it made included, removes no
	// owner, and sees and manages nothing inside the product. It knows the
	// product exists, as it did.
	if got, err := s.role(admin, s.cargopai, assignment.ID); err != ErrRoleAssignmentNotFound || got != (domain.RoleAssignment{}) {
		t.Errorf("the administrator reads the assignment it made: got %+v and %v, want nothing and exactly %v", got, err, ErrRoleAssignmentNotFound)
	}
	if err := s.revokeRole(admin, s.cargopai, assignment.ID); err != ErrForbidden {
		t.Errorf("the administrator removes the owner it named: got %v, want exactly %v", err, ErrForbidden)
	}
	if _, err := s.workspace(admin, s.engineering); err != ErrWorkspaceNotFound {
		t.Errorf("the administrator reads a workspace: got %v, want exactly %v", err, ErrWorkspaceNotFound)
	}
	if _, err := s.service.CreateWorkspace(ctx, CreateWorkspace{Caller: admin, ProductID: string(s.cargopai.ID), Key: "its", DisplayName: "Its"}); err != ErrForbidden {
		t.Errorf("the administrator creates a workspace: got %v, want exactly %v", err, ErrForbidden)
	}
	if _, err := s.grant(admin, s.engineering, mallory, domain.RoleInternalAuthor); err != ErrForbidden {
		t.Errorf("the administrator makes somebody a member: got %v, want exactly %v", err, ErrForbidden)
	}
	if _, err := s.product(admin, s.cargopai); err != nil {
		t.Errorf("the administrator reads the product: %v", err)
	}
	if !s.w.roles[assignment.ID].InForce() || len(s.w.workspaces) != 3 {
		t.Errorf("what the administrator was refused left the assignment %+v and %d workspaces", s.w.roles[assignment.ID], len(s.w.workspaces))
	}

	// The owner it named is an owner like any other: it removes the one
	// that was named at registration.
	if err := s.revokeRole(frank, s.cargopai, s.w.ownerOf(t, s.cargopai, alice).ID); err != nil {
		t.Errorf("the owner the administrator named removes the first owner: %v", err)
	}

	// An administrator that owns a product acts there as its owner, and is
	// recorded as one.
	named(t, s.service, s.parcel, bob, admin)
	s.w.quiet()
	if _, err := s.grantRole(admin, s.parcel, carol); err != nil {
		t.Fatalf("an administrator that owns the product names an owner: %v", err)
	}
	if len(s.w.audited) != 1 || s.w.audited[0].entry.Rule != "AC-10" {
		t.Errorf("audited %+v, want one record under the rule of the owners", s.w.audited)
	}
	if err := s.revokeRole(admin, s.parcel, s.w.ownerOf(t, s.parcel, carol).ID); err != nil {
		t.Errorf("an administrator that owns the product removes an owner: %v", err)
	}
}

// An owner of the product and a platform administrator name an owner, and
// nobody else. The answer to anyone else depends on the product alone: it is
// the same whatever the request says.
func TestGrantProductRoleRefusesWhoeverNeitherOwnsTheProductNorAdministersThePlatform(t *testing.T) {
	for name, c := range withoutSay {
		t.Run(name, func(t *testing.T) {
			s := newBoard(t)
			before := maps.Clone(s.w.roles)

			requests := map[string]GrantProductRole{
				"somebody else":                      {Principal: mallory, Role: "product_owner"},
				"itself":                             {Principal: c.caller, Role: "product_owner"},
				"a role nobody defined":              {Principal: mallory, Role: "superuser"},
				"a principal nobody could be":        {Principal: domain.Principal{Issuer: "https://other.example", Subject: "x"}, Role: "product_owner"},
				"a role the principal holds already": {Principal: alice, Role: "product_owner"},
			}
			for what, command := range requests {
				s.w.quiet()
				command.Caller, command.CorrelationID, command.ProductID = c.caller, "correlation-8", string(s.cargopai.ID)

				assignment, err := s.service.GrantProductRole(ctx, command)
				if err != c.want || assignment != (domain.RoleAssignment{}) {
					t.Fatalf("%s: got %+v and %v, want nothing and exactly %v", what, assignment, err, c.want)
				}
				// The decision is made inside the transaction and recorded
				// once that transaction is over.
				want := []audited{{ports.AuditEntry{
					Actor: c.caller, Action: "product_role.grant", Rule: "DENY-03",
					ProductID: s.cargopai.ID, ResourceType: "RoleAssignment", CorrelationID: "correlation-8",
				}, false}}
				if !slices.Equal(s.w.audited, want) {
					t.Errorf("%s: audited\n %+v\nwant, outside the transaction,\n %+v", what, s.w.audited, want)
				}
				if !maps.Equal(s.w.roles, before) || len(s.w.events) != 0 {
					t.Errorf("%s: a refused grant left the roles %+v and the events %v", what, s.w.roles, s.w.events)
				}
			}
		})
	}
}

func TestGrantProductRoleRefusesWhatCannotBeARoleAssignment(t *testing.T) {
	s := newBoard(t)
	before := maps.Clone(s.w.roles)

	grant := func(caller domain.Principal, productID string, principal domain.Principal, role string) error {
		_, err := s.service.GrantProductRole(ctx, GrantProductRole{
			Caller: caller, CorrelationID: "correlation-8", ProductID: productID, Principal: principal, Role: role,
		})
		return err
	}
	cargopai, owner := string(s.cargopai.ID), string(domain.RoleProductOwner)
	untrusted := domain.Principal{Issuer: "https://other.example", Subject: "mallory"}
	cases := map[string]struct {
		err  error
		want error
	}{
		"a role nobody defined":            {grant(alice, cargopai, mallory, "product_reader"), domain.ErrInvalid},
		"a role of a workspace":            {grant(alice, cargopai, mallory, string(domain.RoleWorkspaceMaintainer)), domain.ErrInvalid},
		"no role":                          {grant(alice, cargopai, mallory, ""), domain.ErrInvalid},
		"a principal that is no principal": {grant(alice, cargopai, domain.Principal{Issuer: trustedIssuer}, owner), domain.ErrInvalid},
		// A role of a principal that can never authenticate would never be
		// used.
		"a principal under an issuer nobody trusts": {grant(alice, cargopai, untrusted, owner), ErrOwnerIssuerNotTrusted},
		"a role the principal holds already":        {grant(alice, cargopai, frank, owner), ports.ErrRoleAssignmentExists},
		"the role the owner holds itself":           {grant(alice, cargopai, alice, owner), ports.ErrRoleAssignmentExists},
		"a product that does not exist":             {grant(alice, missingID, mallory, owner), ErrProductNotFound},
		"a product that is no identifier":           {grant(alice, "cargopai", mallory, owner), ErrProductNotFound},
		// The administrator is held to the same rules as an owner.
		"an owner the administrator names twice":            {grant(admin, cargopai, frank, owner), ports.ErrRoleAssignmentExists},
		"somebody nobody trusts, named by an administrator": {grant(admin, cargopai, untrusted, owner), ErrOwnerIssuerNotTrusted},
		"a role nobody defined, from the administrator":     {grant(admin, cargopai, mallory, "superuser"), domain.ErrInvalid},
		"a missing product, for the administrator":          {grant(admin, missingID, mallory, owner), ErrProductNotFound},
	}
	for name, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: got %v, want %v", name, c.err, c.want)
		}
	}
	if !maps.Equal(s.w.roles, before) || len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("refused grants left roles %+v, audit %+v, events %v", s.w.roles, s.w.audited, s.w.events)
	}

	// Whoever does not own the product yet can be made an owner of it,
	// whatever else it is. A role that was revoked can be granted again.
	accepted := map[string]error{
		"a role that was held and revoked":       grant(alice, cargopai, grace, owner),
		"a member of a workspace":                grant(alice, cargopai, carol, owner),
		"the owner of another product":           grant(alice, cargopai, bob, owner),
		"the platform administrator":             grant(alice, cargopai, admin, owner),
		"the owner of this product, in another":  grant(bob, string(s.parcel.ID), alice, owner),
		"somebody the administrator names owner": grant(admin, string(s.parcel.ID), mallory, owner),
	}
	for name, err := range accepted {
		if err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
	if len(s.w.roles) != len(before)+len(accepted) {
		t.Errorf("there are %d role assignments, want %d more than the %d there were", len(s.w.roles), len(accepted), len(before))
	}
	// A role granted again is a new assignment. The record of the one that
	// was revoked stays as the revocation left it.
	if kept := s.w.roles[s.former.ID]; kept != before[s.former.ID] || kept.InForce() {
		t.Errorf("the record of the revoked role is now %+v, want it as it was", kept)
	}
}

// INV-01: a suspended product takes no new owner, from an owner or from the
// administrator. Its owners can still be removed, down to the last.
func TestASuspendedProductTakesNoOwnerAndLetsItsOwnersGo(t *testing.T) {
	s := newBoard(t)
	suspended := s.cargopai
	suspended.Status = domain.ProductSuspended
	s.w.products[s.cargopai.ID] = suspended
	before := maps.Clone(s.w.roles)

	for name, caller := range map[string]domain.Principal{"an owner": alice, "the platform administrator": admin} {
		assignment, err := s.grantRole(caller, s.cargopai, carol)
		if !errors.Is(err, domain.ErrProductNotActive) || assignment != (domain.RoleAssignment{}) {
			t.Fatalf("%s: got %+v and %v, want nothing and %v", name, assignment, err, domain.ErrProductNotActive)
		}
	}
	if !maps.Equal(s.w.roles, before) || len(s.w.events) != 0 || len(s.w.audited) != 0 {
		t.Errorf("a suspended product got roles %+v, events %v, audit %+v", s.w.roles, s.w.events, s.w.audited)
	}

	if err := s.revokeRole(alice, s.cargopai, s.second.ID); err != nil {
		t.Fatalf("revoke a role of a suspended product: %v", err)
	}
	if s.w.roles[s.second.ID].InForce() {
		t.Error("the role of a suspended product is still in force")
	}
	if err := s.revokeRole(alice, s.cargopai, s.first.ID); !errors.Is(err, domain.ErrLastOwner) {
		t.Errorf("the last owner of a suspended product: got %v, want %v", err, domain.ErrLastOwner)
	}
}

// INV-13: the assignment, its audit fact and its event are one transaction.
// Whatever fails takes all of it down.
func TestGrantProductRoleLeavesNothingWhenAnyPartOfItFails(t *testing.T) {
	down := errors.New("down")
	for _, step := range []string{"audit", "events", "commit", "read", "held", "roles"} {
		for name, caller := range map[string]domain.Principal{"an owner": alice, "the platform administrator": admin} {
			t.Run(step+" fails for "+name, func(t *testing.T) {
				s := newBoard(t)
				before := maps.Clone(s.w.roles)
				s.w.failing[step] = down

				if assignment, err := s.grantRole(caller, s.cargopai, carol); !errors.Is(err, down) || assignment != (domain.RoleAssignment{}) {
					t.Fatalf("got %+v and %v, want nothing and the failure", assignment, err)
				}
				if !maps.Equal(s.w.roles, before) || len(s.w.audited)+len(s.w.events) != 0 {
					t.Errorf("a failed grant left roles %+v, audit %+v, events %v", s.w.roles, s.w.audited, s.w.events)
				}
			})
		}
	}

	t.Run("a commit of unknown outcome is reported as such", func(t *testing.T) {
		s := newBoard(t)
		s.w.failing["commit"] = ports.ErrOutcomeUncertain

		if _, err := s.grantRole(alice, s.cargopai, carol); !errors.Is(err, ports.ErrOutcomeUncertain) {
			t.Fatalf("got %v, want %v", err, ports.ErrOutcomeUncertain)
		}
	})
}

func TestRevokeProductRoleEndsTheAssignmentAndRecordsWhoDidIt(t *testing.T) {
	s := newBoard(t)
	s.w.now = started.Add(time.Hour)

	if err := s.revokeRole(alice, s.cargopai, s.second.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	want := s.second
	want.Version, want.RevokedAt, want.RevokedBy = 2, s.w.now, alice
	if got := s.w.roles[s.second.ID]; got != want || got.InForce() {
		t.Errorf("stored\n %+v\nwant, not in force,\n %+v", got, want)
	}
	wantAudit := []audited{{ports.AuditEntry{
		Actor: alice, Action: "product_role.revoke", Allowed: true, Rule: "AC-10",
		ProductID: s.cargopai.ID, ResourceType: "RoleAssignment", ResourceID: string(s.second.ID), CorrelationID: "correlation-9",
	}, true}}
	if !slices.Equal(s.w.audited, wantAudit) {
		t.Errorf("audited\n %+v\nwant, inside the transaction,\n %+v", s.w.audited, wantAudit)
	}
	if want := []string{"ProductRoleChanged " + string(s.second.ID) + " revoked correlation-9"}; !slices.Equal(s.w.events, want) {
		t.Errorf("events %v, want %v", s.w.events, want)
	}
	if s.w.transactions != 1 {
		t.Errorf("%d transactions, want 1", s.w.transactions)
	}
	// The roles of the others are as they were.
	if got := s.w.roles[s.first.ID]; got != s.first {
		t.Errorf("the role of alice is now %+v, want it untouched", got)
	}
	if got := s.w.roles[s.elsewhere.ID]; got != s.elsewhere {
		t.Errorf("the role of bob in Parcel is now %+v, want it untouched", got)
	}
}

// An owner gives up its own role as long as another owner stays. With
// nothing else in the product it is a stranger to it from then on.
func TestAnOwnerRevokesItsOwnRoleWhileAnotherOwnerStays(t *testing.T) {
	s := newBoard(t)

	if err := s.revokeRole(alice, s.cargopai, s.first.ID); err != nil {
		t.Fatalf("an owner revokes its own role: %v", err)
	}
	want := s.first
	want.Version, want.RevokedAt, want.RevokedBy = 2, s.w.now, alice
	if got := s.w.roles[s.first.ID]; got != want {
		t.Errorf("stored\n %+v\nwant\n %+v", got, want)
	}
	if _, err := s.product(alice, s.cargopai); err != ErrProductNotFound {
		t.Errorf("the product, once she gave up her role: got %v, want exactly %v", err, ErrProductNotFound)
	}

	// frank is the last owner now, and has to stay one.
	if err := s.revokeRole(frank, s.cargopai, s.second.ID); !errors.Is(err, domain.ErrLastOwner) {
		t.Errorf("the last owner revokes its own role: got %v, want %v", err, domain.ErrLastOwner)
	}
	if !s.w.roles[s.second.ID].InForce() {
		t.Error("the role of the last owner was revoked")
	}
}

// A product always has an owner. The last assignment in force that makes
// somebody one is not revoked: what was revoked before does not count, and
// neither do the owners of another product.
func TestTheLastOwnerOfAProductIsNotRevoked(t *testing.T) {
	s := newBoard(t)
	before := maps.Clone(s.w.roles)

	// Parcel has one owner, whatever CargoPai has.
	err := s.revokeRole(bob, s.parcel, s.elsewhere.ID)
	if !errors.Is(err, domain.ErrLastOwner) {
		t.Fatalf("got %v, want %v", err, domain.ErrLastOwner)
	}
	// bob may revoke a role of Parcel: it is the product that cannot lose
	// him. What a state does not allow is not a refusal of the caller, and
	// leaves no audit record, as a key that is taken leaves none.
	if !maps.Equal(s.w.roles, before) || len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("a refused revocation left roles %+v, audit %+v, events %v", s.w.roles, s.w.audited, s.w.events)
	}

	// CargoPai has two. Once frank is gone alice is the last, although
	// grace owned the product once.
	if err := s.revokeRole(alice, s.cargopai, s.second.ID); err != nil {
		t.Fatalf("revoke one of two owners: %v", err)
	}
	s.w.quiet()
	if err := s.revokeRole(alice, s.cargopai, s.first.ID); !errors.Is(err, domain.ErrLastOwner) {
		t.Errorf("the last of two owners: got %v, want %v", err, domain.ErrLastOwner)
	}
	if !s.w.roles[s.first.ID].InForce() || len(s.w.audited)+len(s.w.events) != 0 {
		t.Errorf("a refused revocation left the role %+v, audit %+v, events %v", s.w.roles[s.first.ID], s.w.audited, s.w.events)
	}

	// Once another owner is named, the one who was the last can go.
	named(t, s.service, s.parcel, bob, carol)
	if err := s.revokeRole(bob, s.parcel, s.elsewhere.ID); err != nil {
		t.Errorf("revoke an owner that is not the last any more: %v", err)
	}
}

// Only an owner of the product removes an owner. Naming an owner is not
// removing one: the administrator knows the product and may not. The answer
// does not say whether the assignment exists.
func TestRevokeProductRoleRefusesWhoeverDoesNotOwnTheProduct(t *testing.T) {
	callers := maps.Clone(withoutSay)
	callers["the platform administrator"] = answered{admin, ErrForbidden}
	for name, c := range callers {
		t.Run(name, func(t *testing.T) {
			s := newBoard(t)
			before := maps.Clone(s.w.roles)

			for _, asked := range []struct{ id, recorded string }{
				{string(s.second.ID), string(s.second.ID)},
				{string(s.first.ID), string(s.first.ID)},
				// What names no assignment in force of the product is
				// answered the same and names no resource in the record.
				{missingID, ""},
				{string(s.former.ID), ""},
				{string(s.elsewhere.ID), ""},
				{"frank", ""},
			} {
				s.w.quiet()
				err := s.service.RevokeProductRole(ctx, RevokeProductRole{
					Caller: c.caller, CorrelationID: "correlation-9", ProductID: string(s.cargopai.ID), RoleAssignmentID: asked.id,
				})
				if err != c.want {
					t.Fatalf("got %v, want exactly %v", err, c.want)
				}
				want := []audited{{ports.AuditEntry{
					Actor: c.caller, Action: "product_role.revoke", Rule: "DENY-03",
					ProductID: s.cargopai.ID, ResourceType: "RoleAssignment", ResourceID: asked.recorded, CorrelationID: "correlation-9",
				}, false}}
				if !slices.Equal(s.w.audited, want) {
					t.Errorf("audited\n %+v\nwant, outside the transaction,\n %+v", s.w.audited, want)
				}
				if !maps.Equal(s.w.roles, before) || len(s.w.events) != 0 {
					t.Errorf("a refused revocation left the roles %+v and the events %v", s.w.roles, s.w.events)
				}
			}
		})
	}
}

// An assignment that is not in force is not found, whoever ended it. Nothing
// is recorded for a revocation that found nothing to do.
func TestRevokeProductRoleFindsNothingToRevoke(t *testing.T) {
	s := newBoard(t)
	before := maps.Clone(s.w.roles)

	revoke := func(productID, assignmentID string) error {
		return s.service.RevokeProductRole(ctx, RevokeProductRole{
			Caller: alice, CorrelationID: "correlation-9", ProductID: productID, RoleAssignmentID: assignmentID,
		})
	}
	cargopai := string(s.cargopai.ID)
	cases := map[string]struct {
		err  error
		want error
	}{
		"an assignment that does not exist":      {revoke(cargopai, missingID), ErrRoleAssignmentNotFound},
		"an assignment that was revoked already": {revoke(cargopai, string(s.former.ID)), ErrRoleAssignmentNotFound},
		"an assignment of another product":       {revoke(cargopai, string(s.elsewhere.ID)), ErrRoleAssignmentNotFound},
		"an assignment that is no identifier":    {revoke(cargopai, "frank"), ErrRoleAssignmentNotFound},
		"a product that does not exist":          {revoke(missingID, string(s.second.ID)), ErrProductNotFound},
		"a product that is no identifier":        {revoke("cargopai", string(s.second.ID)), ErrProductNotFound},
	}
	for name, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: got %v, want %v", name, c.err, c.want)
		}
	}
	if !maps.Equal(s.w.roles, before) || len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("revocations that found nothing left roles %+v, audit %+v, events %v", s.w.roles, s.w.audited, s.w.events)
	}

	// The store finds it ended after it was read: something ended it that
	// did not lock the owners first. It is not in force, which is what was
	// asked for, and this revocation leaves no record.
	s.w.failing["unassign"] = ports.ErrRoleAssignmentGone
	if err := revoke(cargopai, string(s.second.ID)); !errors.Is(err, ErrRoleAssignmentNotFound) || errors.Is(err, ports.ErrRoleAssignmentGone) {
		t.Errorf("a revocation that lost the race: got %v, want %v and nothing of the store", err, ErrRoleAssignmentNotFound)
	}
	if len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("a revocation that lost the race left audit %+v and events %v", s.w.audited, s.w.events)
	}
}

func TestRevokeProductRoleLeavesTheAssignmentInForceWhenAnyPartOfItFails(t *testing.T) {
	down := errors.New("down")
	for _, step := range []string{"audit", "events", "commit", "read", "held", "roles", "lock", "unassign"} {
		t.Run(step+" fails", func(t *testing.T) {
			s := newBoard(t)
			before := maps.Clone(s.w.roles)
			s.w.failing[step] = down

			if err := s.revokeRole(alice, s.cargopai, s.second.ID); !errors.Is(err, down) {
				t.Fatalf("got %v, want the failure", err)
			}
			if !maps.Equal(s.w.roles, before) || len(s.w.audited)+len(s.w.events) != 0 {
				t.Errorf("a failed revocation left roles %+v, audit %+v, events %v", s.w.roles, s.w.audited, s.w.events)
			}
		})
	}

	t.Run("a commit of unknown outcome is reported as such", func(t *testing.T) {
		s := newBoard(t)
		s.w.failing["commit"] = ports.ErrOutcomeUncertain

		if err := s.revokeRole(alice, s.cargopai, s.second.ID); !errors.Is(err, ports.ErrOutcomeUncertain) {
			t.Fatalf("got %v, want %v", err, ports.ErrOutcomeUncertain)
		}
	})
}

// What a revocation decides from is read once the owners are locked, and a
// revocation that finds them locked waits. So it decides from what the one
// it waited for left behind, not from what was there when it arrived.
func TestARevocationThatWaitedDecidesFromWhatItFindsThen(t *testing.T) {
	// ends is the assignment as it is once by has revoked it, in a
	// transaction of its own that committed.
	ends := func(s board, assignment domain.RoleAssignment, by domain.Principal) func() {
		return func() {
			revoked, err := assignment.Revoke(by, s.w.now, 2)
			if err != nil {
				t.Fatalf("revoke the role of %s: %v", assignment.Principal.Subject, err)
			}
			s.w.roles[assignment.ID] = revoked
		}
	}

	t.Run("two owners revoke each other at once", func(t *testing.T) {
		s := newBoard(t)
		// While frank waits to revoke alice, alice revokes him.
		s.w.meanwhile = ends(s, s.second, alice)

		// He owns the product no more, and is answered as somebody who does
		// not: not as an owner who would be the last.
		if err := s.revokeRole(frank, s.cargopai, s.first.ID); err != ErrProductNotFound {
			t.Fatalf("got %v, want exactly %v", err, ErrProductNotFound)
		}
		want := []audited{{ports.AuditEntry{
			Actor: frank, Action: "product_role.revoke", Rule: "DENY-03",
			ProductID: s.cargopai.ID, ResourceType: "RoleAssignment", ResourceID: string(s.first.ID), CorrelationID: "correlation-9",
		}, false}}
		if !slices.Equal(s.w.audited, want) || len(s.w.events) != 0 {
			t.Errorf("audited\n %+v\nwith the events %v\nwant, outside the transaction and with no event,\n %+v", s.w.audited, s.w.events, want)
		}
		// One of the two is left, and it is the one who came first.
		if !s.w.roles[s.first.ID].InForce() || s.w.roles[s.second.ID].InForce() {
			t.Errorf("alice's role is %+v and frank's %+v, want hers in force and his revoked", s.w.roles[s.first.ID], s.w.roles[s.second.ID])
		}
	})

	t.Run("the other owner gave up its role in the meantime", func(t *testing.T) {
		s := newBoard(t)
		s.w.meanwhile = ends(s, s.second, frank)

		// alice was one of two when she asked to give up hers. She is the
		// last now.
		if err := s.revokeRole(alice, s.cargopai, s.first.ID); !errors.Is(err, domain.ErrLastOwner) {
			t.Fatalf("got %v, want %v", err, domain.ErrLastOwner)
		}
		if !s.w.roles[s.first.ID].InForce() || len(s.w.audited)+len(s.w.events) != 0 {
			t.Errorf("the role is %+v, audit %+v, events %v, want it in force and nothing recorded", s.w.roles[s.first.ID], s.w.audited, s.w.events)
		}
	})

	t.Run("somebody else revoked the same assignment in the meantime", func(t *testing.T) {
		s := newBoard(t)
		named(t, s.service, s.cargopai, alice, carol)
		s.w.quiet()
		s.w.meanwhile = ends(s, s.second, carol)

		// It is not in force, which is what alice asked for. The revocation
		// that did it has its own record, and hers leaves none.
		if err := s.revokeRole(alice, s.cargopai, s.second.ID); !errors.Is(err, ErrRoleAssignmentNotFound) {
			t.Fatalf("got %v, want %v", err, ErrRoleAssignmentNotFound)
		}
		if got := s.w.roles[s.second.ID]; got.InForce() || got.RevokedBy != carol || len(s.w.audited)+len(s.w.events) != 0 {
			t.Errorf("the role is %+v, audit %+v, events %v, want it revoked by carol and nothing recorded", got, s.w.audited, s.w.events)
		}
	})
}

func TestGetProductRoleAnswersAnOwnerOfTheProduct(t *testing.T) {
	s := newBoard(t)

	// An owner reads the role of another owner and its own.
	if got, err := s.role(alice, s.cargopai, s.second.ID); err != nil || got != s.second {
		t.Fatalf("the role of another owner: got %+v and %v, want the assignment", got, err)
	}
	if got, err := s.role(frank, s.cargopai, s.second.ID); err != nil || got != s.second {
		t.Fatalf("its own role: got %+v and %v, want the assignment", got, err)
	}

	get := func(productID, assignmentID string) error {
		got, err := s.service.GetProductRole(ctx, GetProductRole{
			Caller: alice, CorrelationID: "correlation-10", ProductID: productID, RoleAssignmentID: assignmentID,
		})
		if got != (domain.RoleAssignment{}) {
			t.Errorf("an assignment that is not found was returned: %+v", got)
		}
		return err
	}
	cargopai := string(s.cargopai.ID)
	missing := map[string]struct {
		err  error
		want error
	}{
		"an assignment that does not exist": {get(cargopai, missingID), ErrRoleAssignmentNotFound},
		// A revoked assignment grants nothing and is not read either.
		"an assignment that was revoked":      {get(cargopai, string(s.former.ID)), ErrRoleAssignmentNotFound},
		"an assignment of another product":    {get(cargopai, string(s.elsewhere.ID)), ErrRoleAssignmentNotFound},
		"an assignment that is no identifier": {get(cargopai, "frank"), ErrRoleAssignmentNotFound},
		"a product that does not exist":       {get(missingID, string(s.second.ID)), ErrProductNotFound},
		"a product that is no identifier":     {get("cargopai", string(s.second.ID)), ErrProductNotFound},
	}
	for name, c := range missing {
		if c.err != c.want {
			t.Errorf("%s: got %v, want exactly %v", name, c.err, c.want)
		}
	}
	if len(s.w.audited) != 0 || s.w.transactions != 0 {
		t.Errorf("the reads of an owner left the audit entries %+v and %d transactions", s.w.audited, s.w.transactions)
	}
}

// Who owns a product is the owners' to manage and nobody else's to read.
// Whoever does not own the product is answered as far as it may know, and
// the same whether or not the assignment exists.
func TestGetProductRoleIsWithheldFromWhoeverDoesNotOwnTheProduct(t *testing.T) {
	cases := map[string]answered{
		"a stranger":                               {mallory, ErrProductNotFound},
		"the owner of another product":             {bob, ErrProductNotFound},
		"whoever was a member and is not any more": {erin, ErrProductNotFound},
		"whoever was an owner and is not any more": {grace, ErrProductNotFound},
		// They know the product, and nothing of who owns it. The
		// administrator names an owner and reads none.
		"the platform administrator":    {admin, ErrRoleAssignmentNotFound},
		"a member of a workspace":       {carol, ErrRoleAssignmentNotFound},
		"a member of another workspace": {dave, ErrRoleAssignmentNotFound},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newBoard(t)

			for _, asked := range []struct{ id, recorded string }{
				{string(s.second.ID), string(s.second.ID)},
				{missingID, ""},
				{string(s.former.ID), ""},
				{string(s.elsewhere.ID), ""},
			} {
				s.w.quiet()
				got, err := s.role(c.caller, s.cargopai, domain.RoleAssignmentID(asked.id))
				if err != c.want || got != (domain.RoleAssignment{}) {
					t.Fatalf("got %+v and %v, want nothing and exactly %v", got, err, c.want)
				}
				want := []audited{{ports.AuditEntry{
					Actor: c.caller, Action: "product_role.read", Rule: "DENY-03",
					ProductID: s.cargopai.ID, ResourceType: "RoleAssignment", ResourceID: asked.recorded, CorrelationID: "correlation-10",
				}, false}}
				if !slices.Equal(s.w.audited, want) {
					t.Errorf("audited\n %+v\nwant\n %+v", s.w.audited, want)
				}
			}
		})
	}
}

// A role that is revoked grants nothing from that moment on: every use case
// reads what the caller holds when it decides. With nothing left in the
// product its former owner is a stranger to it.
func TestARevokedOwnerLosesWhatTheRoleGaveAtOnce(t *testing.T) {
	s := newBoard(t)

	// what frank is answered by everything an owner may do.
	attempts := func() map[string]error {
		_, product := s.product(frank, s.cargopai)
		_, workspace := s.workspace(frank, s.engineering)
		_, create := s.service.CreateWorkspace(ctx, CreateWorkspace{Caller: frank, ProductID: string(s.cargopai.ID), Key: "his", DisplayName: "His"})
		_, member := s.grant(frank, s.engineering, mallory, domain.RoleInternalAuthor)
		_, membership := s.membership(frank, s.engineering, s.maintainer.ID)
		unmember := s.revoke(frank, s.engineering, s.author.ID)
		_, owner := s.grantRole(frank, s.cargopai, mallory)
		_, role := s.role(frank, s.cargopai, s.first.ID)
		unowner := s.revokeRole(frank, s.cargopai, missingID)
		return map[string]error{
			"GetProduct": product, "GetWorkspace": workspace, "CreateWorkspace": create,
			"GrantMembership": member, "GetMembership": membership, "RevokeMembership": unmember,
			"GrantProductRole": owner, "GetProductRole": role, "RevokeProductRole": unowner,
		}
	}

	if _, err := s.workspace(frank, s.operations); err != nil {
		t.Fatalf("before the revocation: %v", err)
	}
	if err := s.revokeRole(alice, s.cargopai, s.second.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	before, workspaces, memberships := maps.Clone(s.w.roles), len(s.w.workspaces), maps.Clone(s.w.memberships)

	for name, err := range attempts() {
		if err != ErrProductNotFound {
			t.Errorf("%s, once his role is revoked: got %v, want exactly %v", name, err, ErrProductNotFound)
		}
	}
	if !maps.Equal(s.w.roles, before) || len(s.w.workspaces) != workspaces || !maps.Equal(s.w.memberships, memberships) {
		t.Errorf("what a revoked owner was refused left roles %+v, %d workspaces, memberships %+v", s.w.roles, len(s.w.workspaces), s.w.memberships)
	}

	// Granted again, the role lets him in again: what counts is what is in
	// force.
	again := named(t, s.service, s.cargopai, alice, frank)
	if again.ID == s.second.ID {
		t.Error("the role granted again is the assignment that was revoked")
	}
	if got, err := s.role(frank, s.cargopai, again.ID); err != nil || got != again {
		t.Errorf("after a new grant: got %+v and %v, want the new assignment", got, err)
	}
	if _, err := s.workspace(frank, s.operations); err != nil {
		t.Errorf("after a new grant he reads a workspace: %v", err)
	}
}

// Taking the role of an owner away takes away what it granted and nothing
// more. An owner who is also a member of a workspace keeps what the
// membership gives: its workspace and the product of it.
func TestARevokedOwnerWhoIsAMemberKeepsWhatTheMembershipGivesAndNoMore(t *testing.T) {
	s := newBoard(t)
	own := joined(t, s.service, s.engineering, alice, frank, domain.RoleWorkspaceMaintainer)
	if err := s.revokeRole(alice, s.cargopai, s.second.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	before, workspaces, memberships := maps.Clone(s.w.roles), len(s.w.workspaces), maps.Clone(s.w.memberships)

	if got, err := s.product(frank, s.cargopai); err != nil || got != s.cargopai {
		t.Errorf("the product of his workspace: got %+v and %v, want the product", got, err)
	}
	if got, err := s.workspace(frank, s.engineering); err != nil || got != s.engineering {
		t.Errorf("his workspace: got %+v and %v, want the workspace", got, err)
	}

	_, operations := s.workspace(frank, s.operations)
	_, create := s.service.CreateWorkspace(ctx, CreateWorkspace{Caller: frank, ProductID: string(s.cargopai.ID), Key: "his", DisplayName: "His"})
	_, member := s.grant(frank, s.engineering, mallory, domain.RoleInternalAuthor)
	_, membership := s.membership(frank, s.engineering, own.ID)
	unmember := s.revoke(frank, s.engineering, s.maintainer.ID)
	_, owner := s.grantRole(frank, s.cargopai, mallory)
	_, role := s.role(frank, s.cargopai, s.first.ID)
	unowner := s.revokeRole(frank, s.cargopai, s.first.ID)
	for name, c := range map[string]struct {
		err  error
		want error
	}{
		// A member of one workspace is not a member of another, and reads
		// no membership, its own included.
		"another workspace": {operations, ErrWorkspaceNotFound},
		"GetMembership":     {membership, ErrMembershipNotFound},
		// A member knows the product and manages nothing in it.
		"CreateWorkspace":   {create, ErrForbidden},
		"GrantMembership":   {member, ErrForbidden},
		"RevokeMembership":  {unmember, ErrForbidden},
		"GrantProductRole":  {owner, ErrForbidden},
		"RevokeProductRole": {unowner, ErrForbidden},
		"GetProductRole":    {role, ErrRoleAssignmentNotFound},
	} {
		if c.err != c.want {
			t.Errorf("%s, as a member who owned the product: got %v, want exactly %v", name, c.err, c.want)
		}
	}
	if !maps.Equal(s.w.roles, before) || len(s.w.workspaces) != workspaces || !maps.Equal(s.w.memberships, memberships) {
		t.Errorf("what a member was refused left roles %+v, %d workspaces, memberships %+v", s.w.roles, len(s.w.workspaces), s.w.memberships)
	}
}

// A refusal stands whether or not it could be recorded.
func TestARefusalAboutARoleStandsWhetherOrNotItCouldBeAudited(t *testing.T) {
	s := newBoard(t)
	before := maps.Clone(s.w.roles)
	s.w.failing["audit"] = errors.New("the audit trail is down")

	_, err := s.grantRole(mallory, s.cargopai, mallory)
	if !errors.Is(err, ErrProductNotFound) || !errors.Is(err, ErrRefusalNotAudited) {
		t.Errorf("a grant: got %v, want %v and %v", err, ErrProductNotFound, ErrRefusalNotAudited)
	}
	_, err = s.role(admin, s.cargopai, s.second.ID)
	if !errors.Is(err, ErrRoleAssignmentNotFound) || !errors.Is(err, ErrRefusalNotAudited) {
		t.Errorf("a read: got %v, want %v and %v", err, ErrRoleAssignmentNotFound, ErrRefusalNotAudited)
	}
	err = s.revokeRole(admin, s.cargopai, s.second.ID)
	if !errors.Is(err, ErrForbidden) || !errors.Is(err, ErrRefusalNotAudited) {
		t.Errorf("a revocation: got %v, want %v and %v", err, ErrForbidden, ErrRefusalNotAudited)
	}
	if !maps.Equal(s.w.roles, before) {
		t.Errorf("refusals left the roles %+v, want them as they were", s.w.roles)
	}
}
