package application

import (
	"context"
	"errors"
	"slices"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

var (
	heidi = domain.Principal{Issuer: trustedIssuer, Subject: "heidi"}
	ivan  = domain.Principal{Issuer: trustedIssuer, Subject: "ivan"}
)

// auditor makes principal an auditor of the product, as one of its owners.
func auditor(t *testing.T, service *ProductGovernance, product domain.Product, owner, principal domain.Principal) domain.RoleAssignment {
	t.Helper()

	assignment, err := service.GrantProductRole(ctx, GrantProductRole{
		Caller: owner, CorrelationID: "correlation-a", ProductID: string(product.ID),
		Principal: principal, Role: string(domain.RoleInternalAuditor),
	})
	if err != nil {
		t.Fatalf("make %s an auditor of %s: %v", principal.Subject, product.Key, err)
	}
	return assignment
}

// auditScope is what Product Governance says of the caller to whoever reads
// the audit records of the product. The reading is done in a transaction, as
// the one that reads the records does it.
func (s team) auditScope(caller domain.Principal, productID string) (AuditScope, error) {
	var scope AuditScope
	err := s.w.WithinTransaction(ctx, func(ctx context.Context) error {
		var err error
		scope, err = s.service.ReadAuditScope(ctx, caller, domain.ProductID(productID))
		return err
	})
	return scope, err
}

// Only a principal that holds the role of an auditor in the product reads
// what was recorded about it. Whoever else holds something in the product
// knows that it exists and is no auditor; to anyone else, and for a product
// nobody registered, there is nothing to know.
func TestTheAuditScopeSaysWhoMayReadTheTrailOfAProduct(t *testing.T) {
	s := newTeam(t)
	cargopai := string(s.cargopai.ID)

	// frank audits CargoPai and ivan audits Parcel. heidi audited CargoPai
	// until alice revoked her role. grace holds a security role in CargoPai.
	auditor(t, s.service, s.cargopai, alice, frank)
	auditor(t, s.service, s.parcel, bob, ivan)
	former := auditor(t, s.service, s.cargopai, alice, heidi)
	if err := s.revokeRole(alice, s.cargopai, former.ID); err != nil {
		t.Fatalf("revoke the role of heidi: %v", err)
	}
	if _, err := s.service.GrantProductRole(ctx, GrantProductRole{
		Caller: alice, CorrelationID: "correlation-a", ProductID: cargopai, Principal: grace, Role: string(domain.RoleSecurityAdmin),
	}); err != nil {
		t.Fatalf("make grace a security administrator: %v", err)
	}
	s.w.quiet()

	knows, nothing := AuditScope{Found: true, Known: true}, AuditScope{Found: true}
	cases := map[string]struct {
		caller domain.Principal
		want   AuditScope
	}{
		"an auditor of the product":                    {frank, AuditScope{Found: true, Known: true, Auditor: true}},
		"an owner of the product":                      {alice, knows},
		"a member of a workspace of the product":       {carol, knows},
		"a security administrator of the product":      {grace, knows},
		"the platform administrator":                   {admin, knows},
		"an auditor of another product":                {ivan, nothing},
		"the owner of another product":                 {bob, nothing},
		"whoever audited the product and does not now": {heidi, nothing},
		"whoever was a member and is not any more":     {erin, nothing},
		"a stranger": {mallory, nothing},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := s.auditScope(c.caller, cargopai); err != nil || got != c.want {
				t.Errorf("got %+v and %v, want %+v", got, err, c.want)
			}
		})
	}

	// Nobody is somebody's auditor by being it somewhere else.
	if got, err := s.auditScope(ivan, string(s.parcel.ID)); err != nil || got != (AuditScope{Found: true, Known: true, Auditor: true}) {
		t.Errorf("the auditor of Parcel reads the scope of Parcel as %+v (%v), want an auditor", got, err)
	}
	if got, err := s.auditScope(frank, string(s.parcel.ID)); err != nil || got != nothing {
		t.Errorf("the auditor of CargoPai reads the scope of Parcel as %+v (%v), want a stranger", got, err)
	}

	// There is no product to know, whoever asks, and an identifier that is
	// none names no product.
	for _, caller := range []domain.Principal{frank, alice, admin, mallory} {
		if got, err := s.auditScope(caller, missingID); err != nil || got != (AuditScope{}) {
			t.Errorf("%s reads the scope of a product nobody registered as %+v (%v), want nothing", caller.Subject, got, err)
		}
	}
	if got, err := s.auditScope(frank, "not-a-product"); !errors.Is(err, ErrProductNotFound) || got != (AuditScope{}) {
		t.Errorf("an identifier that is none: got %+v and %v, want nothing and %v", got, err, ErrProductNotFound)
	}
	if got, err := s.auditScope(domain.Principal{}, cargopai); !errors.Is(err, ErrForbidden) || got != (AuditScope{}) {
		t.Errorf("a request without a principal: got %+v and %v, want nothing and %v", got, err, ErrForbidden)
	}

	// Reading what a caller holds records nothing and changes nothing.
	if len(s.w.audited) != 0 || len(s.w.events) != 0 {
		t.Errorf("reading the scope left the audit records %+v and the events %v", s.w.audited, s.w.events)
	}
}

// A role that was revoked grants nothing from that moment on: the scope is
// read from what the caller holds then.
func TestARevokedAuditorIsNoAuditorAtOnce(t *testing.T) {
	s := newTeam(t)
	cargopai := string(s.cargopai.ID)
	role := auditor(t, s.service, s.cargopai, alice, frank)

	if got, err := s.auditScope(frank, cargopai); err != nil || !got.Auditor {
		t.Fatalf("the auditor reads the scope as %+v (%v), want an auditor", got, err)
	}
	if err := s.revokeRole(alice, s.cargopai, role.ID); err != nil {
		t.Fatalf("revoke the role: %v", err)
	}
	if got, err := s.auditScope(frank, cargopai); err != nil || got != (AuditScope{Found: true}) {
		t.Errorf("once revoked it reads the scope as %+v (%v), want a stranger", got, err)
	}

	// The role can be granted again, as an assignment of its own.
	again := auditor(t, s.service, s.cargopai, alice, frank)
	if again.ID == role.ID {
		t.Errorf("the role was granted again under the identifier %s of the one that was revoked", again.ID)
	}
	if got, err := s.auditScope(frank, cargopai); err != nil || !got.Auditor {
		t.Errorf("granted again it reads the scope as %+v (%v), want an auditor", got, err)
	}
}

// Without a transaction the roles of the caller are not locked, and the scope
// is not read: whoever decides from it does so where the records are read.
func TestTheAuditScopeIsReadInATransactionOnly(t *testing.T) {
	s := newTeam(t)
	auditor(t, s.service, s.cargopai, alice, frank)

	if got, err := s.service.ReadAuditScope(ctx, frank, s.cargopai.ID); err == nil || got.Auditor {
		t.Errorf("read outside a transaction: got %+v and %v, want a refusal and no auditor", got, err)
	}
}

// Administering the platform conveys nothing inside a product (F05 §6), and
// reading what was recorded about it is inside. An owner names an auditor,
// under the rule of the owners, and may name itself: it reads the trail then,
// and the grant is on record. An administrator that owns the product is an
// owner there, and is recorded as one.
func TestAnAdministratorWhoOwnsNothingIsRefusedTheAuditorRoleAndAnOwnerNamesOne(t *testing.T) {
	s := newTeam(t)
	cargopai := string(s.cargopai.ID)
	grantAuditor := func(caller domain.Principal, product domain.Product, principal domain.Principal) (domain.RoleAssignment, error) {
		return s.service.GrantProductRole(ctx, GrantProductRole{
			Caller: caller, CorrelationID: "correlation-8", ProductID: string(product.ID),
			Principal: principal, Role: string(domain.RoleInternalAuditor),
		})
	}
	before := len(s.w.roles)

	// The administrator names owners and no auditor. It is told it may not,
	// and the refusal is on record, as that of the security roles is.
	if assignment, err := grantAuditor(admin, s.cargopai, frank); err != ErrForbidden || assignment != (domain.RoleAssignment{}) {
		t.Fatalf("the administrator names an auditor: got %+v and %v, want nothing and exactly %v", assignment, err, ErrForbidden)
	}
	wantRefusal := []audited{{ports.AuditEntry{
		Actor: admin, Action: "product_role.grant", Rule: "DENY-03",
		ProductID: s.cargopai.ID, ResourceType: "RoleAssignment", CorrelationID: "correlation-8",
	}, false}}
	if !slices.Equal(s.w.audited, wantRefusal) || len(s.w.roles) != before || len(s.w.events) != 0 {
		t.Errorf("the refusal left the audit records\n %+v\nwant, outside the transaction,\n %+v\nand %d roles and the events %v",
			s.w.audited, wantRefusal, len(s.w.roles)-before, s.w.events)
	}

	// An owner names one, and itself.
	s.w.quiet()
	assignment, err := grantAuditor(alice, s.cargopai, frank)
	if err != nil {
		t.Fatalf("the owner names an auditor: %v", err)
	}
	want := domain.RoleAssignment{
		ID: assignment.ID, ProductID: s.cargopai.ID, Principal: frank, Role: domain.RoleInternalAuditor,
		Version: 1, GrantedAt: s.w.now, GrantedBy: alice,
	}
	if assignment != want || s.w.roles[assignment.ID] != want {
		t.Errorf("assignment\n %+v\nstored\n %+v\nwant\n %+v", assignment, s.w.roles[assignment.ID], want)
	}
	wantGrant := []audited{{ports.AuditEntry{
		Actor: alice, Action: "product_role.grant", Allowed: true, Rule: "AC-10",
		ProductID: s.cargopai.ID, ResourceType: "RoleAssignment", ResourceID: string(assignment.ID), CorrelationID: "correlation-8",
	}, true}}
	if !slices.Equal(s.w.audited, wantGrant) {
		t.Errorf("audited\n %+v\nwant, inside the transaction,\n %+v", s.w.audited, wantGrant)
	}
	if want := []string{"ProductRoleChanged " + string(assignment.ID) + " granted correlation-8"}; !slices.Equal(s.w.events, want) {
		t.Errorf("events %v, want %v", s.w.events, want)
	}
	if _, err := grantAuditor(alice, s.cargopai, alice); err != nil {
		t.Errorf("the owner names itself an auditor: %v", err)
	}
	if got, err := s.auditScope(alice, cargopai); err != nil || !got.Auditor {
		t.Errorf("the owner that named itself reads the scope as %+v (%v), want an auditor", got, err)
	}
	// The role is held once at a time, as the others are.
	if _, err := grantAuditor(alice, s.cargopai, frank); !errors.Is(err, ports.ErrRoleAssignmentExists) {
		t.Errorf("the role named twice: got %v, want %v", err, ports.ErrRoleAssignmentExists)
	}

	// An administrator that owns a product acts there as its owner.
	named(t, s.service, s.parcel, bob, admin)
	s.w.quiet()
	if _, err := grantAuditor(admin, s.parcel, ivan); err != nil {
		t.Fatalf("an administrator that owns the product names an auditor: %v", err)
	}
	if len(s.w.audited) != 1 || s.w.audited[0].entry.Rule != "AC-10" || !s.w.audited[0].entry.Allowed {
		t.Errorf("audited %+v, want one record under the rule of the owners", s.w.audited)
	}
}

// An auditor reads the trail and manages nothing: it is no owner, so it names
// no role and ends none, and it reads none of the role assignments.
func TestAnAuditorIsNoOwnerAndManagesNoRole(t *testing.T) {
	s := newTeam(t)
	role := auditor(t, s.service, s.cargopai, alice, frank)
	s.w.quiet()
	before := len(s.w.roles)

	if _, err := s.grantRole(frank, s.cargopai, mallory); err != ErrForbidden {
		t.Errorf("the auditor names an owner: got %v, want exactly %v", err, ErrForbidden)
	}
	if err := s.revokeRole(frank, s.cargopai, role.ID); err != ErrForbidden {
		t.Errorf("the auditor revokes its own role: got %v, want exactly %v", err, ErrForbidden)
	}
	if _, err := s.service.ListProductRoles(ctx, ListProductRoles{Caller: frank, CorrelationID: "correlation-3", ProductID: string(s.cargopai.ID)}); err != ErrForbidden {
		t.Errorf("the auditor lists the roles: got %v, want exactly %v", err, ErrForbidden)
	}
	if _, err := s.workspace(frank, s.engineering); err != ErrWorkspaceNotFound {
		t.Errorf("the auditor reads a workspace: got %v, want exactly %v", err, ErrWorkspaceNotFound)
	}
	if len(s.w.roles) != before || !s.w.roles[role.ID].InForce() {
		t.Errorf("what the auditor was refused left %d roles and its own as %+v", len(s.w.roles), s.w.roles[role.ID])
	}

	// It knows the product, as whoever holds a role in it does.
	if got, err := s.product(frank, s.cargopai); err != nil || got.ID != s.cargopai.ID {
		t.Errorf("the auditor reads the product: got %+v and %v", got, err)
	}
	// An owner ends the role, and records it under its rule.
	s.w.quiet()
	if err := s.revokeRole(alice, s.cargopai, role.ID); err != nil {
		t.Fatalf("the owner revokes the role of the auditor: %v", err)
	}
	if len(s.w.audited) != 1 || s.w.audited[0].entry.Action != "product_role.revoke" || s.w.audited[0].entry.Rule != "AC-10" ||
		!s.w.audited[0].entry.Allowed || !s.w.audited[0].transacted {
		t.Errorf("audited %+v, want one record of the revocation under the rule of the owners, inside the transaction", s.w.audited)
	}
	if _, err := s.product(frank, s.cargopai); err != ErrProductNotFound {
		t.Errorf("the revoked auditor reads the product: got %v, want exactly %v", err, ErrProductNotFound)
	}
}
