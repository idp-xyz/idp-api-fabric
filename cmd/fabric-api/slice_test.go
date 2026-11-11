package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/identity"
	"go.idp.xyz/idp-api-fabric/internal/platform/identity/devissuer"
	governancedomain "go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

// These tests run the whole slice as fabric-api assembles it: the transport,
// the validator and the token verifier of the product, the use cases, and a
// real PostgreSQL reached as the application role. Only the identity provider
// is a stand-in, and it is the development issuer, whose tokens are verified
// the way any issuer's are.

const audience = "fabric-api"

// stack is a running fabric-api on a database of its own.
type stack struct {
	t      *testing.T
	issuer *devissuer.Issuer
	server *httptest.Server
	// owner reads what the application role cannot: the audit trail and the
	// outbox.
	owner *pgx.Conn
	log   *lockedBuffer
}

// lockedBuffer is a log the server writes while the test reads.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// signingKey is generated once: it is slow under the race detector.
var signingKey = sync.OnceValues(devissuer.GenerateKey)

// start runs fabric-api with the subjects as platform administrators.
func start(t *testing.T, administrators ...string) *stack {
	t.Helper()

	key, err := signingKey()
	if err != nil {
		t.Fatalf("generate the signing key: %v", err)
	}
	keys := http.NewServeMux()
	provider := httptest.NewServer(keys)
	t.Cleanup(provider.Close)
	issuer, err := devissuer.New(provider.URL, key)
	if err != nil {
		t.Fatalf("build the issuer: %v", err)
	}
	keys.Handle("/", issuer.Handler())

	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	t.Cleanup(fabric.Close)

	configured := settings{identity: identity.Config{Issuer: issuer.URL(), Audience: audience, JWKSURL: issuer.JWKSURL()}}
	for _, subject := range administrators {
		configured.administrators = append(configured.administrators, governancedomain.Principal{Issuer: issuer.URL(), Subject: subject})
	}
	verifier, err := identity.NewVerifier(configured.identity)
	if err != nil {
		t.Fatalf("build the verifier: %v", err)
	}
	log := &lockedBuffer{}
	handler, err := compose(fabric, verifier, configured, slog.New(slog.NewTextHandler(log, nil)))
	if err != nil {
		t.Fatalf("compose the service: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return &stack{t: t, issuer: issuer, server: server, owner: pgtest.Connect(t, database.Migrator), log: log}
}

// token is an access token of the issuer for the subject, addressed to this
// deployment.
func (s *stack) token(subject string) string {
	s.t.Helper()

	token, err := s.issuer.Mint(devissuer.TokenRequest{Subject: subject, Audience: []string{audience}})
	if err != nil {
		s.t.Fatalf("mint a token for %s: %v", subject, err)
	}
	return token
}

// answer is what the service answered: the status, the headers and the
// members of the JSON body.
type answer struct {
	status int
	header http.Header
	body   map[string]any
}

func (a answer) code() string {
	code, _ := a.body["error_code"].(string)
	return code
}

func (a answer) id(member string) string {
	id, _ := a.body[member].(string)
	return id
}

// call sends one request as the subject, or without credentials if the
// subject is empty.
func (s *stack) call(method, path, subject, body string) answer {
	s.t.Helper()

	token := ""
	if subject != "" {
		token = s.token(subject)
	}
	return s.callWith(method, path, token, body)
}

func (s *stack) callWith(method, path, token, body string) answer {
	s.t.Helper()

	request, err := http.NewRequest(method, s.server.URL+path, strings.NewReader(body))
	if err != nil {
		s.t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		s.t.Fatalf("%s %s: read the answer: %v", method, path, err)
	}
	got := answer{status: response.StatusCode, header: response.Header}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &got.body); err != nil {
			s.t.Fatalf("%s %s answered %d with a body that is not a JSON object: %s", method, path, response.StatusCode, raw)
		}
	}
	return got
}

func registration(key string, owners ...string) func(s *stack) string {
	return func(s *stack) string {
		named := make([]map[string]string, 0, len(owners))
		for _, subject := range owners {
			named = append(named, map[string]string{"issuer": s.issuer.URL(), "subject": subject})
		}
		body, _ := json.Marshal(map[string]any{
			"product_key": key, "display_name": "Product " + key,
			"owner_organization": map[string]string{"identity_provider": s.issuer.URL(), "organization": "org-" + key},
			"owners":             named,
		})
		return string(body)
	}
}

// register registers a product as the administrator and returns its
// identifier.
func (s *stack) register(administrator, key string, owners ...string) string {
	s.t.Helper()

	got := s.call(http.MethodPost, "/v1/products", administrator, registration(key, owners...)(s))
	if got.status != http.StatusCreated {
		s.t.Fatalf("register %s: status %d %v", key, got.status, got.body)
	}
	return got.id("product_id")
}

// audited is one row of the audit trail.
type audited struct {
	Subject      string
	Action       string
	Outcome      string
	Rule         string
	ProductID    string
	ResourceType string
	ResourceID   string
}

func (s *stack) audit() []audited {
	s.t.Helper()

	rows, err := s.owner.Query(s.t.Context(), `
		SELECT actor_subject, action, outcome, rule_id, coalesce(product_id::text, ''), resource_type, coalesce(resource_id::text, '')
		FROM platformoperations.audit_record ORDER BY recorded_at, action, outcome`)
	if err != nil {
		s.t.Fatalf("read the audit trail: %v", err)
	}
	found, err := pgx.CollectRows(rows, pgx.RowToStructByPos[audited])
	if err != nil {
		s.t.Fatalf("read the audit trail: %v", err)
	}
	return found
}

func (s *stack) count(table string) int {
	s.t.Helper()

	var rows int
	if err := s.owner.QueryRow(s.t.Context(), `SELECT count(*) FROM `+table).Scan(&rows); err != nil {
		s.t.Fatalf("count %s: %v", table, err)
	}
	return rows
}

// The happy path of UC-01: a platform administrator registers a product and
// names its owner, the owner gives it a workspace, and both read back what
// they may. Every write leaves its row, its audit record and its event, and
// all of them carry the identifier of the request that caused them.
func TestAProductIsRegisteredAndGivenAWorkspace(t *testing.T) {
	s := start(t, "platform-admin")

	registered := s.call(http.MethodPost, "/v1/products", "platform-admin", registration("cargopai", "alice")(s))
	if registered.status != http.StatusCreated {
		t.Fatalf("register: status %d %v", registered.status, registered.body)
	}
	productID := registered.id("product_id")
	if got, want := registered.header.Get("Location"), "/v1/products/"+productID; got != want {
		t.Errorf("Location %q, want %q", got, want)
	}
	if registered.body["product_key"] != "cargopai" || registered.body["status"] != "ACTIVE" {
		t.Errorf("the product is %v", registered.body)
	}

	// What is read back is what the registration answered.
	for _, reader := range []string{"platform-admin", "alice"} {
		read := s.call(http.MethodGet, registered.header.Get("Location"), reader, "")
		if read.status != http.StatusOK || fmt.Sprint(read.body) != fmt.Sprint(registered.body) {
			t.Errorf("%s reads %d %v, want what the registration answered: %v", reader, read.status, read.body, registered.body)
		}
		if got := read.header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control %q, want no-store", got)
		}
	}

	created := s.call(http.MethodPost, "/v1/products/"+productID+"/workspaces", "alice",
		`{"workspace_key":"api-engineering","display_name":"API engineering"}`)
	if created.status != http.StatusCreated {
		t.Fatalf("create the workspace: status %d %v", created.status, created.body)
	}
	workspaceID := created.id("workspace_id")
	if created.body["product_id"] != productID || created.body["workspace_key"] != "api-engineering" {
		t.Errorf("the workspace is %v", created.body)
	}
	read := s.call(http.MethodGet, created.header.Get("Location"), "alice", "")
	if read.status != http.StatusOK || fmt.Sprint(read.body) != fmt.Sprint(created.body) {
		t.Errorf("the owner reads %d %v, want what the creation answered: %v", read.status, read.body, created.body)
	}

	wantAudit := []audited{
		{"platform-admin", "product.register", "SUCCEEDED", "AC-08", productID, "Product", productID},
		{"alice", "workspace.manage", "SUCCEEDED", "AC-09", productID, "Workspace", workspaceID},
	}
	if got := s.audit(); !slices.Equal(got, wantAudit) {
		t.Errorf("the audit trail holds\n %+v\nwant\n %+v", got, wantAudit)
	}

	// The role of the owner says who granted it.
	var grantedBy string
	if err := s.owner.QueryRow(t.Context(),
		`SELECT granted_by_subject FROM productgovernance.product_role_assignment
		 WHERE product_id = $1 AND principal_subject = 'alice' AND role = 'product_owner'`, productID).Scan(&grantedBy); err != nil || grantedBy != "platform-admin" {
		t.Errorf("the owner's role was granted by %q (%v), want the administrator", grantedBy, err)
	}

	// One event per write, in the order of the writes, in the partition of
	// the product, each with the correlation identifier of its audit record.
	type event struct {
		Type        string
		Scope       string
		Partition   string
		Subject     string
		Correlation string
	}
	rows, err := s.owner.Query(t.Context(),
		`SELECT event_type, scope, partition_key, subject, correlation_id FROM bento.outbox ORDER BY enqueued_seq`)
	if err != nil {
		t.Fatalf("read the outbox: %v", err)
	}
	events, err := pgx.CollectRows(rows, pgx.RowToStructByPos[event])
	if err != nil {
		t.Fatalf("read the outbox: %v", err)
	}
	if len(events) != 2 ||
		events[0].Type != "fabric.productgovernance.product.registered" || events[0].Subject != "product/"+productID ||
		events[1].Type != "fabric.productgovernance.workspace.created" || events[1].Subject != "workspace/"+workspaceID {
		t.Fatalf("the outbox holds %+v, want the registration and then the creation", events)
	}
	for _, stored := range events {
		if stored.Scope != productID || stored.Partition != productID {
			t.Errorf("the event %s is scoped to %q in partition %q, want the product", stored.Type, stored.Scope, stored.Partition)
		}
		var audits int
		if err := s.owner.QueryRow(t.Context(),
			`SELECT count(*) FROM platformoperations.audit_record WHERE correlation_id = $1`, stored.Correlation).Scan(&audits); err != nil || audits != 1 {
			t.Errorf("the event %s shares its correlation identifier with %d audit records (%v), want 1", stored.Type, audits, err)
		}
	}
}

// AC-08: only a platform administrator registers a product.
func TestOnlyAPlatformAdministratorRegistersAProduct(t *testing.T) {
	s := start(t, "platform-admin")
	s.register("platform-admin", "parcel", "bob")

	for _, caller := range []string{"mallory", "bob"} {
		got := s.call(http.MethodPost, "/v1/products", caller, registration("intruder", caller)(s))
		if got.status != http.StatusForbidden || got.code() != "FORBIDDEN" {
			t.Errorf("%s registers a product: %d %v, want 403 FORBIDDEN", caller, got.status, got.body)
		}
	}
	if products := s.count("productgovernance.product"); products != 1 {
		t.Errorf("%d products, want 1", products)
	}
	refused := s.audit()[1:]
	want := []audited{
		{"bob", "product.register", "DENIED", "DENY-03", "", "Product", ""},
		{"mallory", "product.register", "DENIED", "DENY-03", "", "Product", ""},
	}
	slices.SortFunc(refused, func(a, b audited) int { return strings.Compare(a.Subject, b.Subject) })
	if !slices.Equal(refused, want) {
		t.Errorf("the refusals are recorded as\n %+v\nwant\n %+v", refused, want)
	}
}

// A deployment that names no administrator has none: nothing is defaulted.
func TestNobodyRegistersAProductWhereNobodyAdministersThePlatform(t *testing.T) {
	s := start(t)

	got := s.call(http.MethodPost, "/v1/products", "platform-admin", registration("cargopai", "alice")(s))
	if got.status != http.StatusForbidden {
		t.Fatalf("status %d %v, want 403", got.status, got.body)
	}
	if products := s.count("productgovernance.product"); products != 0 {
		t.Errorf("%d products, want none", products)
	}
}

// FR-001: a key that is taken is refused, whoever asks and however often.
func TestAProductKeyIsTakenOnce(t *testing.T) {
	s := start(t, "platform-admin")
	first := s.register("platform-admin", "cargopai", "alice")

	again := s.call(http.MethodPost, "/v1/products", "platform-admin", registration("cargopai", "bob")(s))
	if again.status != http.StatusConflict || again.code() != "PRODUCT_KEY_TAKEN" {
		t.Fatalf("a second registration of the key: %d %v, want 409 PRODUCT_KEY_TAKEN", again.status, again.body)
	}
	if products, roles, events := s.count("productgovernance.product"), s.count("productgovernance.product_role_assignment"), s.count("bento.outbox"); products != 1 || roles != 1 || events != 1 {
		t.Errorf("%d products, %d roles and %d events, want one of each", products, roles, events)
	}
	// bob was named in the registration that failed. He owns nothing.
	if got := s.call(http.MethodGet, "/v1/products/"+first, "bob", ""); got.status != http.StatusNotFound {
		t.Errorf("the owner named in the refused registration reads the product: %d", got.status)
	}

	// Several at once: one wins, the others find the key taken.
	const racers = 6
	statuses := make(chan int, racers)
	for range racers {
		go func() {
			statuses <- s.call(http.MethodPost, "/v1/products", "platform-admin", registration("parcel", "bob")(s)).status
		}()
	}
	won := 0
	for range racers {
		switch status := <-statuses; status {
		case http.StatusCreated:
			won++
		case http.StatusConflict:
		default:
			t.Errorf("a registration answered %d, want 201 or 409", status)
		}
	}
	if won != 1 || s.count("productgovernance.product") != 2 {
		t.Errorf("%d registrations of one key succeeded and %d products exist, want 1 and 2", won, s.count("productgovernance.product"))
	}
}

// An owner under an issuer this deployment does not trust could never
// authenticate: the product would have nobody to manage it.
func TestAnOwnerIsNamedUnderTheTrustedIssuer(t *testing.T) {
	s := start(t, "platform-admin")

	body := strings.Replace(registration("cargopai", "alice")(s), `"issuer":"`+s.issuer.URL()+`"`, `"issuer":"https://other.example"`, 1)
	got := s.call(http.MethodPost, "/v1/products", "platform-admin", body)
	if got.status != http.StatusUnprocessableEntity || got.code() != "OWNER_ISSUER_NOT_TRUSTED" {
		t.Fatalf("status %d %v, want 422 OWNER_ISSUER_NOT_TRUSTED", got.status, got.body)
	}
	if rows := s.count("productgovernance.product") + s.count("platformoperations.audit_record") + s.count("bento.outbox"); rows != 0 {
		t.Errorf("a refused registration left %d rows", rows)
	}
}

// NFR-01, AC-09, F05 §5.2: what belongs to one product is not reachable from
// another. To whoever holds no role in a product, the product and everything
// inside it does not exist, and the answer does not differ from the answer
// for something that really does not.
func TestWhatBelongsToOneProductIsOutOfReachOfAnother(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "alice")
	parcel := s.register("platform-admin", "parcel", "bob")

	created := s.call(http.MethodPost, "/v1/products/"+cargopai+"/workspaces", "alice", `{"workspace_key":"api","display_name":"API"}`)
	if created.status != http.StatusCreated {
		t.Fatalf("create the workspace: %d %v", created.status, created.body)
	}
	workspace := created.id("workspace_id")
	missing := "00000000-0000-4000-8000-000000000000"

	// The answer for what exists and is withheld is the answer for what is
	// missing, member for member, apart from the identifier of the request.
	same := func(withheld, absent answer) bool {
		delete(withheld.body, "trace_id")
		delete(absent.body, "trace_id")
		return withheld.status == absent.status && fmt.Sprint(withheld.body) == fmt.Sprint(absent.body)
	}

	for _, stranger := range []string{"bob", "mallory"} {
		t.Run(stranger+" and CargoPai", func(t *testing.T) {
			withheld := s.call(http.MethodGet, "/v1/products/"+cargopai, stranger, "")
			absent := s.call(http.MethodGet, "/v1/products/"+missing, stranger, "")
			if withheld.status != http.StatusNotFound || withheld.code() != "PRODUCT_NOT_FOUND" || !same(withheld, absent) {
				t.Errorf("reads the product: %d %v; a missing product: %d %v", withheld.status, withheld.body, absent.status, absent.body)
			}

			withheld = s.call(http.MethodGet, "/v1/products/"+cargopai+"/workspaces/"+workspace, stranger, "")
			absent = s.call(http.MethodGet, "/v1/products/"+missing+"/workspaces/"+workspace, stranger, "")
			if withheld.status != http.StatusNotFound || withheld.code() != "PRODUCT_NOT_FOUND" || !same(withheld, absent) {
				t.Errorf("reads the workspace: %d %v; under a missing product: %d %v", withheld.status, withheld.body, absent.status, absent.body)
			}

			withheld = s.call(http.MethodPost, "/v1/products/"+cargopai+"/workspaces", stranger, `{"workspace_key":"intruder","display_name":"Intruder"}`)
			absent = s.call(http.MethodPost, "/v1/products/"+missing+"/workspaces", stranger, `{"workspace_key":"intruder","display_name":"Intruder"}`)
			if withheld.status != http.StatusNotFound || withheld.code() != "PRODUCT_NOT_FOUND" || !same(withheld, absent) {
				t.Errorf("creates a workspace: %d %v; in a missing product: %d %v", withheld.status, withheld.body, absent.status, absent.body)
			}
		})
	}

	// The owner of Parcel does not reach the workspace of CargoPai by putting
	// its identifier under his own product.
	got := s.call(http.MethodGet, "/v1/products/"+parcel+"/workspaces/"+workspace, "bob", "")
	if got.status != http.StatusNotFound || got.code() != "WORKSPACE_NOT_FOUND" {
		t.Errorf("a workspace of CargoPai under Parcel: %d %v, want 404 WORKSPACE_NOT_FOUND", got.status, got.body)
	}

	// The platform administrator registered both products. That gives no
	// power inside them and no view of what is inside.
	got = s.call(http.MethodPost, "/v1/products/"+cargopai+"/workspaces", "platform-admin", `{"workspace_key":"intruder","display_name":"Intruder"}`)
	if got.status != http.StatusForbidden || got.code() != "FORBIDDEN" {
		t.Errorf("the administrator creates a workspace: %d %v, want 403 FORBIDDEN", got.status, got.body)
	}
	got = s.call(http.MethodGet, "/v1/products/"+cargopai+"/workspaces/"+workspace, "platform-admin", "")
	if got.status != http.StatusNotFound || got.code() != "WORKSPACE_NOT_FOUND" {
		t.Errorf("the administrator reads a workspace: %d %v, want 404 WORKSPACE_NOT_FOUND", got.status, got.body)
	}

	if workspaces := s.count("productgovernance.workspace"); workspaces != 1 {
		t.Errorf("%d workspaces, want the one its owner created", workspaces)
	}

	// Every refusal of something that exists is on record, with who asked
	// and under which rule. What does not exist was not withheld from
	// anybody and is not.
	refused := map[audited]int{}
	for _, record := range s.audit() {
		if record.Outcome == "DENIED" {
			refused[record]++
		}
	}
	want := map[audited]int{
		{"bob", "product.read", "DENIED", "DENY-03", cargopai, "Product", cargopai}:                 1,
		{"bob", "workspace.read", "DENIED", "DENY-03", cargopai, "Workspace", workspace}:            1,
		{"bob", "workspace.manage", "DENIED", "DENY-03", cargopai, "Workspace", ""}:                 1,
		{"mallory", "product.read", "DENIED", "DENY-03", cargopai, "Product", cargopai}:             1,
		{"mallory", "workspace.read", "DENIED", "DENY-03", cargopai, "Workspace", workspace}:        1,
		{"mallory", "workspace.manage", "DENIED", "DENY-03", cargopai, "Workspace", ""}:             1,
		{"platform-admin", "workspace.manage", "DENIED", "DENY-03", cargopai, "Workspace", ""}:      1,
		{"platform-admin", "workspace.read", "DENIED", "DENY-03", cargopai, "Workspace", workspace}: 1,
	}
	if fmt.Sprint(refused) != fmt.Sprint(want) {
		t.Errorf("the refusals on record are\n %v\nwant\n %v", refused, want)
	}
}

// A workspace key is unique inside its product and nowhere else.
func TestAWorkspaceKeyIsTakenOncePerProduct(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "alice")
	parcel := s.register("platform-admin", "parcel", "alice")
	workspace := `{"workspace_key":"api","display_name":"API"}`

	if got := s.call(http.MethodPost, "/v1/products/"+cargopai+"/workspaces", "alice", workspace); got.status != http.StatusCreated {
		t.Fatalf("the first workspace: %d %v", got.status, got.body)
	}
	again := s.call(http.MethodPost, "/v1/products/"+cargopai+"/workspaces", "alice", workspace)
	if again.status != http.StatusConflict || again.code() != "WORKSPACE_KEY_TAKEN" {
		t.Errorf("the key again in the same product: %d %v, want 409 WORKSPACE_KEY_TAKEN", again.status, again.body)
	}
	if got := s.call(http.MethodPost, "/v1/products/"+parcel+"/workspaces", "alice", workspace); got.status != http.StatusCreated {
		t.Errorf("the key in another product: %d %v, want 201", got.status, got.body)
	}
	if workspaces := s.count("productgovernance.workspace"); workspaces != 2 {
		t.Errorf("%d workspaces, want 2", workspaces)
	}
}

// SEC-01, SEC-02: a token is accepted only if the trusted issuer signed it
// for this deployment and it is still valid. Nothing is served to anything
// else, and nothing is written.
func TestOnlyATokenOfTheTrustedIssuerForThisDeploymentIsAccepted(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "alice")
	before := s.count("platformoperations.audit_record")

	mint := func(request devissuer.TokenRequest) string {
		t.Helper()
		token, err := s.issuer.Mint(request)
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		return token
	}
	otherKey, err := devissuer.GenerateKey()
	if err != nil {
		t.Fatalf("generate another key: %v", err)
	}
	impostor, err := devissuer.New(s.issuer.URL(), otherKey)
	if err != nil {
		t.Fatalf("build the impostor: %v", err)
	}
	forged, err := impostor.Mint(devissuer.TokenRequest{Subject: "platform-admin", Audience: []string{audience}})
	if err != nil {
		t.Fatalf("mint as the impostor: %v", err)
	}
	valid := s.token("alice")

	refused := map[string]string{
		// An ID token is addressed to the client that asked for it.
		"addressed to another service": mint(devissuer.TokenRequest{Subject: "alice", Audience: []string{"some-client"}}),
		"expired":                      mint(devissuer.TokenRequest{Subject: "alice", Audience: []string{audience}, IssuedAt: time.Now().Add(-time.Hour), TTL: time.Minute}),
		"not valid yet":                mint(devissuer.TokenRequest{Subject: "alice", Audience: []string{audience}, IssuedAt: time.Now().Add(time.Hour)}),
		"signed with a key the issuer does not publish": forged,
		"altered after it was signed":                   valid[:len(valid)-6] + "AAAAAA",
		"not a token":                                   "alice",
	}
	for name, token := range refused {
		t.Run(name, func(t *testing.T) {
			for _, request := range []struct{ method, path, body string }{
				{http.MethodGet, "/v1/products/" + product, ""},
				{http.MethodPost, "/v1/products", registration("intruder", "mallory")(s)},
				{http.MethodPost, "/v1/products/" + product + "/workspaces", `{"workspace_key":"intruder","display_name":"Intruder"}`},
			} {
				got := s.callWith(request.method, request.path, token, request.body)
				if got.status != http.StatusUnauthorized || got.code() != "TOKEN_INVALID" {
					t.Errorf("%s %s: %d %v, want 401 TOKEN_INVALID", request.method, request.path, got.status, got.body)
				}
				if got.header.Get("WWW-Authenticate") != `Bearer error="invalid_token"` {
					t.Errorf("WWW-Authenticate %q", got.header.Get("WWW-Authenticate"))
				}
			}
		})
	}
	if got := s.callWith(http.MethodGet, "/v1/products/"+product, "", ""); got.status != http.StatusUnauthorized || got.code() != "CREDENTIALS_MISSING" {
		t.Errorf("without credentials: %d %v, want 401 CREDENTIALS_MISSING", got.status, got.body)
	}

	if products, workspaces := s.count("productgovernance.product"), s.count("productgovernance.workspace"); products != 1 || workspaces != 0 {
		t.Errorf("%d products and %d workspaces, want 1 and none", products, workspaces)
	}
	// Nobody was identified, so nothing was written to the audit trail: what
	// an anonymous caller can make the process store is nothing.
	if after := s.count("platformoperations.audit_record"); after != before {
		t.Errorf("unauthenticated requests wrote %d audit records", after-before)
	}
	// The log has every refusal, and none of the tokens.
	logged := s.log.String()
	if strings.Count(logged, "authentication refused") < len(refused)*3 {
		t.Errorf("the log has %d refusals, want at least %d", strings.Count(logged, "authentication refused"), len(refused)*3)
	}
	for _, token := range refused {
		if len(token) > 20 && strings.Contains(logged, token) {
			t.Error("the log holds a token")
		}
	}
}

// The identity provider being out of reach is not the caller's fault and is
// not answered as one. No request is let in on the strength of it either.
func TestAProviderOutOfReachLetsNobodyIn(t *testing.T) {
	key, err := signingKey()
	if err != nil {
		t.Fatal(err)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(down.Close)
	issuer, err := devissuer.New(down.URL, key)
	if err != nil {
		t.Fatal(err)
	}

	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	t.Cleanup(fabric.Close)
	configured := settings{
		identity:       identity.Config{Issuer: issuer.URL(), Audience: audience, JWKSURL: issuer.JWKSURL()},
		administrators: []governancedomain.Principal{{Issuer: issuer.URL(), Subject: "platform-admin"}},
	}
	verifier, err := identity.NewVerifier(configured.identity)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := compose(fabric, verifier, configured, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	s := &stack{t: t, issuer: issuer, server: server, owner: pgtest.Connect(t, database.Migrator), log: &lockedBuffer{}}

	got := s.call(http.MethodPost, "/v1/products", "platform-admin", registration("cargopai", "alice")(s))
	if got.status != http.StatusServiceUnavailable || got.code() != "IDENTITY_PROVIDER_UNAVAILABLE" {
		t.Fatalf("status %d %v, want 503 IDENTITY_PROVIDER_UNAVAILABLE", got.status, got.body)
	}
	if products := s.count("productgovernance.product"); products != 0 {
		t.Errorf("%d products were registered while nobody could be identified", products)
	}
	// What needs no caller is still served.
	if health := s.callWith(http.MethodGet, "/healthz", "", ""); health.status != http.StatusOK {
		t.Errorf("GET /healthz: %d, want 200", health.status)
	}
}

// A request that does not match the contract changes nothing.
func TestARequestThatDoesNotMatchTheContractChangesNothing(t *testing.T) {
	s := start(t, "platform-admin")

	for name, body := range map[string]string{
		"a key that is not a key":  strings.Replace(registration("cargopai", "alice")(s), `"cargopai"`, `"Cargo Pai"`, 1),
		"no owner":                 registration("cargopai")(s),
		"a member nobody declared": strings.Replace(registration("cargopai", "alice")(s), `"owners"`, `"status":"SUSPENDED","owners"`, 1),
		"not JSON":                 `{"product_key"`,
	} {
		got := s.call(http.MethodPost, "/v1/products", "platform-admin", body)
		if got.status != http.StatusBadRequest || got.code() != "REQUEST_INVALID" {
			t.Errorf("%s: %d %v, want 400 REQUEST_INVALID", name, got.status, got.body)
		}
	}
	if rows := s.count("productgovernance.product") + s.count("platformoperations.audit_record") + s.count("bento.outbox"); rows != 0 {
		t.Errorf("requests that do not match the contract left %d rows", rows)
	}
}

// The process answers /healthz and /version to anybody, and HEAD as GET.
func TestTheAnonymousOperationsAnswerAnybody(t *testing.T) {
	s := start(t)

	if got := s.callWith(http.MethodGet, "/healthz", "", ""); got.status != http.StatusOK || got.body["status"] != "ok" {
		t.Errorf("GET /healthz: %d %v", got.status, got.body)
	}
	if got := s.callWith(http.MethodGet, "/version", "", ""); got.status != http.StatusOK || got.body["version"] == nil {
		t.Errorf("GET /version: %d %v", got.status, got.body)
	}
	head, err := http.Head(s.server.URL + "/healthz")
	if err != nil {
		t.Fatalf("HEAD /healthz: %v", err)
	}
	defer head.Body.Close()
	if body, _ := io.ReadAll(head.Body); head.StatusCode != http.StatusOK || len(body) != 0 {
		t.Errorf("HEAD /healthz: %d with %d bytes of body, want 200 and none", head.StatusCode, len(body))
	}
}

// An administrator that cannot be a principal is a mistake in the
// configuration, and the process does not start with it.
func TestAnAdministratorNobodyCouldBeKeepsTheServiceFromStarting(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	t.Cleanup(fabric.Close)

	configured := settings{
		identity:       identity.Config{Issuer: "https://issuer.example", Audience: audience, JWKSURL: "https://issuer.example/jwks"},
		administrators: []governancedomain.Principal{{Issuer: "https://issuer.example", Subject: ""}},
	}
	verifier, err := identity.NewVerifier(configured.identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compose(fabric, verifier, configured, slog.New(slog.DiscardHandler)); err == nil ||
		!strings.Contains(err.Error(), "IDP_FABRIC_PLATFORM_ADMIN_SUBJECTS") {
		t.Fatalf("got %v, want an error that names the setting", err)
	}
}
