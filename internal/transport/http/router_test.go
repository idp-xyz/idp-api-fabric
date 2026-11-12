package http

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/buildinfo"
)

func TestTheAnonymousOperationsNeedNoCredentials(t *testing.T) {
	s := newService(t)

	health := s.do(t, call{Method: http.MethodGet, Target: "/healthz"})
	if health.Code != http.StatusOK {
		t.Fatalf("GET /healthz: status %d, want 200", health.Code)
	}
	if got := health.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type %q, want application/json", got)
	}
	if got, want := members(t, health), (map[string]any{"status": "ok"}); !reflect.DeepEqual(got, want) {
		t.Errorf("body %v, want %v", got, want)
	}

	version := s.do(t, call{Method: http.MethodGet, Target: "/version"})
	if version.Code != http.StatusOK {
		t.Fatalf("GET /version: status %d, want 200", version.Code)
	}
	if got := version.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", got)
	}
	want := map[string]any{"version": "v-test", "commit": "c-test", "built_at": "t-test"}
	if got := members(t, version); !reflect.DeepEqual(got, want) {
		t.Errorf("build identity %v, want %v", got, want)
	}

	// An operation declared anonymous does not look at credentials at all:
	// a token nobody accepts changes nothing.
	for _, c := range []call{
		{Method: http.MethodGet, Target: "/healthz", Token: "mallory"},
		{Method: http.MethodGet, Target: "/version", Token: "down"},
		{Method: http.MethodHead, Target: "/healthz"},
	} {
		if response := s.do(t, c); response.Code != http.StatusOK {
			t.Errorf("%s %s with the token %q: status %d, want 200", c.Method, c.Target, c.Token, response.Code)
		}
	}
}

func TestWhatTheContractDoesNotRouteIsAProblem(t *testing.T) {
	s := newService(t)

	cases := []struct {
		name   string
		call   call
		status int
		code   string
		allow  string
	}{
		{"an unknown path", call{Method: http.MethodGet, Target: "/v1/slots"}, http.StatusNotFound, CodeRouteNotFound, ""},
		{"a known path under another method", call{Method: http.MethodPost, Target: "/healthz"},
			http.StatusMethodNotAllowed, CodeMethodNotAllowed, "GET, HEAD"},
		{"a collection that cannot be read yet", call{Method: http.MethodGet, Target: "/v1/products", Token: "alice"},
			http.StatusMethodNotAllowed, CodeMethodNotAllowed, "POST"},
		{"a product that cannot be removed", call{Method: http.MethodDelete, Target: "/v1/products/" + productID, Token: "alice"},
			http.StatusMethodNotAllowed, CodeMethodNotAllowed, "GET, HEAD"},
		{"the members of a workspace that cannot be listed yet", call{Method: http.MethodGet, Target: memberships, Token: "alice"},
			http.StatusMethodNotAllowed, CodeMethodNotAllowed, "POST"},
		// A membership is granted and revoked. Nothing about it changes in
		// between.
		{"a membership that cannot be changed", call{Method: http.MethodPatch, Target: memberships + "/" + membershipID, Token: "alice"},
			http.StatusMethodNotAllowed, CodeMethodNotAllowed, "GET, HEAD, DELETE"},
		{"the owners of a product that cannot be listed yet", call{Method: http.MethodGet, Target: roleAssignments, Token: "alice"},
			http.StatusMethodNotAllowed, CodeMethodNotAllowed, "POST"},
		// A role assignment is granted and revoked, as a membership is.
		{"a role assignment that cannot be changed", call{Method: http.MethodPut, Target: roleAssignments + "/" + roleAssignmentID, Token: "alice"},
			http.StatusMethodNotAllowed, CodeMethodNotAllowed, "GET, HEAD, DELETE"},
		// The mux would redirect these to the path it cleans them to. The
		// contract routes neither, so neither is served nor redirected.
		{"a path with a dot segment", call{Method: http.MethodGet, Target: "/v1/products/../healthz"},
			http.StatusNotFound, CodeRouteNotFound, ""},
		{"a path with a doubled slash", call{Method: http.MethodGet, Target: "/v1//products/" + productID, Token: "alice"},
			http.StatusNotFound, CodeRouteNotFound, ""},
		{"a path with a trailing slash", call{Method: http.MethodGet, Target: "/healthz/"},
			http.StatusNotFound, CodeRouteNotFound, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			response := s.do(t, c.call)
			wantProblem(t, response, c.status, c.code, "")
			if got := response.Header().Get("Allow"); got != c.allow {
				t.Errorf("Allow %q, want %q", got, c.allow)
			}
		})
	}
	if s.governance.calls() != 0 {
		t.Errorf("%d requests the contract does not route reached a use case", s.governance.calls())
	}
}

const registration = `{"product_key":"cargopai","display_name":"CargoPai",` +
	`"owner_organization":{"identity_provider":"https://issuer.example","organization":"org-cargopai"},` +
	`"owners":[{"issuer":"https://issuer.example","subject":"alice"},{"issuer":"https://issuer.example","subject":"bob"}]}`

// grant is the body of a grant of a membership: bob maintains the workspace.
const grant = `{"member":{"issuer":"https://issuer.example","subject":"bob"},"role":"workspace_maintainer"}`

// roleGrant is the body of a grant of a role in a product: bob owns it.
const roleGrant = `{"principal":{"issuer":"https://issuer.example","subject":"bob"},"role":"product_owner"}`

// protected are the operations that need a verified caller: every operation
// but the two declared anonymous.
var protected = []call{
	{Method: http.MethodPost, Target: "/v1/products", Body: registration},
	{Method: http.MethodGet, Target: "/v1/products/" + productID},
	{Method: http.MethodPost, Target: "/v1/products/" + productID + "/workspaces", Body: `{"workspace_key":"api","display_name":"API"}`},
	{Method: http.MethodGet, Target: "/v1/products/" + productID + "/workspaces/" + workspaceID},
	{Method: http.MethodPost, Target: memberships, Body: grant},
	{Method: http.MethodGet, Target: memberships + "/" + membershipID},
	{Method: http.MethodDelete, Target: memberships + "/" + membershipID},
	{Method: http.MethodPost, Target: roleAssignments, Body: roleGrant},
	{Method: http.MethodGet, Target: roleAssignments + "/" + roleAssignmentID},
	{Method: http.MethodDelete, Target: roleAssignments + "/" + roleAssignmentID},
}

func TestAProtectedOperationNeedsAVerifiedCaller(t *testing.T) {
	credentials := []struct {
		name      string
		header    []string
		status    int
		code      string
		challenge string
		detail    string
	}{
		{"no credentials", nil, http.StatusUnauthorized, CodeCredentialsMissing, "Bearer", ""},
		{"a token nobody accepts", []string{"Bearer mallory"}, http.StatusUnauthorized, CodeTokenInvalid, `Bearer error="invalid_token"`, ""},
		{"credentials of another scheme", []string{"Basic YWxpY2U6c2VjcmV0"}, http.StatusUnauthorized, CodeTokenInvalid, `Bearer error="invalid_token"`, ""},
		{"the scheme without a token", []string{"Bearer"}, http.StatusUnauthorized, CodeTokenInvalid, `Bearer error="invalid_token"`, ""},
		{"an empty token", []string{"Bearer "}, http.StatusUnauthorized, CodeTokenInvalid, `Bearer error="invalid_token"`, ""},
		{"a token in two parts", []string{"Bearer alice extra"}, http.StatusUnauthorized, CodeTokenInvalid, `Bearer error="invalid_token"`, ""},
		// Two headers, one of them good. Which one counts is not for the
		// server to pick.
		{"two Authorization headers", []string{"Bearer alice", "Bearer mallory"}, http.StatusUnauthorized, CodeTokenInvalid, `Bearer error="invalid_token"`, ""},
		// The token was not judged, so the answer is not about the token.
		{"a token that cannot be judged now", []string{"Bearer down"}, http.StatusServiceUnavailable, CodeIdentityProviderUnavailable, "",
			"The token could not be verified at the moment. It may be valid."},
	}
	for _, c := range credentials {
		t.Run(c.name, func(t *testing.T) {
			s := newService(t)
			for _, operation := range protected {
				request := httptest.NewRequest(operation.Method, operation.Target, strings.NewReader(operation.Body))
				if operation.Body != "" {
					request.Header.Set("Content-Type", "application/json")
				}
				for _, value := range c.header {
					request.Header.Add("Authorization", value)
				}
				response := httptest.NewRecorder()
				s.handler.ServeHTTP(response, request)
				checkAgainstContract(t, operation, response)

				wantProblem(t, response, c.status, c.code, c.detail)
				if got := response.Header().Get("WWW-Authenticate"); got != c.challenge {
					t.Errorf("%s %s: WWW-Authenticate %q, want %q", operation.Method, operation.Target, got, c.challenge)
				}
			}
			if s.governance.calls() != 0 {
				t.Errorf("%d requests without a verified caller reached a use case", s.governance.calls())
			}
		})
	}

	t.Run("a token the provider vouches for", func(t *testing.T) {
		s := newService(t)
		for _, operation := range protected {
			operation.Token = "alice"
			if response := s.do(t, operation); response.Code >= 300 {
				t.Errorf("%s %s: status %d: %s", operation.Method, operation.Target, response.Code, response.Body)
			}
		}
		if s.governance.calls() != len(protected) {
			t.Errorf("%d requests reached a use case, want %d", s.governance.calls(), len(protected))
		}
	})
}

// The validator asks who sends a request before it reads what the request
// says: a caller nobody vouches for learns nothing about what a valid request
// looks like.
func TestWhoSendsARequestIsAskedBeforeWhatItSays(t *testing.T) {
	s := newService(t)

	invalid := []call{
		{Method: http.MethodPost, Target: "/v1/products", Body: `{"product_key":"Not A Key"}`},
		{Method: http.MethodPost, Target: "/v1/products", Body: `{not json`},
		{Method: http.MethodGet, Target: "/v1/products/not-an-identifier"},
		{Method: http.MethodPost, Target: "/v1/products/not-an-identifier/workspaces", Body: `{}`},
		{Method: http.MethodPost, Target: memberships, Body: `{"role":"superuser"}`},
		{Method: http.MethodDelete, Target: memberships + "/not-an-identifier"},
		{Method: http.MethodPost, Target: roleAssignments, Body: `{"role":"superuser"}`},
		{Method: http.MethodDelete, Target: roleAssignments + "/not-an-identifier"},
	}
	for _, c := range invalid {
		wantProblem(t, s.do(t, c), http.StatusUnauthorized, CodeCredentialsMissing, "")
		c.Token = "mallory"
		wantProblem(t, s.do(t, c), http.StatusUnauthorized, CodeTokenInvalid, "")
		c.Token = "alice"
		if response := s.do(t, c); response.Code != http.StatusBadRequest {
			t.Errorf("%s %s with a good token: status %d, want 400", c.Method, c.Target, response.Code)
		}
	}
}

// Why a token is refused is for whoever operates the service. The caller is
// told that it is, and the log never gets the token.
func TestTheReasonATokenIsRefusedIsLoggedAndNotSent(t *testing.T) {
	s := newService(t)

	const token = "a-token-that-must-not-be-written-anywhere"
	response := s.do(t, call{Method: http.MethodGet, Target: "/v1/products/" + productID, Token: token})
	traceID := wantProblem(t, response, http.StatusUnauthorized, CodeTokenInvalid, "")

	logged := s.log.String()
	for _, want := range []string{"authentication refused", "correlation_id=" + traceID, "the token signature does not verify"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log lacks %q:\n%s", want, logged)
		}
	}
	if strings.Contains(logged, token) || strings.Contains(response.Body.String(), token) {
		t.Errorf("the token was written to the log or to the answer:\n%s\n%s", logged, response.Body)
	}
	if strings.Contains(response.Body.String(), "signature") {
		t.Errorf("the answer says why the token was refused: %s", response.Body)
	}
}

// A HEAD is a GET whose answer has no body. It is validated and
// authenticated as the GET it is.
func TestHEADIsServedAsTheGETItIs(t *testing.T) {
	s := newService(t)
	target := "/v1/products/" + productID

	wantProblem(t, s.do(t, call{Method: http.MethodHead, Target: target}), http.StatusUnauthorized, CodeCredentialsMissing, "")
	wantProblem(t, s.do(t, call{Method: http.MethodHead, Target: target, Token: "mallory"}), http.StatusUnauthorized, CodeTokenInvalid, "")
	if s.governance.calls() != 0 {
		t.Fatalf("%d HEAD requests without a verified caller reached a use case", s.governance.calls())
	}

	response := s.do(t, call{Method: http.MethodHead, Target: target, Token: "alice"})
	if response.Code != http.StatusOK || len(s.governance.productsRead) != 1 {
		t.Errorf("HEAD with a good token: status %d and %d reads, want 200 and 1", response.Code, len(s.governance.productsRead))
	}
	response = s.do(t, call{Method: http.MethodHead, Target: "/v1/products/not-an-identifier", Token: "alice"})
	if response.Code != http.StatusBadRequest {
		t.Errorf("HEAD of a product whose identifier is none: status %d, want 400", response.Code)
	}
}

// The generated code enforces close to nothing of the contract. The
// validator does: a request that does not match never reaches a use case, and
// the answer says where it does not match without repeating what was sent.
func TestARequestThatDoesNotMatchTheContractIsRefused(t *testing.T) {
	replace := func(old, new string) string { return strings.Replace(registration, old, new, 1) }
	products, workspaces := "/v1/products", "/v1/products/"+productID+"/workspaces"

	cases := []struct {
		name   string
		call   call
		status int
		code   string
		detail string
	}{
		{"a required member is missing", call{Method: http.MethodPost, Target: products, Body: replace(`"display_name":"CargoPai",`, ``)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /display_name (required)."},
		{"a member the contract does not declare", call{Method: http.MethodPost, Target: products, Body: replace(`"owners"`, `"canary":true,"owners"`)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract (properties)."},
		// A decoder that ignores case would read this as product_key.
		{"a member in another case", call{Method: http.MethodPost, Target: products, Body: replace(`"product_key"`, `"Product_Key"`)},
			http.StatusBadRequest, CodeRequestInvalid, ""},
		{"a key that is not a key", call{Method: http.MethodPost, Target: products, Body: replace(`"cargopai"`, `"Canary Key"`)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /product_key (pattern)."},
		{"a name of the wrong type", call{Method: http.MethodPost, Target: products, Body: replace(`"CargoPai"`, `42`)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /display_name (type)."},
		{"a null name", call{Method: http.MethodPost, Target: products, Body: replace(`"CargoPai"`, `null`)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /display_name (nullable)."},
		{"a name that is too long", call{Method: http.MethodPost, Target: products, Body: replace(`"CargoPai"`, `"`+strings.Repeat("n", 121)+`"`)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /display_name (maxLength)."},
		{"a name with a line break", call{Method: http.MethodPost, Target: products, Body: replace(`"CargoPai"`, `"canary\nname"`)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /display_name (pattern)."},
		{"no owner", call{Method: http.MethodPost, Target: products, Body: replace(`{"issuer":"https://issuer.example","subject":"alice"},{"issuer":"https://issuer.example","subject":"bob"}`, ``)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /owners (minItems)."},
		{"the same owner twice", call{Method: http.MethodPost, Target: products, Body: replace(`"subject":"bob"`, `"subject":"alice"`)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /owners (uniqueItems)."},
		{"an owner without a subject", call{Method: http.MethodPost, Target: products, Body: replace(`,"subject":"bob"`, ``)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /owners/1/subject (required)."},
		{"an owner with an empty subject", call{Method: http.MethodPost, Target: products, Body: replace(`"subject":"bob"`, `"subject":""`)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /owners/1/subject (minLength)."},
		{"an owner with a role of its own", call{Method: http.MethodPost, Target: products, Body: replace(`"subject":"bob"`, `"subject":"bob","role":"canary"`)},
			http.StatusBadRequest, CodeRequestInvalid, ""},
		{"an owning organization without a reference", call{Method: http.MethodPost, Target: products, Body: replace(`,"organization":"org-cargopai"`, ``)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /owner_organization/organization (required)."},
		{"a body that is not JSON", call{Method: http.MethodPost, Target: products, Body: `{"canary"`},
			http.StatusBadRequest, CodeRequestInvalid, "The request body cannot be read as the contract declares it."},
		{"a body that is not an object", call{Method: http.MethodPost, Target: products, Body: `["canary"]`},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract (type)."},
		{"no body", call{Method: http.MethodPost, Target: products},
			http.StatusBadRequest, CodeRequestInvalid, "The operation requires a request body."},
		{"a body of another media type", call{Method: http.MethodPost, Target: products, Body: registration, Header: map[string]string{"Content-Type": "text/plain"}},
			http.StatusUnsupportedMediaType, CodeUnsupportedMediaType, "The request body is not of a media type the operation accepts."},
		{"a body larger than the limit", call{Method: http.MethodPost, Target: products, Body: `{"display_name":"` + strings.Repeat("c", requestBodyMaxBytes) + `"}`},
			http.StatusRequestEntityTooLarge, CodeRequestTooLarge, "The request body is larger than 65536 bytes."},
		{"a product identifier that is none", call{Method: http.MethodGet, Target: "/v1/products/canary"},
			http.StatusBadRequest, CodeRequestInvalid, "The path parameter product_id does not match the contract."},
		{"a product identifier in upper case", call{Method: http.MethodGet, Target: "/v1/products/" + strings.ToUpper(productID)},
			http.StatusBadRequest, CodeRequestInvalid, "The path parameter product_id does not match the contract."},
		{"a workspace identifier that is none", call{Method: http.MethodGet, Target: workspaces + "/canary"},
			http.StatusBadRequest, CodeRequestInvalid, "The path parameter workspace_id does not match the contract."},
		{"a body where the operation takes none", call{Method: http.MethodGet, Target: "/v1/products/" + productID, Body: `{"canary":true}`},
			http.StatusBadRequest, CodeRequestInvalid, "The request does not match the contract."},
		{"a workspace key that is not a key", call{Method: http.MethodPost, Target: workspaces, Body: `{"workspace_key":"Canary","display_name":"API"}`},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /workspace_key (pattern)."},
		{"a workspace without a name", call{Method: http.MethodPost, Target: workspaces, Body: `{"workspace_key":"api"}`},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /display_name (required)."},
		{"a workspace that names its product", call{Method: http.MethodPost, Target: workspaces, Body: `{"workspace_key":"api","display_name":"API","product_id":"` + productID + `"}`},
			http.StatusBadRequest, CodeRequestInvalid, ""},
		{"a membership without a member", call{Method: http.MethodPost, Target: memberships, Body: `{"role":"internal_author"}`},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /member (required)."},
		{"a membership without a role", call{Method: http.MethodPost, Target: memberships, Body: strings.Replace(grant, `,"role":"workspace_maintainer"`, ``, 1)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /role (required)."},
		{"a role nobody defined", call{Method: http.MethodPost, Target: memberships, Body: strings.Replace(grant, `"workspace_maintainer"`, `"canary"`, 1)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /role (enum)."},
		{"the role that owns a product", call{Method: http.MethodPost, Target: memberships, Body: strings.Replace(grant, `"workspace_maintainer"`, `"product_owner"`, 1)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /role (enum)."},
		{"a member without an issuer", call{Method: http.MethodPost, Target: memberships, Body: strings.Replace(grant, `"issuer":"https://issuer.example",`, ``, 1)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /member/issuer (required)."},
		// A membership is in force until it is revoked and is granted by the
		// caller. The request says neither when it ends nor who grants it.
		{"a membership that says when it ends", call{Method: http.MethodPost, Target: memberships, Body: strings.Replace(grant, `"role"`, `"expires_at":"2027-01-01T00:00:00Z","role"`, 1)},
			http.StatusBadRequest, CodeRequestInvalid, ""},
		{"a membership that says who grants it", call{Method: http.MethodPost, Target: memberships,
			Body: strings.Replace(grant, `"role"`, `"granted_by":{"issuer":"https://issuer.example","subject":"canary"},"role"`, 1)},
			http.StatusBadRequest, CodeRequestInvalid, ""},
		{"a membership identifier that is none", call{Method: http.MethodGet, Target: memberships + "/canary"},
			http.StatusBadRequest, CodeRequestInvalid, "The path parameter membership_id does not match the contract."},
		{"a body where a revocation takes none", call{Method: http.MethodDelete, Target: memberships + "/" + membershipID, Body: `{"canary":true}`},
			http.StatusBadRequest, CodeRequestInvalid, "The request does not match the contract."},
		{"a role without a principal", call{Method: http.MethodPost, Target: roleAssignments, Body: `{"role":"product_owner"}`},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /principal (required)."},
		{"a principal without a role", call{Method: http.MethodPost, Target: roleAssignments, Body: strings.Replace(roleGrant, `,"role":"product_owner"`, ``, 1)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /role (required)."},
		{"a role of a product nobody defined", call{Method: http.MethodPost, Target: roleAssignments, Body: strings.Replace(roleGrant, `"product_owner"`, `"canary"`, 1)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /role (enum)."},
		{"a role of a workspace in a product", call{Method: http.MethodPost, Target: roleAssignments, Body: strings.Replace(roleGrant, `"product_owner"`, `"workspace_maintainer"`, 1)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /role (enum)."},
		{"a principal without an issuer", call{Method: http.MethodPost, Target: roleAssignments, Body: strings.Replace(roleGrant, `"issuer":"https://issuer.example",`, ``, 1)},
			http.StatusBadRequest, CodeRequestInvalid, "The request body does not match the contract at /principal/issuer (required)."},
		// The body of a grant of a membership names a member, not a
		// principal: it is not the body of a grant of a role in a product.
		{"a member where a principal is named", call{Method: http.MethodPost, Target: roleAssignments, Body: strings.Replace(grant, `"workspace_maintainer"`, `"product_owner"`, 1)},
			http.StatusBadRequest, CodeRequestInvalid, ""},
		// A role assignment is in force until it is revoked, is granted by
		// the caller and is of the product of the path. The request says
		// none of the three.
		{"a role that says when it ends", call{Method: http.MethodPost, Target: roleAssignments, Body: strings.Replace(roleGrant, `"role"`, `"expires_at":"2027-01-01T00:00:00Z","role"`, 1)},
			http.StatusBadRequest, CodeRequestInvalid, ""},
		{"a role that says who grants it", call{Method: http.MethodPost, Target: roleAssignments,
			Body: strings.Replace(roleGrant, `"role"`, `"granted_by":{"issuer":"https://issuer.example","subject":"canary"},"role"`, 1)},
			http.StatusBadRequest, CodeRequestInvalid, ""},
		{"a role that names its product", call{Method: http.MethodPost, Target: roleAssignments, Body: strings.Replace(roleGrant, `"role"`, `"product_id":"`+productID+`","role"`, 1)},
			http.StatusBadRequest, CodeRequestInvalid, ""},
		{"a role assignment identifier that is none", call{Method: http.MethodGet, Target: roleAssignments + "/canary"},
			http.StatusBadRequest, CodeRequestInvalid, "The path parameter role_assignment_id does not match the contract."},
		{"a role assignment identifier in upper case", call{Method: http.MethodDelete, Target: roleAssignments + "/" + strings.ToUpper(roleAssignmentID)},
			http.StatusBadRequest, CodeRequestInvalid, "The path parameter role_assignment_id does not match the contract."},
		{"a body where the revocation of a role takes none", call{Method: http.MethodDelete, Target: roleAssignments + "/" + roleAssignmentID, Body: `{"canary":true}`},
			http.StatusBadRequest, CodeRequestInvalid, "The request does not match the contract."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newService(t)
			c.call.Token = "platform-admin"
			response := s.do(t, c.call)

			if c.detail == "" {
				// The validator reports these under one keyword or another;
				// what matters is that they are refused.
				if response.Code != c.status || members(t, response)["error_code"] != c.code {
					t.Fatalf("status %d, want %d %s: %s", response.Code, c.status, c.code, response.Body)
				}
			} else {
				wantProblem(t, response, c.status, c.code, c.detail)
			}
			if strings.Contains(strings.ToLower(response.Body.String()), "canary") {
				t.Errorf("the answer repeats what was sent: %s", response.Body)
			}
			if strings.Contains(strings.ToLower(s.log.String()), "canary") {
				t.Errorf("the log repeats what was sent: %s", s.log)
			}
			if s.governance.calls() != 0 {
				t.Errorf("a request that does not match the contract reached a use case")
			}
		})
	}
}

func TestAMediaTypeWithParametersIsStillJSON(t *testing.T) {
	s := newService(t)

	response := s.do(t, call{Method: http.MethodPost, Target: "/v1/products", Token: "platform-admin", Body: registration,
		Header: map[string]string{"Content-Type": "application/json; charset=utf-8"}})
	if response.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", response.Code, response.Body)
	}
}

func TestPlaceShowsOnlyPlainNamesAndIndexes(t *testing.T) {
	cases := map[string]struct {
		tokens []string
		want   string
	}{
		"nothing":                 {nil, ""},
		"a member":                {[]string{"display_name"}, "/display_name"},
		"a member of an element":  {[]string{"owners", "1", "subject"}, "/owners/1/subject"},
		"a name with a space":     {[]string{"owners", "not a name"}, ""},
		"a name with a slash":     {[]string{"a/b"}, ""},
		"a name with a line feed": {[]string{"a\nb"}, ""},
		"a name that is too long": {[]string{strings.Repeat("a", 65)}, ""},
		"an empty name":           {[]string{""}, ""},
	}
	for name, c := range cases {
		if got := place(c.tokens); got != c.want {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

func TestBearerTokenTakesExactlyOneBearerToken(t *testing.T) {
	cases := map[string]struct {
		headers    []string
		token      string
		present    bool
		wellFormed bool
	}{
		"no header":              {nil, "", false, false},
		"a bearer token":         {[]string{"Bearer abc.def.ghi"}, "abc.def.ghi", true, true},
		"the scheme in any case": {[]string{"bearer abc"}, "abc", true, true},
		"another scheme":         {[]string{"Basic abc"}, "", true, false},
		"no token":               {[]string{"Bearer"}, "", true, false},
		"an empty token":         {[]string{"Bearer "}, "", true, false},
		"two spaces":             {[]string{"Bearer  abc"}, "", true, false},
		"a token with a tab":     {[]string{"Bearer a\tb"}, "", true, false},
		"two headers":            {[]string{"Bearer abc", "Bearer abc"}, "", true, false},
		"an empty header":        {[]string{""}, "", true, false},
	}
	for name, c := range cases {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		for _, value := range c.headers {
			request.Header.Add("Authorization", value)
		}
		token, present, wellFormed := bearerToken(request)
		if token != c.token || present != c.present || wellFormed != c.wellFormed {
			t.Errorf("%s: got %q, present %t, well formed %t; want %q, %t, %t", name, token, present, wellFormed, c.token, c.present, c.wellFormed)
		}
	}
}

// failing stands in for the operations of the contract. Its liveness
// operation fails, and its version operation returns nothing at all.
type failing struct {
	operations
	cause error
}

func (f failing) GetHealthz(context.Context, GetHealthzRequestObject) (GetHealthzResponseObject, error) {
	return nil, f.cause
}

func (failing) GetVersion(context.Context, GetVersionRequestObject) (GetVersionResponseObject, error) {
	return nil, nil
}

func TestAFailureNobodyExpectedIsAnInternalErrorWhoseCauseIsLoggedAndNotSent(t *testing.T) {
	cause := errors.New("connect to 10.0.0.7: password authentication failed for user fabric_app")
	cases := []struct {
		name   string
		target string
		logged string
	}{
		{"the operation returns an error", "/healthz?token=in-the-query", cause.Error()},
		{"the operation returns neither a response nor an error", "/version?token=in-the-query", "GetVersion returned neither"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var log bytes.Buffer
			handler, err := newHandler(slog.New(slog.NewTextHandler(&log, nil)), tokens{}, failing{cause: cause})
			if err != nil {
				t.Fatalf("build the handler: %v", err)
			}

			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, c.target, nil))

			traceID := wantProblem(t, recorder, http.StatusInternalServerError, CodeInternalError, "")
			if strings.Contains(recorder.Body.String(), "10.0.0.7") {
				t.Errorf("the cause reached the caller: %s", recorder.Body)
			}
			if !strings.Contains(log.String(), c.logged) || !strings.Contains(log.String(), "correlation_id="+traceID) {
				t.Errorf("the cause must be logged with the identifier of the request, got %q", log.String())
			}
			if strings.Contains(log.String(), "in-the-query") {
				t.Errorf("the log must not quote the query string, got %q", log.String())
			}
		})
	}
}

func TestWriteProblemKeepsWhatTheCallerSet(t *testing.T) {
	detail, instance, trace := "Read the slot again before retrying.", "/v1/publication-slots/slot-1", "4bf92f3577b34da6a3ce929d0e0e4736"
	problem := Problem{
		Type:      "https://fabric.example.org/problems/stale-generation",
		Title:     "The slot generation is stale",
		Status:    http.StatusPreconditionFailed,
		Detail:    &detail,
		Instance:  &instance,
		ErrorCode: "CONCURRENT_MODIFICATION",
		TraceID:   &trace,
	}
	recorder := httptest.NewRecorder()
	WriteProblem(recorder, problem)

	if recorder.Code != http.StatusPreconditionFailed {
		t.Fatalf("status %d, want 412", recorder.Code)
	}
	want := map[string]any{
		"type":       problem.Type,
		"title":      problem.Title,
		"status":     float64(http.StatusPreconditionFailed),
		"detail":     detail,
		"instance":   instance,
		"error_code": problem.ErrorCode,
		"trace_id":   trace,
	}
	if got := members(t, recorder); !reflect.DeepEqual(got, want) {
		t.Errorf("problem members %v, want %v", got, want)
	}
}

func TestWriteProblemAnswersInternalErrorForWhatIsNotAProblem(t *testing.T) {
	detail := "written for another response"
	cases := map[string]Problem{
		"status below 400":   {Status: http.StatusOK, ErrorCode: "ANY", Detail: &detail},
		"status above 599":   {Status: 600, ErrorCode: "ANY"},
		"missing error code": {Status: http.StatusConflict, Detail: &detail},
	}
	for name, problem := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			WriteProblem(recorder, problem)

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status %d, want 500", recorder.Code)
			}
			want := map[string]any{
				"type": "about:blank", "title": "Internal Server Error",
				"status": float64(http.StatusInternalServerError), "error_code": CodeInternalError,
			}
			if got := members(t, recorder); !reflect.DeepEqual(got, want) {
				t.Errorf("problem members %v, want %v", got, want)
			}
		})
	}
}

func TestTheTransportIsWiredCompletelyOrNotAtAll(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	complete := Config{Logger: logger, Build: buildinfo.Info{}, Verifier: tokens{}, Governance: newGovernance()}
	if _, err := NewHandler(complete); err != nil {
		t.Fatalf("a complete wiring was refused: %v", err)
	}

	for name, remove := range map[string]func(c *Config){
		"a logger":      func(c *Config) { c.Logger = nil },
		"a verifier":    func(c *Config) { c.Verifier = nil },
		"the use cases": func(c *Config) { c.Governance = nil },
	} {
		incomplete := complete
		remove(&incomplete)
		if _, err := NewHandler(incomplete); err == nil {
			t.Errorf("a wiring without %s was accepted", name)
		}
	}
}
