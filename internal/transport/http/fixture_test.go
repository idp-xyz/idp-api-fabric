package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"go.idp.xyz/idp-api-fabric/internal/platform/buildinfo"
	"go.idp.xyz/idp-api-fabric/internal/platform/identity"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

var testBuild = buildinfo.Info{Version: "v-test", Commit: "c-test", BuiltAt: "t-test"}

const issuer = "https://issuer.example"

var (
	admin = identity.Principal{Issuer: issuer, Subject: "platform-admin"}
	alice = identity.Principal{Issuer: issuer, Subject: "alice"}
)

// tokens stands in for the identity provider. A token is the subject it is
// about, which keeps the requests of the tests readable; "down" is a token
// that cannot be judged because the keys of the provider are out of reach.
// The real verifier is tested in internal/platform/identity.
type tokens struct{}

func (tokens) Verify(_ context.Context, token string) (identity.Principal, error) {
	switch token {
	case admin.Subject:
		return admin, nil
	case alice.Subject:
		return alice, nil
	case "down":
		return identity.Principal{}, fmt.Errorf("%w: the issuer does not answer", identity.ErrKeySetUnavailable)
	}
	return identity.Principal{}, identity.ErrSignatureInvalid
}

const (
	productID        = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
	workspaceID      = "7d2e9f10-3a4b-4c5d-8e6f-0a1b2c3d4e5f"
	membershipID     = "3f8b1c2d-4e5a-4b6c-9d7e-8f9a0b1c2d3e"
	roleAssignmentID = "5a6b7c8d-9e0f-4a1b-8c2d-3e4f5a6b7c8d"

	// memberships is where the memberships of the workspace are granted.
	memberships = "/v1/products/" + productID + "/workspaces/" + workspaceID + "/memberships"
	// roleAssignments is where the roles of the product are granted.
	roleAssignments = "/v1/products/" + productID + "/role-assignments"
)

var createdAt = time.Date(2026, 11, 11, 9, 0, 0, 123456000, time.UTC)

func cargopai() domain.Product {
	return domain.Product{
		ID: productID, Key: "cargopai", DisplayName: "CargoPai", Status: domain.ProductActive,
		Owner:   domain.OrganizationRef{IdentityProvider: issuer, Organization: "org-cargopai"},
		Version: 1, CreatedAt: createdAt, CreatedBy: domain.Principal(admin), UpdatedAt: createdAt,
	}
}

func engineering() domain.Workspace {
	return domain.Workspace{
		ID: workspaceID, ProductID: productID, Key: "api-engineering", DisplayName: "API engineering",
		Status: domain.WorkspaceActive, Version: 1, CreatedAt: createdAt, CreatedBy: domain.Principal(alice), UpdatedAt: createdAt,
	}
}

// maintainer is bob's membership of the workspace, as alice granted it.
func maintainer() domain.Membership {
	return domain.Membership{
		ID: membershipID, ProductID: productID, WorkspaceID: workspaceID,
		Principal: domain.Principal{Issuer: issuer, Subject: "bob"}, Role: domain.RoleWorkspaceMaintainer,
		Version: 1, GrantedAt: createdAt, GrantedBy: domain.Principal(alice),
	}
}

// ownership is bob's role of an owner of the product, as alice granted it.
func ownership() domain.RoleAssignment {
	return domain.RoleAssignment{
		ID: roleAssignmentID, ProductID: productID,
		Principal: domain.Principal{Issuer: issuer, Subject: "bob"}, Role: domain.RoleProductOwner,
		Version: 1, GrantedAt: createdAt, GrantedBy: domain.Principal(alice),
	}
}

// governance stands in for the use cases of Product Governance. It keeps
// what it was asked and answers what the test set. The real use cases are
// tested where they live, and the whole of it in cmd/fabric-api.
type governance struct {
	product    domain.Product
	workspace  domain.Workspace
	membership domain.Membership
	assignment domain.RoleAssignment
	err        error
	// A list answers a page of so many items, each the one above of its
	// kind, and says that the next page starts behind next. With no item
	// the page holds no list at all, as a store that found nothing may
	// return it.
	items int
	next  *ports.Position

	registered        []application.RegisterProduct
	productsRead      []application.GetProduct
	created           []application.CreateWorkspace
	spacesRead        []application.GetWorkspace
	granted           []application.GrantMembership
	membershipsRead   []application.GetMembership
	revoked           []application.RevokeMembership
	rolesGranted      []application.GrantProductRole
	rolesRead         []application.GetProductRole
	rolesRevoked      []application.RevokeProductRole
	productsListed    []application.ListProducts
	spacesListed      []application.ListWorkspaces
	membershipsListed []application.ListMemberships
	rolesListed       []application.ListProductRoles
}

func newGovernance() *governance {
	return &governance{product: cargopai(), workspace: engineering(), membership: maintainer(), assignment: ownership(), items: 1}
}

func (g *governance) calls() int {
	return len(g.registered) + len(g.productsRead) + len(g.created) + len(g.spacesRead) +
		len(g.granted) + len(g.membershipsRead) + len(g.revoked) +
		len(g.rolesGranted) + len(g.rolesRead) + len(g.rolesRevoked) +
		len(g.productsListed) + len(g.spacesListed) + len(g.membershipsListed) + len(g.rolesListed)
}

func (g *governance) RegisterProduct(_ context.Context, command application.RegisterProduct) (domain.Product, error) {
	g.registered = append(g.registered, command)
	return g.product, g.err
}

func (g *governance) GetProduct(_ context.Context, query application.GetProduct) (domain.Product, error) {
	g.productsRead = append(g.productsRead, query)
	return g.product, g.err
}

func (g *governance) CreateWorkspace(_ context.Context, command application.CreateWorkspace) (domain.Workspace, error) {
	g.created = append(g.created, command)
	return g.workspace, g.err
}

func (g *governance) GetWorkspace(_ context.Context, query application.GetWorkspace) (domain.Workspace, error) {
	g.spacesRead = append(g.spacesRead, query)
	return g.workspace, g.err
}

func (g *governance) GrantMembership(_ context.Context, command application.GrantMembership) (domain.Membership, error) {
	g.granted = append(g.granted, command)
	return g.membership, g.err
}

func (g *governance) GetMembership(_ context.Context, query application.GetMembership) (domain.Membership, error) {
	g.membershipsRead = append(g.membershipsRead, query)
	return g.membership, g.err
}

func (g *governance) RevokeMembership(_ context.Context, command application.RevokeMembership) error {
	g.revoked = append(g.revoked, command)
	return g.err
}

func (g *governance) GrantProductRole(_ context.Context, command application.GrantProductRole) (domain.RoleAssignment, error) {
	g.rolesGranted = append(g.rolesGranted, command)
	return g.assignment, g.err
}

func (g *governance) GetProductRole(_ context.Context, query application.GetProductRole) (domain.RoleAssignment, error) {
	g.rolesRead = append(g.rolesRead, query)
	return g.assignment, g.err
}

func (g *governance) RevokeProductRole(_ context.Context, command application.RevokeProductRole) error {
	g.rolesRevoked = append(g.rolesRevoked, command)
	return g.err
}

// page is the page a list of the stand-in answers: so many times the item,
// and where the next page starts.
func page[T any](g *governance, item T) application.Page[T] {
	if g.err != nil {
		return application.Page[T]{}
	}
	var items []T
	for range g.items {
		items = append(items, item)
	}
	return application.Page[T]{Items: items, Next: g.next}
}

func (g *governance) ListProducts(_ context.Context, query application.ListProducts) (application.Page[domain.Product], error) {
	g.productsListed = append(g.productsListed, query)
	return page(g, g.product), g.err
}

func (g *governance) ListWorkspaces(_ context.Context, query application.ListWorkspaces) (application.Page[domain.Workspace], error) {
	g.spacesListed = append(g.spacesListed, query)
	return page(g, g.workspace), g.err
}

func (g *governance) ListMemberships(_ context.Context, query application.ListMemberships) (application.Page[domain.Membership], error) {
	g.membershipsListed = append(g.membershipsListed, query)
	return page(g, g.membership), g.err
}

func (g *governance) ListProductRoles(_ context.Context, query application.ListProductRoles) (application.Page[domain.RoleAssignment], error) {
	g.rolesListed = append(g.rolesListed, query)
	return page(g, g.assignment), g.err
}

// service is the transport under test, with what it logged.
type service struct {
	handler    http.Handler
	governance *governance
	log        *bytes.Buffer
}

func newService(t *testing.T) *service {
	t.Helper()

	s := &service{governance: newGovernance(), log: &bytes.Buffer{}}
	handler, err := NewHandler(Config{
		Logger: slog.New(slog.NewTextHandler(s.log, nil)), Build: testBuild, Verifier: tokens{}, Governance: s.governance,
	})
	if err != nil {
		t.Fatalf("build the handler: %v", err)
	}
	s.handler = handler
	return s
}

// call is one request. Token is the bearer token; Header adds to it.
type call struct {
	Method string
	Target string
	Token  string
	Body   string
	Header map[string]string
}

// do sends the request and returns the answer, after checking it against the
// contract: whatever the transport answers is something the contract
// declares.
func (s *service) do(t *testing.T, c call) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(c.Method, c.Target, strings.NewReader(c.Body))
	if c.Body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}
	for name, value := range c.Header {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, request)

	checkAgainstContract(t, c, recorder)
	return recorder
}

// checkAgainstContract fails the test if the answer is not one the contract
// declares for the operation: its status, its headers and its body. An
// answer to a request the contract does not route is not checked, since the
// contract says nothing about it.
func checkAgainstContract(t *testing.T, c call, recorder *httptest.ResponseRecorder) {
	t.Helper()

	contract, err := loadContract()
	if err != nil {
		t.Fatalf("load the contract: %v", err)
	}
	router, err := gorillamux.NewRouter(contract)
	if err != nil {
		t.Fatalf("route the contract: %v", err)
	}
	method := c.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	request := httptest.NewRequest(method, c.Target, nil)
	route, pathParams, err := router.FindRoute(request)
	if errors.Is(err, routers.ErrPathNotFound) || errors.Is(err, routers.ErrMethodNotAllowed) {
		return
	}
	if err != nil {
		t.Fatalf("find the route of %s %s: %v", c.Method, c.Target, err)
	}

	body := recorder.Body.Bytes()
	if c.Method == http.MethodHead {
		// The server drops the body of the answer to a HEAD; the recorder of
		// a test does not.
		return
	}
	err = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{Request: request, PathParams: pathParams, Route: route},
		Status:                 recorder.Code,
		Header:                 recorder.Header(),
		Body:                   io.NopCloser(bytes.NewReader(body)),
		Options:                &openapi3filter.Options{IncludeResponseStatus: true},
	})
	if err != nil {
		t.Errorf("%s %s answered %d, which the contract does not declare that way: %v\n%s", c.Method, c.Target, recorder.Code, err, body)
	}
}

// members decodes a JSON object body into its members.
func members(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("the body is not a JSON object: %v\n%s", err, recorder.Body)
	}
	return decoded
}

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// wantProblem checks that the answer is the problem with the status and the
// code, and nothing more than a problem: a type, a title, the status, the
// code, the identifier of the request and, if detail is not empty, that
// detail. It returns the identifier of the request.
func wantProblem(t *testing.T, recorder *httptest.ResponseRecorder, status int, code, detail string) string {
	t.Helper()

	if recorder.Code != status {
		t.Fatalf("status %d, want %d: %s", recorder.Code, status, recorder.Body)
	}
	if got := recorder.Header().Get("Content-Type"); got != ProblemContentType {
		t.Errorf("Content-Type %q, want %s", got, ProblemContentType)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", got)
	}

	got := members(t, recorder)
	traceID, _ := got["trace_id"].(string)
	if !uuid.MatchString(traceID) {
		t.Errorf("trace_id %v, want the identifier of the request", got["trace_id"])
	}
	want := map[string]any{
		"type": "about:blank", "title": http.StatusText(status), "status": float64(status),
		"error_code": code, "trace_id": traceID,
	}
	if detail != "" {
		want["detail"] = detail
	}
	if len(got) != len(want) {
		t.Errorf("problem members %v, want %v", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("problem member %s is %v, want %v", name, got[name], value)
		}
	}
	return traceID
}
