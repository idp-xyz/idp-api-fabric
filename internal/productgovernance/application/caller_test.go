package application

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// callerHolds makes principal hold the role in the product, as whoever
// grants that role: an owner of the product, or the platform administrator
// for an auditor.
func callerHolds(t *testing.T, service *ProductGovernance, product domain.Product, by, principal domain.Principal, role domain.Role) domain.RoleAssignment {
	t.Helper()

	assignment, err := service.GrantProductRole(ctx, GrantProductRole{
		Caller: by, CorrelationID: "correlation-h", ProductID: string(product.ID),
		Principal: principal, Role: string(role),
	})
	if err != nil {
		t.Fatalf("make %s hold %s in %s: %v", principal.Subject, role, product.Key, err)
	}
	return assignment
}

// callerStanding is what the caller holds in the product, as the use case
// answers it.
func (s team) callerStanding(caller domain.Principal, product domain.Product) (CallerStanding, error) {
	return s.service.GetCallerInProduct(ctx, GetCallerInProduct{
		Caller: caller, CorrelationID: "correlation-c", ProductID: string(product.ID),
	})
}

// callerStandingOf is what the caller of the product is told it holds. What it
// does not hold is an empty list, which is not a missing one.
func callerStandingOf(product domain.Product, roles []domain.Role, workspaces ...domain.HeldInWorkspace) CallerStanding {
	if roles == nil {
		roles = []domain.Role{}
	}
	if workspaces == nil {
		workspaces = []domain.HeldInWorkspace{}
	}
	return CallerStanding{ProductID: product.ID, Roles: roles, Workspaces: workspaces}
}

// heldIn is what a caller holds in a workspace.
func heldIn(workspace domain.Workspace, roles ...domain.Role) domain.HeldInWorkspace {
	return domain.HeldInWorkspace{WorkspaceID: workspace.ID, Roles: roles}
}

// The caller is told who it is, and the one thing that is not in its token:
// whether the deployment names it an administrator of the platform. A
// principal is its issuer and its subject together.
func TestGetCallerSaysWhoCallsAndWhetherItAdministersThePlatform(t *testing.T) {
	_, service := newWorld(t)

	cases := map[string]struct {
		caller        domain.Principal
		administrator bool
	}{
		"the platform administrator":                            {admin, true},
		"somebody who is not":                                   {alice, false},
		"the subject of the administrator under another issuer": {domain.Principal{Issuer: "https://other.example", Subject: admin.Subject}, false},
		"another subject under the issuer of the administrator": {domain.Principal{Issuer: admin.Issuer, Subject: "Platform-Admin"}, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := service.GetCaller(ctx, GetCaller{Caller: c.caller})
			want := CallerIdentity{Principal: c.caller, PlatformAdministrator: c.administrator}
			if err != nil || got != want {
				t.Errorf("got %+v and %v, want %+v", got, err, want)
			}
		})
	}
}

// Who the caller is, is in the request and in the configuration of the
// deployment, and nowhere else. Every read of a store, and the audit trail,
// would fail here; the caller is answered all the same, and nothing is
// recorded: nobody is refused.
func TestGetCallerReadsNothingFromTheStoresAndRecordsNothing(t *testing.T) {
	w, service := newWorld(t)
	registered(t, service, "cargopai", alice)
	w.quiet()

	down := errors.New("the database is down")
	for _, step := range []string{"read", "roles", "held", "audit", "lock", "events", "commit"} {
		w.failing[step] = down
	}
	for _, caller := range []domain.Principal{alice, admin, mallory} {
		if got, err := service.GetCaller(ctx, GetCaller{Caller: caller}); err != nil || got.Principal != caller {
			t.Errorf("%s is told it is %+v (%v), want to be answered", caller.Subject, got, err)
		}
	}
	if w.transactions != 0 || len(w.audited) != 0 || len(w.events) != 0 {
		t.Errorf("who the caller is left %d transactions, the audit entries %+v and the events %v, want none", w.transactions, w.audited, w.events)
	}
}

// What the caller holds is what it holds: each holder is told its own roles
// of the product and its own roles in each workspace, each role once, in a
// fixed order, and nothing of anybody else, in this product or in another.
func TestGetCallerInProductGivesEachHolderWhatItHoldsAndNothingOfAnybodyElse(t *testing.T) {
	s := newTeam(t)
	reports := created(t, s.service, s.cargopai, alice, "reports")

	// frank holds three roles of the product and no workspace, granted in no
	// order of their names: the owner hands out the security roles and the
	// administrator appoints the auditor. alice holds a role of Parcel,
	// which gives her nothing in CargoPai and takes nothing from it.
	for _, grant := range []struct {
		role domain.Role
		by   domain.Principal
	}{{domain.RoleSecurityReviewer, alice}, {domain.RoleInternalAuditor, admin}, {domain.RoleSecurityAdmin, alice}} {
		callerHolds(t, s.service, s.cargopai, grant.by, frank, grant.role)
	}
	callerHolds(t, s.service, s.parcel, bob, alice, domain.RoleSecurityAdmin)
	// carol has two more roles in the workspace she maintains.
	joined(t, s.service, s.engineering, alice, carol, domain.RoleInternalReviewer)
	joined(t, s.service, s.engineering, alice, carol, domain.RoleInternalAuthor)
	// grace owns the product and audits it, and is a member of all three of
	// its workspaces, the last first. Nobody is made both any more: hers is
	// an assignment from before, which is left as it is and told as it is.
	named(t, s.service, s.cargopai, alice, grace)
	s.w.assigned(s.cargopai, grace, domain.RoleInternalAuditor, alice)
	joined(t, s.service, reports, alice, grace, domain.RoleWorkspaceMaintainer)
	joined(t, s.service, reports, alice, grace, domain.RoleInternalAuthor)
	joined(t, s.service, s.operations, alice, grace, domain.RoleInternalReviewer)
	joined(t, s.service, s.engineering, alice, grace, domain.RoleInternalPublisher)
	s.w.quiet()

	cases := map[string]struct {
		caller  domain.Principal
		product domain.Product
		want    CallerStanding
	}{
		"an owner that is a member of nothing": {alice, s.cargopai, callerStandingOf(s.cargopai, []domain.Role{domain.RoleProductOwner})},
		"the owner of another product":         {bob, s.parcel, callerStandingOf(s.parcel, []domain.Role{domain.RoleProductOwner})},
		"a member in three roles of one workspace": {carol, s.cargopai, callerStandingOf(s.cargopai, nil,
			heldIn(s.engineering, domain.RoleInternalAuthor, domain.RoleInternalReviewer, domain.RoleWorkspaceMaintainer))},
		"a member of another workspace": {dave, s.cargopai, callerStandingOf(s.cargopai, nil, heldIn(s.operations, domain.RoleInternalAuthor))},
		"somebody that holds three roles of the product and no workspace": {frank, s.cargopai, callerStandingOf(s.cargopai,
			[]domain.Role{domain.RoleInternalAuditor, domain.RoleSecurityAdmin, domain.RoleSecurityReviewer})},
		"an owner and auditor that is a member of every workspace": {grace, s.cargopai, callerStandingOf(s.cargopai,
			[]domain.Role{domain.RoleInternalAuditor, domain.RoleProductOwner},
			heldIn(s.engineering, domain.RoleInternalPublisher),
			heldIn(s.operations, domain.RoleInternalReviewer),
			heldIn(reports, domain.RoleInternalAuthor, domain.RoleWorkspaceMaintainer))},
		"a role in another product, in that product": {alice, s.parcel, callerStandingOf(s.parcel, []domain.Role{domain.RoleSecurityAdmin})},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			// The stores hand out what they hold in no order a caller may
			// rely on; the answer is the same every time.
			for range 30 {
				got, err := s.callerStanding(c.caller, c.product)
				if err != nil || !reflect.DeepEqual(got, c.want) {
					t.Fatalf("got\n %+v and %v\nwant\n %+v", got, err, c.want)
				}
			}
		})
	}
	if len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("reading what is held left the audit entries %+v and the events %v, want none", s.w.audited, s.w.events)
	}
}

// Administering the platform holds nothing in a product, and a platform
// administrator knows the product exists, as GetProduct has it: it is told
// that it holds nothing, in two empty lists. It holds what it is given, an
// owner's role for one, and that is told as it is told to anyone.
func TestAnAdministratorWhoHoldsNothingInTheProductIsAnsweredWithNothingToShow(t *testing.T) {
	s := newTeam(t)

	got, err := s.callerStanding(admin, s.cargopai)
	if want := callerStandingOf(s.cargopai, nil); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("the administrator is told %+v (%v), want nothing held and no refusal", got, err)
	}
	if len(s.w.audited) != 0 {
		t.Errorf("what the administrator is told left the audit entries %+v, want none", s.w.audited)
	}

	// Nothing keeps it from naming itself an owner.
	callerHolds(t, s.service, s.cargopai, admin, admin, domain.RoleProductOwner)
	got, err = s.callerStanding(admin, s.cargopai)
	if want := callerStandingOf(s.cargopai, []domain.Role{domain.RoleProductOwner}); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("the administrator that owns the product is told %+v (%v), want %+v", got, err, want)
	}
}

// To whoever holds nothing in a product, the product does not exist: the
// answer is the one a missing product gets, and only the audit trail tells
// them apart (F05 §5.2). A refusal is recorded once, with who asked, under
// which rule, and where; a product that is not there, and an identifier that
// is none, are not withheld from anybody and are not recorded.
func TestGetCallerInProductHidesAProductFromAStrangerAsIfItWereMissing(t *testing.T) {
	s := newTeam(t)
	down := errors.New("the database is down")

	_, errMissing := s.service.GetCallerInProduct(ctx, GetCallerInProduct{Caller: mallory, CorrelationID: "correlation-c", ProductID: missingID})
	if errMissing != ErrProductNotFound {
		t.Fatalf("a missing product: got %v, want exactly %v", errMissing, ErrProductNotFound)
	}
	if len(s.w.audited) != 0 {
		t.Fatalf("a missing product left the audit entries %+v", s.w.audited)
	}
	// An identifier that is none is not looked up: no store is read for it.
	s.w.failing["read"], s.w.failing["roles"], s.w.failing["held"] = down, down, down
	for _, caller := range []domain.Principal{mallory, alice, admin} {
		got, err := s.service.GetCallerInProduct(ctx, GetCallerInProduct{Caller: caller, CorrelationID: "correlation-c", ProductID: "cargopai"})
		if err != ErrProductNotFound || !reflect.DeepEqual(got, CallerStanding{}) {
			t.Errorf("%s, an identifier that is none: got %+v and %v, want nothing and exactly %v", caller.Subject, got, err, ErrProductNotFound)
		}
	}
	clear(s.w.failing)
	if len(s.w.audited) != 0 {
		t.Fatalf("an identifier that is none left the audit entries %+v", s.w.audited)
	}

	for name, caller := range map[string]domain.Principal{
		"a stranger":                               mallory,
		"the owner of another product":             bob,
		"whoever was a member and is not any more": erin,
	} {
		t.Run(name, func(t *testing.T) {
			s.w.quiet()

			got, err := s.callerStanding(caller, s.cargopai)
			if err != errMissing || !reflect.DeepEqual(got, CallerStanding{}) {
				t.Fatalf("got %+v and %v, want nothing and the error of a missing product", got, err)
			}
			want := []audited{{ports.AuditEntry{
				Actor: caller, Action: "product.read", Allowed: false, Rule: "DENY-03",
				ProductID: s.cargopai.ID, ResourceType: "Product", ResourceID: string(s.cargopai.ID), CorrelationID: "correlation-c",
			}, false}}
			if !slices.Equal(s.w.audited, want) {
				t.Errorf("audited\n %+v\nwant\n %+v", s.w.audited, want)
			}
			if s.w.transactions != 0 || len(s.w.events) != 0 {
				t.Errorf("a refusal left %d transactions and the events %v, want none", s.w.transactions, s.w.events)
			}
		})
	}
}

// A refusal stands whether or not it could be recorded: the caller is never
// let in because the audit trail is down.
func TestARefusedStandingThatCannotBeAuditedIsStillRefused(t *testing.T) {
	s := newTeam(t)
	s.w.failing["audit"] = errors.New("the audit trail is down")

	got, err := s.callerStanding(mallory, s.cargopai)
	if !errors.Is(err, ErrProductNotFound) || !errors.Is(err, ErrRefusalNotAudited) || !strings.Contains(err.Error(), "the audit trail is down") {
		t.Errorf("got %v, want a refusal that says why it could not be audited", err)
	}
	if !reflect.DeepEqual(got, CallerStanding{}) {
		t.Errorf("a refusal that could not be audited told %+v", got)
	}
	// What is answered is not recorded, so it does not need the trail.
	if _, err := s.callerStanding(alice, s.cargopai); err != nil {
		t.Errorf("an owner, while the audit trail is down: %v", err)
	}
}

// What the caller holds is read when it asks, and never kept from one request
// to the next: a membership or a role that was revoked is not in the next
// answer, the last of them makes the caller a stranger, and what is granted
// again is a new grant that is told as one (F05 §5.3).
func TestWhatWasRevokedIsNotHeldAtTheNextRead(t *testing.T) {
	s := newTeam(t)
	role := callerHolds(t, s.service, s.cargopai, alice, frank, domain.RoleSecurityAdmin)
	membership := joined(t, s.service, s.operations, alice, frank, domain.RoleInternalReviewer)

	holds := func(roles []domain.Role, workspaces ...domain.HeldInWorkspace) {
		t.Helper()
		got, err := s.callerStanding(frank, s.cargopai)
		if want := callerStandingOf(s.cargopai, roles, workspaces...); err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("frank is told\n %+v and %v\nwant\n %+v", got, err, want)
		}
	}
	holds([]domain.Role{domain.RoleSecurityAdmin}, heldIn(s.operations, domain.RoleInternalReviewer))

	if err := s.revoke(alice, s.operations, membership.ID); err != nil {
		t.Fatalf("revoke the membership: %v", err)
	}
	holds([]domain.Role{domain.RoleSecurityAdmin})

	if err := s.revokeRole(alice, s.cargopai, role.ID); err != nil {
		t.Fatalf("revoke the role: %v", err)
	}
	if got, err := s.callerStanding(frank, s.cargopai); err != ErrProductNotFound || !reflect.DeepEqual(got, CallerStanding{}) {
		t.Errorf("holding nothing, frank is told %+v and %v, want nothing and %v", got, err, ErrProductNotFound)
	}

	if again := callerHolds(t, s.service, s.cargopai, alice, frank, domain.RoleSecurityAdmin); again.ID == role.ID {
		t.Errorf("the role was granted again under the identifier %s of the one that was revoked", again.ID)
	}
	holds([]domain.Role{domain.RoleSecurityAdmin})

	// carol held one membership, and no role: once it is revoked she is a
	// stranger, not a holder of nothing.
	if err := s.revoke(alice, s.engineering, s.maintainer.ID); err != nil {
		t.Fatalf("revoke the membership of carol: %v", err)
	}
	if got, err := s.callerStanding(carol, s.cargopai); err != ErrProductNotFound || !reflect.DeepEqual(got, CallerStanding{}) {
		t.Errorf("carol, once her only membership is revoked, is told %+v and %v, want nothing and %v", got, err, ErrProductNotFound)
	}
}

// Nothing is locked and no transaction is opened: the answer is the roles
// themselves, and every decision that follows reads them again. A lock on the
// roles of the caller is for whoever decides, in the transaction that acts on
// the decision; the stand-in of the stores refuses one outside a transaction.
func TestTheStandingIsReadWithoutALockOrATransaction(t *testing.T) {
	s := newTeam(t)
	callerHolds(t, s.service, s.cargopai, alice, frank, domain.RoleSecurityAdmin)
	joined(t, s.service, s.engineering, alice, frank, domain.RoleInternalAuthor)
	s.w.quiet()

	for _, caller := range []domain.Principal{alice, carol, frank, admin, mallory} {
		_, err := s.callerStanding(caller, s.cargopai)
		if err != nil && !errors.Is(err, ErrProductNotFound) {
			t.Errorf("%s: got %v, want an answer or the refusal of a stranger", caller.Subject, err)
		}
	}
	if s.w.transactions != 0 {
		t.Errorf("reading what is held opened %d transactions, want none", s.w.transactions)
	}
	for _, record := range s.w.audited {
		if record.transacted {
			t.Errorf("a refusal was recorded inside a transaction: %+v", record)
		}
	}
}

// A product that takes no new work keeps what it holds, and tells it: asking
// what is held is not new work. A stranger is still told that it is not there.
func TestTheStandingOfASuspendedProductIsStillRead(t *testing.T) {
	s := newTeam(t)
	suspended := s.cargopai
	suspended.Status = domain.ProductSuspended
	s.w.products[s.cargopai.ID] = suspended

	for name, c := range map[string]struct {
		caller domain.Principal
		want   CallerStanding
	}{
		"an owner":                   {alice, callerStandingOf(s.cargopai, []domain.Role{domain.RoleProductOwner})},
		"a member":                   {carol, callerStandingOf(s.cargopai, nil, heldIn(s.engineering, domain.RoleWorkspaceMaintainer))},
		"the platform administrator": {admin, callerStandingOf(s.cargopai, nil)},
	} {
		if got, err := s.callerStanding(c.caller, s.cargopai); err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v and %v, want %+v", name, got, err, c.want)
		}
	}
	if _, err := s.callerStanding(mallory, s.cargopai); err != ErrProductNotFound {
		t.Errorf("a stranger: got %v, want %v", err, ErrProductNotFound)
	}
}

// A store that cannot be read is a failure, not an answer: the caller is
// not told that it holds nothing, nor that there is no such product, and
// nothing is recorded as a refusal.
func TestAFailedReadOfTheStandingIsAFailureNotAnEmptyAnswer(t *testing.T) {
	for _, step := range []string{"read", "roles", "held"} {
		for _, caller := range []domain.Principal{alice, mallory} {
			t.Run(step+" as "+caller.Subject, func(t *testing.T) {
				s := newTeam(t)
				down := errors.New("the database is down")
				s.w.failing[step] = down

				got, err := s.callerStanding(caller, s.cargopai)
				if !errors.Is(err, down) || errors.Is(err, ErrProductNotFound) || errors.Is(err, ErrForbidden) {
					t.Errorf("got %v, want the failure that was, and neither of the refusals", err)
				}
				if !reflect.DeepEqual(got, CallerStanding{}) {
					t.Errorf("a failed read told %+v", got)
				}
				if len(s.w.audited) != 0 {
					t.Errorf("a failed read left the audit entries %+v, want none", s.w.audited)
				}
			})
		}
	}
}

// A request that reaches a use case without a principal is refused, and
// nothing is read for it: the transport authenticates every request of these
// operations, so this only happens when the wiring is wrong.
func TestTheCallerReadsRefuseARequestWithoutAPrincipal(t *testing.T) {
	s := newTeam(t)
	down := errors.New("the database is down")
	s.w.failing["read"], s.w.failing["roles"], s.w.failing["held"] = down, down, down

	for name, nobody := range map[string]domain.Principal{
		"no principal":   {},
		"no subject":     {Issuer: trustedIssuer},
		"no issuer":      {Subject: "alice"},
		"a control sign": {Issuer: trustedIssuer, Subject: "ali\nce"},
		"a long subject": {Issuer: trustedIssuer, Subject: strings.Repeat("a", domain.ReferenceMaxLength+1)},
		"a long issuer":  {Issuer: strings.Repeat("i", domain.ReferenceMaxLength+1), Subject: "alice"},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := s.service.GetCaller(ctx, GetCaller{Caller: nobody}); !errors.Is(err, ErrForbidden) || got != (CallerIdentity{}) {
				t.Errorf("GetCaller: got %+v and %v, want nothing and %v", got, err, ErrForbidden)
			}
			got, err := s.service.GetCallerInProduct(ctx, GetCallerInProduct{Caller: nobody, ProductID: string(s.cargopai.ID)})
			if !errors.Is(err, ErrForbidden) || errors.Is(err, down) || !reflect.DeepEqual(got, CallerStanding{}) {
				t.Errorf("GetCallerInProduct: got %+v and %v, want nothing and %v, with no store read", got, err, ErrForbidden)
			}
		})
	}
	if len(s.w.audited) != 0 || s.w.transactions != 0 {
		t.Errorf("requests without a principal left the audit entries %+v and %d transactions", s.w.audited, s.w.transactions)
	}
}
