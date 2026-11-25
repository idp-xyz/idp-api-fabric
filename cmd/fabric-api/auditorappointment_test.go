package main

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

// These tests run the appointment of an auditor as fabric-api assembles it:
// the transport, the validator, the use cases of Product Governance and a
// real PostgreSQL reached as the application role.

// roleAssignmentsOf is where the roles of the product are granted and listed.
func roleAssignmentsOf(product string) string { return "/v1/products/" + product + "/role-assignments" }

// A platform administrator appoints who audits a product and dismisses it.
// An owner of the product does neither, for anybody or for itself, and the
// auditor does not end its own role. An auditor is independent of what it
// audits: no owner of the product and no platform administrator is
// appointed, and no auditor of the product is named its owner. The owner
// hands out the security roles as before, which the administrator does not.
// What was done is on record under the rule of whoever did it, and what was
// refused under the rule it was refused by.
func TestOnlyAPlatformAdministratorAppointsAndDismissesAnAuditor(t *testing.T) {
	s := start(t, "platform-admin", "deputy-admin")
	cargopai := s.register("platform-admin", "cargopai", "alice")
	engineering := s.createWorkspace(cargopai, "alice", "engineering")
	s.join(cargopai, engineering, "alice", "carol", "workspace_maintainer")
	path := auditPath(cargopai)
	events := s.count("bento.outbox")

	// An owner appoints nobody: not a stranger, not a member, not itself.
	for _, subject := range []string{"ada", "carol", "alice"} {
		s.requireThat(s.grantRole(cargopai, "alice", subject, "internal_auditor"), http.StatusForbidden, "FORBIDDEN")
	}
	s.requireThat(s.call(http.MethodGet, path, "ada", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")

	appointed := s.grantRole(cargopai, "platform-admin", "ada", "internal_auditor")
	s.requireThat(appointed, http.StatusCreated, "")
	location := appointed.header.Get("Location")
	if appointed.body["role"] != "internal_auditor" || appointed.body["product_id"] != cargopai ||
		appointed.body["principal"].(map[string]any)["subject"] != "ada" || location != roleAssignmentsOf(cargopai)+"/"+appointed.id("role_assignment_id") ||
		appointed.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the appointment: %v %v", appointed.body, appointed.header)
	}
	s.requireThat(s.call(http.MethodGet, path, "ada", ""), http.StatusOK, "")
	s.requireThat(s.grantRole(cargopai, "platform-admin", "ada", "internal_auditor"), http.StatusConflict, "ROLE_ASSIGNMENT_EXISTS")
	// Appointing gave the administrator nothing to read.
	s.requireThat(s.call(http.MethodGet, path, "platform-admin", ""), http.StatusForbidden, "FORBIDDEN")

	// Nobody audits what it owns or administers, and no auditor is named an
	// owner of what it audits, whoever names it.
	for _, c := range []struct{ grantor, subject, role string }{
		{"platform-admin", "alice", "internal_auditor"},
		{"platform-admin", "platform-admin", "internal_auditor"},
		{"platform-admin", "deputy-admin", "internal_auditor"},
		{"deputy-admin", "platform-admin", "internal_auditor"},
		{"platform-admin", "ada", "product_owner"},
		{"alice", "ada", "product_owner"},
	} {
		got := s.grantRole(cargopai, c.grantor, c.subject, c.role)
		if got.status != http.StatusForbidden || got.code() != "AUDITOR_NOT_INDEPENDENT" {
			t.Errorf("%s makes %s hold %s: %d %v, want 403 AUDITOR_NOT_INDEPENDENT", c.grantor, c.subject, c.role, got.status, got.body)
		}
	}
	// A member of a workspace is neither, and audits. The security roles are
	// the owner's to hand out, as before.
	member := s.grantRole(cargopai, "deputy-admin", "carol", "internal_auditor")
	s.requireThat(member, http.StatusCreated, "")
	security := s.grantRole(cargopai, "alice", "sam", "security_admin")
	s.requireThat(security, http.StatusCreated, "")
	s.requireThat(s.grantRole(cargopai, "platform-admin", "sam", "security_reviewer"), http.StatusForbidden, "FORBIDDEN")

	// Whoever is audited does not send its auditor away, and the auditor does
	// not leave by itself. The administrator dismisses it, once.
	s.requireThat(s.call(http.MethodDelete, location, "alice", ""), http.StatusForbidden, "FORBIDDEN")
	s.requireThat(s.call(http.MethodDelete, location, "ada", ""), http.StatusForbidden, "FORBIDDEN")
	s.requireThat(s.call(http.MethodGet, path, "ada", ""), http.StatusOK, "")
	s.requireThat(s.call(http.MethodDelete, location, "platform-admin", ""), http.StatusNoContent, "")
	s.requireThat(s.call(http.MethodGet, path, "ada", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	s.requireThat(s.call(http.MethodDelete, location, "platform-admin", ""), http.StatusForbidden, "FORBIDDEN")
	// It ends no assignment of another role, and the owner ends those.
	s.requireThat(s.call(http.MethodDelete, security.header.Get("Location"), "platform-admin", ""), http.StatusForbidden, "FORBIDDEN")
	s.requireThat(s.call(http.MethodDelete, security.header.Get("Location"), "alice", ""), http.StatusNoContent, "")

	record := func(subject, action, outcome, rule, resource string) audited {
		return audited{subject, action, outcome, rule, cargopai, "RoleAssignment", resource}
	}
	want := map[audited]int{
		record("alice", "product_role.grant", "DENIED", "DENY-03", ""):                                              3,
		record("platform-admin", "product_role.grant", "SUCCEEDED", "AC-08", last(location)):                        1,
		record("platform-admin", "product_role.grant", "DENIED", "DENY-06", ""):                                     4,
		record("deputy-admin", "product_role.grant", "DENIED", "DENY-06", ""):                                       1,
		record("alice", "product_role.grant", "DENIED", "DENY-06", ""):                                              1,
		record("deputy-admin", "product_role.grant", "SUCCEEDED", "AC-08", last(member.header.Get("Location"))):     1,
		record("alice", "product_role.grant", "SUCCEEDED", "AC-10", last(security.header.Get("Location"))):          1,
		record("platform-admin", "product_role.grant", "DENIED", "DENY-03", ""):                                     1,
		record("alice", "product_role.revoke", "DENIED", "DENY-03", last(location)):                                 1,
		record("ada", "product_role.revoke", "DENIED", "DENY-03", last(location)):                                   1,
		record("platform-admin", "product_role.revoke", "SUCCEEDED", "AC-08", last(location)):                       1,
		record("platform-admin", "product_role.revoke", "DENIED", "DENY-03", ""):                                    1,
		record("platform-admin", "product_role.revoke", "DENIED", "DENY-03", last(security.header.Get("Location"))): 1,
		record("alice", "product_role.revoke", "SUCCEEDED", "AC-10", last(security.header.Get("Location"))):         1,
	}
	if got := s.recorded(func(r audited) bool { return strings.HasPrefix(r.Action, "product_role.") }); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("the audit trail holds\n %v\nwant\n %v", got, want)
	}
	// An event for each grant and each revocation that was made, and none for
	// what was refused.
	if got := s.count("bento.outbox") - events; got != 5 {
		t.Errorf("%d events were written, want the three grants and the two revocations", got)
	}
}

// An auditor an owner made while an owner could is an auditor still: nothing
// is taken from what was granted before. It is the administrator's to find
// and to end now, and not the owner's, its own included. An owner that made
// itself an auditor then reads the trail until the administrator ends that,
// and is not made one again.
func TestAnAuditorAnOwnerMadeBeforeStillReadsTheTrailUntilAnAdministratorEndsIt(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "alice")
	path, roles := auditPath(cargopai), roleAssignmentsOf(cargopai)

	// The rows as the build before wrote them: granted by the owner, to
	// somebody and to itself.
	for _, subject := range []string{"olga", "alice"} {
		if _, err := s.owner.Exec(t.Context(), `
			INSERT INTO productgovernance.product_role_assignment
			    (product_id, principal_issuer, principal_subject, role, granted_at, granted_by_issuer, granted_by_subject)
			VALUES ($1, $2, $3, 'internal_auditor', clock_timestamp(), $2, 'alice')`, cargopai, s.issuer.URL(), subject); err != nil {
			t.Fatalf("write the role of %s as the build before did: %v", subject, err)
		}
	}
	for _, subject := range []string{"olga", "alice"} {
		if got := s.listed(path, subject, 10); len(got) == 0 {
			t.Errorf("%s, an auditor from before, reads no record", subject)
		}
	}
	// Whoever operates the service finds them with two read-only statements:
	// the auditors nobody who administers the platform appointed, and the
	// accounts that own a product and audit it.
	subjects := func(statement string, parameters ...any) []string {
		t.Helper()
		rows, err := s.owner.Query(t.Context(), statement, parameters...)
		if err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
		found, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
		return found
	}
	if got := subjects(`
		SELECT principal_subject FROM productgovernance.product_role_assignment
		WHERE role = 'internal_auditor' AND revoked_at IS NULL AND granted_by_subject <> ALL ($1::text[])
		ORDER BY granted_at`, []string{"platform-admin"}); !slices.Equal(got, []string{"olga", "alice"}) {
		t.Errorf("the auditors no administrator appointed are %v, want the two from before", got)
	}
	if got := subjects(`
		SELECT a.principal_subject
		FROM productgovernance.product_role_assignment a
		JOIN productgovernance.product_role_assignment o USING (product_id, principal_issuer, principal_subject)
		WHERE a.role = 'internal_auditor' AND o.role = 'product_owner' AND a.revoked_at IS NULL AND o.revoked_at IS NULL`); !slices.Equal(got, []string{"alice"}) {
		t.Errorf("the accounts that own a product and audit it are %v, want the owner that made itself an auditor", got)
	}

	listed, next := s.page(roles+"?role=internal_auditor", "platform-admin")
	if got := told(listed, "principal"); !slices.Equal(got, []string{"olga", "alice"}) || next != "" {
		t.Fatalf("the administrator lists the auditors %v and a token %q, want the two from before and no token", got, next)
	}
	for _, item := range listed {
		assignment := roles + "/" + fmt.Sprint(item["role_assignment_id"])
		s.requireThat(s.call(http.MethodDelete, assignment, "alice", ""), http.StatusForbidden, "FORBIDDEN")
	}
	for _, subject := range []string{"olga", "alice"} {
		s.requireThat(s.call(http.MethodGet, path, subject, ""), http.StatusOK, "")
	}
	for _, item := range listed {
		s.revoke(roles+"/"+fmt.Sprint(item["role_assignment_id"]), "platform-admin")
	}
	s.requireThat(s.call(http.MethodGet, path, "olga", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	s.requireThat(s.call(http.MethodGet, path, "alice", ""), http.StatusForbidden, "FORBIDDEN")
	if listed, _ := s.page(roles+"?role=internal_auditor", "platform-admin"); len(listed) != 0 {
		t.Errorf("once they are dismissed the administrator lists %v, want no auditor", listed)
	}
	// alice owns the product as she did, and is not made its auditor again.
	if got, want := s.owners(cargopai), []string{"alice"}; !slices.Equal(got, want) {
		t.Errorf("the owners are %v, want %v", got, want)
	}
	s.requireThat(s.grantRole(cargopai, "platform-admin", "alice", "internal_auditor"), http.StatusForbidden, "AUDITOR_NOT_INDEPENDENT")
}

// To dismiss an auditor the administrator has to find it. An administrator
// that does not own the product lists its auditors by asking for that role:
// those the table holds in force, oldest first, a page at a time, each as
// one read shows it. It lists and reads no other role and not all of them,
// as before, and each refusal is on record. An owner lists every role of
// its product, or those of one, and nobody else lists any.
func TestTheAdministratorListsAndReadsTheAuditorsTheTableHoldsAndNoOtherRole(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai, parcel := s.register("platform-admin", "cargopai", "alice"), s.register("platform-admin", "parcel", "bob")
	roles := roleAssignmentsOf(cargopai)
	security := s.grantRole(cargopai, "alice", "sam", "security_admin")
	s.requireThat(security, http.StatusCreated, "")
	for _, subject := range []string{"ada", "ben"} {
		s.auditorOf(cargopai, subject)
	}
	gone := s.auditorOf(cargopai, "dee")
	s.revoke(gone, "platform-admin")
	s.auditorOf(cargopai, "cy")
	elsewhere := s.auditorOf(parcel, "eve")

	rows, err := s.owner.Query(t.Context(), `
		SELECT role_assignment_id::text FROM productgovernance.product_role_assignment
		WHERE product_id = $1 AND role = 'internal_auditor' AND revoked_at IS NULL
		ORDER BY granted_at, role_assignment_id`, cargopai)
	if err != nil {
		t.Fatal(err)
	}
	held, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(held) != 3 {
		t.Fatalf("the table holds the auditors %v (%v), want three", held, err)
	}

	listed, next := s.page(roles+"?role=internal_auditor", "platform-admin")
	if got := told(listed, "role_assignment_id"); !slices.Equal(got, held) || next != "" {
		t.Fatalf("the administrator lists %v and a token %q, want the auditors the table holds, %v, and no token", got, next, held)
	}
	if got := told(listed, "principal"); !slices.Equal(got, []string{"ada", "ben", "cy"}) || !slices.Equal(told(listed, "role"), []string{"internal_auditor", "internal_auditor", "internal_auditor"}) {
		t.Errorf("the administrator lists %v as %v, want ada, ben and cy as auditors", got, told(listed, "role"))
	}
	pages := s.auditPagesOf(roles+"?role=internal_auditor", "platform-admin", 2)
	if len(pages) != 2 || len(pages[0]) != 2 || len(pages[1]) != 1 || !slices.Equal(told(slices.Concat(pages...), "role_assignment_id"), held) {
		t.Errorf("in pages of two the administrator reads %v, want a full page and the rest, the same auditors", pages)
	}
	for _, item := range listed {
		read := s.call(http.MethodGet, roles+"/"+fmt.Sprint(item["role_assignment_id"]), "platform-admin", "")
		if read.status != http.StatusOK || fmt.Sprint(read.body) != fmt.Sprint(item) || read.header.Get("Cache-Control") != "no-store" {
			t.Errorf("the auditor listed as %v is read as %d %v", item, read.status, read.body)
		}
	}
	if other, _ := s.page(roleAssignmentsOf(parcel)+"?role=internal_auditor", "platform-admin"); !slices.Equal(told(other, "role_assignment_id"), []string{last(elsewhere)}) {
		t.Errorf("the auditors of the other product are listed as %v, want its one auditor", other)
	}

	// An owner reads what the administrator reads of the auditors, and every
	// role besides.
	if mine, _ := s.page(roles+"?role=internal_auditor", "alice"); fmt.Sprint(mine) != fmt.Sprint(listed) {
		t.Errorf("the owner lists the auditors as %v, the administrator as %v", mine, listed)
	}
	all, _ := s.page(roles, "alice")
	if got := told(all, "principal"); !slices.Equal(got, []string{"alice", "sam", "ada", "ben", "cy"}) {
		t.Fatalf("the owner lists the roles of %v, want its own, the security administrator and the three auditors", got)
	}
	if owners, _ := s.page(roles+"?role=product_owner", "alice"); !slices.Equal(told(owners, "principal"), []string{"alice"}) {
		t.Errorf("the owner lists the owners %v, want itself", told(owners, "principal"))
	}
	if reviewers, _ := s.page(roles+"?role=security_reviewer", "alice"); len(reviewers) != 0 {
		t.Errorf("the owner lists the security reviewers %v, want none", reviewers)
	}
	owner := fmt.Sprint(all[0]["role_assignment_id"])

	before := len(s.audit())
	s.expect("the administrator reads nothing but the auditors", "platform-admin", []exchange{
		{http.MethodGet, roles, "", http.StatusForbidden, "FORBIDDEN"},
		{http.MethodGet, roles + "?role=product_owner", "", http.StatusForbidden, "FORBIDDEN"},
		{http.MethodGet, roles + "?role=security_admin", "", http.StatusForbidden, "FORBIDDEN"},
		{http.MethodGet, roles + "?role=security_reviewer", "", http.StatusForbidden, "FORBIDDEN"},
		{http.MethodGet, roles + "/" + owner, "", http.StatusNotFound, "ROLE_ASSIGNMENT_NOT_FOUND"},
		{http.MethodGet, roles + "/" + last(security.header.Get("Location")), "", http.StatusNotFound, "ROLE_ASSIGNMENT_NOT_FOUND"},
		{http.MethodGet, roles + "/" + last(gone), "", http.StatusNotFound, "ROLE_ASSIGNMENT_NOT_FOUND"},
		{http.MethodGet, roles + "/" + last(elsewhere), "", http.StatusNotFound, "ROLE_ASSIGNMENT_NOT_FOUND"},
		// What the contract does not let through is refused before anybody
		// is asked, and leaves no record.
		{http.MethodGet, roles + "?role=superuser", "", http.StatusBadRequest, "REQUEST_INVALID"},
	})
	s.expect("an auditor lists no role, its own included", "ada", []exchange{
		{http.MethodGet, roles + "?role=internal_auditor", "", http.StatusForbidden, "FORBIDDEN"},
		{http.MethodGet, roles + "/" + held[0], "", http.StatusNotFound, "ROLE_ASSIGNMENT_NOT_FOUND"},
	})
	s.expect("a stranger is told of no product", "mallory", []exchange{
		{http.MethodGet, roles + "?role=internal_auditor", "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	})
	record := func(subject, resource string) audited {
		return audited{subject, "product_role.read", "DENIED", "DENY-03", cargopai, "RoleAssignment", resource}
	}
	want := map[audited]int{
		record("platform-admin", ""):                                    6,
		record("platform-admin", owner):                                 1,
		record("platform-admin", last(security.header.Get("Location"))): 1,
		record("ada", ""):                                               1,
		record("ada", held[0]):                                          1,
		record("mallory", ""):                                           1,
	}
	got := map[audited]int{}
	for _, r := range s.audit()[before:] {
		got[r]++
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("the refusals are on record as\n %v\nwant\n %v", got, want)
	}
}

// A grant does not wait for the roles of whoever it names. A revocation
// holds the rows of every owner of the product while it decides. A grant
// that is made meanwhile reads what is committed and is answered, where one
// that asked for a lock on those rows would wait, and could wait for a
// revocation that waits for the role of its caller.
func TestAGrantDoesNotWaitForTheRolesOfWhoeverItNames(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "alice")

	holder := pgtest.Connect(t, s.database.Migrator)
	tx, err := holder.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	// The owners of the product, held as a revocation holds them.
	if _, err := tx.Exec(t.Context(), `
		SELECT role_assignment_id FROM productgovernance.product_role_assignment
		WHERE product_id = $1 AND role = 'product_owner' AND revoked_at IS NULL
		ORDER BY role_assignment_id FOR UPDATE`, cargopai); err != nil {
		t.Fatalf("hold the owners of the product: %v", err)
	}

	for _, c := range []struct {
		subject, role string
		status        int
		code          string
	}{
		{"alice", "internal_auditor", http.StatusForbidden, "AUDITOR_NOT_INDEPENDENT"},
		{"ada", "internal_auditor", http.StatusCreated, ""},
		{"ada", "product_owner", http.StatusForbidden, "AUDITOR_NOT_INDEPENDENT"},
	} {
		answered := make(chan answer, 1)
		go func() { answered <- s.grantRole(cargopai, "platform-admin", c.subject, c.role) }()
		select {
		case got := <-answered:
			if got.status != c.status || got.code() != c.code {
				t.Errorf("%s is made %s while the owners are held: %d %v, want %d %q", c.subject, c.role, got.status, got.body, c.status, c.code)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the grant of %s to %s waits for the owners that another transaction holds", c.role, c.subject)
		}
	}
}

// Whoever holds nothing in a product is told of its auditors what it is told
// of a product that is missing, whatever it asks: to appoint one, to dismiss
// one, to list them or to read the assignment of one. What it asked of the
// product that is there is on record, once each, and what it asked of the
// one that is not left nothing.
func TestWhoHoldsNothingInAProductIsToldOfItsAuditorsWhatItIsToldOfAProductThatIsMissing(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "alice")
	location := s.auditorOf(cargopai, "ada")
	const missing = "3e1c6a9d-74f2-4b0a-98c3-b2e5f4a6c7d8"
	before := len(s.audit())

	asks := map[string]func(product string) answer{
		"to appoint an auditor": func(product string) answer {
			return s.grantRole(product, "mallory", "mallory", "internal_auditor")
		},
		"to dismiss the auditor": func(product string) answer {
			return s.call(http.MethodDelete, roleAssignmentsOf(product)+"/"+last(location), "mallory", "")
		},
		"for the auditors": func(product string) answer {
			return s.call(http.MethodGet, roleAssignmentsOf(product)+"?role=internal_auditor", "mallory", "")
		},
		"for the assignment of the auditor": func(product string) answer {
			return s.call(http.MethodGet, roleAssignmentsOf(product)+"/"+last(location), "mallory", "")
		},
	}
	for name, ask := range asks {
		withheld, absent := ask(cargopai), ask(missing)
		if withheld.status != http.StatusNotFound || withheld.code() != "PRODUCT_NOT_FOUND" || !sameAnswer(withheld, absent) {
			t.Errorf("a stranger asks %s: %d %v of the product that is there and %d %v of one that is not, want 404 PRODUCT_NOT_FOUND of both",
				name, withheld.status, withheld.body, absent.status, absent.body)
		}
	}
	if s.call(http.MethodGet, auditPath(cargopai), "ada", "").status != http.StatusOK {
		t.Error("the auditor does not read any more")
	}

	record := func(action, resource string) audited {
		return audited{"mallory", action, "DENIED", "DENY-03", cargopai, "RoleAssignment", resource}
	}
	want := map[audited]int{
		record("product_role.grant", ""):              1,
		record("product_role.revoke", last(location)): 1,
		record("product_role.read", ""):               1,
		record("product_role.read", last(location)):   1,
	}
	got := map[audited]int{}
	for _, r := range s.audit()[before:] {
		got[r]++
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("what the stranger asked is on record as\n %v\nwant\n %v", got, want)
	}
}
