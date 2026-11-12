package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
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

// A status the contract does not declare is not sent: a caller would branch
// on a value it was never told of.
func TestAStatusTheContractDoesNotDeclareIsNotSent(t *testing.T) {
	s := newService(t)
	s.governance.product.Status = "ARCHIVED"
	s.governance.workspace.Status = "ARCHIVED"

	for _, operation := range protected {
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
		for operation, err := range map[string]error{"RegisterProduct": errRegister, "GetProduct": errGet, "CreateWorkspace": errCreate, "GetWorkspace": errGetWorkspace} {
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
	if got := schemas["Reference"].Value.MaxLength; got == nil || *got != domain.ReferenceMaxLength {
		t.Errorf("the contract bounds a reference at %v characters, the model at %d", got, domain.ReferenceMaxLength)
	}
	// A principal is named by a reference and authenticates under its
	// subject. With two bounds, somebody could be named who can never
	// authenticate.
	if verifier, model := identity.SubjectMaxLength, domain.ReferenceMaxLength; verifier != model {
		t.Errorf("the verifier bounds a subject at %d characters, the model a reference at %d", verifier, model)
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

		_, err = product.AssignFirstOwners([]domain.Principal{{Issuer: issuer, Subject: reference}}, domain.Principal(admin))
		if contract, model := registerWith("cargopai", "CargoPai", "org-cargopai", reference), err == nil; contract != model {
			t.Errorf("the subject %q of an owner: the contract accepts it %t, the model %t", reference, contract, model)
		}
	}
}
