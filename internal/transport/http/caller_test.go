package http

import (
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

const (
	// callerPath is where the caller reads who it is, and standingPath where
	// it reads what it holds in the product.
	callerPath   = "/v1/caller"
	standingPath = "/v1/products/" + productID + "/caller"
)

// who the token of a request is about is answered as the contract declares:
// the principal and whether it administers the platform, and nothing else.
var callerBodies = map[string]map[string]any{
	alice.Subject: {
		"principal":              map[string]any{"issuer": issuer, "subject": alice.Subject},
		"platform_administrator": false,
	},
	admin.Subject: {
		"principal":              map[string]any{"issuer": issuer, "subject": admin.Subject},
		"platform_administrator": true,
	},
}

// What the caller holds is answered without who granted it, when, and under
// which identifier: the roles of the product, and for each workspace the roles
// in it.
var callerInProductBody = map[string]any{
	"product_id": productID,
	"roles":      []any{"product_owner"},
	"workspaces": []any{map[string]any{"workspace_id": workspaceID, "roles": []any{"workspace_maintainer"}}},
}

// The caller is told who the token of its request is about, as the use case
// says it, and the use case is asked about that principal and no other.
func TestGetCallerAnswersWhoTheTokenIsAbout(t *testing.T) {
	for _, c := range []struct {
		token     string
		principal domain.Principal
	}{
		{alice.Subject, domain.Principal(alice)},
		{admin.Subject, domain.Principal(admin)},
	} {
		t.Run(c.token, func(t *testing.T) {
			s := newService(t)

			response := s.do(t, call{Method: http.MethodGet, Target: callerPath, Token: c.token})
			if response.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control %q, want no-store", got)
			}
			if got := response.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type %q, want application/json", got)
			}
			// Nothing is changed from what it says, so there is no version.
			if got := response.Header().Get("ETag"); got != "" {
				t.Errorf("ETag %q, want none", got)
			}
			if got, want := members(t, response), callerBodies[c.token]; !reflect.DeepEqual(got, want) {
				t.Errorf("body\n %v\nwant\n %v", got, want)
			}
			if want := []application.GetCaller{{Caller: c.principal}}; !slices.Equal(s.governance.callersRead, want) || s.governance.calls() != 1 {
				t.Errorf("the use case was asked %+v of %d calls, want %+v alone", s.governance.callersRead, s.governance.calls(), want)
			}
		})
	}
}

// The caller is told about itself and nobody else: the operations take no
// parameter and no body, and a request that sends one is refused where the
// contract is checked, after it is known who sends it. What else a request
// carries is not who the caller is.
func TestNothingTheRequestSaysChangesWhoTheCallerIs(t *testing.T) {
	const (
		undeclared = "The request has a query parameter the operation does not declare."
		bodiless   = "The request does not match the contract."
	)
	for _, c := range []struct {
		name   string
		call   call
		detail string
	}{
		{"a subject in the query of who the caller is", call{Method: http.MethodGet, Target: callerPath + "?subject=bob"}, undeclared},
		{"an issuer and a subject in the query", call{Method: http.MethodGet, Target: callerPath + "?issuer=" + issuer + "&subject=bob"}, undeclared},
		{"a principal without a value", call{Method: http.MethodGet, Target: callerPath + "?principal"}, undeclared},
		{"a body of who the caller is", call{Method: http.MethodGet, Target: callerPath, Body: `{"subject":"bob"}`}, bodiless},
		{"a subject in the query of what is held", call{Method: http.MethodGet, Target: standingPath + "?subject=bob"}, undeclared},
		{"an issuer and a subject in the query of what is held", call{Method: http.MethodGet, Target: standingPath + "?issuer=" + issuer + "&subject=bob"}, undeclared},
		{"a body of what is held", call{Method: http.MethodGet, Target: standingPath, Body: `{"subject":"bob"}`}, bodiless},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newService(t)

			// Who sends the request is asked before what it says.
			wantProblem(t, s.do(t, c.call), http.StatusUnauthorized, CodeCredentialsMissing, "")
			c.call.Token = "mallory"
			wantProblem(t, s.do(t, c.call), http.StatusUnauthorized, CodeTokenInvalid, "")

			c.call.Token = alice.Subject
			response := s.do(t, c.call)
			wantProblem(t, response, http.StatusBadRequest, CodeRequestInvalid, c.detail)
			if strings.Contains(response.Body.String(), "bob") || strings.Contains(s.log.String(), "bob") {
				t.Errorf("what was sent was repeated in the answer or in the log:\n%s\n%s", response.Body, s.log)
			}
			if s.governance.calls() != 0 {
				t.Errorf("a request that names a principal reached a use case %d times", s.governance.calls())
			}
		})
	}

	t.Run("what else the request carries", func(t *testing.T) {
		s := newService(t)
		elsewhere := map[string]string{
			"X-Forwarded-User": "bob", "X-Subject": "bob", "X-Remote-User": "bob", "X-Principal": "bob",
			"X-Issuer": "https://other.example", "Forwarded": "for=bob", "Referer": "https://issuer.example/bob",
		}
		for _, target := range []string{callerPath, standingPath} {
			if response := s.do(t, call{Method: http.MethodGet, Target: target, Token: alice.Subject, Header: elsewhere}); response.Code != http.StatusOK {
				t.Fatalf("GET %s: status %d, want 200: %s", target, response.Code, response.Body)
			}
		}
		if got := s.governance.callersRead; len(got) != 1 || got[0].Caller != domain.Principal(alice) {
			t.Errorf("who the caller is was asked of %+v, want alice", got)
		}
		if got := s.governance.standingsRead; len(got) != 1 || got[0].Caller != domain.Principal(alice) || got[0].ProductID != productID {
			t.Errorf("what is held was asked of %+v, want alice in the product of the path", got)
		}
	})
}

// What the caller holds is answered as the use case says it, in its order: the
// roles of the product, and for each workspace its roles in it. Nothing else
// is shown of what is held, who granted it, or when; and nothing held is a list
// with nothing in it, not a missing one.
func TestGetCallerInProductAnswersWhatTheContractDeclaresAndNothingMore(t *testing.T) {
	t.Run("what the fixtures hold", func(t *testing.T) {
		s := newService(t)

		response := s.do(t, call{Method: http.MethodGet, Target: standingPath, Token: alice.Subject})
		if response.Code != http.StatusOK {
			t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
		}
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control %q, want no-store", got)
		}
		for _, header := range []string{"ETag", "Location"} {
			if got := response.Header().Get(header); got != "" {
				t.Errorf("%s %q, want none: nothing is changed from what it says", header, got)
			}
		}
		if got := members(t, response); !reflect.DeepEqual(got, callerInProductBody) {
			t.Errorf("body\n %v\nwant\n %v", got, callerInProductBody)
		}
		if len(s.governance.standingsRead) != 1 || s.governance.calls() != 1 {
			t.Fatalf("the use case was asked %d times of %d calls, want once and no other", len(s.governance.standingsRead), s.governance.calls())
		}
		query := s.governance.standingsRead[0]
		if !uuid.MatchString(query.CorrelationID) {
			t.Errorf("correlation identifier %q, want one the server generated", query.CorrelationID)
		}
		query.CorrelationID = ""
		if want := (application.GetCallerInProduct{Caller: domain.Principal(alice), ProductID: productID}); query != want {
			t.Errorf("the use case was asked %+v, want %+v", query, want)
		}
	})

	t.Run("what the use case says, in the order it says it", func(t *testing.T) {
		s := newService(t)
		s.governance.held = &application.CallerStanding{
			ProductID: productID,
			Roles:     []domain.Role{domain.RoleSecurityReviewer, domain.RoleInternalAuditor},
			Workspaces: []domain.HeldInWorkspace{
				{WorkspaceID: workspaceID, Roles: []domain.Role{domain.RoleInternalReviewer, domain.RoleInternalAuthor}},
				{WorkspaceID: "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d", Roles: []domain.Role{domain.RoleWorkspaceMaintainer}},
			},
		}

		want := map[string]any{
			"product_id": productID,
			"roles":      []any{"security_reviewer", "internal_auditor"},
			"workspaces": []any{
				map[string]any{"workspace_id": workspaceID, "roles": []any{"internal_reviewer", "internal_author"}},
				map[string]any{"workspace_id": "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d", "roles": []any{"workspace_maintainer"}},
			},
		}
		response := s.do(t, call{Method: http.MethodGet, Target: standingPath, Token: alice.Subject})
		if got := members(t, response); response.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
			t.Errorf("%d\n %v\nwant\n %v", response.Code, got, want)
		}
	})

	// What the caller is a member of is not a page of a list: all of it is
	// told, however many workspaces that is, and the contract, which checks the
	// answer here, takes as many.
	t.Run("more workspaces than a page holds", func(t *testing.T) {
		const workspaces = 120
		s := newService(t)
		held := application.CallerStanding{ProductID: productID}
		want := make([]any, 0, workspaces)
		for number := range workspaces {
			id := fmt.Sprintf("00000000-0000-4000-8000-%012d", number+1)
			held.Workspaces = append(held.Workspaces, domain.HeldInWorkspace{WorkspaceID: domain.WorkspaceID(id), Roles: []domain.Role{domain.RoleInternalAuthor}})
			want = append(want, map[string]any{"workspace_id": id, "roles": []any{"internal_author"}})
		}
		s.governance.held = &held

		response := s.do(t, call{Method: http.MethodGet, Target: standingPath, Token: alice.Subject})
		got := members(t, response)
		if response.Code != http.StatusOK || !reflect.DeepEqual(got["workspaces"], want) || len(got) != 3 {
			listed, _ := got["workspaces"].([]any)
			t.Errorf("%d with %d of %d workspaces and the members %v, want 200 with all of them and no token for a next page",
				response.Code, len(listed), workspaces, slices.Sorted(maps.Keys(got)))
		}
	})

	for name, held := range map[string]application.CallerStanding{
		"a use case that answers with no lists at all": {ProductID: productID},
		"a use case that answers with empty ones":      {ProductID: productID, Roles: []domain.Role{}, Workspaces: []domain.HeldInWorkspace{}},
	} {
		t.Run("nothing held, "+name, func(t *testing.T) {
			s := newService(t)
			s.governance.held = &held

			response := s.do(t, call{Method: http.MethodGet, Target: standingPath, Token: admin.Subject})
			want := map[string]any{"product_id": productID, "roles": []any{}, "workspaces": []any{}}
			if got := members(t, response); response.Code != http.StatusOK || !reflect.DeepEqual(got, want) {
				t.Errorf("%d\n %v\nwant\n %v", response.Code, got, want)
			}
			if body := response.Body.String(); strings.Contains(body, "null") || !strings.Contains(body, `"roles":[]`) || !strings.Contains(body, `"workspaces":[]`) {
				t.Errorf("nothing held is told as %s, want two empty lists and no null", body)
			}
		})
	}
}

// A value the contract does not declare is not sent: a caller would branch on
// a role it was never told of, or take a workspace for one it holds something
// in. Silently leaving it out would be as wrong, so the request is failed and
// the log says why.
func TestARoleTheContractDoesNotDeclareIsNotSentAsAnIdentity(t *testing.T) {
	cases := map[string]struct {
		held   application.CallerStanding
		logged string
	}{
		"a role of the product": {application.CallerStanding{ProductID: productID, Roles: []domain.Role{"ARCHIVED"}},
			"which the contract does not declare"},
		"one among others it declares": {application.CallerStanding{ProductID: productID, Roles: []domain.Role{domain.RoleProductOwner, "ARCHIVED"}},
			"which the contract does not declare"},
		"a role of a workspace": {application.CallerStanding{ProductID: productID,
			Workspaces: []domain.HeldInWorkspace{{WorkspaceID: workspaceID, Roles: []domain.Role{"ARCHIVED"}}}},
			"which the contract does not declare"},
		"a role of the product held in a workspace": {application.CallerStanding{ProductID: productID,
			Workspaces: []domain.HeldInWorkspace{{WorkspaceID: workspaceID, Roles: []domain.Role{domain.RoleProductOwner}}}},
			"which the contract does not declare"},
		"a workspace of the product held as a role of the product": {application.CallerStanding{ProductID: productID,
			Roles: []domain.Role{domain.RoleWorkspaceMaintainer}},
			"which the contract does not declare"},
		"a workspace in which nothing is held": {application.CallerStanding{ProductID: productID,
			Workspaces: []domain.HeldInWorkspace{{WorkspaceID: workspaceID}}},
			"holds nothing in a workspace"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newService(t)
			s.governance.held = &c.held

			response := s.do(t, call{Method: http.MethodGet, Target: standingPath, Token: alice.Subject})
			wantProblem(t, response, http.StatusInternalServerError, CodeInternalError, "")
			if body := response.Body.String(); strings.Contains(body, "ARCHIVED") || strings.Contains(body, "product_owner") || strings.Contains(body, "workspace_maintainer") {
				t.Errorf("the answer shows what it holds: %s", body)
			}
			if !strings.Contains(s.log.String(), c.logged) {
				t.Errorf("the log does not say what went wrong (%q):\n%s", c.logged, s.log)
			}
		})
	}
}

// The caller is read, and nothing is written or removed: the two paths take
// GET and, as every GET, HEAD.
func TestTheCallerIsReadWithGETAndHEADOnly(t *testing.T) {
	for _, target := range []string{callerPath, standingPath} {
		t.Run(target, func(t *testing.T) {
			s := newService(t)

			for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
				response := s.do(t, call{Method: method, Target: target, Token: alice.Subject})
				wantProblem(t, response, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "")
				if got := response.Header().Get("Allow"); got != "GET, HEAD" {
					t.Errorf("%s: Allow %q, want GET, HEAD", method, got)
				}
			}
			if s.governance.calls() != 0 {
				t.Errorf("a method the contract does not route reached a use case %d times", s.governance.calls())
			}
			if head := s.do(t, call{Method: http.MethodHead, Target: target, Token: alice.Subject}); head.Code != http.StatusOK || s.governance.calls() != 1 {
				t.Errorf("HEAD: status %d and %d calls, want 200 and 1", head.Code, s.governance.calls())
			}
		})
	}
}

// operationOf is the operation of the contract at the path, under GET.
func operationOf(t *testing.T, path, id string) *openapi3.Operation {
	t.Helper()

	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	item := contract.Paths.Find(path)
	if item == nil || item.Get == nil || item.Get.OperationID != id {
		t.Fatalf("the contract has no %s as GET %s", id, path)
	}
	if len(item.Operations()) != 1 {
		t.Errorf("the contract takes %d methods at %s, want GET alone", len(item.Operations()), path)
	}
	return item.Get
}

// The roles the contract declares for what the caller holds are the roles of
// the model, of a product and of a workspace, and the answer names them
// through that declaration and no other.
func TestTheContractDeclaresTheCallerInTheRolesTheModelKnows(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	schemas := contract.Components.Schemas

	for _, roles := range []struct {
		of     string
		schema string
		model  []domain.Role
		in     string
	}{
		{"a product", "ProductRole", domain.ProductRoles(), "CallerInProduct"},
		{"a workspace", "WorkspaceRole", domain.WorkspaceRoles(), "CallerInWorkspace"},
	} {
		if schemas[roles.schema] == nil || schemas[roles.in] == nil || schemas[roles.in].Value.Properties["roles"] == nil {
			t.Errorf("the contract declares no %s, or no roles of %s", roles.schema, roles.in)
			continue
		}
		var declared, modelled []string
		for _, value := range schemas[roles.schema].Value.Enum {
			role, _ := value.(string)
			declared = append(declared, role)
		}
		for _, role := range roles.model {
			modelled = append(modelled, string(role))
		}
		slices.Sort(declared)
		slices.Sort(modelled)
		if len(modelled) == 0 || !slices.Equal(declared, modelled) {
			t.Errorf("the contract declares the roles of %s %v, the model %v", roles.of, declared, modelled)
		}
		list := schemas[roles.in].Value.Properties["roles"].Value
		if !list.Type.Is("array") || list.Items == nil || list.Items.Ref != "#/components/schemas/"+roles.schema {
			t.Errorf("the roles of %s are declared as %v of %+v, want an array of %s", roles.in, list.Type, list.Items, roles.schema)
		}
	}

	// What else of the answer is told by what the contract already declares.
	for _, c := range []struct{ schema, member, ref string }{
		{"Caller", "principal", "#/components/schemas/Principal"},
		{"CallerInProduct", "product_id", "#/components/schemas/Identifier"},
		{"CallerInWorkspace", "workspace_id", "#/components/schemas/Identifier"},
	} {
		if schemas[c.schema] == nil || schemas[c.schema].Value.Properties[c.member] == nil {
			t.Errorf("the contract declares no %s of %s", c.member, c.schema)
		} else if got := schemas[c.schema].Value.Properties[c.member].Ref; got != c.ref {
			t.Errorf("the %s of %s is declared by %q, want %q", c.member, c.schema, got, c.ref)
		}
	}
	items := schemas["CallerInProduct"].Value.Properties["workspaces"]
	if items == nil || items.Value.Items == nil || items.Value.Items.Ref != "#/components/schemas/CallerInWorkspace" || !items.Value.Type.Is("array") {
		t.Errorf("the workspaces of CallerInProduct are declared as %+v, want an array of CallerInWorkspace", items)
	}
}

// Each operation declares every way it is answered, and no other: a page of
// what the caller is, a request the contract refuses, a caller who is not told
// who it is, a product that is not there (for the one that names a product),
// and a failure of the service. Every status is declared by name: the default
// answers any status the contract does not name with the same problem, so a
// status that is left out changes nothing in what a client is told, apart from
// what the generated code knows of it. What the caller is told of itself is
// private to it and says so on every answer: no cache keeps it, whichever
// header a change would let through.
func TestTheContractDeclaresHowTheCallerIsAnswered(t *testing.T) {
	const problem, unauthenticated = "#/components/responses/Problem", "#/components/responses/Unauthenticated"

	for _, c := range []struct {
		path, id, schema string
		statuses         map[string]string
		parameters       []string
	}{
		{callerPath, "getCaller", "#/components/schemas/Caller",
			map[string]string{"400": problem, "401": unauthenticated, "503": problem, "default": problem}, nil},
		{"/v1/products/{product_id}/caller", "getCallerInProduct", "#/components/schemas/CallerInProduct",
			map[string]string{"400": problem, "401": unauthenticated, "404": problem, "503": problem, "default": problem},
			[]string{"#/components/parameters/ProductId"}},
	} {
		t.Run(c.id, func(t *testing.T) {
			operation := operationOf(t, c.path, c.id)

			answers := operation.Responses.Map()
			for status, want := range c.statuses {
				got := "nothing"
				if declared := answers[status]; declared != nil {
					got = declared.Ref
				}
				if got != want {
					t.Errorf("the contract declares for %s of %s %s, want %s", status, c.id, got, want)
				}
			}
			want := append(slices.Sorted(maps.Keys(c.statuses)), "200")
			slices.Sort(want)
			if got := slices.Sorted(maps.Keys(answers)); !slices.Equal(got, want) {
				t.Errorf("the contract declares the statuses %v for %s, want %v", got, c.id, want)
			}

			// What the caller is told: JSON, of the schema of the operation, and
			// never cached. Nothing is changed from it, so no version and no place.
			told := answers["200"]
			if told == nil || told.Value == nil {
				t.Fatalf("the contract declares no answer of %s", c.id)
			}
			if media := told.Value.Content.Get("application/json"); len(told.Value.Content) != 1 || media == nil || media.Schema == nil || media.Schema.Ref != c.schema {
				t.Errorf("the contract declares the answer of %s as %v, want %s as JSON and nothing else", c.id, slices.Sorted(maps.Keys(told.Value.Content)), c.schema)
			}
			header := told.Value.Headers["Cache-Control"]
			if header == nil || header.Value == nil || header.Value.Schema == nil || header.Value.Schema.Value == nil {
				t.Fatalf("the contract declares no Cache-Control on the answer of %s", c.id)
			}
			if !header.Value.Required {
				t.Errorf("the contract does not require Cache-Control on the answer of %s", c.id)
			}
			// Only no-store, whether the contract says it as a constant or as the
			// one member of an enumeration. The validator that checks the answers
			// of the tests does not look at a constant, so what the contract
			// declares is read here and not asked of it.
			schema := header.Value.Schema.Value
			if onlyNoStore := schema.Const == "no-store" && len(schema.Enum) == 0 ||
				schema.Const == nil && len(schema.Enum) == 1 && schema.Enum[0] == "no-store"; !onlyNoStore || !schema.Type.Is("string") {
				t.Errorf("the contract takes Cache-Control as %v (const %v, enum %v) on the answer of %s, want no-store and no other value",
					schema.Type, schema.Const, schema.Enum, c.id)
			}
			if len(told.Value.Headers) != 1 {
				t.Errorf("the contract declares the headers %v on the answer of %s, want Cache-Control alone", slices.Sorted(maps.Keys(told.Value.Headers)), c.id)
			}

			// It takes nothing the caller could name somebody else with: no body,
			// and no parameter but the product of the path.
			var parameters []string
			for _, parameter := range operation.Parameters {
				parameters = append(parameters, parameter.Ref)
			}
			if !slices.Equal(parameters, c.parameters) {
				t.Errorf("%s takes the parameters %v, want %v", c.id, parameters, c.parameters)
			}
			if operation.RequestBody != nil {
				t.Errorf("%s takes a request body", c.id)
			}
			// It is private unless it says otherwise, and it does not.
			if operation.Security != nil {
				t.Errorf("%s declares a security requirement of its own: %v", c.id, *operation.Security)
			}
		})
	}

	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	bearerAlone := false
	if len(contract.Security) == 1 && len(contract.Security[0]) == 1 {
		_, bearerAlone = contract.Security[0]["bearerToken"]
	}
	if !bearerAlone {
		t.Errorf("the contract requires %v of every operation that says nothing, want a bearer token alone", contract.Security)
	}
}

// What the service answers about the caller satisfies the schemas the contract
// declares, and those schemas are as closed as the answers: they take no member
// that is not declared, and no list that is not as the answer has it. The
// answers are of the caller, so one that carried who granted what, an
// identifier of a membership, or a name of the caller is one the contract does
// not take.
func TestTheContractStatesTheCallerAsItIsAnswered(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	s := newService(t)

	read := func(target, token string) map[string]any {
		t.Helper()

		response := s.do(t, call{Method: http.MethodGet, Target: target, Token: token})
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200: %s", target, response.Code, response.Body)
		}
		return members(t, response)
	}
	s.governance.held = &application.CallerStanding{
		ProductID: productID,
		Roles:     []domain.Role{domain.RoleInternalAuditor, domain.RoleProductOwner},
		Workspaces: []domain.HeldInWorkspace{
			{WorkspaceID: workspaceID, Roles: []domain.Role{domain.RoleInternalAuthor, domain.RoleWorkspaceMaintainer}},
		},
	}

	for name, c := range map[string]struct {
		schema  string
		body    func() map[string]any
		refused map[string]func(map[string]any)
	}{
		"who the caller is": {"Caller", func() map[string]any { return read(callerPath, admin.Subject) }, map[string]func(map[string]any){
			"a member the schema does not declare": func(body map[string]any) { body["display_name"] = "Alice" },
			"no principal":                         func(body map[string]any) { delete(body, "principal") },
			"no word on the platform":              func(body map[string]any) { delete(body, "platform_administrator") },
			"a word on the platform that is none":  func(body map[string]any) { body["platform_administrator"] = "yes" },
			"a principal that says more": func(body map[string]any) {
				body["principal"].(map[string]any)["email"] = "alice@example.com"
			},
			"a principal that is a name":        func(body map[string]any) { body["principal"] = "alice" },
			"a principal without a subject":     func(body map[string]any) { delete(body["principal"].(map[string]any), "subject") },
			"a principal without an issuer":     func(body map[string]any) { delete(body["principal"].(map[string]any), "issuer") },
			"a subject that has a control sign": func(body map[string]any) { body["principal"].(map[string]any)["subject"] = "ali\nce" },
		}},
		"what the caller holds in a product": {"CallerInProduct", func() map[string]any { return read(standingPath, alice.Subject) }, map[string]func(map[string]any){
			"a member the schema does not declare": func(body map[string]any) { body["granted_by"] = map[string]any{"issuer": issuer, "subject": "alice"} },
			"no product":                           func(body map[string]any) { delete(body, "product_id") },
			"a product that is no identifier":      func(body map[string]any) { body["product_id"] = "cargopai" },
			"no roles of the product":              func(body map[string]any) { delete(body, "roles") },
			"roles that are none":                  func(body map[string]any) { body["roles"] = nil },
			"a role nobody defined":                func(body map[string]any) { body["roles"] = []any{"superuser"} },
			"a role of a workspace as one of the product": func(body map[string]any) {
				body["roles"] = []any{"workspace_maintainer"}
			},
			"a role twice":             func(body map[string]any) { body["roles"] = []any{"product_owner", "product_owner"} },
			"no workspaces":            func(body map[string]any) { delete(body, "workspaces") },
			"workspaces that are none": func(body map[string]any) { body["workspaces"] = nil },
			"a workspace that says more": func(body map[string]any) {
				body["workspaces"].([]any)[0].(map[string]any)["membership_id"] = membershipID
			},
			"a workspace without an identifier": func(body map[string]any) { delete(body["workspaces"].([]any)[0].(map[string]any), "workspace_id") },
			"a workspace that is no identifier": func(body map[string]any) {
				body["workspaces"].([]any)[0].(map[string]any)["workspace_id"] = "engineering"
			},
			"a workspace without roles": func(body map[string]any) { delete(body["workspaces"].([]any)[0].(map[string]any), "roles") },
			"a workspace in which nothing is held": func(body map[string]any) {
				body["workspaces"].([]any)[0].(map[string]any)["roles"] = []any{}
			},
			"a role of the product held in a workspace": func(body map[string]any) {
				body["workspaces"].([]any)[0].(map[string]any)["roles"] = []any{"product_owner"}
			},
			"a role of a workspace nobody defined": func(body map[string]any) {
				body["workspaces"].([]any)[0].(map[string]any)["roles"] = []any{"superuser"}
			},
			"a role of a workspace twice": func(body map[string]any) {
				body["workspaces"].([]any)[0].(map[string]any)["roles"] = []any{"internal_author", "internal_author"}
			},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			declared := contract.Components.Schemas[c.schema]
			if declared == nil {
				t.Fatalf("the contract declares no %s", c.schema)
			}
			if err := declared.Value.VisitJSON(c.body()); err != nil {
				t.Fatalf("the contract refuses what the service answers: %v\n%v", err, c.body())
			}
			for what, change := range c.refused {
				body := c.body()
				change(body)
				if err := declared.Value.VisitJSON(body); err == nil {
					t.Errorf("the contract accepts an answer with %s: %v", what, body)
				}
			}
		})
	}

	// The members each schema requires are those of what it answers, and the
	// roles of a product and of a workspace are listed in none of the others.
	required := map[string][]string{
		"Caller":            {"platform_administrator", "principal"},
		"CallerInProduct":   {"product_id", "roles", "workspaces"},
		"CallerInWorkspace": {"roles", "workspace_id"},
	}
	for schema, want := range required {
		declared := contract.Components.Schemas[schema]
		if declared == nil {
			t.Errorf("the contract declares no %s", schema)
			continue
		}
		if got := slices.Sorted(slices.Values(declared.Value.Required)); !slices.Equal(got, want) {
			t.Errorf("the contract requires the members %v of %s, want %v", got, schema, want)
		}
		if got := slices.Sorted(maps.Keys(declared.Value.Properties)); !slices.Equal(got, want) {
			t.Errorf("the contract declares the members %v of %s, want %v", got, schema, want)
		}
	}

	// What the caller is a member of is told whole, however many workspaces
	// that is: no page is the bound of it, and the contract bounds it by nothing.
	if workspaces := contract.Components.Schemas["CallerInProduct"].Value.Properties["workspaces"]; workspaces == nil || workspaces.Value.MaxItems != nil {
		t.Errorf("the contract bounds the workspaces of CallerInProduct at %+v, want them unbounded", workspaces)
	}
}
