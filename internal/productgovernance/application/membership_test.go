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
	dave = domain.Principal{Issuer: trustedIssuer, Subject: "dave"}
	erin = domain.Principal{Issuer: trustedIssuer, Subject: "erin"}
)

// missingID is an identifier that names nothing.
const missingID = "00000000-0000-4000-8000-999999999999"

// joined makes member hold the role in the workspace, as an owner of its
// product.
func joined(t *testing.T, service *ProductGovernance, workspace domain.Workspace, owner, member domain.Principal, role domain.Role) domain.Membership {
	t.Helper()

	membership, err := service.GrantMembership(ctx, GrantMembership{
		Caller: owner, CorrelationID: "correlation-m", ProductID: string(workspace.ProductID), WorkspaceID: string(workspace.ID),
		Member: member, Role: string(role),
	})
	if err != nil {
		t.Fatalf("make %s a member of %s: %v", member.Subject, workspace.Key, err)
	}
	return membership
}

// team is two products and who holds what in them.
//
// CargoPai is owned by alice and has two workspaces. carol maintains the
// first, dave writes in the second, and erin was a member of the first until
// alice revoked her membership. Parcel is owned by bob and has one
// workspace. mallory holds nothing anywhere.
type team struct {
	w       *world
	service *ProductGovernance

	cargopai, parcel        domain.Product
	engineering, operations domain.Workspace
	sorting                 domain.Workspace

	// maintainer is carol's membership of engineering, author dave's of
	// operations, and former the one erin held in engineering.
	maintainer, author, former domain.Membership
}

func newTeam(t *testing.T) team {
	t.Helper()

	w, service := newWorld(t)
	s := team{w: w, service: service}
	s.cargopai, s.parcel = registered(t, service, "cargopai", alice), registered(t, service, "parcel", bob)
	s.engineering = created(t, service, s.cargopai, alice, "api-engineering")
	s.operations = created(t, service, s.cargopai, alice, "operations")
	s.sorting = created(t, service, s.parcel, bob, "sorting")
	s.maintainer = joined(t, service, s.engineering, alice, carol, domain.RoleWorkspaceMaintainer)
	s.author = joined(t, service, s.operations, alice, dave, domain.RoleInternalAuthor)
	s.former = joined(t, service, s.engineering, alice, erin, domain.RoleInternalAuthor)
	if err := s.revoke(alice, s.engineering, s.former.ID); err != nil {
		t.Fatalf("revoke the membership of erin: %v", err)
	}
	w.quiet()
	return s
}

func (s team) grant(caller domain.Principal, workspace domain.Workspace, member domain.Principal, role domain.Role) (domain.Membership, error) {
	return s.service.GrantMembership(ctx, GrantMembership{
		Caller: caller, CorrelationID: "correlation-5", ProductID: string(workspace.ProductID), WorkspaceID: string(workspace.ID),
		Member: member, Role: string(role),
	})
}

func (s team) revoke(caller domain.Principal, workspace domain.Workspace, id domain.MembershipID) error {
	return s.service.RevokeMembership(ctx, RevokeMembership{
		Caller: caller, CorrelationID: "correlation-6", ProductID: string(workspace.ProductID), WorkspaceID: string(workspace.ID),
		MembershipID: string(id),
	})
}

func (s team) membership(caller domain.Principal, workspace domain.Workspace, id domain.MembershipID) (domain.Membership, error) {
	return s.service.GetMembership(ctx, GetMembership{
		Caller: caller, CorrelationID: "correlation-7", ProductID: string(workspace.ProductID), WorkspaceID: string(workspace.ID),
		MembershipID: string(id),
	})
}

func (s team) product(caller domain.Principal, product domain.Product) (domain.Product, error) {
	return s.service.GetProduct(ctx, GetProduct{Caller: caller, CorrelationID: "correlation-2", ProductID: string(product.ID)})
}

func (s team) workspace(caller domain.Principal, workspace domain.Workspace) (domain.Workspace, error) {
	return s.service.GetWorkspace(ctx, GetWorkspace{
		Caller: caller, CorrelationID: "correlation-4", ProductID: string(workspace.ProductID), WorkspaceID: string(workspace.ID),
	})
}

// notOwners are the callers that do not own CargoPai, with what each is told
// when it asks for what only an owner may do. Whoever may know the product
// exists is told it may not; to anyone else there is no such product.
var notOwners = map[string]struct {
	caller domain.Principal
	want   error
}{
	// Registering a product gives no power inside it.
	"the platform administrator": {admin, ErrForbidden},
	// A member knows the product and does not manage it.
	"a member of the workspace":                    {carol, ErrForbidden},
	"a member of another workspace of the product": {dave, ErrForbidden},
	"a stranger":                   {mallory, ErrProductNotFound},
	"the owner of another product": {bob, ErrProductNotFound},
	// A membership that was revoked leaves a stranger behind.
	"whoever was a member and is not any more": {erin, ErrProductNotFound},
}

func TestGrantMembershipStoresTheMembershipTheAuditFactAndTheEventTogether(t *testing.T) {
	s := newTeam(t)
	s.w.now = started.Add(time.Hour)
	before := len(s.w.memberships)

	membership, err := s.grant(alice, s.engineering, bob, domain.RoleInternalAuthor)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}

	want := domain.Membership{
		ID: membership.ID, ProductID: s.cargopai.ID, WorkspaceID: s.engineering.ID, Principal: bob, Role: domain.RoleInternalAuthor,
		Version: 1, GrantedAt: s.w.now, GrantedBy: alice,
	}
	if membership != want || s.w.memberships[membership.ID] != want {
		t.Errorf("membership\n %+v\nstored\n %+v\nwant\n %+v", membership, s.w.memberships[membership.ID], want)
	}
	if _, err := domain.ParseMembershipID(string(membership.ID)); err != nil || len(s.w.memberships) != before+1 {
		t.Errorf("the membership has the identifier %q and there are %d memberships, want a new one", membership.ID, len(s.w.memberships))
	}
	wantAudit := []audited{{ports.AuditEntry{
		Actor: alice, Action: "member.grant", Allowed: true, Rule: "AC-10",
		ProductID: s.cargopai.ID, ResourceType: "Member", ResourceID: string(membership.ID), CorrelationID: "correlation-5",
	}, true}}
	if !slices.Equal(s.w.audited, wantAudit) {
		t.Errorf("audited\n %+v\nwant, inside the transaction,\n %+v", s.w.audited, wantAudit)
	}
	if want := []string{"MembershipChanged " + string(membership.ID) + " granted correlation-5"}; !slices.Equal(s.w.events, want) {
		t.Errorf("events %v, want %v", s.w.events, want)
	}
	if s.w.transactions != 1 {
		t.Errorf("%d transactions, want 1", s.w.transactions)
	}
}

// AC-10: only an owner of the product makes somebody a member of one of its
// workspaces. The answer to anyone else depends on the product alone: it is
// the same whether or not the workspace exists and whatever the request
// says.
func TestGrantMembershipRefusesWhoeverDoesNotOwnTheProduct(t *testing.T) {
	for name, c := range notOwners {
		t.Run(name, func(t *testing.T) {
			s := newTeam(t)
			before := maps.Clone(s.w.memberships)

			requests := map[string]GrantMembership{
				"its workspace":                   {WorkspaceID: string(s.engineering.ID), Member: mallory, Role: "internal_author"},
				"a workspace that is not there":   {WorkspaceID: missingID, Member: mallory, Role: "internal_author"},
				"an identifier that is none":      {WorkspaceID: "api-engineering", Member: mallory, Role: "internal_author"},
				"a role nobody defined":           {WorkspaceID: string(s.engineering.ID), Member: mallory, Role: "superuser"},
				"a member nobody could be":        {WorkspaceID: string(s.engineering.ID), Member: domain.Principal{Issuer: "https://other.example", Subject: "x"}, Role: "internal_author"},
				"a role the member holds already": {WorkspaceID: string(s.engineering.ID), Member: carol, Role: "workspace_maintainer"},
			}
			for what, command := range requests {
				s.w.quiet()
				command.Caller, command.CorrelationID, command.ProductID = c.caller, "correlation-5", string(s.cargopai.ID)

				membership, err := s.service.GrantMembership(ctx, command)
				if err != c.want || membership != (domain.Membership{}) {
					t.Fatalf("%s: got %+v and %v, want nothing and exactly %v", what, membership, err, c.want)
				}
				// The decision is made inside the transaction and recorded
				// once that transaction is over.
				want := []audited{{ports.AuditEntry{
					Actor: c.caller, Action: "member.grant", Rule: "DENY-03",
					ProductID: s.cargopai.ID, ResourceType: "Member", CorrelationID: "correlation-5",
				}, false}}
				if !slices.Equal(s.w.audited, want) {
					t.Errorf("%s: audited\n %+v\nwant, outside the transaction,\n %+v", what, s.w.audited, want)
				}
				if !maps.Equal(s.w.memberships, before) || len(s.w.events) != 0 {
					t.Errorf("%s: a refused grant left the memberships %+v and the events %v", what, s.w.memberships, s.w.events)
				}
			}
		})
	}
}

func TestGrantMembershipRefusesWhatCannotBeAMembership(t *testing.T) {
	s := newTeam(t)
	// A second product of alice's, to name one of its workspaces under
	// CargoPai.
	express := registered(t, s.service, "express", alice)
	dispatch := created(t, s.service, express, alice, "dispatch")
	s.w.quiet()
	before := maps.Clone(s.w.memberships)

	grant := func(productID, workspaceID string, member domain.Principal, role string) error {
		_, err := s.service.GrantMembership(ctx, GrantMembership{
			Caller: alice, CorrelationID: "correlation-5", ProductID: productID, WorkspaceID: workspaceID, Member: member, Role: role,
		})
		return err
	}
	cargopai, engineering, author := string(s.cargopai.ID), string(s.engineering.ID), string(domain.RoleInternalAuthor)
	cases := map[string]struct {
		err  error
		want error
	}{
		"a role nobody defined":         {grant(cargopai, engineering, mallory, "internal_reader"), domain.ErrInvalid},
		"the role that owns a product":  {grant(cargopai, engineering, mallory, string(domain.RoleProductOwner)), domain.ErrInvalid},
		"no role":                       {grant(cargopai, engineering, mallory, ""), domain.ErrInvalid},
		"a member that is no principal": {grant(cargopai, engineering, domain.Principal{Issuer: trustedIssuer}, author), domain.ErrInvalid},
		// A membership of a principal that can never authenticate would
		// never be used.
		"a member under an issuer nobody trusts": {
			grant(cargopai, engineering, domain.Principal{Issuer: "https://other.example", Subject: "mallory"}, author), ErrMemberIssuerNotTrusted},
		"a role the member holds already":   {grant(cargopai, engineering, carol, string(domain.RoleWorkspaceMaintainer)), ports.ErrMembershipExists},
		"a workspace that does not exist":   {grant(cargopai, missingID, mallory, author), ErrWorkspaceNotFound},
		"a workspace of another product":    {grant(cargopai, string(dispatch.ID), mallory, author), ErrWorkspaceNotFound},
		"a workspace that is no identifier": {grant(cargopai, "api-engineering", mallory, author), ErrWorkspaceNotFound},
		"a product that does not exist":     {grant(missingID, engineering, mallory, author), ErrProductNotFound},
		"a product that is no identifier":   {grant("cargopai", engineering, mallory, author), ErrProductNotFound},
	}
	for name, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: got %v, want %v", name, c.err, c.want)
		}
	}
	if !maps.Equal(s.w.memberships, before) || len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("refused grants left memberships %+v, audit %+v, events %v", s.w.memberships, s.w.audited, s.w.events)
	}

	// What differs from a membership in force is another membership: another
	// role in the same workspace, the same role in another workspace. A role
	// that was revoked can be granted again. An owner may be a member of its
	// own workspace, and so may the administrator of the platform.
	accepted := map[string]error{
		"another role in the same workspace":   grant(cargopai, engineering, carol, author),
		"the same role in another workspace":   grant(cargopai, string(s.operations.ID), carol, string(domain.RoleWorkspaceMaintainer)),
		"a role that was held and revoked":     grant(cargopai, engineering, erin, author),
		"the owner in its own workspace":       grant(cargopai, engineering, alice, author),
		"the platform administrator as member": grant(cargopai, engineering, admin, author),
		"somebody in the same owner's product": grant(string(express.ID), string(dispatch.ID), carol, author),
	}
	for name, err := range accepted {
		if err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
	if len(s.w.memberships) != len(before)+len(accepted) {
		t.Errorf("there are %d memberships, want %d more than the %d there were", len(s.w.memberships), len(accepted), len(before))
	}
}

// INV-01: a suspended product takes no new member. Its members can still be
// removed.
func TestASuspendedProductTakesNoMemberAndLetsItsMembersGo(t *testing.T) {
	s := newTeam(t)
	suspended := s.cargopai
	suspended.Status = domain.ProductSuspended
	s.w.products[s.cargopai.ID] = suspended
	before := maps.Clone(s.w.memberships)

	membership, err := s.grant(alice, s.engineering, bob, domain.RoleInternalAuthor)
	if !errors.Is(err, domain.ErrProductNotActive) || membership != (domain.Membership{}) {
		t.Fatalf("got %+v and %v, want nothing and %v", membership, err, domain.ErrProductNotActive)
	}
	if !maps.Equal(s.w.memberships, before) || len(s.w.events) != 0 || len(s.w.audited) != 0 {
		t.Errorf("a suspended product got memberships %+v, events %v, audit %+v", s.w.memberships, s.w.events, s.w.audited)
	}

	if err := s.revoke(alice, s.engineering, s.maintainer.ID); err != nil {
		t.Fatalf("revoke a membership of a suspended product: %v", err)
	}
	if s.w.memberships[s.maintainer.ID].InForce() {
		t.Error("the membership of a suspended product is still in force")
	}
}

// INV-13: the membership, its audit fact and its event are one transaction.
// Whatever fails takes all of it down.
func TestGrantMembershipLeavesNothingWhenAnyPartOfItFails(t *testing.T) {
	down := errors.New("down")
	for _, step := range []string{"audit", "events", "commit", "read", "held"} {
		t.Run(step+" fails", func(t *testing.T) {
			s := newTeam(t)
			before := maps.Clone(s.w.memberships)
			s.w.failing[step] = down

			if membership, err := s.grant(alice, s.engineering, bob, domain.RoleInternalAuthor); !errors.Is(err, down) || membership != (domain.Membership{}) {
				t.Fatalf("got %+v and %v, want nothing and the failure", membership, err)
			}
			if !maps.Equal(s.w.memberships, before) || len(s.w.audited)+len(s.w.events) != 0 {
				t.Errorf("a failed grant left memberships %+v, audit %+v, events %v", s.w.memberships, s.w.audited, s.w.events)
			}
		})
	}

	t.Run("a commit of unknown outcome is reported as such", func(t *testing.T) {
		s := newTeam(t)
		s.w.failing["commit"] = ports.ErrOutcomeUncertain

		if _, err := s.grant(alice, s.engineering, bob, domain.RoleInternalAuthor); !errors.Is(err, ports.ErrOutcomeUncertain) {
			t.Fatalf("got %v, want %v", err, ports.ErrOutcomeUncertain)
		}
	})
}

func TestRevokeMembershipEndsTheMembershipAndRecordsWhoDidIt(t *testing.T) {
	s := newTeam(t)
	s.w.now = started.Add(time.Hour)

	if err := s.revoke(alice, s.engineering, s.maintainer.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	want := s.maintainer
	want.Version, want.RevokedAt, want.RevokedBy = 2, s.w.now, alice
	if got := s.w.memberships[s.maintainer.ID]; got != want || got.InForce() {
		t.Errorf("stored\n %+v\nwant, not in force,\n %+v", got, want)
	}
	wantAudit := []audited{{ports.AuditEntry{
		Actor: alice, Action: "member.revoke", Allowed: true, Rule: "AC-10",
		ProductID: s.cargopai.ID, ResourceType: "Member", ResourceID: string(s.maintainer.ID), CorrelationID: "correlation-6",
	}, true}}
	if !slices.Equal(s.w.audited, wantAudit) {
		t.Errorf("audited\n %+v\nwant, inside the transaction,\n %+v", s.w.audited, wantAudit)
	}
	if want := []string{"MembershipChanged " + string(s.maintainer.ID) + " revoked correlation-6"}; !slices.Equal(s.w.events, want) {
		t.Errorf("events %v, want %v", s.w.events, want)
	}
	if s.w.transactions != 1 {
		t.Errorf("%d transactions, want 1", s.w.transactions)
	}
	// The membership of somebody else is as it was.
	if got := s.w.memberships[s.author.ID]; got != s.author {
		t.Errorf("the membership of dave is now %+v, want it untouched", got)
	}
}

// Whoever may grant a membership may revoke one, and nobody else: not the
// member itself, and not a member of the same workspace. The answer does not
// say whether the membership exists.
func TestRevokeMembershipRefusesWhoeverDoesNotOwnTheProduct(t *testing.T) {
	for name, c := range notOwners {
		t.Run(name, func(t *testing.T) {
			s := newTeam(t)
			before := maps.Clone(s.w.memberships)

			for _, asked := range []struct{ workspace, membership, recorded string }{
				// carol's own membership, when carol asks.
				{string(s.engineering.ID), string(s.maintainer.ID), string(s.maintainer.ID)},
				// What names no membership in force of the workspace is
				// answered the same and names no resource in the record.
				{string(s.engineering.ID), missingID, ""},
				{string(s.engineering.ID), string(s.former.ID), ""},
				{string(s.operations.ID), string(s.maintainer.ID), ""},
				{missingID, string(s.maintainer.ID), ""},
				{string(s.engineering.ID), "carol", ""},
			} {
				s.w.quiet()
				err := s.service.RevokeMembership(ctx, RevokeMembership{
					Caller: c.caller, CorrelationID: "correlation-6", ProductID: string(s.cargopai.ID),
					WorkspaceID: asked.workspace, MembershipID: asked.membership,
				})
				if err != c.want {
					t.Fatalf("got %v, want exactly %v", err, c.want)
				}
				want := []audited{{ports.AuditEntry{
					Actor: c.caller, Action: "member.revoke", Rule: "DENY-03",
					ProductID: s.cargopai.ID, ResourceType: "Member", ResourceID: asked.recorded, CorrelationID: "correlation-6",
				}, false}}
				if !slices.Equal(s.w.audited, want) {
					t.Errorf("audited\n %+v\nwant, outside the transaction,\n %+v", s.w.audited, want)
				}
				if !maps.Equal(s.w.memberships, before) || len(s.w.events) != 0 {
					t.Errorf("a refused revocation left the memberships %+v and the events %v", s.w.memberships, s.w.events)
				}
			}
		})
	}
}

// A membership that is not in force is not found, whoever ended it. Nothing
// is recorded for a revocation that found nothing to do.
func TestRevokeMembershipFindsNothingToRevoke(t *testing.T) {
	s := newTeam(t)
	before := maps.Clone(s.w.memberships)

	revoke := func(productID, workspaceID, membershipID string) error {
		return s.service.RevokeMembership(ctx, RevokeMembership{
			Caller: alice, CorrelationID: "correlation-6", ProductID: productID, WorkspaceID: workspaceID, MembershipID: membershipID,
		})
	}
	cargopai, engineering := string(s.cargopai.ID), string(s.engineering.ID)
	cases := map[string]struct {
		err  error
		want error
	}{
		"a membership that does not exist":      {revoke(cargopai, engineering, missingID), ErrMembershipNotFound},
		"a membership that was revoked already": {revoke(cargopai, engineering, string(s.former.ID)), ErrMembershipNotFound},
		"a membership of another workspace":     {revoke(cargopai, engineering, string(s.author.ID)), ErrMembershipNotFound},
		"a membership under another workspace":  {revoke(cargopai, string(s.operations.ID), string(s.maintainer.ID)), ErrMembershipNotFound},
		"a membership that is no identifier":    {revoke(cargopai, engineering, "carol"), ErrMembershipNotFound},
		"a workspace that does not exist":       {revoke(cargopai, missingID, string(s.maintainer.ID)), ErrWorkspaceNotFound},
		"a workspace that is no identifier":     {revoke(cargopai, "api-engineering", string(s.maintainer.ID)), ErrWorkspaceNotFound},
		"a product that does not exist":         {revoke(missingID, engineering, string(s.maintainer.ID)), ErrProductNotFound},
		"a product that is no identifier":       {revoke("cargopai", engineering, string(s.maintainer.ID)), ErrProductNotFound},
	}
	for name, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%s: got %v, want %v", name, c.err, c.want)
		}
	}
	if !maps.Equal(s.w.memberships, before) || len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("revocations that found nothing left memberships %+v, audit %+v, events %v", s.w.memberships, s.w.audited, s.w.events)
	}

	// Somebody else ended it between the read and the write. It is not in
	// force, which is what was asked for; the revocation that did it has its
	// own record, and this one leaves none.
	s.w.failing["revoke"] = ports.ErrMembershipGone
	if err := revoke(cargopai, engineering, string(s.maintainer.ID)); !errors.Is(err, ErrMembershipNotFound) || errors.Is(err, ports.ErrMembershipGone) {
		t.Errorf("a revocation that lost the race: got %v, want %v and nothing of the store", err, ErrMembershipNotFound)
	}
	if len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("a revocation that lost the race left audit %+v and events %v", s.w.audited, s.w.events)
	}
}

func TestRevokeMembershipLeavesTheMembershipInForceWhenAnyPartOfItFails(t *testing.T) {
	down := errors.New("down")
	for _, step := range []string{"audit", "events", "commit", "read", "held", "revoke"} {
		t.Run(step+" fails", func(t *testing.T) {
			s := newTeam(t)
			before := maps.Clone(s.w.memberships)
			s.w.failing[step] = down

			if err := s.revoke(alice, s.engineering, s.maintainer.ID); !errors.Is(err, down) {
				t.Fatalf("got %v, want the failure", err)
			}
			if !maps.Equal(s.w.memberships, before) || len(s.w.audited)+len(s.w.events) != 0 {
				t.Errorf("a failed revocation left memberships %+v, audit %+v, events %v", s.w.memberships, s.w.audited, s.w.events)
			}
		})
	}

	t.Run("a commit of unknown outcome is reported as such", func(t *testing.T) {
		s := newTeam(t)
		s.w.failing["commit"] = ports.ErrOutcomeUncertain

		if err := s.revoke(alice, s.engineering, s.maintainer.ID); !errors.Is(err, ports.ErrOutcomeUncertain) {
			t.Fatalf("got %v, want %v", err, ports.ErrOutcomeUncertain)
		}
	})
}

func TestGetMembershipAnswersAnOwnerOfTheProduct(t *testing.T) {
	s := newTeam(t)

	if got, err := s.membership(alice, s.engineering, s.maintainer.ID); err != nil || got != s.maintainer {
		t.Fatalf("an owner: got %+v and %v, want the membership", got, err)
	}

	get := func(productID, workspaceID, membershipID string) error {
		got, err := s.service.GetMembership(ctx, GetMembership{
			Caller: alice, CorrelationID: "correlation-7", ProductID: productID, WorkspaceID: workspaceID, MembershipID: membershipID,
		})
		if got != (domain.Membership{}) {
			t.Errorf("a membership that is not found was returned: %+v", got)
		}
		return err
	}
	cargopai, engineering := string(s.cargopai.ID), string(s.engineering.ID)
	missing := map[string]struct {
		err  error
		want error
	}{
		"a membership that does not exist": {get(cargopai, engineering, missingID), ErrMembershipNotFound},
		// A revoked membership grants nothing and is not read either.
		"a membership that was revoked":      {get(cargopai, engineering, string(s.former.ID)), ErrMembershipNotFound},
		"a membership of another workspace":  {get(cargopai, engineering, string(s.author.ID)), ErrMembershipNotFound},
		"a membership that is no identifier": {get(cargopai, engineering, "carol"), ErrMembershipNotFound},
		"a workspace that does not exist":    {get(cargopai, missingID, string(s.maintainer.ID)), ErrWorkspaceNotFound},
		"a workspace that is no identifier":  {get(cargopai, "api-engineering", string(s.maintainer.ID)), ErrWorkspaceNotFound},
		"a product that does not exist":      {get(missingID, engineering, string(s.maintainer.ID)), ErrProductNotFound},
		"a product that is no identifier":    {get("cargopai", engineering, string(s.maintainer.ID)), ErrProductNotFound},
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

// The members of a workspace are the owner's to manage and nobody else's to
// read. Whoever does not own the product is answered as far as it may know,
// and the same whether or not the membership exists.
func TestGetMembershipIsWithheldFromWhoeverDoesNotOwnTheProduct(t *testing.T) {
	cases := map[string]struct {
		caller domain.Principal
		want   error
	}{
		"a stranger":                               {mallory, ErrProductNotFound},
		"the owner of another product":             {bob, ErrProductNotFound},
		"whoever was a member and is not any more": {erin, ErrProductNotFound},
		// Both know the product and nothing of this workspace.
		"the platform administrator":                   {admin, ErrWorkspaceNotFound},
		"a member of another workspace of the product": {dave, ErrWorkspaceNotFound},
		// The member knows the workspace. Its members are withheld, its own
		// membership included.
		"the member itself": {carol, ErrMembershipNotFound},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newTeam(t)

			for _, asked := range []struct{ id, recorded string }{
				{string(s.maintainer.ID), string(s.maintainer.ID)},
				{missingID, ""},
				{string(s.former.ID), ""},
			} {
				s.w.quiet()
				got, err := s.membership(c.caller, s.engineering, domain.MembershipID(asked.id))
				if err != c.want || got != (domain.Membership{}) {
					t.Fatalf("got %+v and %v, want nothing and exactly %v", got, err, c.want)
				}
				want := []audited{{ports.AuditEntry{
					Actor: c.caller, Action: "member.read", Rule: "DENY-03",
					ProductID: s.cargopai.ID, ResourceType: "Member", ResourceID: asked.recorded, CorrelationID: "correlation-7",
				}, false}}
				if !slices.Equal(s.w.audited, want) {
					t.Errorf("audited\n %+v\nwant\n %+v", s.w.audited, want)
				}
			}
		})
	}
}

// DC-001: an explicit membership in force lets a principal into the
// workspace it names, and into nothing else.
func TestAMemberReadsItsWorkspaceAndItsProductAndNothingElse(t *testing.T) {
	s := newTeam(t)

	if got, err := s.workspace(carol, s.engineering); err != nil || got != s.engineering {
		t.Errorf("her workspace: got %+v and %v, want the workspace", got, err)
	}
	if got, err := s.product(carol, s.cargopai); err != nil || got != s.cargopai {
		t.Errorf("the product of her workspace: got %+v and %v, want the product", got, err)
	}
	if len(s.w.audited) != 0 {
		t.Fatalf("reads that were allowed left the audit entries %+v", s.w.audited)
	}

	// Being a member of one workspace of a product is not being a member of
	// another (F03 §2). She knows the product, so she is told there is no
	// such workspace, as for one that does not exist.
	for _, asked := range []struct{ id, recorded string }{{string(s.operations.ID), string(s.operations.ID)}, {missingID, ""}} {
		s.w.quiet()
		got, err := s.service.GetWorkspace(ctx, GetWorkspace{
			Caller: carol, CorrelationID: "correlation-4", ProductID: string(s.cargopai.ID), WorkspaceID: asked.id,
		})
		if err != ErrWorkspaceNotFound || got != (domain.Workspace{}) {
			t.Errorf("another workspace of the product: got %+v and %v, want nothing and exactly %v", got, err, ErrWorkspaceNotFound)
		}
		want := []audited{{ports.AuditEntry{
			Actor: carol, Action: "workspace.read", Rule: "DENY-03",
			ProductID: s.cargopai.ID, ResourceType: "Workspace", ResourceID: asked.recorded, CorrelationID: "correlation-4",
		}, false}}
		if !slices.Equal(s.w.audited, want) {
			t.Errorf("audited\n %+v\nwant\n %+v", s.w.audited, want)
		}
	}

	// A member manages nothing, and is nothing in another product.
	if _, err := s.service.CreateWorkspace(ctx, CreateWorkspace{Caller: carol, ProductID: string(s.cargopai.ID), Key: "hers", DisplayName: "Hers"}); err != ErrForbidden {
		t.Errorf("a member who creates a workspace: got %v, want exactly %v", err, ErrForbidden)
	}
	if _, err := s.product(carol, s.parcel); err != ErrProductNotFound {
		t.Errorf("another product: got %v, want exactly %v", err, ErrProductNotFound)
	}
	if _, err := s.workspace(carol, s.sorting); err != ErrProductNotFound {
		t.Errorf("a workspace of another product: got %v, want exactly %v", err, ErrProductNotFound)
	}
	if len(s.w.workspaces) != 3 {
		t.Errorf("there are %d workspaces, want the 3 there were", len(s.w.workspaces))
	}
}

// DC-001: a membership that is revoked grants nothing from that moment on.
// With nothing left in the product its former member is a stranger to it,
// and is answered as for a product that does not exist.
func TestARevokedMembershipGrantsNothingFromTheMomentItIsRevoked(t *testing.T) {
	s := newTeam(t)

	if _, err := s.workspace(carol, s.engineering); err != nil {
		t.Fatalf("before the revocation: %v", err)
	}
	if err := s.revoke(alice, s.engineering, s.maintainer.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	s.w.quiet()

	_, errMissing := s.service.GetProduct(ctx, GetProduct{Caller: carol, ProductID: missingID})
	if _, err := s.product(carol, s.cargopai); err != errMissing || err != ErrProductNotFound {
		t.Errorf("the product: got %v, want exactly what a missing product gets, %v", err, errMissing)
	}
	if _, err := s.workspace(carol, s.engineering); err != ErrProductNotFound {
		t.Errorf("the workspace: got %v, want exactly %v", err, ErrProductNotFound)
	}
	if len(s.w.audited) != 2 || s.w.audited[0].entry.Action != "product.read" || s.w.audited[1].entry.Action != "workspace.read" {
		t.Errorf("the two refusals left the audit entries %+v, want one each", s.w.audited)
	}

	// Granted again, it lets her in again: what counts is what is in force.
	joined(t, s.service, s.engineering, alice, carol, domain.RoleInternalAuthor)
	if got, err := s.workspace(carol, s.engineering); err != nil || got != s.engineering {
		t.Errorf("after a new grant: got %+v and %v, want the workspace", got, err)
	}
}

// DC-001: taking one grant away takes away what it granted and nothing more.
func TestRevokingOneMembershipLeavesTheOthersInForce(t *testing.T) {
	s := newTeam(t)
	// carol maintains engineering. She also writes there, and in operations.
	author := joined(t, s.service, s.engineering, alice, carol, domain.RoleInternalAuthor)
	elsewhere := joined(t, s.service, s.operations, alice, carol, domain.RoleInternalAuthor)

	reads := func() (product, engineering, operations error) {
		_, product = s.product(carol, s.cargopai)
		_, engineering = s.workspace(carol, s.engineering)
		_, operations = s.workspace(carol, s.operations)
		return product, engineering, operations
	}
	steps := []struct {
		revoke                           domain.Membership
		in                               domain.Workspace
		product, engineering, operations error
	}{
		// Still a member of engineering, in her other role.
		{s.maintainer, s.engineering, nil, nil, nil},
		// No longer one. She knows the product through operations.
		{author, s.engineering, nil, ErrWorkspaceNotFound, nil},
		// Nothing left: a stranger.
		{elsewhere, s.operations, ErrProductNotFound, ErrProductNotFound, ErrProductNotFound},
	}
	for index, step := range steps {
		if err := s.revoke(alice, step.in, step.revoke.ID); err != nil {
			t.Fatalf("revocation %d: %v", index+1, err)
		}
		product, engineering, operations := reads()
		if product != step.product || engineering != step.engineering || operations != step.operations {
			t.Errorf("after revocation %d she reads the product: %v, engineering: %v, operations: %v\nwant %v, %v, %v",
				index+1, product, engineering, operations, step.product, step.engineering, step.operations)
		}
	}
	// What dave holds was never touched.
	if _, err := s.workspace(dave, s.operations); err != nil {
		t.Errorf("dave reads his workspace: %v", err)
	}
}

// DC-001: one principal, three grants, two products. bob owns Parcel,
// maintains one workspace of CargoPai and writes in the other. Each grant
// lets him do what it names where it names it, and nothing carries over from
// one product to the other.
func TestWhatAPrincipalHoldsInOneProductIsNothingInAnother(t *testing.T) {
	s := newTeam(t)
	joined(t, s.service, s.engineering, alice, bob, domain.RoleWorkspaceMaintainer)
	joined(t, s.service, s.operations, alice, bob, domain.RoleInternalAuthor)

	// In Parcel, which he owns, he manages workspaces and members.
	if _, err := s.service.CreateWorkspace(ctx, CreateWorkspace{Caller: bob, ProductID: string(s.parcel.ID), Key: "returns", DisplayName: "Returns"}); err != nil {
		t.Errorf("the owner of Parcel creates a workspace of it: %v", err)
	}
	sorter, err := s.grant(bob, s.sorting, mallory, domain.RoleInternalAuthor)
	if err != nil {
		t.Fatalf("the owner of Parcel makes somebody a member: %v", err)
	}
	if err := s.revoke(bob, s.sorting, sorter.ID); err != nil {
		t.Errorf("the owner of Parcel revokes that membership: %v", err)
	}

	// In CargoPai he reads his two workspaces and manages nothing: owning
	// one product is not owning another, and neither is being a member.
	if _, err := s.workspace(bob, s.engineering); err != nil {
		t.Errorf("he reads the workspace he maintains: %v", err)
	}
	if _, err := s.workspace(bob, s.operations); err != nil {
		t.Errorf("he reads the workspace he writes in: %v", err)
	}
	if _, err := s.service.CreateWorkspace(ctx, CreateWorkspace{Caller: bob, ProductID: string(s.cargopai.ID), Key: "his", DisplayName: "His"}); err != ErrForbidden {
		t.Errorf("he creates a workspace of CargoPai: got %v, want exactly %v", err, ErrForbidden)
	}
	if _, err := s.grant(bob, s.engineering, mallory, domain.RoleInternalAuthor); err != ErrForbidden {
		t.Errorf("he makes somebody a member in CargoPai: got %v, want exactly %v", err, ErrForbidden)
	}
	if err := s.revoke(bob, s.engineering, s.maintainer.ID); err != ErrForbidden {
		t.Errorf("he revokes a membership in CargoPai: got %v, want exactly %v", err, ErrForbidden)
	}

	// alice owns CargoPai and is nothing in Parcel.
	if _, err := s.product(alice, s.parcel); err != ErrProductNotFound {
		t.Errorf("the owner of CargoPai reads Parcel: got %v, want exactly %v", err, ErrProductNotFound)
	}
	if _, err := s.grant(alice, s.sorting, carol, domain.RoleInternalAuthor); err != ErrProductNotFound {
		t.Errorf("the owner of CargoPai makes somebody a member in Parcel: got %v, want exactly %v", err, ErrProductNotFound)
	}
	if !s.w.memberships[s.maintainer.ID].InForce() {
		t.Error("a refused revocation ended the membership")
	}
}

// A refusal stands whether or not it could be recorded.
func TestARefusalAboutAMembershipStandsWhetherOrNotItCouldBeAudited(t *testing.T) {
	s := newTeam(t)
	before := maps.Clone(s.w.memberships)
	s.w.failing["audit"] = errors.New("the audit trail is down")

	_, err := s.grant(mallory, s.engineering, mallory, domain.RoleWorkspaceMaintainer)
	if !errors.Is(err, ErrProductNotFound) || !errors.Is(err, ErrRefusalNotAudited) {
		t.Errorf("a grant: got %v, want %v and %v", err, ErrProductNotFound, ErrRefusalNotAudited)
	}
	_, err = s.membership(carol, s.engineering, s.maintainer.ID)
	if !errors.Is(err, ErrMembershipNotFound) || !errors.Is(err, ErrRefusalNotAudited) {
		t.Errorf("a read: got %v, want %v and %v", err, ErrMembershipNotFound, ErrRefusalNotAudited)
	}
	err = s.revoke(dave, s.engineering, s.maintainer.ID)
	if !errors.Is(err, ErrForbidden) || !errors.Is(err, ErrRefusalNotAudited) {
		t.Errorf("a revocation: got %v, want %v and %v", err, ErrForbidden, ErrRefusalNotAudited)
	}
	if !maps.Equal(s.w.memberships, before) {
		t.Errorf("refusals left the memberships %+v, want them as they were", s.w.memberships)
	}
}

// What a caller holds decides every answer. When it cannot be read, nobody
// is let in and nobody is told there is nothing: the request fails.
func TestNobodyIsLetInWhenWhatTheyHoldCannotBeRead(t *testing.T) {
	down := errors.New("the memberships cannot be read")
	s := newTeam(t)
	before := maps.Clone(s.w.memberships)
	s.w.failing["held"] = down

	for _, caller := range []domain.Principal{alice, carol, mallory, admin} {
		_, errProduct := s.product(caller, s.cargopai)
		_, errWorkspace := s.workspace(caller, s.engineering)
		_, errCreate := s.service.CreateWorkspace(ctx, CreateWorkspace{Caller: caller, ProductID: string(s.cargopai.ID), Key: "other", DisplayName: "Other"})
		_, errGrant := s.grant(caller, s.engineering, bob, domain.RoleInternalAuthor)
		_, errGet := s.membership(caller, s.engineering, s.maintainer.ID)
		errRevoke := s.revoke(caller, s.engineering, s.maintainer.ID)
		for name, err := range map[string]error{
			"GetProduct": errProduct, "GetWorkspace": errWorkspace, "CreateWorkspace": errCreate,
			"GrantMembership": errGrant, "GetMembership": errGet, "RevokeMembership": errRevoke,
		} {
			if !errors.Is(err, down) {
				t.Errorf("%s as %s: got %v, want the failure", name, caller.Subject, err)
			}
			for _, answer := range []error{ErrForbidden, ErrProductNotFound, ErrWorkspaceNotFound, ErrMembershipNotFound} {
				if errors.Is(err, answer) {
					t.Errorf("%s as %s: the failure is answered as %v", name, caller.Subject, answer)
				}
			}
		}
	}
	if !maps.Equal(s.w.memberships, before) || len(s.w.workspaces) != 3 || len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("failed requests left memberships %+v, %d workspaces, audit %+v, events %v", s.w.memberships, len(s.w.workspaces), s.w.audited, s.w.events)
	}
}
