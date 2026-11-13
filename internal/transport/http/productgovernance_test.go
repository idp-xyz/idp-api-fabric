package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/identity"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// The answer shows the product as the contract declares it and nothing the
// contract does not: not who registered it, not its version.
var productBody = map[string]any{
	"product_id": productID, "product_key": "cargopai", "display_name": "CargoPai", "status": "ACTIVE",
	"owner_organization": map[string]any{"identity_provider": issuer, "organization": "org-cargopai"},
	"created_at":         "2026-11-11T09:00:00.123456Z",
}

var workspaceBody = map[string]any{
	"workspace_id": workspaceID, "product_id": productID, "workspace_key": "api-engineering",
	"display_name": "API engineering", "status": "ACTIVE", "created_at": "2026-11-11T09:00:00.123456Z",
}

// A membership is shown without who granted it and without its version.
var membershipBody = map[string]any{
	"membership_id": membershipID, "product_id": productID, "workspace_id": workspaceID,
	"member": map[string]any{"issuer": issuer, "subject": "bob"}, "role": "workspace_maintainer",
	"granted_at": "2026-11-11T09:00:00.123456Z",
}

// A role assignment is shown as a membership is: without who granted it and
// without its version.
var roleAssignmentBody = map[string]any{
	"role_assignment_id": roleAssignmentID, "product_id": productID,
	"principal": map[string]any{"issuer": issuer, "subject": "bob"}, "role": "product_owner",
	"granted_at": "2026-11-11T09:00:00.123456Z",
}

func TestRegisterProductHandsTheRequestToTheUseCaseAsTheVerifiedCaller(t *testing.T) {
	s := newService(t)

	response := s.do(t, call{Method: http.MethodPost, Target: "/v1/products", Token: "platform-admin", Body: registration})
	if response.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", response.Code, response.Body)
	}
	if got, want := response.Header().Get("Location"), "/v1/products/"+productID; got != want {
		t.Errorf("Location %q, want %q", got, want)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", got)
	}
	if got := members(t, response); !reflect.DeepEqual(got, productBody) {
		t.Errorf("body\n %v\nwant\n %v", got, productBody)
	}

	if len(s.governance.registered) != 1 {
		t.Fatalf("%d registrations reached the use case, want 1", len(s.governance.registered))
	}
	command := s.governance.registered[0]
	if !uuid.MatchString(command.CorrelationID) {
		t.Errorf("correlation identifier %q, want one the server generated", command.CorrelationID)
	}
	command.CorrelationID = ""
	want := application.RegisterProduct{
		// The caller is who the token is about, not anything the body says.
		Caller: domain.Principal(admin),
		Key:    "cargopai", DisplayName: "CargoPai",
		Owner:  domain.OrganizationRef{IdentityProvider: issuer, Organization: "org-cargopai"},
		Owners: []domain.Principal{{Issuer: issuer, Subject: "alice"}, {Issuer: issuer, Subject: "bob"}},
	}
	if !reflect.DeepEqual(command, want) {
		t.Errorf("command\n %+v\nwant\n %+v", command, want)
	}
}

func TestTheOtherOperationsHandTheirRequestsToTheirUseCases(t *testing.T) {
	s := newService(t)
	product, workspaces := "/v1/products/"+productID, "/v1/products/"+productID+"/workspaces"

	read := s.do(t, call{Method: http.MethodGet, Target: product, Token: "alice"})
	if read.Code != http.StatusOK || !reflect.DeepEqual(members(t, read), productBody) {
		t.Errorf("GET product: %d %s", read.Code, read.Body)
	}
	if got := read.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("GET product: Cache-Control %q, want no-store", got)
	}

	created := s.do(t, call{Method: http.MethodPost, Target: workspaces, Token: "alice",
		Body: `{"workspace_key":"api-engineering","display_name":"API engineering"}`})
	if created.Code != http.StatusCreated || !reflect.DeepEqual(members(t, created), workspaceBody) {
		t.Errorf("POST workspace: %d %s", created.Code, created.Body)
	}
	if got, want := created.Header().Get("Location"), workspaces+"/"+workspaceID; got != want {
		t.Errorf("POST workspace: Location %q, want %q", got, want)
	}

	readWorkspace := s.do(t, call{Method: http.MethodGet, Target: workspaces + "/" + workspaceID, Token: "alice"})
	if readWorkspace.Code != http.StatusOK || !reflect.DeepEqual(members(t, readWorkspace), workspaceBody) {
		t.Errorf("GET workspace: %d %s", readWorkspace.Code, readWorkspace.Body)
	}

	g := s.governance
	if len(g.productsRead) != 1 || len(g.created) != 1 || len(g.spacesRead) != 1 {
		t.Fatalf("the use cases were called %d, %d and %d times, want once each", len(g.productsRead), len(g.created), len(g.spacesRead))
	}
	caller := domain.Principal(alice)
	correlation := func(id string) string {
		if !uuid.MatchString(id) {
			t.Errorf("correlation identifier %q, want one the server generated", id)
		}
		return ""
	}
	g.productsRead[0].CorrelationID = correlation(g.productsRead[0].CorrelationID)
	g.created[0].CorrelationID = correlation(g.created[0].CorrelationID)
	g.spacesRead[0].CorrelationID = correlation(g.spacesRead[0].CorrelationID)

	if want := (application.GetProduct{Caller: caller, ProductID: productID}); g.productsRead[0] != want {
		t.Errorf("GetProduct %+v, want %+v", g.productsRead[0], want)
	}
	if want := (application.CreateWorkspace{Caller: caller, ProductID: productID, Key: "api-engineering", DisplayName: "API engineering"}); g.created[0] != want {
		t.Errorf("CreateWorkspace %+v, want %+v", g.created[0], want)
	}
	if want := (application.GetWorkspace{Caller: caller, ProductID: productID, WorkspaceID: workspaceID}); g.spacesRead[0] != want {
		t.Errorf("GetWorkspace %+v, want %+v", g.spacesRead[0], want)
	}
}

func TestTheMembershipOperationsHandTheirRequestsToTheirUseCases(t *testing.T) {
	s := newService(t)
	membership := memberships + "/" + membershipID

	granted := s.do(t, call{Method: http.MethodPost, Target: memberships, Token: "alice", Body: grant})
	if granted.Code != http.StatusCreated || !reflect.DeepEqual(members(t, granted), membershipBody) {
		t.Errorf("POST membership: %d %s", granted.Code, granted.Body)
	}
	if got := granted.Header().Get("Location"); got != membership {
		t.Errorf("POST membership: Location %q, want %q", got, membership)
	}
	read := s.do(t, call{Method: http.MethodGet, Target: membership, Token: "alice"})
	if read.Code != http.StatusOK || !reflect.DeepEqual(members(t, read), membershipBody) {
		t.Errorf("GET membership: %d %s", read.Code, read.Body)
	}
	for name, response := range map[string]*httptest.ResponseRecorder{"POST": granted, "GET": read} {
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s membership: Cache-Control %q, want no-store", name, got)
		}
	}
	revoked := s.do(t, call{Method: http.MethodDelete, Target: membership, Token: "alice"})
	if revoked.Code != http.StatusNoContent || revoked.Body.Len() != 0 {
		t.Errorf("DELETE membership: %d %q, want 204 and no body", revoked.Code, revoked.Body)
	}

	g := s.governance
	if len(g.granted) != 1 || len(g.membershipsRead) != 1 || len(g.revoked) != 1 {
		t.Fatalf("the use cases were called %d, %d and %d times, want once each", len(g.granted), len(g.membershipsRead), len(g.revoked))
	}
	for _, id := range []*string{&g.granted[0].CorrelationID, &g.membershipsRead[0].CorrelationID, &g.revoked[0].CorrelationID} {
		if !uuid.MatchString(*id) {
			t.Errorf("correlation identifier %q, want one the server generated", *id)
		}
		*id = ""
	}

	// The caller is who the token is about. The member is who the body names.
	caller := domain.Principal(alice)
	if want := (application.GrantMembership{
		Caller: caller, ProductID: productID, WorkspaceID: workspaceID,
		Member: domain.Principal{Issuer: issuer, Subject: "bob"}, Role: "workspace_maintainer",
	}); g.granted[0] != want {
		t.Errorf("GrantMembership %+v, want %+v", g.granted[0], want)
	}
	if want := (application.GetMembership{Caller: caller, ProductID: productID, WorkspaceID: workspaceID, MembershipID: membershipID}); g.membershipsRead[0] != want {
		t.Errorf("GetMembership %+v, want %+v", g.membershipsRead[0], want)
	}
	if want := (application.RevokeMembership{Caller: caller, ProductID: productID, WorkspaceID: workspaceID, MembershipID: membershipID}); g.revoked[0] != want {
		t.Errorf("RevokeMembership %+v, want %+v", g.revoked[0], want)
	}
}

func TestTheRoleOperationsHandTheirRequestsToTheirUseCases(t *testing.T) {
	s := newService(t)
	assignment := roleAssignments + "/" + roleAssignmentID

	granted := s.do(t, call{Method: http.MethodPost, Target: roleAssignments, Token: "alice", Body: roleGrant})
	if granted.Code != http.StatusCreated || !reflect.DeepEqual(members(t, granted), roleAssignmentBody) {
		t.Errorf("POST role assignment: %d %s", granted.Code, granted.Body)
	}
	if got := granted.Header().Get("Location"); got != assignment {
		t.Errorf("POST role assignment: Location %q, want %q", got, assignment)
	}
	read := s.do(t, call{Method: http.MethodGet, Target: assignment, Token: "alice"})
	if read.Code != http.StatusOK || !reflect.DeepEqual(members(t, read), roleAssignmentBody) {
		t.Errorf("GET role assignment: %d %s", read.Code, read.Body)
	}
	for name, response := range map[string]*httptest.ResponseRecorder{"POST": granted, "GET": read} {
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s role assignment: Cache-Control %q, want no-store", name, got)
		}
	}
	revoked := s.do(t, call{Method: http.MethodDelete, Target: assignment, Token: "alice"})
	if revoked.Code != http.StatusNoContent || revoked.Body.Len() != 0 {
		t.Errorf("DELETE role assignment: %d %q, want 204 and no body", revoked.Code, revoked.Body)
	}

	g := s.governance
	if len(g.rolesGranted) != 1 || len(g.rolesRead) != 1 || len(g.rolesRevoked) != 1 || g.calls() != 3 {
		t.Fatalf("the use cases were called %d, %d and %d times of %d calls, want once each and no other",
			len(g.rolesGranted), len(g.rolesRead), len(g.rolesRevoked), g.calls())
	}
	for _, id := range []*string{&g.rolesGranted[0].CorrelationID, &g.rolesRead[0].CorrelationID, &g.rolesRevoked[0].CorrelationID} {
		if !uuid.MatchString(*id) {
			t.Errorf("correlation identifier %q, want one the server generated", *id)
		}
		*id = ""
	}

	// The caller is who the token is about. Who gets the role is who the
	// body names.
	caller := domain.Principal(alice)
	if want := (application.GrantProductRole{
		Caller: caller, ProductID: productID,
		Principal: domain.Principal{Issuer: issuer, Subject: "bob"}, Role: "product_owner",
	}); g.rolesGranted[0] != want {
		t.Errorf("GrantProductRole %+v, want %+v", g.rolesGranted[0], want)
	}
	if want := (application.GetProductRole{Caller: caller, ProductID: productID, RoleAssignmentID: roleAssignmentID}); g.rolesRead[0] != want {
		t.Errorf("GetProductRole %+v, want %+v", g.rolesRead[0], want)
	}
	if want := (application.RevokeProductRole{Caller: caller, ProductID: productID, RoleAssignmentID: roleAssignmentID}); g.rolesRevoked[0] != want {
		t.Errorf("RevokeProductRole %+v, want %+v", g.rolesRevoked[0], want)
	}
}

// Every request gets an identifier of its own, and it is the server's: what
// a caller sends is not written into the audit trail as one.
func TestEveryRequestGetsItsOwnIdentifierFromTheServer(t *testing.T) {
	s := newService(t)

	for range 2 {
		s.do(t, call{Method: http.MethodGet, Target: "/v1/products/" + productID, Token: "alice",
			Header: map[string]string{"X-Request-Id": "from-the-caller", "X-Correlation-Id": "from-the-caller", "Traceparent": "00-from-the-caller-01"}})
	}
	first, second := s.governance.productsRead[0].CorrelationID, s.governance.productsRead[1].CorrelationID
	if !uuid.MatchString(first) || !uuid.MatchString(second) || first == second {
		t.Errorf("correlation identifiers %q and %q, want two the server generated", first, second)
	}
}

// What a use case reports is answered as the problem the contract declares
// for it. A product that is withheld and a product that does not exist are
// one answer.
func TestAFailureAUseCaseNamesIsAnsweredWithItsProblem(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
		detail string
	}{
		"the caller may not":          {application.ErrForbidden, http.StatusForbidden, CodeForbidden, "The caller may not perform this action."},
		"no such product":             {application.ErrProductNotFound, http.StatusNotFound, CodeProductNotFound, ""},
		"no such workspace":           {application.ErrWorkspaceNotFound, http.StatusNotFound, CodeWorkspaceNotFound, ""},
		"no such membership":          {application.ErrMembershipNotFound, http.StatusNotFound, CodeMembershipNotFound, ""},
		"no such role assignment":     {application.ErrRoleAssignmentNotFound, http.StatusNotFound, CodeRoleAssignmentNotFound, ""},
		"the role is held already":    {ports.ErrMembershipExists, http.StatusConflict, CodeMembershipExists, "The principal holds this role in the workspace already."},
		"the product has the owner":   {ports.ErrRoleAssignmentExists, http.StatusConflict, CodeRoleAssignmentExists, "The principal holds this role in the product already."},
		"the last owner":              {fmt.Errorf("revoke the role: %w", domain.ErrLastOwner), http.StatusConflict, CodeLastOwner, "The product has no other owner, and always has one."},
		"a member nobody can be":      {application.ErrMemberIssuerNotTrusted, http.StatusUnprocessableEntity, CodeMemberIssuerNotTrusted, "A member is named under an issuer this deployment does not trust."},
		"the product key is taken":    {ports.ErrProductKeyTaken, http.StatusConflict, CodeProductKeyTaken, "A product with this key is registered already."},
		"the workspace key is taken":  {ports.ErrWorkspaceKeyTaken, http.StatusConflict, CodeWorkspaceKeyTaken, "The product has a workspace with this key already."},
		"the product is suspended":    {domain.ErrProductNotActive, http.StatusConflict, CodeProductNotActive, "The product is suspended and takes no new work."},
		"an owner nobody can be":      {application.ErrOwnerIssuerNotTrusted, http.StatusUnprocessableEntity, CodeOwnerIssuerNotTrusted, "An owner is named under an issuer this deployment does not trust."},
		"a value the model refuses":   {fmt.Errorf("%w: a product key is not a key, it was canary", domain.ErrInvalid), http.StatusUnprocessableEntity, CodeRequestUnprocessable, ""},
		"a commit of unknown outcome": {fmt.Errorf("%w: the connection to 10.0.0.7 was lost", ports.ErrOutcomeUncertain), http.StatusServiceUnavailable, CodeOutcomeUncertain, "It is not known whether the change was committed. Read before retrying."},
		"wrapped on its way up":       {fmt.Errorf("create the workspace: %w", ports.ErrWorkspaceKeyTaken), http.StatusConflict, CodeWorkspaceKeyTaken, "The product has a workspace with this key already."},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			for _, operation := range protected {
				s := newService(t)
				s.governance.err = c.err
				operation.Token = "alice"

				response := s.do(t, operation)
				wantProblem(t, response, c.status, c.code, c.detail)
				if body := response.Body.String(); strings.Contains(body, "canary") || strings.Contains(body, "10.0.0.7") {
					t.Errorf("the answer carries the cause: %s", body)
				}
			}
		})
	}
}

func TestAFailureOfAUseCaseNobodyNamedIsAnInternalError(t *testing.T) {
	s := newService(t)
	s.governance.err = errors.New("read the product: connect to 10.0.0.7: connection refused")

	response := s.do(t, call{Method: http.MethodGet, Target: "/v1/products/" + productID, Token: "alice"})
	traceID := wantProblem(t, response, http.StatusInternalServerError, CodeInternalError, "")
	if strings.Contains(response.Body.String(), "10.0.0.7") {
		t.Errorf("the cause reached the caller: %s", response.Body)
	}
	if logged := s.log.String(); !strings.Contains(logged, "connection refused") || !strings.Contains(logged, "correlation_id="+traceID) {
		t.Errorf("the cause must be logged with the identifier of the request:\n%s", logged)
	}
}

// A commit of unknown outcome and a refusal nobody could record are for
// whoever operates the service to know of.
func TestWhatAnOperatorHasToKnowIsLogged(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		logged string
	}{
		"a commit of unknown outcome": {fmt.Errorf("%w: the connection was lost", ports.ErrOutcomeUncertain), http.StatusServiceUnavailable, "operation failed"},
		"a refusal that was not audited": {fmt.Errorf("%w; %w: the audit trail is down", application.ErrProductNotFound, application.ErrRefusalNotAudited),
			http.StatusNotFound, "a refusal could not be audited"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newService(t)
			s.governance.err = c.err

			response := s.do(t, call{Method: http.MethodGet, Target: "/v1/products/" + productID, Token: "alice"})
			if response.Code != c.status {
				t.Fatalf("status %d, want %d", response.Code, c.status)
			}
			if logged := s.log.String(); !strings.Contains(logged, "level=ERROR") || !strings.Contains(logged, c.logged) {
				t.Errorf("the log lacks an error saying %q:\n%s", c.logged, logged)
			}
			if strings.Contains(response.Body.String(), "audit") {
				t.Errorf("the answer tells the caller about the audit trail: %s", response.Body)
			}
		})
	}

	// A refusal that was recorded is the normal course of things and is not
	// logged as a failure.
	s := newService(t)
	s.governance.err = application.ErrProductNotFound
	s.do(t, call{Method: http.MethodGet, Target: "/v1/products/" + productID, Token: "alice"})
	if strings.Contains(s.log.String(), "level=ERROR") {
		t.Errorf("an ordinary refusal was logged as an error:\n%s", s.log)
	}
}

// A status or a role the contract does not declare is not sent: a caller
// would branch on a value it was never told of.
func TestAValueTheContractDoesNotDeclareIsNotSent(t *testing.T) {
	s := newService(t)
	s.governance.product.Status = "ARCHIVED"
	s.governance.workspace.Status = "ARCHIVED"
	s.governance.membership.Role = "ARCHIVED"
	s.governance.assignment.Role = "ARCHIVED"

	for _, operation := range protected {
		if operation.Method == http.MethodDelete {
			// A revocation is answered without a body.
			continue
		}
		operation.Token = "alice"
		response := s.do(t, operation)
		wantProblem(t, response, http.StatusInternalServerError, CodeInternalError, "")
		if strings.Contains(response.Body.String(), "ARCHIVED") {
			t.Errorf("the undeclared status was sent: %s", response.Body)
		}
	}
	if !strings.Contains(s.log.String(), "which the contract does not declare") {
		t.Errorf("the log does not say what went wrong:\n%s", s.log)
	}
}

// The contract shows a membership in force and has nothing to say that one
// is not. A revoked membership is not sent as if it were in force.
func TestARevokedMembershipIsNotSent(t *testing.T) {
	s := newService(t)
	revoked, err := maintainer().Revoke(domain.Principal(alice), createdAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("revoke the membership: %v", err)
	}
	s.governance.membership = revoked

	for _, operation := range []call{
		{Method: http.MethodPost, Target: memberships, Body: grant},
		{Method: http.MethodGet, Target: memberships + "/" + membershipID},
		{Method: http.MethodGet, Target: memberships},
	} {
		operation.Token = "alice"
		wantProblem(t, s.do(t, operation), http.StatusInternalServerError, CodeInternalError, "")
	}
	if !strings.Contains(s.log.String(), "the membership was revoked") {
		t.Errorf("the log does not say what went wrong:\n%s", s.log)
	}
}

// Neither is a revoked role assignment: whoever read it would take its
// principal for an owner.
func TestARevokedRoleAssignmentIsNotSent(t *testing.T) {
	s := newService(t)
	revoked, err := ownership().Revoke(domain.Principal(alice), createdAt.Add(time.Hour), 2)
	if err != nil {
		t.Fatalf("revoke the role assignment: %v", err)
	}
	s.governance.assignment = revoked

	for _, operation := range []call{
		{Method: http.MethodPost, Target: roleAssignments, Body: roleGrant},
		{Method: http.MethodGet, Target: roleAssignments + "/" + roleAssignmentID},
		{Method: http.MethodGet, Target: roleAssignments},
	} {
		operation.Token = "alice"
		wantProblem(t, s.do(t, operation), http.StatusInternalServerError, CodeInternalError, "")
	}
	if !strings.Contains(s.log.String(), "the role assignment was revoked") {
		t.Errorf("the log does not say what went wrong:\n%s", s.log)
	}
}

// An operation of Product Governance is never served without a verified
// caller, even if it were wired around the validator.
func TestAnOperationReachedWithoutAPrincipalIsFailed(t *testing.T) {
	g := newGovernance()
	o := operations{build: testBuild, governance: g}

	unauthenticated := context.WithValue(context.Background(), stateKey{}, &requestState{correlationID: "c"})
	for name, ctx := range map[string]context.Context{"no request state": context.Background(), "no principal": unauthenticated} {
		_, errRegister := o.RegisterProduct(ctx, RegisterProductRequestObject{Body: &ProductRegistration{}})
		_, errGet := o.GetProduct(ctx, GetProductRequestObject{ProductID: productID})
		_, errCreate := o.CreateWorkspace(ctx, CreateWorkspaceRequestObject{ProductID: productID, Body: &WorkspaceCreation{}})
		_, errGetWorkspace := o.GetWorkspace(ctx, GetWorkspaceRequestObject{ProductID: productID, WorkspaceID: workspaceID})
		_, errGrant := o.GrantMembership(ctx, GrantMembershipRequestObject{ProductID: productID, WorkspaceID: workspaceID, Body: &MembershipGrant{}})
		_, errGetMembership := o.GetMembership(ctx, GetMembershipRequestObject{ProductID: productID, WorkspaceID: workspaceID, MembershipID: membershipID})
		_, errRevoke := o.RevokeMembership(ctx, RevokeMembershipRequestObject{ProductID: productID, WorkspaceID: workspaceID, MembershipID: membershipID})
		_, errGrantRole := o.GrantProductRole(ctx, GrantProductRoleRequestObject{ProductID: productID, Body: &ProductRoleGrant{}})
		_, errGetRole := o.GetProductRole(ctx, GetProductRoleRequestObject{ProductID: productID, RoleAssignmentID: roleAssignmentID})
		_, errRevokeRole := o.RevokeProductRole(ctx, RevokeProductRoleRequestObject{ProductID: productID, RoleAssignmentID: roleAssignmentID})
		_, errListProducts := o.ListProducts(ctx, ListProductsRequestObject{})
		_, errListWorkspaces := o.ListWorkspaces(ctx, ListWorkspacesRequestObject{ProductID: productID})
		_, errListMemberships := o.ListMemberships(ctx, ListMembershipsRequestObject{ProductID: productID, WorkspaceID: workspaceID})
		_, errListRoles := o.ListProductRoles(ctx, ListProductRolesRequestObject{ProductID: productID})
		for operation, err := range map[string]error{
			"RegisterProduct": errRegister, "GetProduct": errGet, "CreateWorkspace": errCreate, "GetWorkspace": errGetWorkspace,
			"GrantMembership": errGrant, "GetMembership": errGetMembership, "RevokeMembership": errRevoke,
			"GrantProductRole": errGrantRole, "GetProductRole": errGetRole, "RevokeProductRole": errRevokeRole,
			"ListProducts": errListProducts, "ListWorkspaces": errListWorkspaces,
			"ListMemberships": errListMemberships, "ListProductRoles": errListRoles,
		} {
			if err == nil {
				t.Errorf("%s with %s was served", operation, name)
			}
			if _, named := problemOf(err); named {
				t.Errorf("%s with %s is answered as something the caller did: %v", operation, name, err)
			}
		}
	}
	if g.calls() != 0 {
		t.Errorf("%d operations without a principal reached a use case", g.calls())
	}
}

// The contract states the rules of the model, so that what the validator
// lets through the model accepts, and what the model refuses the validator
// has refused already with a place and a keyword.
func TestTheContractStatesTheRulesOfTheModel(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatalf("load the contract: %v", err)
	}
	schemas := contract.Components.Schemas

	if got := schemas["Key"].Value.Pattern; got != domain.KeyPattern {
		t.Errorf("the contract states the key pattern %q, the model %q", got, domain.KeyPattern)
	}
	if got := schemas["DisplayName"].Value.MaxLength; got == nil || *got != domain.DisplayNameMaxLength {
		t.Errorf("the contract bounds a display name at %v characters, the model at %d", got, domain.DisplayNameMaxLength)
	}
	if got := schemas["ProductRegistration"].Value.Properties["owners"].Value.MaxItems; got == nil || *got != domain.MaxFirstOwners {
		t.Errorf("the contract allows %v first owners, the model %d", got, domain.MaxFirstOwners)
	}
	// A page holds as many items as the use cases give in one, and as many
	// by default as they choose when the request names no size.
	size := contract.Components.Parameters["PageSize"].Value.Schema.Value
	if size.Min == nil || *size.Min != 1 || size.Max == nil || *size.Max != application.MaxPageSize ||
		fmt.Sprint(size.Default) != fmt.Sprint(application.DefaultPageSize) {
		t.Errorf("the contract takes a page of %v to %v items, %v by default; the use cases 1 to %d, %d by default",
			size.Min, size.Max, size.Default, application.MaxPageSize, application.DefaultPageSize)
	}
	for _, list := range []string{"ProductList", "WorkspaceList", "MembershipList", "ProductRoleAssignmentList"} {
		if schemas[list] == nil || schemas[list].Value.Properties["items"] == nil {
			t.Errorf("the contract declares no items of %s", list)
			continue
		}
		if got := schemas[list].Value.Properties["items"].Value.MaxItems; got == nil || *got != application.MaxPageSize {
			t.Errorf("the contract bounds %s at %v items, the use cases a page at %d", list, got, application.MaxPageSize)
		}
		if !slices.Contains(schemas[list].Value.Required, "items") || slices.Contains(schemas[list].Value.Required, "next_page_token") {
			t.Errorf("%s requires %v, want the items always and the token only when a page follows", list, schemas[list].Value.Required)
		}
	}
	if got := schemas["Reference"].Value.MaxLength; got == nil || *got != domain.ReferenceMaxLength {
		t.Errorf("the contract bounds a reference at %v characters, the model at %d", got, domain.ReferenceMaxLength)
	}
	// A principal is named by a reference and authenticates under its
	// subject. With two bounds, somebody could be named who can never
	// authenticate.
	if verifier, model := identity.SubjectMaxLength, domain.ReferenceMaxLength; verifier != model {
		t.Errorf("the verifier bounds a subject at %d characters, the model a reference at %d", verifier, model)
	}
	// The roles the contract declares are the roles the model knows, of a
	// workspace and of a product, and a grant and what it answers name
	// their role through that declaration and no other.
	for _, roles := range []struct {
		of      string
		schema  string
		model   []domain.Role
		grant   string
		granted string
	}{
		{"a workspace", "WorkspaceRole", domain.WorkspaceRoles(), "MembershipGrant", "Membership"},
		{"a product", "ProductRole", domain.ProductRoles(), "ProductRoleGrant", "ProductRoleAssignment"},
	} {
		if schemas[roles.schema] == nil {
			t.Errorf("the contract declares no %s", roles.schema)
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
		for _, object := range []string{roles.grant, roles.granted} {
			if schemas[object] == nil || schemas[object].Value.Properties["role"] == nil {
				t.Errorf("the contract declares no role of %s", object)
				continue
			}
			if got, want := schemas[object].Value.Properties["role"].Ref, "#/components/schemas/"+roles.schema; got != want {
				t.Errorf("the role of %s is declared by %q, want %q", object, got, want)
			}
		}
	}

	owner := domain.OrganizationRef{IdentityProvider: issuer, Organization: "org-cargopai"}
	at := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	registerWith := func(key, displayName, organization, subject string) (contractAccepts bool) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"product_key": key, "display_name": displayName,
			"owner_organization": map[string]any{"identity_provider": issuer, "organization": organization},
			"owners":             []map[string]any{{"issuer": issuer, "subject": subject}},
		})
		if err != nil {
			t.Fatal(err)
		}
		response := newService(t).do(t, call{Method: http.MethodPost, Target: "/v1/products", Token: "platform-admin", Body: string(body)})
		if response.Code != http.StatusCreated && response.Code != http.StatusBadRequest {
			t.Fatalf("status %d for the key %q, the name %q, the organization %q and the subject %q: %s",
				response.Code, key, displayName, organization, subject, response.Body)
		}
		return response.Code == http.StatusCreated
	}
	register := func(key, displayName string) (contractAccepts bool) {
		t.Helper()
		return registerWith(key, displayName, "org-cargopai", "alice")
	}

	keys := []string{
		"ab", "a", "", "cargopai", "idp-parcel", "a1", "1a", "-a", "a-", "a--b", "Cargo", "cargo_pai", "cargo pai", "cargo.pai",
		"cargopaï", "a" + strings.Repeat("b", 62), "a" + strings.Repeat("b", 63), "cargo\n", "cargo\npai",
	}
	for _, key := range keys {
		_, err := domain.NewProduct(productID, key, "CargoPai", owner, domain.Principal(admin), at)
		if contract, model := register(key, "CargoPai"), err == nil; contract != model {
			t.Errorf("the key %q: the contract accepts it %t, the model %t", key, contract, model)
		}
	}

	names := []string{
		"C", "", "CargoPai", "IDP 跨境小包", "Cargo Pai (EU)", " CargoPai", "CargoPai ", "   ", "Cargo\nPai", "Cargo\tPai", "Cargo\u0000Pai",
		"\u3000CargoPai", "CargoPai\u00a0", "Cargo\u00a0Pai", "Cargo\u0085Pai", "Cargo\u2028Pai", "\u200bCargoPai",
		strings.Repeat("名", 120), strings.Repeat("名", 121), strings.Repeat("n", 120), strings.Repeat("n", 121),
	}
	for _, name := range names {
		_, err := domain.NewProduct(productID, "cargopai", name, owner, domain.Principal(admin), at)
		if contract, model := register("cargopai", name), err == nil; contract != model {
			t.Errorf("the display name %q: the contract accepts it %t, the model %t", name, contract, model)
		}
	}

	// A reference is bounded in characters on both sides: one of 255
	// characters that take three bytes each is as good as one of 255 letters.
	references := []string{
		"org-cargopai", "", "o", "组织", "org cargopai", " org ", "org\ncargopai", "org\tcargopai", "org\u0000", "org\u0085cargopai",
		"org\u2028cargopai", strings.Repeat("r", 255), strings.Repeat("r", 256), strings.Repeat("组", 255), strings.Repeat("组", 256),
	}
	product, err := domain.NewProduct(productID, "cargopai", "CargoPai", owner, domain.Principal(admin), at)
	if err != nil {
		t.Fatalf("register the product: %v", err)
	}
	for _, reference := range references {
		_, err := domain.NewProduct(productID, "cargopai", "CargoPai",
			domain.OrganizationRef{IdentityProvider: issuer, Organization: reference}, domain.Principal(admin), at)
		if contract, model := registerWith("cargopai", "CargoPai", reference, "alice"), err == nil; contract != model {
			t.Errorf("the organization %q: the contract accepts it %t, the model %t", reference, contract, model)
		}

		_, err = product.AssignFirstOwners([]domain.RoleAssignmentID{"0f1e2d3c-4b5a-4968-8776-655443322110"},
			[]domain.Principal{{Issuer: issuer, Subject: reference}}, domain.Principal(admin))
		if contract, model := registerWith("cargopai", "CargoPai", "org-cargopai", reference), err == nil; contract != model {
			t.Errorf("the subject %q of an owner: the contract accepts it %t, the model %t", reference, contract, model)
		}
	}

	// A role is granted in a product or in a workspace, never in both: what
	// the contract lets through as the role of a grant is what the model
	// grants there.
	workspace, err := domain.NewWorkspace(workspaceID, product, "api-engineering", "API engineering", domain.Principal(alice), at)
	if err != nil {
		t.Fatalf("create the workspace: %v", err)
	}
	bob := domain.Principal{Issuer: issuer, Subject: "bob"}
	grantWith := func(target, named, role string) (contractAccepts bool) {
		t.Helper()
		body, err := json.Marshal(map[string]any{named: map[string]any{"issuer": issuer, "subject": "bob"}, "role": role})
		if err != nil {
			t.Fatal(err)
		}
		response := newService(t).do(t, call{Method: http.MethodPost, Target: target, Token: "alice", Body: string(body)})
		if response.Code != http.StatusCreated && response.Code != http.StatusBadRequest {
			t.Fatalf("status %d for the role %q at %s: %s", response.Code, role, target, response.Body)
		}
		return response.Code == http.StatusCreated
	}
	for _, role := range []string{
		"product_owner", "workspace_maintainer", "internal_author", "", "owner", "platform_admin",
		"PRODUCT_OWNER", "product_owner ", " product_owner", "product-owner", "Internal_Author", "internal_author\n",
	} {
		_, err := domain.GrantProductRole(roleAssignmentID, product, bob, domain.Role(role), domain.Principal(alice), at)
		if contract, model := grantWith(roleAssignments, "principal", role), err == nil; contract != model {
			t.Errorf("the role %q in a product: the contract accepts it %t, the model %t", role, contract, model)
		}
		_, err = domain.GrantMembership(membershipID, product, workspace, bob, domain.Role(role), domain.Principal(alice), at)
		if contract, model := grantWith(memberships, "member", role), err == nil; contract != model {
			t.Errorf("the role %q in a workspace: the contract accepts it %t, the model %t", role, contract, model)
		}
	}
}
