package http

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// A list of the role assignments of a product is kept to one role by naming
// it. The role reaches the use case as it was sent, beside the size and the
// position of the page, and a list that names none asks for every role. It
// is the use case that decides who lists which: the transport hands the
// role on whoever asks.
func TestTheRoleOfAListOfRolesReachesItsUseCase(t *testing.T) {
	asked := func(t *testing.T, target, token string) application.ListProductRoles {
		t.Helper()

		s := newService(t)
		response := s.do(t, call{Method: http.MethodGet, Target: target, Token: token})
		if response.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200: %s", target, response.Code, response.Body)
		}
		if len(s.governance.rolesListed) != 1 || s.governance.calls() != 1 {
			t.Fatalf("%s: the use case of the list was asked %d times of %d calls, want once and no other",
				target, len(s.governance.rolesListed), s.governance.calls())
		}
		return s.governance.rolesListed[0]
	}

	if got := asked(t, roleAssignments, "alice"); got.Role != "" || got.Caller != domain.Principal(alice) || got.ProductID != productID {
		t.Errorf("a list that names no role asked for %+v, want every role of the product for the caller", got)
	}
	for _, role := range domain.ProductRoles() {
		for _, token := range []string{"alice", "platform-admin"} {
			if got := asked(t, roleAssignments+"?role="+string(role), token); got.Role != string(role) || got.ProductID != productID {
				t.Errorf("%s lists the role %s: the use case was asked for %+v", token, role, got)
			}
		}
	}

	// The role is no part of where a page starts: a page of the list that
	// is kept to a role is read behind a position of the list.
	behind := ports.Position{At: createdAt, ID: roleAssignmentID}
	got := asked(t, roleAssignments+"?role=internal_auditor&page_size=7&page_token="+encodePageToken(listOfRoleAssignments, behind), "platform-admin")
	if got.Role != "internal_auditor" || got.PageSize != 7 || got.After == nil || *got.After != behind || got.Caller != domain.Principal(admin) {
		t.Errorf("a page of the auditors behind a position asked for %+v behind %+v, want 7 auditors behind %+v for the administrator", got, got.After, behind)
	}
}

// A role the contract does not give a product is refused before the use
// case is asked: one nobody defined, a role of a workspace, one written in
// another case, and a parameter that names none.
func TestARoleThatIsNoneOfAProductIsRefusedBeforeTheListIsAsked(t *testing.T) {
	for name, query := range map[string]string{
		"a role nobody defined":     "role=superuser",
		"a role of a workspace":     "role=internal_author",
		"a role in another case":    "role=INTERNAL_AUDITOR",
		"a role that is empty":      "role=",
		"a role with a space in it": "role=internal_auditor%20",
		"two roles in one value":    "role=internal_auditor,product_owner",
	} {
		t.Run(name, func(t *testing.T) {
			for _, token := range []string{"alice", "platform-admin"} {
				s := newService(t)
				response := s.do(t, call{Method: http.MethodGet, Target: roleAssignments + "?" + query, Token: token})
				wantProblem(t, response, http.StatusBadRequest, CodeRequestInvalid, "The query parameter role does not match the contract.")
				if s.governance.calls() != 0 {
					t.Errorf("%s: the use cases were asked %d times, want none", token, s.governance.calls())
				}
			}
		})
	}
}

// A grant that would make somebody audit what it answers for is refused
// with a code of its own, apart from the refusal of a caller that may not
// grant at all: the caller may appoint, and not this principal. A refusal
// that could not be recorded is the same answer, and is logged.
func TestAnAuditorThatIsNotIndependentIsAnsweredWithItsOwnCode(t *testing.T) {
	const detail = "An auditor of a product neither owns it nor administers the platform."
	grant := call{Method: http.MethodPost, Target: roleAssignments, Token: "platform-admin",
		Body: strings.Replace(roleGrant, `"product_owner"`, `"internal_auditor"`, 1)}

	s := newService(t)
	s.governance.err = application.ErrAuditorNotIndependent
	response := s.do(t, grant)
	wantProblem(t, response, http.StatusForbidden, CodeAuditorNotIndependent, detail)
	if len(s.governance.rolesGranted) != 1 || s.governance.rolesGranted[0].Role != "internal_auditor" ||
		s.governance.rolesGranted[0].Caller != domain.Principal(admin) {
		t.Errorf("the use case was asked %+v, want the one appointment by the administrator", s.governance.rolesGranted)
	}
	if strings.Contains(s.log.String(), "level=ERROR") {
		t.Errorf("a refusal that was recorded is logged as a failure:\n%s", s.log)
	}
	// The code is the one the contract names for it, and none a caller
	// branches on already.
	if got := members(t, response)["error_code"]; got != "AUDITOR_NOT_INDEPENDENT" || CodeAuditorNotIndependent == CodeForbidden {
		t.Errorf("the refusal has the code %v, want AUDITOR_NOT_INDEPENDENT, which is not that of a caller that may not grant", got)
	}

	s = newService(t)
	s.governance.err = fmt.Errorf("%w; %w: the audit trail is down", application.ErrAuditorNotIndependent, application.ErrRefusalNotAudited)
	response = s.do(t, grant)
	wantProblem(t, response, http.StatusForbidden, CodeAuditorNotIndependent, detail)
	if logged := s.log.String(); !strings.Contains(logged, "level=ERROR") || !strings.Contains(logged, "a refusal could not be audited") {
		t.Errorf("the log lacks an error saying that the refusal could not be audited:\n%s", logged)
	}
	if strings.Contains(response.Body.String(), "audit trail") {
		t.Errorf("the answer tells the caller about the audit trail: %s", response.Body)
	}
}

// The contract says who appoints and dismisses an auditor: a platform
// administrator that does not own the product, and no owner of it. It
// declares the role a list is kept to as an optional parameter of the roles
// a product has, and says of each operation what an administrator and an
// owner may do with an auditor. Nothing in it still states the rule of
// before, under which an owner made an auditor, itself included.
func TestTheContractSaysThatAnAdministratorAppointsAndDismissesAnAuditor(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	const roles = "/v1/products/{product_id}/role-assignments"
	list := contract.Paths.Find(roles).Get

	role := list.Parameters.GetByInAndName("query", "role")
	if role == nil || role.Required || role.Schema == nil || role.Schema.Ref != "#/components/schemas/ProductRole" {
		t.Fatalf("the list of roles declares the role it is kept to as %+v, want an optional query parameter of the roles of a product", role)
	}
	declared := contract.Components.Schemas["ProductRole"].Value.Enum
	if len(declared) != len(domain.ProductRoles()) {
		t.Errorf("the contract gives a product the roles %v, the model %v", declared, domain.ProductRoles())
	}
	for _, role := range domain.ProductRoles() {
		found := false
		for _, value := range declared {
			found = found || value == string(role)
		}
		if !found {
			t.Errorf("the contract does not give a product the role %s", role)
		}
	}
	for _, status := range []string{"400", "403", "404"} {
		if list.Responses.Value(status) == nil {
			t.Errorf("the list of roles does not declare %s", status)
		}
	}
	if grant := contract.Paths.Find(roles).Post; grant.Responses.Value("403") == nil {
		t.Error("a grant does not declare 403, which an appointment that is not independent is answered with")
	}

	for name, c := range map[string]struct {
		text      string
		says, not []string
	}{
		"listProductRoles": {
			text: list.Description,
			says: []string{
				"lists the auditors of the product, whom it appoints and dismisses, by asking for role=internal_auditor",
				"without role, or with another role, it is refused (403 FORBIDDEN)",
				"The role is part of what is read, so every page but the last is full",
			},
			not: []string{"and a platform administrator, who names an owner and reads none"},
		},
		"grantProductRole": {
			text: contract.Paths.Find(roles).Post.Description,
			says: []string{
				"It does not grant internal_auditor, to itself or to anybody (403 FORBIDDEN)",
				"A platform administrator that does not own the product may name owners and appoint auditors, and grants neither security role (403 FORBIDDEN)",
				"An administrator that owns the product acts as an owner there, and another administrator appoints its auditors",
				"no owner of the product and no platform administrator is appointed one, and no auditor of the product is named its owner (403 AUDITOR_NOT_INDEPENDENT)",
				"an assignment that was in force before is left as it is",
			},
			not: []string{"makes itself an auditor", "may grant any of them", "and no other role"},
		},
		"getProductRole": {
			text: contract.Paths.Find(roles + "/{role_assignment_id}").Get.Description,
			says: []string{"reads the assignment of an auditor, which is its own to end, and no other"},
			not:  []string{"and a platform administrator, who names an owner and reads none"},
		},
		"revokeProductRole": {
			text: contract.Paths.Find(roles + "/{role_assignment_id}").Delete.Description,
			says: []string{
				"An owner of the product ends every assignment but that of an auditor",
				"It does not dismiss an auditor (403 FORBIDDEN)",
				"A platform administrator that does not own the product ends the assignment of an auditor, as it made it, and no other",
				"an assignment that is not in force included, is refused (403 FORBIDDEN)",
				"An auditor does not end its own assignment",
			},
			not: []string{"Only an owner of the product may", "A platform administrator names owners and removes none"},
		},
		"listAuditRecords": {
			text: contract.Paths.Find("/v1/products/{product_id}/audit-records").Get.Description,
			says: []string{"An auditor is appointed by a platform administrator (grantProductRole) from those who neither own the product nor administer the platform"},
			not:  []string{"makes itself an auditor"},
		},
		"the roles of a product": {
			text: contract.Components.Schemas["ProductRole"].Value.Description,
			says: []string{"the product roles but that of an auditor", "is appointed and dismissed by a platform administrator"},
		},
	} {
		// A description is read as one line, however the contract breaks it.
		text := strings.Join(strings.Fields(c.text), " ")
		for _, said := range c.says {
			if !strings.Contains(text, said) {
				t.Errorf("%s does not say %q:\n%s", name, said, text)
			}
		}
		for _, said := range c.not {
			if strings.Contains(text, said) {
				t.Errorf("%s still says %q:\n%s", name, said, text)
			}
		}
	}
}
