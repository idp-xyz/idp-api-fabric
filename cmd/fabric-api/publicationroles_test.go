package main

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
)

// The roles a release and its publication are done under are granted like
// the others. A publisher is a member of a workspace, named by an owner of
// the product. Who blocks access to what is published and who clears a block
// hold a role in the product, and only an owner names them: the platform
// administrator names owners, as before, and nobody else. Neither security
// role makes its holder an owner.
func TestTheRolesOfPublicationAreGrantedByAnOwnerOfTheProduct(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "owner")
	workspace := s.createWorkspace(product, "owner", "engineering")
	s.join(product, workspace, "owner", "publisher", "internal_publisher")
	roles := "/v1/products/" + product + "/role-assignments"
	memberships := "/v1/products/" + product + "/workspaces/" + workspace + "/memberships"
	grant := func(role, subject string) string {
		return fmt.Sprintf(`{"role":%q,"principal":{"issuer":%q,"subject":%q}}`, role, s.issuer.URL(), subject)
	}

	s.expect("the administrator names owners and no security role", "platform-admin", []exchange{
		{http.MethodPost, roles, grant("security_admin", "security"), http.StatusForbidden, "FORBIDDEN"},
		{http.MethodPost, roles, grant("security_reviewer", "clearer"), http.StatusForbidden, "FORBIDDEN"},
		{http.MethodPost, roles, grant("product_owner", "second"), http.StatusCreated, ""},
	})
	s.expect("an owner names who blocks and who clears", "owner", []exchange{
		{http.MethodPost, roles, grant("security_admin", "security"), http.StatusCreated, ""},
		{http.MethodPost, roles, grant("security_reviewer", "clearer"), http.StatusCreated, ""},
		{http.MethodPost, roles, grant("release_manager", "nobody"), http.StatusBadRequest, "REQUEST_INVALID"},
	})
	s.expect("a security role is not the role of an owner", "security", []exchange{
		{http.MethodPost, roles, grant("security_reviewer", "friend"), http.StatusForbidden, "FORBIDDEN"},
		{http.MethodGet, roles, "", http.StatusForbidden, "FORBIDDEN"},
		{http.MethodPost, memberships, s.membership("friend", "internal_publisher"), http.StatusForbidden, "FORBIDDEN"},
	})
	s.expect("a publisher manages no member and no role", "publisher", []exchange{
		{http.MethodPost, memberships, s.membership("friend", "internal_publisher"), http.StatusForbidden, "FORBIDDEN"},
		{http.MethodGet, roles, "", http.StatusForbidden, "FORBIDDEN"},
	})

	held := func(path, member string) []string {
		t.Helper()
		items, next := s.page(path, "owner")
		if next != "" {
			t.Fatalf("%s: the list has more than one page", path)
		}
		var said []string
		for _, item := range items {
			principal, _ := item[member].(map[string]any)
			said = append(said, fmt.Sprintf("%v:%v", principal["subject"], item["role"]))
		}
		slices.Sort(said)
		return said
	}
	if got, want := held(roles, "principal"), []string{
		"clearer:security_reviewer", "owner:product_owner", "second:product_owner", "security:security_admin",
	}; !slices.Equal(got, want) {
		t.Errorf("the roles of the product are %v, want %v", got, want)
	}
	if got, want := held(memberships, "member"), []string{"publisher:internal_publisher"}; !slices.Equal(got, want) {
		t.Errorf("the members of the workspace are %v, want %v", got, want)
	}

	// What the administrator was refused is on record as a refusal, and the
	// two grants of a security role under the rule of the owners.
	refused := occurrences(s.recorded(func(row audited) bool {
		return row.Subject == "platform-admin" && row.Action == "product_role.grant" && row.Outcome != "SUCCEEDED"
	}))
	granted := occurrences(s.recorded(func(row audited) bool {
		return row.Subject == "owner" && row.Action == "product_role.grant" && row.Outcome == "SUCCEEDED" && row.Rule == "AC-10"
	}))
	if refused != 2 || granted != 2 {
		t.Errorf("%d refusals of the administrator and %d grants of the owner are on record, want 2 of each: %v",
			refused, granted, s.recorded(func(row audited) bool { return row.Action == "product_role.grant" }))
	}
}
