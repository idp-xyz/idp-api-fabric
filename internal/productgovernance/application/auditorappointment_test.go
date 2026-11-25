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

// deputy administers the platform beside admin, in the tests that take two
// administrators.
var deputy = domain.Principal{Issuer: trustedIssuer, Subject: "deputy-admin"}

// assigned puts a role assignment in force into the tables as it is, without
// a use case deciding on it: one that was made before the rules of today, by
// whoever granted it then.
func (w *world) assigned(product domain.Product, principal domain.Principal, role domain.Role, by domain.Principal) domain.RoleAssignment {
	assignment := domain.RoleAssignment{
		ID: domain.RoleAssignmentID(w.NewID()), ProductID: product.ID, Principal: principal, Role: role,
		Version: 1, GrantedAt: w.now, GrantedBy: by,
	}
	w.roles[assignment.ID] = assignment
	return assignment
}

// give asks, as caller, that principal hold the role in the product.
func (s team) give(caller domain.Principal, product domain.Product, principal domain.Principal, role domain.Role) (domain.RoleAssignment, error) {
	return s.service.GrantProductRole(ctx, GrantProductRole{
		Caller: caller, CorrelationID: "correlation-8", ProductID: string(product.ID), Principal: principal, Role: string(role),
	})
}

// listOf asks, as caller, for a page of the assignments of the product that
// grant the role. No role asks for those of every role.
func (s team) listOf(caller domain.Principal, product domain.Product, role string, size int, after *ports.Position) (Page[domain.RoleAssignment], error) {
	return s.service.ListProductRoles(ctx, ListProductRoles{
		Caller: caller, CorrelationID: "correlation-14", ProductID: string(product.ID), Role: role, PageSize: size, After: after,
	})
}

// roleRecord is the audit record of something asked about a role assignment
// of the product. What was done is recorded inside the transaction that did
// it, and what was refused once that transaction is over.
func roleRecord(caller domain.Principal, product domain.Product, action, rule, correlation string, id domain.RoleAssignmentID, done bool) audited {
	return audited{ports.AuditEntry{
		Actor: caller, Action: action, Allowed: done, Rule: rule,
		ProductID: product.ID, ResourceType: "RoleAssignment", ResourceID: string(id), CorrelationID: correlation,
	}, done}
}

func granted(caller domain.Principal, product domain.Product, rule string, id domain.RoleAssignmentID) audited {
	return roleRecord(caller, product, "product_role.grant", rule, "correlation-8", id, true)
}

// notGranted is the record of a grant that was refused, under the rule it
// was refused by. A refused grant names no assignment.
func notGranted(caller domain.Principal, product domain.Product, rule string) audited {
	return roleRecord(caller, product, "product_role.grant", rule, "correlation-8", "", false)
}

func ended(caller domain.Principal, product domain.Product, rule string, id domain.RoleAssignmentID) audited {
	return roleRecord(caller, product, "product_role.revoke", rule, "correlation-9", id, true)
}

// notEnded is the record of a revocation that was refused. It names the
// assignment if there is one in force.
func notEnded(caller domain.Principal, product domain.Product, id domain.RoleAssignmentID) audited {
	return roleRecord(caller, product, "product_role.revoke", "DENY-03", "correlation-9", id, false)
}

// notRead is the record of a read of role assignments that was refused,
// under the correlation of the read: that of one assignment, which it names
// if there is one in force, or that of a list.
func notRead(caller domain.Principal, product domain.Product, correlation string, id domain.RoleAssignmentID) audited {
	return roleRecord(caller, product, "product_role.read", "DENY-03", correlation, id, false)
}

// A platform administrator appoints who audits a product and dismisses it,
// under its own rule (AC-08), as it names the owners. The appointment, its
// audit fact and its event are made together, and the auditor reads the
// trail from then on, until it is dismissed. Appointing gives the
// administrator nothing inside the product. A dismissal that is sent again
// finds no auditor, and an administrator ends nothing else.
func TestAPlatformAdministratorAppointsAnAuditorAndDismissesItUnderItsRule(t *testing.T) {
	s := newTeam(t)
	s.w.now = started.Add(time.Hour)
	cargopai := string(s.cargopai.ID)

	appointed, err := s.give(admin, s.cargopai, frank, domain.RoleInternalAuditor)
	if err != nil {
		t.Fatalf("the administrator appoints an auditor: %v", err)
	}
	want := domain.RoleAssignment{
		ID: appointed.ID, ProductID: s.cargopai.ID, Principal: frank, Role: domain.RoleInternalAuditor,
		Version: 1, GrantedAt: s.w.now, GrantedBy: admin,
	}
	if appointed != want || s.w.roles[appointed.ID] != want {
		t.Errorf("assignment\n %+v\nstored\n %+v\nwant\n %+v", appointed, s.w.roles[appointed.ID], want)
	}
	if want := []audited{granted(admin, s.cargopai, "AC-08", appointed.ID)}; !slices.Equal(s.w.audited, want) {
		t.Errorf("audited\n %+v\nwant, inside the transaction,\n %+v", s.w.audited, want)
	}
	if want := []string{"ProductRoleChanged " + string(appointed.ID) + " granted correlation-8"}; !slices.Equal(s.w.events, want) {
		t.Errorf("events %v, want %v", s.w.events, want)
	}
	if s.w.transactions != 1 {
		t.Errorf("%d transactions, want 1", s.w.transactions)
	}
	if got, err := s.auditScope(frank, cargopai); err != nil || !got.Auditor {
		t.Errorf("whoever was appointed reads the scope as %+v (%v), want an auditor", got, err)
	}
	// The role is held once at a time, as the others are.
	if _, err := s.give(admin, s.cargopai, frank, domain.RoleInternalAuditor); !errors.Is(err, ports.ErrRoleAssignmentExists) {
		t.Errorf("the auditor appointed twice: got %v, want %v", err, ports.ErrRoleAssignmentExists)
	}
	// The administrator is no auditor by appointing one, and sees nothing
	// inside the product.
	if got, err := s.auditScope(admin, cargopai); err != nil || got.Auditor {
		t.Errorf("the administrator reads the scope as %+v (%v), want no auditor", got, err)
	}
	if _, err := s.workspace(admin, s.engineering); err != ErrWorkspaceNotFound {
		t.Errorf("the administrator reads a workspace: got %v, want exactly %v", err, ErrWorkspaceNotFound)
	}

	s.w.quiet()
	if err := s.revokeRole(admin, s.cargopai, appointed.ID); err != nil {
		t.Fatalf("the administrator dismisses the auditor: %v", err)
	}
	gone := appointed
	gone.Version, gone.RevokedAt, gone.RevokedBy = 2, s.w.now, admin
	if got := s.w.roles[appointed.ID]; got != gone || got.InForce() {
		t.Errorf("stored\n %+v\nwant, not in force,\n %+v", got, gone)
	}
	if want := []audited{ended(admin, s.cargopai, "AC-08", appointed.ID)}; !slices.Equal(s.w.audited, want) {
		t.Errorf("audited\n %+v\nwant, inside the transaction,\n %+v", s.w.audited, want)
	}
	if want := []string{"ProductRoleChanged " + string(appointed.ID) + " revoked correlation-9"}; !slices.Equal(s.w.events, want) {
		t.Errorf("events %v, want %v", s.w.events, want)
	}
	if got, err := s.auditScope(frank, cargopai); err != nil || got != (AuditScope{Found: true}) {
		t.Errorf("once dismissed it reads the scope as %+v (%v), want a stranger", got, err)
	}

	// Sent again, the dismissal finds no auditor to dismiss. What is not
	// there is to an administrator what an assignment of another role is.
	s.w.quiet()
	if err := s.revokeRole(admin, s.cargopai, appointed.ID); err != ErrForbidden {
		t.Errorf("the dismissal sent again: got %v, want exactly %v", err, ErrForbidden)
	}
	if want := []audited{notEnded(admin, s.cargopai, "")}; !slices.Equal(s.w.audited, want) || len(s.w.events) != 0 {
		t.Errorf("audited\n %+v\nand the events %v; want, outside the transaction,\n %+v\nand no event", s.w.audited, s.w.events, want)
	}
}

// Whoever is audited does not pick who audits it. An owner of the product
// appoints no auditor, whoever it names, itself least of all: it is told
// that it may not, the refusal is on record, and nothing is granted. So no
// owner reads what was recorded about its product by its own doing.
func TestAnOwnerAppointsNoAuditorWhoeverItNamesItselfIncluded(t *testing.T) {
	s := newBoard(t)
	before := maps.Clone(s.w.roles)

	for name, c := range map[string]struct{ owner, principal domain.Principal }{
		"somebody who holds nothing in the product": {alice, mallory},
		"a member of a workspace of the product":    {alice, carol},
		"itself":                                    {alice, alice},
		"another owner":                             {alice, frank},
		"an owner that was named since, for itself": {frank, frank},
	} {
		s.w.quiet()
		got, err := s.give(c.owner, s.cargopai, c.principal, domain.RoleInternalAuditor)
		if err != ErrForbidden || got != (domain.RoleAssignment{}) {
			t.Fatalf("%s: got %+v and %v, want nothing and exactly %v", name, got, err, ErrForbidden)
		}
		if want := []audited{notGranted(c.owner, s.cargopai, "DENY-03")}; !slices.Equal(s.w.audited, want) {
			t.Errorf("%s: audited\n %+v\nwant, outside the transaction,\n %+v", name, s.w.audited, want)
		}
		if !maps.Equal(s.w.roles, before) || len(s.w.events) != 0 {
			t.Errorf("%s: the refused appointment left the roles %+v and the events %v", name, s.w.roles, s.w.events)
		}
	}
	for _, owner := range []domain.Principal{alice, frank} {
		if got, err := s.auditScope(owner, string(s.cargopai.ID)); err != nil || got.Auditor {
			t.Errorf("%s reads the scope as %+v (%v), want an owner that is no auditor", owner.Subject, got, err)
		}
	}
}

// Nor does whoever is audited send its auditor away. An owner ends every
// assignment of its product but that of an auditor: it is told that it may
// not, the refusal names the assignment, and the auditor stays one. The
// owner still reads who audits its product, one assignment and the list.
func TestAnOwnerDismissesNoAuditorAndTheRoleStaysInForce(t *testing.T) {
	s := newBoard(t)
	role := auditor(t, s.service, s.cargopai, heidi)

	for _, owner := range []domain.Principal{alice, frank} {
		s.w.quiet()
		if err := s.revokeRole(owner, s.cargopai, role.ID); err != ErrForbidden {
			t.Fatalf("%s dismisses the auditor: got %v, want exactly %v", owner.Subject, err, ErrForbidden)
		}
		if want := []audited{notEnded(owner, s.cargopai, role.ID)}; !slices.Equal(s.w.audited, want) {
			t.Errorf("%s: audited\n %+v\nwant, outside the transaction,\n %+v", owner.Subject, s.w.audited, want)
		}
		if s.w.roles[role.ID] != role || len(s.w.events) != 0 {
			t.Errorf("%s: the refused dismissal left the assignment %+v and the events %v", owner.Subject, s.w.roles[role.ID], s.w.events)
		}
	}
	if got, err := s.auditScope(heidi, string(s.cargopai.ID)); err != nil || !got.Auditor {
		t.Errorf("the auditor reads the scope as %+v (%v), want an auditor still", got, err)
	}

	s.w.quiet()
	if got, err := s.role(alice, s.cargopai, role.ID); err != nil || got != role {
		t.Errorf("the owner reads the assignment of the auditor: got %+v and %v, want %+v", got, err, role)
	}
	page, err := s.listRoles(alice, s.cargopai, 0, nil)
	if want := []domain.RoleAssignment{s.first, s.second, role}; err != nil || !slices.Equal(page.Items, want) {
		t.Errorf("the owner lists the roles: got %+v and %v, want %+v", page.Items, err, want)
	}
	s.untouched(t)

	// It ends what it grants, as before.
	if err := s.revokeRole(alice, s.cargopai, s.second.ID); err != nil {
		t.Errorf("the owner removes another owner: %v", err)
	}
}

// An owner acts as one, whatever else it is. An administrator that owns a
// product is among those the auditor of it audits: it appoints no auditor
// of that product and dismisses none, and another administrator does. It is
// an owner there in everything else, and where it owns nothing it appoints
// as an administrator.
func TestAnAdministratorThatOwnsAProductAppointsAndDismissesNoAuditorOfIt(t *testing.T) {
	s := newTeam(t)
	s.w.administrators = append(s.w.administrators, deputy)
	named(t, s.service, s.parcel, bob, admin)

	s.w.quiet()
	if got, err := s.give(admin, s.parcel, ivan, domain.RoleInternalAuditor); err != ErrForbidden || got != (domain.RoleAssignment{}) {
		t.Fatalf("an administrator that owns the product appoints its auditor: got %+v and %v, want nothing and exactly %v", got, err, ErrForbidden)
	}
	if want := []audited{notGranted(admin, s.parcel, "DENY-03")}; !slices.Equal(s.w.audited, want) {
		t.Errorf("audited\n %+v\nwant, outside the transaction,\n %+v", s.w.audited, want)
	}

	s.w.quiet()
	role, err := s.give(deputy, s.parcel, ivan, domain.RoleInternalAuditor)
	if err != nil {
		t.Fatalf("another administrator appoints the auditor: %v", err)
	}
	if want := []audited{granted(deputy, s.parcel, "AC-08", role.ID)}; !slices.Equal(s.w.audited, want) {
		t.Errorf("audited\n %+v\nwant\n %+v", s.w.audited, want)
	}
	s.w.quiet()
	if err := s.revokeRole(admin, s.parcel, role.ID); err != ErrForbidden {
		t.Fatalf("an administrator that owns the product dismisses its auditor: got %v, want exactly %v", err, ErrForbidden)
	}
	if want := []audited{notEnded(admin, s.parcel, role.ID)}; !slices.Equal(s.w.audited, want) || s.w.roles[role.ID] != role {
		t.Errorf("audited\n %+v\nand the assignment %+v; want\n %+v\nand the assignment in force", s.w.audited, s.w.roles[role.ID], want)
	}

	// An owner there in everything else, and recorded as one.
	s.w.quiet()
	security, err := s.give(admin, s.parcel, carol, domain.RoleSecurityAdmin)
	if err != nil {
		t.Fatalf("an administrator that owns the product grants a security role of it: %v", err)
	}
	if want := []audited{granted(admin, s.parcel, "AC-10", security.ID)}; !slices.Equal(s.w.audited, want) {
		t.Errorf("audited\n %+v\nwant\n %+v", s.w.audited, want)
	}

	// Where it owns nothing it is an administrator and no more.
	s.w.quiet()
	elsewhere, err := s.give(admin, s.cargopai, frank, domain.RoleInternalAuditor)
	if err != nil {
		t.Fatalf("the administrator appoints an auditor of a product it does not own: %v", err)
	}
	if want := []audited{granted(admin, s.cargopai, "AC-08", elsewhere.ID)}; !slices.Equal(s.w.audited, want) {
		t.Errorf("audited\n %+v\nwant\n %+v", s.w.audited, want)
	}
	if err := s.revokeRole(deputy, s.parcel, role.ID); err != nil {
		t.Errorf("another administrator dismisses the auditor: %v", err)
	}
}

// An administrator ends the assignment of an auditor and no other: it names
// owners and removes none, and the security roles are not its to end. An
// assignment that is not there, or not of this product, is to it what one
// of another role is: it is told that it may not, whichever it is. The
// refusal names the assignment if one is in force.
func TestAnAdministratorEndsNoAssignmentButThatOfAnAuditor(t *testing.T) {
	s := newBoard(t)
	security, err := s.give(alice, s.cargopai, carol, domain.RoleSecurityAdmin)
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := s.give(alice, s.cargopai, dave, domain.RoleSecurityReviewer)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := auditor(t, s.service, s.parcel, ivan)
	dismissed := auditor(t, s.service, s.cargopai, heidi)
	if err := s.revokeRole(admin, s.cargopai, dismissed.ID); err != nil {
		t.Fatal(err)
	}
	before := maps.Clone(s.w.roles)

	for name, c := range map[string]struct {
		id      string
		inForce bool
	}{
		"that of the first owner":                 {string(s.first.ID), true},
		"that of an owner named since":            {string(s.second.ID), true},
		"that of a security administrator":        {string(security.ID), true},
		"that of a security reviewer":             {string(reviewer.ID), true},
		"that of an owner who was removed":        {string(s.former.ID), false},
		"that of an auditor it dismissed already": {string(dismissed.ID), false},
		"that of an auditor of another product":   {string(elsewhere.ID), false},
		"one that does not exist":                 {missingID, false},
		"one whose identifier is none":            {"auditor", false},
	} {
		s.w.quiet()
		err := s.service.RevokeProductRole(ctx, RevokeProductRole{
			Caller: admin, CorrelationID: "correlation-9", ProductID: string(s.cargopai.ID), RoleAssignmentID: c.id,
		})
		if err != ErrForbidden {
			t.Fatalf("%s: got %v, want exactly %v", name, err, ErrForbidden)
		}
		want := notEnded(admin, s.cargopai, "")
		if c.inForce {
			want.entry.ResourceID = c.id
		}
		if !slices.Equal(s.w.audited, []audited{want}) {
			t.Errorf("%s: audited\n %+v\nwant, outside the transaction,\n %+v", name, s.w.audited, want)
		}
		if !maps.Equal(s.w.roles, before) || len(s.w.events) != 0 {
			t.Errorf("%s: the refused revocation left the roles %+v and the events %v", name, s.w.roles, s.w.events)
		}
	}
}

// An owner grants and ends every role of its product but that of an auditor,
// under the rule of the owners, as before. An administrator that does not
// own the product still names owners and no security role: those are the
// owners' to hand out.
func TestAnOwnerGrantsAndEndsEveryOtherRoleAndAnAdministratorNamesNoSecurityRole(t *testing.T) {
	s := newTeam(t)

	for _, role := range []domain.Role{domain.RoleProductOwner, domain.RoleSecurityAdmin, domain.RoleSecurityReviewer} {
		s.w.quiet()
		assignment, err := s.give(alice, s.cargopai, frank, role)
		if err != nil {
			t.Fatalf("the owner grants %s: %v", role, err)
		}
		if want := []audited{granted(alice, s.cargopai, "AC-10", assignment.ID)}; !slices.Equal(s.w.audited, want) {
			t.Errorf("%s: audited\n %+v\nwant\n %+v", role, s.w.audited, want)
		}
		s.w.quiet()
		if err := s.revokeRole(alice, s.cargopai, assignment.ID); err != nil {
			t.Fatalf("the owner ends %s: %v", role, err)
		}
		if want := []audited{ended(alice, s.cargopai, "AC-10", assignment.ID)}; !slices.Equal(s.w.audited, want) {
			t.Errorf("%s: audited\n %+v\nwant\n %+v", role, s.w.audited, want)
		}
	}

	before := maps.Clone(s.w.roles)
	for _, role := range []domain.Role{domain.RoleSecurityAdmin, domain.RoleSecurityReviewer} {
		s.w.quiet()
		got, err := s.give(admin, s.cargopai, frank, role)
		if err != ErrForbidden || got != (domain.RoleAssignment{}) {
			t.Fatalf("the administrator grants %s: got %+v and %v, want nothing and exactly %v", role, got, err, ErrForbidden)
		}
		if want := []audited{notGranted(admin, s.cargopai, "DENY-03")}; !slices.Equal(s.w.audited, want) || !maps.Equal(s.w.roles, before) {
			t.Errorf("%s: audited\n %+v\nwant\n %+v\nand the roles as they were", role, s.w.audited, want)
		}
	}
	s.w.quiet()
	owner, err := s.give(admin, s.cargopai, frank, domain.RoleProductOwner)
	if err != nil {
		t.Fatalf("the administrator names an owner: %v", err)
	}
	if want := []audited{granted(admin, s.cargopai, "AC-08", owner.ID)}; !slices.Equal(s.w.audited, want) {
		t.Errorf("audited\n %+v\nwant\n %+v", s.w.audited, want)
	}
}

// Whoever neither owns the product nor administers the platform appoints no
// auditor, dismisses none and reads none, the auditor itself included. Each
// is answered as far as it may know, whatever the request says: to whoever
// may know the product that it may not (and, of one assignment, that there
// is none), to anyone else that there is no such product. An appointment
// that could not be made anyway is refused the same way: whether the
// principal could audit is told to whoever may appoint, and to nobody else.
func TestWhoNeitherOwnsNorAdministersAppointsDismissesAndReadsNoAuditor(t *testing.T) {
	callers := maps.Clone(withoutSay)
	callers["the auditor of the product"] = answered{heidi, ErrForbidden}
	callers["an auditor of another product"] = answered{ivan, ErrProductNotFound}

	for name, c := range callers {
		t.Run(name, func(t *testing.T) {
			s := newBoard(t)
			role := auditor(t, s.service, s.cargopai, heidi)
			auditor(t, s.service, s.parcel, ivan)
			before := maps.Clone(s.w.roles)

			for what, principal := range map[string]domain.Principal{
				"somebody": mallory, "itself": c.caller, "an owner of the product": alice, "the administrator": admin,
			} {
				s.w.quiet()
				got, err := s.give(c.caller, s.cargopai, principal, domain.RoleInternalAuditor)
				if err != c.want || got != (domain.RoleAssignment{}) {
					t.Fatalf("appointing %s: got %+v and %v, want nothing and exactly %v", what, got, err, c.want)
				}
				if want := []audited{notGranted(c.caller, s.cargopai, "DENY-03")}; !slices.Equal(s.w.audited, want) {
					t.Errorf("appointing %s: audited\n %+v\nwant, outside the transaction,\n %+v", what, s.w.audited, want)
				}
			}

			s.w.quiet()
			if err := s.revokeRole(c.caller, s.cargopai, role.ID); err != c.want {
				t.Fatalf("dismissing the auditor: got %v, want exactly %v", err, c.want)
			}
			if want := []audited{notEnded(c.caller, s.cargopai, role.ID)}; !slices.Equal(s.w.audited, want) {
				t.Errorf("dismissing the auditor: audited\n %+v\nwant, outside the transaction,\n %+v", s.w.audited, want)
			}

			s.w.quiet()
			s.w.asked = nil
			page, err := s.listOf(c.caller, s.cargopai, "internal_auditor", 0, nil)
			if err != c.want || page.Items != nil || page.Next != nil {
				t.Fatalf("listing the auditors: got %+v and %v, want nothing and exactly %v", page, err, c.want)
			}
			if want := []audited{notRead(c.caller, s.cargopai, "correlation-14", "")}; !slices.Equal(s.w.audited, want) || len(s.w.asked) != 0 {
				t.Errorf("listing the auditors: audited\n %+v\nand the store was asked %+v; want\n %+v\nand no read", s.w.audited, s.w.asked, want)
			}

			// Whoever knows the product is told of one assignment that
			// there is none, as of any other.
			s.w.quiet()
			unread := c.want
			if unread == ErrForbidden {
				unread = ErrRoleAssignmentNotFound
			}
			if got, err := s.role(c.caller, s.cargopai, role.ID); err != unread || got != (domain.RoleAssignment{}) {
				t.Fatalf("reading the assignment of the auditor: got %+v and %v, want nothing and exactly %v", got, err, unread)
			}
			if want := []audited{notRead(c.caller, s.cargopai, "correlation-10", role.ID)}; !slices.Equal(s.w.audited, want) {
				t.Errorf("reading the assignment of the auditor: audited\n %+v\nwant\n %+v", s.w.audited, want)
			}

			if !maps.Equal(s.w.roles, before) || len(s.w.events) != 0 {
				t.Errorf("what was refused left the roles %+v and the events %v", s.w.roles, s.w.events)
			}
		})
	}
}

// An auditor an owner made when an owner could is an auditor still: nothing
// is taken from what was granted before. It is the administrator's to end
// now, as every auditor is, and not the owner's, its own included. An owner
// that made itself an auditor then audits its product until an
// administrator ends that, and is not made one again.
func TestAnAuditorAnOwnerMadeBeforeStillAuditsAndOnlyAnAdministratorEndsIt(t *testing.T) {
	s := newTeam(t)
	cargopai := string(s.cargopai.ID)
	old := s.w.assigned(s.cargopai, frank, domain.RoleInternalAuditor, alice)
	own := s.w.assigned(s.cargopai, alice, domain.RoleInternalAuditor, alice)

	for _, holder := range []domain.Principal{frank, alice} {
		if got, err := s.auditScope(holder, cargopai); err != nil || !got.Auditor {
			t.Errorf("%s reads the scope as %+v (%v), want the auditor it was made", holder.Subject, got, err)
		}
	}
	for _, role := range []domain.RoleAssignment{old, own} {
		if err := s.revokeRole(alice, s.cargopai, role.ID); err != ErrForbidden || s.w.roles[role.ID] != role {
			t.Errorf("the owner ends the assignment of %s: got %v and %+v, want exactly %v and the assignment in force",
				role.Principal.Subject, err, s.w.roles[role.ID], ErrForbidden)
		}
	}

	s.w.quiet()
	page, err := s.listOf(admin, s.cargopai, "internal_auditor", 0, nil)
	if want := []domain.RoleAssignment{old, own}; err != nil || !slices.Equal(page.Items, want) {
		t.Fatalf("the administrator lists the auditors: got %+v and %v, want %+v", page.Items, err, want)
	}
	for _, role := range []domain.RoleAssignment{old, own} {
		s.w.quiet()
		if err := s.revokeRole(admin, s.cargopai, role.ID); err != nil {
			t.Fatalf("the administrator ends the assignment of %s: %v", role.Principal.Subject, err)
		}
		if want := []audited{ended(admin, s.cargopai, "AC-08", role.ID)}; !slices.Equal(s.w.audited, want) {
			t.Errorf("audited\n %+v\nwant\n %+v", s.w.audited, want)
		}
	}
	for _, holder := range []domain.Principal{frank, alice} {
		if got, err := s.auditScope(holder, cargopai); err != nil || got.Auditor {
			t.Errorf("%s reads the scope as %+v (%v), want no auditor any more", holder.Subject, got, err)
		}
	}
	// alice owns the product as she did, and is not made its auditor again.
	if _, err := s.workspace(alice, s.engineering); err != nil {
		t.Errorf("the owner reads a workspace: %v", err)
	}
	if _, err := s.give(admin, s.cargopai, alice, domain.RoleInternalAuditor); err != ErrAuditorNotIndependent {
		t.Errorf("the owner is appointed an auditor again: got %v, want exactly %v", err, ErrAuditorNotIndependent)
	}
}

// To dismiss an auditor an administrator has to find it. An administrator
// that does not own the product lists its auditors by asking for that role:
// those in force, oldest first, a page at a time, each as one read gives
// it. It lists no other role and not all of them: it names an owner and
// reads none, as before. An owner lists every role, or one.
func TestAnAdministratorListsTheAuditorsOfAProductAndNoOtherRole(t *testing.T) {
	s := newBoard(t)
	security, err := s.give(alice, s.cargopai, carol, domain.RoleSecurityAdmin)
	if err != nil {
		t.Fatal(err)
	}
	first := auditor(t, s.service, s.cargopai, heidi)
	dismissed := auditor(t, s.service, s.cargopai, mallory)
	if err := s.revokeRole(admin, s.cargopai, dismissed.ID); err != nil {
		t.Fatal(err)
	}
	last := auditor(t, s.service, s.cargopai, dave)
	elsewhere := auditor(t, s.service, s.parcel, ivan)
	s.w.quiet()

	page, err := s.listOf(admin, s.cargopai, "internal_auditor", 0, nil)
	if want := []domain.RoleAssignment{first, last}; err != nil || !slices.Equal(page.Items, want) || page.Next != nil {
		t.Fatalf("the auditors of the product: got %+v and %v, want %+v and nothing to follow", page, err, want)
	}
	one, err := s.listOf(admin, s.cargopai, "internal_auditor", 1, nil)
	if err != nil || !slices.Equal(one.Items, []domain.RoleAssignment{first}) || one.Next == nil || *one.Next != ports.PositionOfRoleAssignment(first) {
		t.Fatalf("the first page: got %+v and %v, want the first auditor and where the next page starts", one, err)
	}
	if two, err := s.listOf(admin, s.cargopai, "internal_auditor", 1, one.Next); err != nil ||
		!slices.Equal(two.Items, []domain.RoleAssignment{last}) || two.Next != nil {
		t.Errorf("the second page: got %+v and %v, want the last auditor and nothing to follow", two, err)
	}
	for _, item := range page.Items {
		if got, err := s.role(admin, s.cargopai, item.ID); err != nil || got != item {
			t.Errorf("one read of %s: got %+v and %v, want what the list gave: %+v", item.ID, got, err, item)
		}
	}
	if other, err := s.listOf(admin, s.parcel, "internal_auditor", 0, nil); err != nil || !slices.Equal(other.Items, []domain.RoleAssignment{elsewhere}) {
		t.Errorf("the auditors of the other product: got %+v and %v, want its one auditor", other, err)
	}
	s.untouched(t)

	for name, role := range map[string]string{
		"every role": "", "the owners": "product_owner", "the security administrators": "security_admin",
		"the security reviewers": "security_reviewer", "a role nobody defined": "superuser",
	} {
		s.w.quiet()
		s.w.asked = nil
		page, err := s.listOf(admin, s.cargopai, role, 0, nil)
		if err != ErrForbidden || page.Items != nil || page.Next != nil {
			t.Fatalf("the administrator lists %s: got %+v and %v, want nothing and exactly %v", name, page, err, ErrForbidden)
		}
		if want := []audited{notRead(admin, s.cargopai, "correlation-14", "")}; !slices.Equal(s.w.audited, want) || len(s.w.asked) != 0 {
			t.Errorf("the administrator lists %s: audited\n %+v\nand the store was asked %+v; want\n %+v\nand no read", name, s.w.audited, s.w.asked, want)
		}
	}

	// An owner reads every role of its product, or those of one.
	s.w.quiet()
	for role, want := range map[string][]domain.RoleAssignment{
		"":                  {s.first, s.second, security, first, last},
		"product_owner":     {s.first, s.second},
		"security_admin":    {security},
		"security_reviewer": {},
		"internal_auditor":  {first, last},
	} {
		page, err := s.listOf(alice, s.cargopai, role, 0, nil)
		if err != nil || !slices.Equal(page.Items, want) || page.Items == nil || page.Next != nil {
			t.Errorf("the owner lists the role %q: got %+v and %v, want %+v and nothing to follow", role, page, err, want)
		}
	}
	s.untouched(t)
	// What the request says besides is looked at once the caller reads the
	// list: a role that is none is refused to an owner as what it is.
	if page, err := s.listOf(alice, s.cargopai, "superuser", 0, nil); !errors.Is(err, domain.ErrInvalid) || page.Items != nil {
		t.Errorf("the owner lists a role nobody defined: got %+v and %v, want nothing and %v", page, err, domain.ErrInvalid)
	}
	if _, err := s.service.ListProductRoles(ctx, ListProductRoles{
		Caller: admin, CorrelationID: "correlation-14", ProductID: missingID, Role: "internal_auditor",
	}); err != ErrProductNotFound {
		t.Errorf("the auditors of a product nobody registered: got %v, want exactly %v", err, ErrProductNotFound)
	}
}

// An administrator that does not own the product reads the assignment of an
// auditor in force, which is its own to end, and no other: of every other
// it is told that there is none, as before, whether there is one or not.
func TestAnAdministratorReadsTheAssignmentOfAnAuditorAndNoOther(t *testing.T) {
	s := newBoard(t)
	security, err := s.give(alice, s.cargopai, carol, domain.RoleSecurityAdmin)
	if err != nil {
		t.Fatal(err)
	}
	role := auditor(t, s.service, s.cargopai, heidi)
	elsewhere := auditor(t, s.service, s.parcel, ivan)
	dismissed := auditor(t, s.service, s.cargopai, mallory)
	if err := s.revokeRole(admin, s.cargopai, dismissed.ID); err != nil {
		t.Fatal(err)
	}

	s.w.quiet()
	if got, err := s.role(admin, s.cargopai, role.ID); err != nil || got != role {
		t.Errorf("the assignment of the auditor: got %+v and %v, want %+v", got, err, role)
	}
	if got, err := s.role(admin, s.parcel, elsewhere.ID); err != nil || got != elsewhere {
		t.Errorf("the assignment of the auditor of the other product, under that product: got %+v and %v, want %+v", got, err, elsewhere)
	}
	s.untouched(t)

	for name, c := range map[string]struct {
		id      string
		inForce bool
	}{
		"that of an owner":                        {string(s.first.ID), true},
		"that of a security administrator":        {string(security.ID), true},
		"that of an owner who was removed":        {string(s.former.ID), false},
		"that of an auditor it dismissed":         {string(dismissed.ID), false},
		"that of an auditor of another product":   {string(elsewhere.ID), false},
		"one that does not exist":                 {missingID, false},
		"one whose identifier is none":            {"auditor", false},
		"that of the auditor, under another name": {string(role.ID) + "0", false},
	} {
		s.w.quiet()
		got, err := s.service.GetProductRole(ctx, GetProductRole{
			Caller: admin, CorrelationID: "correlation-10", ProductID: string(s.cargopai.ID), RoleAssignmentID: c.id,
		})
		if err != ErrRoleAssignmentNotFound || got != (domain.RoleAssignment{}) {
			t.Fatalf("%s: got %+v and %v, want nothing and exactly %v", name, got, err, ErrRoleAssignmentNotFound)
		}
		want := notRead(admin, s.cargopai, "correlation-10", "")
		if c.inForce {
			want.entry.ResourceID = c.id
		}
		if !slices.Equal(s.w.audited, []audited{want}) {
			t.Errorf("%s: audited\n %+v\nwant\n %+v", name, s.w.audited, want)
		}
	}
}

// An auditor is independent of what it audits. Nobody audits a product it
// owns, and nobody who administers the platform audits a product: it would
// stand above what it audits, and read what an administrator does not
// read. Nor is whoever audits a product named an owner of it. The grant is
// refused as a separation of duties (DENY-06), to whoever may make it, and
// the refusal is on record whether or not it could be written.
func TestNobodyAuditsAProductItOwnsOrAdministersAndNoAuditorIsNamedItsOwner(t *testing.T) {
	refused := map[string]struct {
		caller, principal domain.Principal
		role              domain.Role
	}{
		"an owner of the product is appointed its auditor":             {admin, alice, domain.RoleInternalAuditor},
		"an owner that was named since is appointed its auditor":       {admin, frank, domain.RoleInternalAuditor},
		"the administrator appoints itself":                            {admin, admin, domain.RoleInternalAuditor},
		"another administrator is appointed":                           {admin, deputy, domain.RoleInternalAuditor},
		"an auditor of the product is named an owner by an owner":      {alice, heidi, domain.RoleProductOwner},
		"an auditor of the product is named an owner by administrator": {admin, heidi, domain.RoleProductOwner},
	}
	for name, c := range refused {
		t.Run(name, func(t *testing.T) {
			s := newBoard(t)
			s.w.administrators = append(s.w.administrators, deputy)
			auditor(t, s.service, s.cargopai, heidi)
			s.w.quiet()
			before := maps.Clone(s.w.roles)

			got, err := s.give(c.caller, s.cargopai, c.principal, c.role)
			if err != ErrAuditorNotIndependent || got != (domain.RoleAssignment{}) {
				t.Fatalf("got %+v and %v, want nothing and exactly %v", got, err, ErrAuditorNotIndependent)
			}
			if want := []audited{notGranted(c.caller, s.cargopai, "DENY-06")}; !slices.Equal(s.w.audited, want) {
				t.Errorf("audited\n %+v\nwant, outside the transaction,\n %+v", s.w.audited, want)
			}
			if !maps.Equal(s.w.roles, before) || len(s.w.events) != 0 {
				t.Errorf("the refused grant left the roles %+v and the events %v", s.w.roles, s.w.events)
			}

			s.w.quiet()
			s.w.failing["audit"] = errors.New("down")
			if _, err := s.give(c.caller, s.cargopai, c.principal, c.role); !errors.Is(err, ErrAuditorNotIndependent) || !errors.Is(err, ErrRefusalNotAudited) {
				t.Errorf("with the audit trail down: got %v, want %v and %v", err, ErrAuditorNotIndependent, ErrRefusalNotAudited)
			}
			if !maps.Equal(s.w.roles, before) {
				t.Errorf("the refused grant left the roles %+v", s.w.roles)
			}
		})
	}

	// It is the product that counts, and what is held in it now.
	s := newBoard(t)
	auditor(t, s.service, s.cargopai, heidi)
	auditor(t, s.service, s.parcel, ivan)
	dismissed := auditor(t, s.service, s.cargopai, mallory)
	if err := s.revokeRole(admin, s.cargopai, dismissed.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name              string
		caller, principal domain.Principal
		role              domain.Role
	}{
		{"the owner of another product audits this one", admin, bob, domain.RoleInternalAuditor},
		{"an auditor of another product is named an owner of this one", alice, ivan, domain.RoleProductOwner},
		{"whoever owned the product and does not any more audits it", admin, grace, domain.RoleInternalAuditor},
		{"whoever audited the product and does not any more is named its owner", alice, mallory, domain.RoleProductOwner},
		{"a member of a workspace of the product audits it", admin, carol, domain.RoleInternalAuditor},
		{"an auditor of the product is given a security role of it", alice, heidi, domain.RoleSecurityAdmin},
	} {
		if _, err := s.give(c.caller, s.cargopai, c.principal, c.role); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

// A grant locks the roles of its caller while it decides, and no other row.
// Whether whoever it names can hold the role is read and not locked: a
// revocation locks every owner of the product at once, and a grant that
// held its caller's role and then waited for the principal's could wait for
// a revocation that waits for it.
func TestAGrantLocksTheRolesOfItsCallerAndOnlyReadsThoseOfWhoeverItNames(t *testing.T) {
	s := newBoard(t)
	auditor(t, s.service, s.cargopai, heidi)

	for name, c := range map[string]struct {
		caller, principal domain.Principal
		role              domain.Role
		want              error
	}{
		"an appointment":                         {admin, carol, domain.RoleInternalAuditor, nil},
		"an appointment of an owner":             {admin, alice, domain.RoleInternalAuditor, ErrAuditorNotIndependent},
		"an owner named by an owner":             {alice, dave, domain.RoleProductOwner, nil},
		"an auditor named an owner":              {alice, heidi, domain.RoleProductOwner, ErrAuditorNotIndependent},
		"an owner named by the administrator":    {admin, mallory, domain.RoleProductOwner, nil},
		"a security role granted by an owner":    {frank, erin, domain.RoleSecurityAdmin, nil},
		"an appointment an owner is refused":     {alice, bob, domain.RoleInternalAuditor, ErrForbidden},
		"a grant a stranger is refused, to boot": {ivan, bob, domain.RoleInternalAuditor, ErrProductNotFound},
	} {
		s.w.locked = nil
		if _, err := s.give(c.caller, s.cargopai, c.principal, c.role); err != c.want {
			t.Errorf("%s: got %v, want exactly %v", name, err, c.want)
		}
		if want := []domain.Principal{c.caller}; !slices.Equal(s.w.locked, want) {
			t.Errorf("%s: the roles of %v were locked, want those of the caller alone, %v", name, s.w.locked, want)
		}
	}
}

// What whoever is named holds decides whether it can hold the role. A grant
// that cannot read it is a failure and grants nothing: a principal whose
// roles could not be read is not taken for one that holds none. A role that
// is held to no separation is granted without that read.
func TestAGrantThatCannotReadWhatItsPrincipalHoldsFailsAndGrantsNothing(t *testing.T) {
	down := errors.New("the roles of the principal are out of reach")
	for name, c := range map[string]struct {
		caller domain.Principal
		role   domain.Role
	}{
		"an appointment":                      {admin, domain.RoleInternalAuditor},
		"an owner named by an owner":          {alice, domain.RoleProductOwner},
		"an owner named by the administrator": {admin, domain.RoleProductOwner},
	} {
		t.Run(name, func(t *testing.T) {
			s := newTeam(t)
			s.w.unreadable, s.w.unreadableRoles = frank, down
			before := maps.Clone(s.w.roles)

			if got, err := s.give(c.caller, s.cargopai, frank, c.role); !errors.Is(err, down) || got != (domain.RoleAssignment{}) {
				t.Fatalf("got %+v and %v, want nothing and the failure", got, err)
			}
			if !maps.Equal(s.w.roles, before) || len(s.w.audited)+len(s.w.events) != 0 {
				t.Errorf("the failed grant left roles %+v, audit %+v, events %v", s.w.roles, s.w.audited, s.w.events)
			}
		})
	}

	s := newTeam(t)
	s.w.unreadable, s.w.unreadableRoles = frank, down
	if _, err := s.give(alice, s.cargopai, frank, domain.RoleSecurityAdmin); err != nil {
		t.Errorf("a security role of a principal whose roles are out of reach: %v, want it granted without reading them", err)
	}
}

// INV-01: a suspended product takes no new auditor, as it takes no new
// owner. Its auditors can still be dismissed.
func TestASuspendedProductTakesNoAuditorAndLetsItsAuditorsGo(t *testing.T) {
	s := newTeam(t)
	role := auditor(t, s.service, s.cargopai, frank)
	suspended := s.cargopai
	suspended.Status = domain.ProductSuspended
	s.w.products[s.cargopai.ID] = suspended
	s.w.quiet()
	before := maps.Clone(s.w.roles)

	if got, err := s.give(admin, s.cargopai, heidi, domain.RoleInternalAuditor); !errors.Is(err, domain.ErrProductNotActive) || got != (domain.RoleAssignment{}) {
		t.Fatalf("got %+v and %v, want nothing and %v", got, err, domain.ErrProductNotActive)
	}
	if !maps.Equal(s.w.roles, before) || len(s.w.events) != 0 || len(s.w.audited) != 0 {
		t.Errorf("a suspended product got roles %+v, events %v, audit %+v", s.w.roles, s.w.events, s.w.audited)
	}
	if err := s.revokeRole(admin, s.cargopai, role.ID); err != nil || s.w.roles[role.ID].InForce() {
		t.Errorf("the auditor of a suspended product is dismissed: got %v and the assignment %+v", err, s.w.roles[role.ID])
	}
}
