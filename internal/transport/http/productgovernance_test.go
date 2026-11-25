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

// The answer shows the product as the contract declares it, with its brand,
// and nothing the contract does not: not who registered it, not who changed
// it last. Its version is not a member either: it is the ETag of an answer
// that shows one product.
var productBody = map[string]any{
	"product_id": productID, "product_key": "cargopai", "display_name": "CargoPai", "status": "ACTIVE",
	"owner_organization": map[string]any{"identity_provider": issuer, "organization": "org-cargopai"},
	"brand":              map[string]any{"name": "CargoPai Developers", "logo_url": brandLogo, "primary_color": "#0A7B3C"},
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

// An audience configuration is shown without who configured it. Its version
// is not a member either: it is the ETag of an answer that shows one.
var audienceConfigBody = map[string]any{
	"audience_config_id": audienceConfigID, "product_id": productID, "audience": "partner", "environment": "sandbox",
	"docs_url": partnerDocs, "created_at": "2026-11-11T09:00:00.123456Z", "updated_at": "2026-11-11T10:00:00.123456Z",
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
	// What shows one product carries its version, which is what a change of
	// it is made from.
	if got := response.Header().Get("ETag"); got != `"4"` {
		t.Errorf("ETag %q, want the version of the product in double quotes", got)
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
	if got := read.Header().Get("ETag"); got != `"4"` {
		t.Errorf("GET product: ETag %q, want the version of the product in double quotes", got)
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

func TestTheAudienceOperationsHandTheirRequestsToTheirUseCases(t *testing.T) {
	s := newService(t)
	config := audienceConfigs + "/" + audienceConfigID

	configured := s.do(t, call{Method: http.MethodPost, Target: audienceConfigs, Token: "alice", Body: configuration})
	if configured.Code != http.StatusCreated || !reflect.DeepEqual(members(t, configured), audienceConfigBody) {
		t.Errorf("POST audience configuration: %d %s", configured.Code, configured.Body)
	}
	if got := configured.Header().Get("Location"); got != config {
		t.Errorf("POST audience configuration: Location %q, want %q", got, config)
	}
	read := s.do(t, call{Method: http.MethodGet, Target: config, Token: "alice"})
	if read.Code != http.StatusOK || !reflect.DeepEqual(members(t, read), audienceConfigBody) {
		t.Errorf("GET audience configuration: %d %s", read.Code, read.Body)
	}
	changed := s.do(t, call{Method: http.MethodPut, Target: config, Token: "alice", Body: docsChange, Header: fromVersion3})
	if changed.Code != http.StatusOK || !reflect.DeepEqual(members(t, changed), audienceConfigBody) {
		t.Errorf("PUT audience configuration: %d %s", changed.Code, changed.Body)
	}
	// What shows one configuration carries its version, which is what a
	// change of it is made from.
	for name, response := range map[string]*httptest.ResponseRecorder{"POST": configured, "GET": read, "PUT": changed} {
		if got := response.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s audience configuration: Cache-Control %q, want no-store", name, got)
		}
		if got := response.Header().Get("ETag"); got != `"3"` {
			t.Errorf("%s audience configuration: ETag %q, want the version of the configuration in double quotes", name, got)
		}
	}
	removed := s.do(t, call{Method: http.MethodDelete, Target: config, Token: "alice"})
	if removed.Code != http.StatusNoContent || removed.Body.Len() != 0 {
		t.Errorf("DELETE audience configuration: %d %q, want 204 and no body", removed.Code, removed.Body)
	}

	g := s.governance
	if len(g.configured) != 1 || len(g.configsRead) != 1 || len(g.configsChanged) != 1 || len(g.configsRemoved) != 1 || g.calls() != 4 {
		t.Fatalf("the use cases were called %d, %d, %d and %d times of %d calls, want once each and no other",
			len(g.configured), len(g.configsRead), len(g.configsChanged), len(g.configsRemoved), g.calls())
	}
	for _, id := range []*string{&g.configured[0].CorrelationID, &g.configsRead[0].CorrelationID, &g.configsChanged[0].CorrelationID, &g.configsRemoved[0].CorrelationID} {
		if !uuid.MatchString(*id) {
			t.Errorf("correlation identifier %q, want one the server generated", *id)
		}
		*id = ""
	}

	caller := domain.Principal(alice)
	if want := (application.ConfigureAudience{
		Caller: caller, ProductID: productID, Audience: "partner", Environment: "sandbox", DocsURL: partnerDocs,
	}); g.configured[0] != want {
		t.Errorf("ConfigureAudience %+v, want %+v", g.configured[0], want)
	}
	if want := (application.GetAudienceConfig{Caller: caller, ProductID: productID, AudienceConfigID: audienceConfigID}); g.configsRead[0] != want {
		t.Errorf("GetAudienceConfig %+v, want %+v", g.configsRead[0], want)
	}
	// The version the change is made from is the one If-Match names.
	if want := (application.ChangeAudienceConfig{
		Caller: caller, ProductID: productID, AudienceConfigID: audienceConfigID, DocsURL: partnerDocs, Version: 3,
	}); g.configsChanged[0] != want {
		t.Errorf("ChangeAudienceConfig %+v, want %+v", g.configsChanged[0], want)
	}
	if want := (application.RemoveAudienceConfig{Caller: caller, ProductID: productID, AudienceConfigID: audienceConfigID}); g.configsRemoved[0] != want {
		t.Errorf("RemoveAudienceConfig %+v, want %+v", g.configsRemoved[0], want)
	}
}

// Where an audience enters the documentation is said by the member docs_url,
// and that there is no entry yet by leaving it out, on the way in and on the
// way out.
func TestAConfigurationWithoutAnEntryHasNoDocsURL(t *testing.T) {
	s := newService(t)
	s.governance.config.DocsURL = ""
	config := audienceConfigs + "/" + audienceConfigID

	for _, operation := range []call{
		{Method: http.MethodPost, Target: audienceConfigs, Body: `{"audience":"public","environment":"production"}`},
		{Method: http.MethodGet, Target: config},
		{Method: http.MethodPut, Target: config, Body: `{}`, Header: fromVersion3},
		{Method: http.MethodGet, Target: audienceConfigs},
	} {
		operation.Token = "alice"
		response := s.do(t, operation)
		if response.Code >= 300 {
			t.Fatalf("%s %s: status %d: %s", operation.Method, operation.Target, response.Code, response.Body)
		}
		if strings.Contains(response.Body.String(), "docs_url") {
			t.Errorf("%s %s: the answer has a docs_url: %s", operation.Method, operation.Target, response.Body)
		}
	}
	g := s.governance
	if got := g.configured[0]; got.Audience != "public" || got.Environment != "production" || got.DocsURL != "" {
		t.Errorf("ConfigureAudience %+v, want public in production without an entry", got)
	}
	if got := g.configsChanged[0]; got.DocsURL != "" || got.Version != 3 {
		t.Errorf("ChangeAudienceConfig %+v, want a change to no entry from version 3", got)
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
		"an auditor of what it owns":  {application.ErrAuditorNotIndependent, http.StatusForbidden, CodeAuditorNotIndependent, "An auditor of a product neither owns it nor administers the platform."},
		"no such product":             {application.ErrProductNotFound, http.StatusNotFound, CodeProductNotFound, ""},
		"no such workspace":           {application.ErrWorkspaceNotFound, http.StatusNotFound, CodeWorkspaceNotFound, ""},
		"no such membership":          {application.ErrMembershipNotFound, http.StatusNotFound, CodeMembershipNotFound, ""},
		"no such role assignment":     {application.ErrRoleAssignmentNotFound, http.StatusNotFound, CodeRoleAssignmentNotFound, ""},
		"no such configuration":       {application.ErrAudienceConfigNotFound, http.StatusNotFound, CodeAudienceConfigNotFound, ""},
		"the audience is configured":  {ports.ErrAudienceConfigExists, http.StatusConflict, CodeAudienceConfigExists, "The product documents its APIs for this audience in this environment already."},
		"a change from a stale read":  {fmt.Errorf("change the audience configuration: %w", application.ErrVersionMismatch), http.StatusPreconditionFailed, CodePreconditionFailed, "It changed since it was read. Read it again before deciding anew."},
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
	s.governance.config.Audience = "ARCHIVED"

	for _, operation := range protected {
		if operation.Method == http.MethodDelete {
			// A revocation is answered without a body.
			continue
		}
		if operation.Target == "/v1/caller" {
			// It says who the caller is, a principal and whether it
			// administers the platform. That shows no status and no role,
			// so there is no value of this kind for it to hold back.
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

// Nor is an audience configuration the contract cannot show: one that was
// removed, or one for an audience or an environment the contract does not
// declare. A portal that read it would document the APIs for an audience
// the product no longer, or never, named.
func TestAnAudienceConfigurationTheContractCannotShowIsNotSent(t *testing.T) {
	removed, err := partnerSandbox().Remove(domain.Principal(alice), createdAt.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("remove the configuration: %v", err)
	}
	everybody, staging := partnerSandbox(), partnerSandbox()
	everybody.Audience, staging.Environment = "everybody", "staging"

	cases := map[string]struct {
		config domain.AudienceConfig
		logged string
	}{
		"a removed configuration":   {removed, "the audience configuration was removed"},
		"an undeclared audience":    {everybody, "for the audience"},
		"an undeclared environment": {staging, "for the environment"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newService(t)
			s.governance.config = c.config
			for _, operation := range []call{
				{Method: http.MethodPost, Target: audienceConfigs, Body: configuration},
				{Method: http.MethodGet, Target: audienceConfigs + "/" + audienceConfigID},
				{Method: http.MethodPut, Target: audienceConfigs + "/" + audienceConfigID, Body: docsChange, Header: fromVersion3},
				{Method: http.MethodGet, Target: audienceConfigs},
			} {
				operation.Token = "alice"
				response := s.do(t, operation)
				wantProblem(t, response, http.StatusInternalServerError, CodeInternalError, "")
				if body := response.Body.String(); strings.Contains(body, "everybody") || strings.Contains(body, "staging") || strings.Contains(body, "docs.cargopai") {
					t.Errorf("%s %s: the answer shows the configuration: %s", operation.Method, operation.Target, body)
				}
			}
			if !strings.Contains(s.log.String(), c.logged) {
				t.Errorf("the log does not say what went wrong:\n%s", s.log)
			}
		})
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
		// Without If-Match as well: who asks is decided before what the
		// request says.
		_, errChangeBrand := o.ChangeProductBrand(ctx, ChangeProductBrandRequestObject{ProductID: productID, Body: &BrandChange{}})
		_, errCreate := o.CreateWorkspace(ctx, CreateWorkspaceRequestObject{ProductID: productID, Body: &WorkspaceCreation{}})
		_, errGetWorkspace := o.GetWorkspace(ctx, GetWorkspaceRequestObject{ProductID: productID, WorkspaceID: workspaceID})
		_, errGrant := o.GrantMembership(ctx, GrantMembershipRequestObject{ProductID: productID, WorkspaceID: workspaceID, Body: &MembershipGrant{}})
		_, errGetMembership := o.GetMembership(ctx, GetMembershipRequestObject{ProductID: productID, WorkspaceID: workspaceID, MembershipID: membershipID})
		_, errRevoke := o.RevokeMembership(ctx, RevokeMembershipRequestObject{ProductID: productID, WorkspaceID: workspaceID, MembershipID: membershipID})
		_, errGrantRole := o.GrantProductRole(ctx, GrantProductRoleRequestObject{ProductID: productID, Body: &ProductRoleGrant{}})
		_, errGetRole := o.GetProductRole(ctx, GetProductRoleRequestObject{ProductID: productID, RoleAssignmentID: roleAssignmentID})
		_, errRevokeRole := o.RevokeProductRole(ctx, RevokeProductRoleRequestObject{ProductID: productID, RoleAssignmentID: roleAssignmentID})
		_, errConfigure := o.ConfigureAudience(ctx, ConfigureAudienceRequestObject{ProductID: productID, Body: &AudienceConfigCreation{}})
		_, errGetConfig := o.GetAudienceConfig(ctx, GetAudienceConfigRequestObject{ProductID: productID, AudienceConfigID: audienceConfigID})
		// Without If-Match as well: who asks is decided before what the
		// request says.
		_, errChangeConfig := o.ChangeAudienceConfig(ctx, ChangeAudienceConfigRequestObject{
			ProductID: productID, AudienceConfigID: audienceConfigID, Body: &AudienceConfigChange{}})
		_, errRemoveConfig := o.RemoveAudienceConfig(ctx, RemoveAudienceConfigRequestObject{ProductID: productID, AudienceConfigID: audienceConfigID})
		_, errListProducts := o.ListProducts(ctx, ListProductsRequestObject{})
		_, errListWorkspaces := o.ListWorkspaces(ctx, ListWorkspacesRequestObject{ProductID: productID})
		_, errListMemberships := o.ListMemberships(ctx, ListMembershipsRequestObject{ProductID: productID, WorkspaceID: workspaceID})
		_, errListRoles := o.ListProductRoles(ctx, ListProductRolesRequestObject{ProductID: productID})
		_, errListConfigs := o.ListAudienceConfigs(ctx, ListAudienceConfigsRequestObject{ProductID: productID})
		_, errGetCaller := o.GetCaller(ctx, GetCallerRequestObject{})
		_, errGetStanding := o.GetCallerInProduct(ctx, GetCallerInProductRequestObject{ProductID: productID})
		for operation, err := range map[string]error{
			"RegisterProduct": errRegister, "GetProduct": errGet, "ChangeProductBrand": errChangeBrand,
			"CreateWorkspace": errCreate, "GetWorkspace": errGetWorkspace,
			"GrantMembership": errGrant, "GetMembership": errGetMembership, "RevokeMembership": errRevoke,
			"GrantProductRole": errGrantRole, "GetProductRole": errGetRole, "RevokeProductRole": errRevokeRole,
			"ConfigureAudience": errConfigure, "GetAudienceConfig": errGetConfig,
			"ChangeAudienceConfig": errChangeConfig, "RemoveAudienceConfig": errRemoveConfig,
			"ListProducts": errListProducts, "ListWorkspaces": errListWorkspaces,
			"ListMemberships": errListMemberships, "ListProductRoles": errListRoles, "ListAudienceConfigs": errListConfigs,
			"GetCaller": errGetCaller, "GetCallerInProduct": errGetStanding,
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
	for _, list := range []string{"ProductList", "WorkspaceList", "MembershipList", "ProductRoleAssignmentList", "AudienceConfigList"} {
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

	// So are the audiences and the environments of a product, and the
	// entry of the documentation, wherever a body names them.
	var audiences, environments []string
	for _, audience := range domain.Audiences() {
		audiences = append(audiences, string(audience))
	}
	for _, environment := range domain.Environments() {
		environments = append(environments, string(environment))
	}
	for _, values := range []struct {
		schema, member string
		model          []string
		named          []string
	}{
		{"Audience", "audience", audiences, []string{"AudienceConfigCreation", "AudienceConfig"}},
		{"Environment", "environment", environments, []string{"AudienceConfigCreation", "AudienceConfig"}},
		{"DocsURL", "docs_url", nil, []string{"AudienceConfigCreation", "AudienceConfigChange", "AudienceConfig"}},
	} {
		if schemas[values.schema] == nil {
			t.Errorf("the contract declares no %s", values.schema)
			continue
		}
		if values.model != nil {
			var declared []string
			for _, value := range schemas[values.schema].Value.Enum {
				name, _ := value.(string)
				declared = append(declared, name)
			}
			slices.Sort(declared)
			modelled := slices.Sorted(slices.Values(values.model))
			if !slices.Equal(declared, modelled) {
				t.Errorf("the contract declares the values of %s %v, the model %v", values.schema, declared, modelled)
			}
		}
		for _, object := range values.named {
			if schemas[object] == nil || schemas[object].Value.Properties[values.member] == nil {
				t.Errorf("the contract declares no %s of %s", values.member, object)
				continue
			}
			if got, want := schemas[object].Value.Properties[values.member].Ref, "#/components/schemas/"+values.schema; got != want {
				t.Errorf("the %s of %s is declared by %q, want %q", values.member, object, got, want)
			}
		}
	}
	if got := schemas["DocsURL"].Value.Pattern; got != domain.DocsURLPattern {
		t.Errorf("the contract states the pattern of an entry %q, the model %q", got, domain.DocsURLPattern)
	}
	if got := schemas["DocsURL"].Value.MaxLength; got == nil || *got != domain.DocsURLMaxLength {
		t.Errorf("the contract bounds an entry at %v characters, the model at %d", got, domain.DocsURLMaxLength)
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

	// What the contract lets through as an audience, an environment and an
	// entry of the documentation, the model configures, and an entry it
	// lets through in a change, the model changes to.
	answered := func(c call, made int) (contractAccepts bool) {
		t.Helper()
		c.Token = "alice"
		response := newService(t).do(t, c)
		if response.Code != made && response.Code != http.StatusBadRequest {
			t.Fatalf("status %d for %s %s: %s", response.Code, c.Body, c.Header, response.Body)
		}
		return response.Code == made
	}
	configureWith := func(audience, environment, docsURL string) (contractAccepts bool) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"audience": audience, "environment": environment, "docs_url": docsURL})
		if err != nil {
			t.Fatal(err)
		}
		return answered(call{Method: http.MethodPost, Target: audienceConfigs, Body: string(body)}, http.StatusCreated)
	}
	changeTo := func(docsURL string) (contractAccepts bool) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"docs_url": docsURL})
		if err != nil {
			t.Fatal(err)
		}
		return answered(call{Method: http.MethodPut, Target: audienceConfigs + "/" + audienceConfigID, Body: string(body), Header: fromVersion3}, http.StatusOK)
	}
	configure := func(audience domain.Audience, environment domain.Environment, docsURL string) error {
		_, err := domain.ConfigureAudience(audienceConfigID, product, audience, environment, docsURL, domain.Principal(alice), at)
		return err
	}

	for _, audience := range []string{
		"public", "partner", "internal", "", "Public", "PARTNER", " public", "internal\n", "everybody", "external", "sandbox",
	} {
		err := configure(domain.Audience(audience), domain.EnvironmentSandbox, partnerDocs)
		if contract, model := configureWith(audience, "sandbox", partnerDocs), err == nil; contract != model {
			t.Errorf("the audience %q: the contract accepts it %t, the model %t", audience, contract, model)
		}
	}
	for _, environment := range []string{
		"sandbox", "production", "", "Sandbox", "PRODUCTION", "production ", "staging", "prod", "test", "partner",
	} {
		err := configure(domain.AudiencePartner, domain.Environment(environment), partnerDocs)
		if contract, model := configureWith("partner", environment, partnerDocs), err == nil; contract != model {
			t.Errorf("the environment %q: the contract accepts it %t, the model %t", environment, contract, model)
		}
	}

	// An entry is bounded in characters on both sides, as a reference is.
	// That there is none is said by leaving the member out, below.
	docsURLs := []string{
		"https://docs.cargopai.example", "https://docs.cargopai.example/", partnerDocs, "https://docs.cargopai.example/partners#sandbox",
		"https://docs.cargopai.example:8443/partners", "https://docs.cargopai.example?audience=partner", "https://localhost",
		"https://192.168.0.1/docs", "https://xn--fiq228c.example/docs", "https://docs.cargopai.example/文档", "https://docs.cargopai.example/a%20b",
		"https://docs.cargopai.example/😀", "https://" + strings.Repeat("a", 63) + ".example", "https://" + strings.Repeat("a", 64) + ".example",
		"http://docs.cargopai.example", "ftp://docs.cargopai.example", "HTTPS://docs.cargopai.example", "//docs.cargopai.example", "docs.cargopai.example",
		"https://", "https:///docs", "https://-docs.example", "https://docs-.example", "https://docs..example", "https://.docs.example",
		"https://文档.example", "https://user:secret@docs.cargopai.example", "https://user@docs.cargopai.example",
		"https://docs.cargopai.example\\@evil.example", "https://docs.cargopai.example:", "https://docs.cargopai.example:123456",
		"https://docs.cargopai.example/a b", "https://docs.cargopai.example/a\tb", "https://docs.cargopai.example/a\nb",
		"https://docs.cargopai.example/\u00a0", "https://docs.cargopai.example/\u0085", "https://docs.cargopai.example/\u2028",
		"https://docs.cargopai.example/\u3000", " https://docs.cargopai.example", "https://docs.cargopai.example ",
		"https://docs.example/" + strings.Repeat("d", 2027), "https://docs.example/" + strings.Repeat("d", 2028),
		"https://docs.example/" + strings.Repeat("文", 2027), "https://docs.example/" + strings.Repeat("文", 2028),
	}
	sandbox, err := domain.ConfigureAudience(audienceConfigID, product, domain.AudiencePartner, domain.EnvironmentSandbox, "", domain.Principal(alice), at)
	if err != nil {
		t.Fatalf("configure the audience: %v", err)
	}
	for _, docsURL := range docsURLs {
		err := configure(domain.AudiencePartner, domain.EnvironmentSandbox, docsURL)
		if contract, model := configureWith("partner", "sandbox", docsURL), err == nil; contract != model {
			t.Errorf("the entry %q of a configuration: the contract accepts it %t, the model %t", docsURL, contract, model)
		}
		_, err = sandbox.ChangeDocsURL(product, docsURL, domain.Principal(alice), at)
		if contract, model := changeTo(docsURL), err == nil; contract != model {
			t.Errorf("the entry %q of a change: the contract accepts it %t, the model %t", docsURL, contract, model)
		}
	}
	if err := configure(domain.AudiencePartner, domain.EnvironmentSandbox, ""); err != nil ||
		!answered(call{Method: http.MethodPost, Target: audienceConfigs, Body: `{"audience":"partner","environment":"sandbox"}`}, http.StatusCreated) {
		t.Errorf("a configuration without an entry: the model refuses it (%v), or the contract does", err)
	}
	if _, err := partnerSandbox().ChangeDocsURL(product, "", domain.Principal(alice), at); err != nil ||
		!answered(call{Method: http.MethodPut, Target: audienceConfigs + "/" + audienceConfigID, Body: `{}`, Header: fromVersion3}, http.StatusOK) {
		t.Errorf("a change to no entry: the model refuses it (%v), or the contract does", err)
	}

	// The brand of a product is declared once for what a change sends and
	// once for what an answer shows, and both name its members through the
	// declarations the model is held to.
	for _, object := range []string{"BrandChange", "Brand"} {
		if schemas[object] == nil {
			t.Errorf("the contract declares no %s", object)
			continue
		}
		for member, declaration := range map[string]string{"name": "DisplayName", "logo_url": "LogoURL", "primary_color": "Color"} {
			property := schemas[object].Value.Properties[member]
			if property == nil {
				t.Errorf("the contract declares no %s of %s", member, object)
				continue
			}
			if got, want := property.Ref, "#/components/schemas/"+declaration; got != want {
				t.Errorf("the %s of %s is declared by %q, want %q", member, object, got, want)
			}
		}
		if got := schemas[object].Value.Required; !slices.Equal(got, []string{"name"}) {
			t.Errorf("%s requires %v, want its name and nothing else", object, got)
		}
	}
	if schemas["LogoURL"] == nil || schemas["Color"] == nil {
		t.Fatal("the contract declares no LogoURL or no Color")
	}
	if got := schemas["LogoURL"].Value.Pattern; got != domain.LogoURLPattern {
		t.Errorf("the contract states the pattern of a logo %q, the model %q", got, domain.LogoURLPattern)
	}
	if got := schemas["LogoURL"].Value.MaxLength; got == nil || *got != domain.LogoURLMaxLength {
		t.Errorf("the contract bounds a logo at %v characters, the model at %d", got, domain.LogoURLMaxLength)
	}
	if got := schemas["Color"].Value.Pattern; got != domain.ColorPattern {
		t.Errorf("the contract states the pattern of a colour %q, the model %q", got, domain.ColorPattern)
	}

	// What the contract lets through as the name, the logo and the colour
	// of a brand, the model takes for the brand of a product. That there is
	// no logo or no colour is said by leaving the member out, below.
	// One service answers them all: its use case changes nothing, so what
	// one request left says nothing to the next.
	branding := newService(t)
	rebrandWith := func(brand map[string]any) (contractAccepts bool) {
		t.Helper()
		body, err := json.Marshal(brand)
		if err != nil {
			t.Fatal(err)
		}
		response := branding.do(t, call{Method: http.MethodPut, Target: productBrand, Token: "alice", Body: string(body), Header: fromVersion4})
		if response.Code != http.StatusOK && response.Code != http.StatusBadRequest {
			t.Fatalf("status %d for the brand %s: %s", response.Code, body, response.Body)
		}
		return response.Code == http.StatusOK
	}
	rebrand := func(brand domain.Brand) error {
		_, err := product.ChangeBrand(brand, domain.Principal(alice), at)
		return err
	}
	for _, name := range names {
		err := rebrand(domain.Brand{Name: name})
		if contract, model := rebrandWith(map[string]any{"name": name}), err == nil; contract != model {
			t.Errorf("the name %q of a brand: the contract accepts it %t, the model %t", name, contract, model)
		}
	}
	for _, logoURL := range docsURLs {
		err := rebrand(domain.Brand{Name: "CargoPai Developers", LogoURL: logoURL})
		if contract, model := rebrandWith(map[string]any{"name": "CargoPai Developers", "logo_url": logoURL}), err == nil; contract != model {
			t.Errorf("the logo %q of a brand: the contract accepts it %t, the model %t", logoURL, contract, model)
		}
	}
	for _, colour := range []string{
		"#0A7B3C", "#0a7b3c", "#0A7b3c", "#000000", "#FFFFFF", "0A7B3C", "#0A7", "#0A7B3", "#0A7B3CF", "#0A7B3CFF", "#0A7B3G", "green", "rgb(10,123,60)",
		" #0A7B3C", "#0A7B3C ", "#0A7B3C\n", "\n#0A7B3C", "#0A7B3C#FFFFFF", "＃0A7B3C", "#", "#０A7B3C",
	} {
		err := rebrand(domain.Brand{Name: "CargoPai Developers", PrimaryColor: colour})
		if contract, model := rebrandWith(map[string]any{"name": "CargoPai Developers", "primary_color": colour}), err == nil; contract != model {
			t.Errorf("the colour %q of a brand: the contract accepts it %t, the model %t", colour, contract, model)
		}
	}
	if err := rebrand(domain.Brand{Name: "CargoPai Developers"}); err != nil || !rebrandWith(map[string]any{"name": "CargoPai Developers"}) {
		t.Errorf("a brand without a logo and without a colour: the model refuses it (%v), or the contract does", err)
	}
}
