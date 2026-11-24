package main

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/identity/devissuer"
)

// These tests run what the caller is told of itself as fabric-api assembles
// it: the transport, the validator and the token verifier of the product, the
// use cases of Product Governance, and a real PostgreSQL reached as the
// application role.

// callerPath is where the caller reads who it is, and standingPath where it
// reads what it holds in the product.
const callerPath = "/v1/caller"

func standingPath(product string) string { return "/v1/products/" + product + "/caller" }

// readCaller is what the subject is told of who it is.
func (s *stack) readCaller(subject string) answer {
	s.t.Helper()
	return s.call(http.MethodGet, callerPath, subject, "")
}

// readStanding is what the subject is told it holds in the product.
func (s *stack) readStanding(product, subject string) answer {
	s.t.Helper()
	return s.call(http.MethodGet, standingPath(product), subject, "")
}

// callerBody is what a caller is told of itself.
func (s *stack) callerBody(subject string, administrator bool) map[string]any {
	return map[string]any{
		"principal":              map[string]any{"issuer": s.issuer.URL(), "subject": subject},
		"platform_administrator": administrator,
	}
}

// heldWorkspace is what a caller holds in one workspace.
type heldWorkspace struct {
	id    string
	roles []string
}

func anyOf(values []string) []any {
	listed := make([]any, 0, len(values))
	for _, value := range values {
		listed = append(listed, value)
	}
	return listed
}

// standingBody is what a caller that holds the roles in the product and in the
// workspaces is told: the roles of the product, and for each workspace its roles
// there, the workspaces by identifier. What it holds nowhere is an empty list.
func standingBody(product string, roles []string, workspaces ...heldWorkspace) map[string]any {
	slices.SortFunc(workspaces, func(a, b heldWorkspace) int { return strings.Compare(a.id, b.id) })
	listed := make([]any, 0, len(workspaces))
	for _, held := range workspaces {
		listed = append(listed, map[string]any{"workspace_id": held.id, "roles": anyOf(held.roles)})
	}
	return map[string]any{"product_id": product, "roles": anyOf(roles), "workspaces": listed}
}

// requireStanding fails the test unless the subject is told what the body says
// of what it holds in the product, as a private answer.
func (s *stack) requireStanding(product, subject string, want map[string]any) {
	s.t.Helper()

	got := s.readStanding(product, subject)
	if got.status != http.StatusOK || !reflect.DeepEqual(got.body, want) {
		s.t.Errorf("%s is told %d %v of what it holds in the product, want 200 %v", subject, got.status, got.body, want)
	}
	if cache := got.header.Get("Cache-Control"); cache != "no-store" {
		s.t.Errorf("%s is told what it holds with Cache-Control %q, want no-store", subject, cache)
	}
}

// heldInTables is what the tables say the subject holds in the product: the
// roles of its assignments in force, and the roles of its memberships in force
// by workspace. It is read as the role that owns the tables, apart from the
// service.
func (s *stack) heldInTables(product, subject string) map[string]any {
	s.t.Helper()

	rows, err := s.owner.Query(s.t.Context(), `
		SELECT role FROM productgovernance.product_role_assignment
		WHERE product_id = $1 AND principal_subject = $2 AND revoked_at IS NULL ORDER BY role`, product, subject)
	if err != nil {
		s.t.Fatalf("read the roles of %s: %v", subject, err)
	}
	roles, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		s.t.Fatalf("read the roles of %s: %v", subject, err)
	}
	rows, err = s.owner.Query(s.t.Context(), `
		SELECT workspace_id::text, role FROM productgovernance.workspace_membership
		WHERE product_id = $1 AND principal_subject = $2 AND revoked_at IS NULL ORDER BY workspace_id, role`, product, subject)
	if err != nil {
		s.t.Fatalf("read the memberships of %s: %v", subject, err)
	}
	type membership struct{ Workspace, Role string }
	memberships, err := pgx.CollectRows(rows, pgx.RowToStructByPos[membership])
	if err != nil {
		s.t.Fatalf("read the memberships of %s: %v", subject, err)
	}
	var workspaces []heldWorkspace
	for _, held := range memberships {
		if last := len(workspaces) - 1; last >= 0 && workspaces[last].id == held.Workspace {
			workspaces[last].roles = append(workspaces[last].roles, held.Role)
		} else {
			workspaces = append(workspaces, heldWorkspace{held.Workspace, []string{held.Role}})
		}
	}
	return standingBody(product, roles, workspaces...)
}

// Whoever is authenticated is told who it is, from its token and from the
// deployment, and nobody else: the operations take no parameter that could name
// another principal, no body, and no header counts. A token that is not
// accepted is not told anything, whichever operation it asks, and nothing is
// recorded for it or for what it is told.
func TestTheCallerIsToldWhoItIsAndNobodyElse(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "alice")
	before := s.count("platformoperations.audit_record")

	// A principal that holds nothing anywhere is told who it is all the same.
	for _, subject := range []string{"alice", "platform-admin", "mallory", "bob"} {
		got := s.readCaller(subject)
		if want := s.callerBody(subject, subject == "platform-admin"); got.status != http.StatusOK || !reflect.DeepEqual(got.body, want) {
			t.Errorf("%s is told %d %v of who it is, want 200 %v", subject, got.status, got.body, want)
		}
		if cache := got.header.Get("Cache-Control"); cache != "no-store" {
			t.Errorf("%s is told who it is with Cache-Control %q, want no-store", subject, cache)
		}
	}

	// Nothing a request says names somebody else.
	token := s.token("alice")
	for _, target := range []string{
		callerPath + "?subject=bob", callerPath + "?issuer=" + s.issuer.URL() + "&subject=bob", callerPath + "?principal",
		standingPath(cargopai) + "?subject=bob", standingPath(cargopai) + "?issuer=" + s.issuer.URL() + "&subject=bob",
	} {
		if got := s.callWith(http.MethodGet, target, token, ""); got.status != http.StatusBadRequest || got.code() != "REQUEST_INVALID" {
			t.Errorf("GET %s answered %d %v, want 400 REQUEST_INVALID", target, got.status, got.body)
		}
	}
	for target, own := range map[string]map[string]any{
		callerPath:             s.callerBody("alice", false),
		standingPath(cargopai): standingBody(cargopai, []string{"product_owner"}),
	} {
		if got := s.callWith(http.MethodGet, target, token, `{"subject":"bob"}`); got.status != http.StatusBadRequest || got.code() != "REQUEST_INVALID" {
			t.Errorf("GET %s with a body answered %d %v, want 400 REQUEST_INVALID", target, got.status, got.body)
		}
		// Headers that say who somebody else is are not read.
		headers := http.Header{"Authorization": {"Bearer " + token}, "X-Forwarded-User": {"bob"}, "X-Subject": {"bob"}}
		if got := s.callWithHeaders(http.MethodGet, target, "", "", headers); got.status != http.StatusOK || !reflect.DeepEqual(got.body, own) {
			t.Errorf("GET %s with the headers of somebody else answered %d %v, want alice's own answer %v", target, got.status, got.body, own)
		}
	}

	// A token that is not accepted is told nothing, of who it is or of what it
	// holds.
	mint := func(request devissuer.TokenRequest) string {
		t.Helper()
		minted, err := s.issuer.Mint(request)
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		return minted
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
	refused := map[string]string{
		"addressed to another service": mint(devissuer.TokenRequest{Subject: "alice", Audience: []string{"some-client"}}),
		"expired":                      mint(devissuer.TokenRequest{Subject: "alice", Audience: []string{audience}, IssuedAt: time.Now().Add(-time.Hour), TTL: time.Minute}),
		"not valid yet":                mint(devissuer.TokenRequest{Subject: "alice", Audience: []string{audience}, IssuedAt: time.Now().Add(time.Hour)}),
		"signed with another key":      forged,
		"altered after it was signed":  token[:len(token)-6] + "AAAAAA",
		"not a token":                  "alice",
	}
	for name, refusedToken := range refused {
		for _, target := range []string{callerPath, standingPath(cargopai)} {
			got := s.callWith(http.MethodGet, target, refusedToken, "")
			if got.status != http.StatusUnauthorized || got.code() != "TOKEN_INVALID" || got.body["principal"] != nil || got.body["roles"] != nil {
				t.Errorf("%s, GET %s: %d %v, want 401 TOKEN_INVALID and nothing of anybody", name, target, got.status, got.body)
			}
			if got.header.Get("WWW-Authenticate") != `Bearer error="invalid_token"` {
				t.Errorf("%s, GET %s: WWW-Authenticate %q", name, target, got.header.Get("WWW-Authenticate"))
			}
		}
	}
	for _, target := range []string{callerPath, standingPath(cargopai)} {
		if got := s.callWith(http.MethodGet, target, "", ""); got.status != http.StatusUnauthorized || got.code() != "CREDENTIALS_MISSING" {
			t.Errorf("GET %s without credentials: %d %v, want 401 CREDENTIALS_MISSING", target, got.status, got.body)
		}
	}

	// Nobody who is told who it is, or what it holds, leaves a record, and
	// neither does a request that is refused before an operation runs.
	if after := s.count("platformoperations.audit_record"); after != before {
		t.Errorf("telling the callers who they are wrote %d audit records, want none", after-before)
	}
}

// Every role there is, of a product and of a workspace, is shown to the one that
// holds it, in the product it holds it in: an owner and a member of nothing,
// someone that holds only a security role or only the auditor's, a member of
// one workspace in each of its roles, and someone that holds several of each.
// What each is told is what the tables say it holds.
func TestEachRoleIsShownToItsHolderInTheProduct(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai, parcel := s.register("platform-admin", "cargopai", "alice"), s.register("platform-admin", "parcel", "bob")
	engineering, operations := s.createWorkspace(cargopai, "alice", "engineering"), s.createWorkspace(cargopai, "alice", "operations")
	sorting := s.createWorkspace(parcel, "bob", "sorting")

	// One holder of each role of a product, and one of each role of a
	// workspace.
	for subject, role := range map[string]string{"sam": "security_admin", "rita": "security_reviewer", "ada": "internal_auditor"} {
		s.requireThat(s.grantRole(cargopai, "alice", subject, role), http.StatusCreated, "")
	}
	s.join(cargopai, engineering, "alice", "mia", "workspace_maintainer")
	s.join(cargopai, engineering, "alice", "uma", "internal_author")
	s.join(cargopai, operations, "alice", "vic", "internal_reviewer")
	s.join(cargopai, operations, "alice", "pat", "internal_publisher")
	// dan holds two roles of the product and three roles in two workspaces; wendy
	// holds a role in Parcel and a workspace of CargoPai, and neither is the
	// other's.
	for _, role := range []string{"security_admin", "internal_auditor"} {
		s.requireThat(s.grantRole(cargopai, "alice", "dan", role), http.StatusCreated, "")
	}
	s.join(cargopai, operations, "alice", "dan", "workspace_maintainer")
	s.join(cargopai, engineering, "alice", "dan", "internal_reviewer")
	s.join(cargopai, engineering, "alice", "dan", "internal_author")
	s.requireThat(s.grantRole(parcel, "bob", "wendy", "security_reviewer"), http.StatusCreated, "")
	s.join(cargopai, engineering, "alice", "wendy", "internal_publisher")
	s.join(parcel, sorting, "bob", "wendy", "internal_author")
	before := s.count("platformoperations.audit_record")

	for _, c := range []struct {
		subject    string
		product    string
		roles      []string
		workspaces []heldWorkspace
	}{
		{"alice", cargopai, []string{"product_owner"}, nil},
		{"sam", cargopai, []string{"security_admin"}, nil},
		{"rita", cargopai, []string{"security_reviewer"}, nil},
		{"ada", cargopai, []string{"internal_auditor"}, nil},
		{"mia", cargopai, nil, []heldWorkspace{{engineering, []string{"workspace_maintainer"}}}},
		{"uma", cargopai, nil, []heldWorkspace{{engineering, []string{"internal_author"}}}},
		{"vic", cargopai, nil, []heldWorkspace{{operations, []string{"internal_reviewer"}}}},
		{"pat", cargopai, nil, []heldWorkspace{{operations, []string{"internal_publisher"}}}},
		{"dan", cargopai, []string{"internal_auditor", "security_admin"}, []heldWorkspace{
			{operations, []string{"workspace_maintainer"}},
			{engineering, []string{"internal_author", "internal_reviewer"}},
		}},
		{"wendy", cargopai, nil, []heldWorkspace{{engineering, []string{"internal_publisher"}}}},
		{"wendy", parcel, []string{"security_reviewer"}, []heldWorkspace{{sorting, []string{"internal_author"}}}},
		{"bob", parcel, []string{"product_owner"}, nil},
		{"platform-admin", cargopai, nil, nil},
	} {
		t.Run(c.subject+" in "+map[string]string{cargopai: "CargoPai", parcel: "Parcel"}[c.product], func(t *testing.T) {
			want := standingBody(c.product, c.roles, c.workspaces...)
			s.requireStanding(c.product, c.subject, want)
			// What each is told is what the tables say, and no more.
			if tables := s.heldInTables(c.product, c.subject); !reflect.DeepEqual(tables, want) {
				t.Errorf("the tables say %v of %s, the test %v", tables, c.subject, want)
			}
			// The workspaces come by identifier, the roles by name, each once.
			got := s.readStanding(c.product, c.subject)
			ids := []string{}
			for _, held := range got.body["workspaces"].([]any) {
				ids = append(ids, held.(map[string]any)["workspace_id"].(string))
			}
			if !slices.IsSorted(ids) {
				t.Errorf("the workspaces come as %v, want them by identifier", ids)
			}
		})
	}
	if after := s.count("platformoperations.audit_record"); after != before {
		t.Errorf("telling the holders what they hold wrote %d audit records, want none", after-before)
	}
}

// What the caller holds is read when it asks, and never kept from one request
// to the next, nor carried by its token: under the very token it used before, a
// membership or a role that was revoked is not in the next answer, the last
// of them makes the caller a stranger to the product, and what is granted again
// is told again.
func TestWhatIsRevokedIsGoneAtTheNextReadUnderTheSameToken(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "alice")
	engineering := s.createWorkspace(cargopai, "alice", "engineering")
	const missing = "00000000-0000-4000-8000-000000000000"

	role := s.grantRole(cargopai, "alice", "frank", "security_admin")
	s.requireThat(role, http.StatusCreated, "")
	membership := s.join(cargopai, engineering, "alice", "frank", "internal_author")
	carols := s.join(cargopai, engineering, "alice", "carol", "workspace_maintainer")
	tokens := map[string]string{"frank": s.token("frank"), "carol": s.token("carol")}

	read := func(subject, product string) answer {
		return s.callWith(http.MethodGet, standingPath(product), tokens[subject], "")
	}
	holds := func(subject string, want map[string]any) {
		t.Helper()
		if got := read(subject, cargopai); got.status != http.StatusOK || !reflect.DeepEqual(got.body, want) {
			t.Errorf("%s is told %d %v, want 200 %v", subject, got.status, got.body, want)
		}
	}
	stranger := func(subject, when string) {
		t.Helper()
		withheld, absent := read(subject, cargopai), read(subject, missing)
		if withheld.status != http.StatusNotFound || withheld.code() != "PRODUCT_NOT_FOUND" || !sameAnswer(withheld, absent) {
			t.Errorf("%s, %s: %d %v; of a product that is missing: %d %v", subject, when, withheld.status, withheld.body, absent.status, absent.body)
		}
	}

	holds("frank", standingBody(cargopai, []string{"security_admin"}, heldWorkspace{engineering, []string{"internal_author"}}))
	holds("carol", standingBody(cargopai, nil, heldWorkspace{engineering, []string{"workspace_maintainer"}}))

	s.revoke(membership, "alice")
	holds("frank", standingBody(cargopai, []string{"security_admin"}))

	s.revoke(role.header.Get("Location"), "alice")
	stranger("frank", "once the last of what it held is revoked")
	holds("carol", standingBody(cargopai, nil, heldWorkspace{engineering, []string{"workspace_maintainer"}}))

	s.revoke(carols, "alice")
	stranger("carol", "once its membership is revoked")

	// Granted again, each is an assignment or a membership of its own.
	again := s.grantRole(cargopai, "alice", "frank", "security_admin")
	s.requireThat(again, http.StatusCreated, "")
	if again.header.Get("Location") == role.header.Get("Location") {
		t.Error("the role was granted again under the identifier of the one that was revoked")
	}
	holds("frank", standingBody(cargopai, []string{"security_admin"}))
	s.join(cargopai, engineering, "alice", "carol", "internal_reviewer")
	holds("carol", standingBody(cargopai, nil, heldWorkspace{engineering, []string{"internal_reviewer"}}))
}

// To whoever holds nothing in a product, the product does not exist: the
// answer is the one a product that is missing gets, member for member, and only
// the audit trail tells them apart. That is whoever owns another product, whoever
// never held anything, a former member and a former owner. Every refusal of
// what exists is on record, once, under the rule that refused it, in the trail of
// the product; a product that is not there was not withheld from anybody and is
// not; and what is answered leaves nothing.
func TestAStrangerIsToldNoSuchProductAsForAProductThatIsMissing(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "alice")
	s.register("platform-admin", "parcel", "bob")
	engineering := s.createWorkspace(cargopai, "alice", "engineering")
	s.join(cargopai, engineering, "alice", "carol", "workspace_maintainer")
	s.revoke(s.join(cargopai, engineering, "alice", "erin", "internal_author"), "alice")
	s.revoke(s.nameOwner(cargopai, "alice", "frank"), "alice")
	const missing = "00000000-0000-4000-8000-000000000000"
	before := s.count("platformoperations.audit_record")

	strangers := []string{"bob", "erin", "frank", "mallory"}
	for _, stranger := range strangers {
		withheld, absent := s.readStanding(cargopai, stranger), s.readStanding(missing, stranger)
		if withheld.status != http.StatusNotFound || withheld.code() != "PRODUCT_NOT_FOUND" || !sameAnswer(withheld, absent) {
			t.Errorf("%s reads what it holds in CargoPai: %d %v; in a product that is missing: %d %v",
				stranger, withheld.status, withheld.body, absent.status, absent.body)
		}
		if withheld.body["product_id"] != nil || withheld.body["roles"] != nil || withheld.body["workspaces"] != nil {
			t.Errorf("%s is told something of CargoPai: %v", stranger, withheld.body)
		}
	}
	// An identifier that is none is no product either, and not withheld.
	if got := s.call(http.MethodGet, standingPath("cargopai"), "mallory", ""); got.status != http.StatusBadRequest || got.code() != "REQUEST_INVALID" {
		t.Errorf("an identifier that is none: %d %v, want 400 REQUEST_INVALID", got.status, got.body)
	}

	// Whoever holds something is answered, and that is not recorded.
	for _, subject := range []string{"alice", "carol", "platform-admin"} {
		if got := s.readStanding(cargopai, subject); got.status != http.StatusOK {
			t.Errorf("%s reads what it holds in CargoPai: %d %v, want 200", subject, got.status, got.body)
		}
	}

	refused := s.recorded(func(record audited) bool { return record.Outcome == "DENIED" })
	want := map[audited]int{}
	for _, stranger := range strangers {
		want[audited{stranger, "product.read", "DENIED", "DENY-03", cargopai, "Product", cargopai}] = 1
	}
	if !reflect.DeepEqual(refused, want) {
		t.Errorf("the refusals on record are\n %v\nwant\n %v", refused, want)
	}
	if after := s.count("platformoperations.audit_record"); after != before+len(strangers) {
		t.Errorf("%d audit records were written, want one for each of the %d strangers and none else", after-before, len(strangers))
	}
}

// The platform administrator is told that it is one, and that it holds nothing
// in a product by being one: it knows the product is there, as it does when it
// reads the product, and is told so in two empty lists, however many workspaces
// the product has. What it is given, it holds, as anybody does. Somebody who
// owns a product and does not administer the platform is told that.
func TestThePlatformAdministratorIsOneAndHoldsNothingInAProduct(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "alice")
	s.createWorkspace(cargopai, "alice", "engineering")
	const missing = "00000000-0000-4000-8000-000000000000"

	for subject, administrator := range map[string]bool{"platform-admin": true, "alice": false} {
		if got := s.readCaller(subject); got.status != http.StatusOK || !reflect.DeepEqual(got.body, s.callerBody(subject, administrator)) {
			t.Errorf("%s is told %d %v of who it is, want administrator %t", subject, got.status, got.body, administrator)
		}
	}
	s.requireStanding(cargopai, "platform-admin", standingBody(cargopai, nil))
	if got := s.readStanding(missing, "platform-admin"); got.status != http.StatusNotFound || got.code() != "PRODUCT_NOT_FOUND" {
		t.Errorf("the administrator reads what it holds in a product that is missing: %d %v, want 404 PRODUCT_NOT_FOUND", got.status, got.body)
	}

	s.nameOwner(cargopai, "platform-admin", "platform-admin")
	s.requireStanding(cargopai, "platform-admin", standingBody(cargopai, []string{"product_owner"}))
	if got := s.readCaller("platform-admin"); !reflect.DeepEqual(got.body, s.callerBody("platform-admin", true)) {
		t.Errorf("the administrator that owns a product is told %v of who it is, want an administrator", got.body)
	}
	// Owning a product does not make a platform administrator.
	if got := s.readCaller("alice"); !reflect.DeepEqual(got.body, s.callerBody("alice", false)) {
		t.Errorf("the owner of a product is told %v of who it is, want that it is no administrator", got.body)
	}
	if refused := s.recorded(func(record audited) bool { return record.Outcome == "DENIED" }); len(refused) != 0 {
		t.Errorf("what the administrator was told left the refusals %v on record, want none", refused)
	}
}

// A product that takes no new work keeps what it holds, and tells it: asking
// what is held is not new work. A stranger is still told that it is not there.
func TestTheStandingInASuspendedProductIsStillRead(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "alice")
	engineering := s.createWorkspace(cargopai, "alice", "engineering")
	s.join(cargopai, engineering, "alice", "carol", "workspace_maintainer")
	s.requireThat(s.grantRole(cargopai, "alice", "frank", "internal_auditor"), http.StatusCreated, "")

	if _, err := s.owner.Exec(t.Context(), `UPDATE productgovernance.product SET status = 'SUSPENDED' WHERE product_id = $1`, cargopai); err != nil {
		t.Fatalf("suspend the product: %v", err)
	}
	if got := s.call(http.MethodGet, "/v1/products/"+cargopai, "alice", ""); got.body["status"] != "SUSPENDED" {
		t.Fatalf("the product is %v, want it suspended", got.body)
	}

	s.requireStanding(cargopai, "alice", standingBody(cargopai, []string{"product_owner"}))
	s.requireStanding(cargopai, "carol", standingBody(cargopai, nil, heldWorkspace{engineering, []string{"workspace_maintainer"}}))
	s.requireStanding(cargopai, "frank", standingBody(cargopai, []string{"internal_auditor"}))
	s.requireStanding(cargopai, "platform-admin", standingBody(cargopai, nil))
	if got := s.readStanding(cargopai, "mallory"); got.status != http.StatusNotFound || got.code() != "PRODUCT_NOT_FOUND" {
		t.Errorf("a stranger asks about a suspended product: %d %v, want 404 PRODUCT_NOT_FOUND", got.status, got.body)
	}
}

// Nothing is locked: what the caller holds is told while a revocation of it is
// in flight, as it was before the revocation, and the next answer after the
// revocation commits is the one without it. A read that took a lock on the roles
// of the caller would wait here until the revocation was over, as the reading of
// the audit trail does, which has the records to read under that role.
func TestTheStandingIsReadWithoutWaitingForARevocation(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "alice")
	role := s.grantRole(cargopai, "alice", "frank", "security_admin")
	s.requireThat(role, http.StatusCreated, "")

	holder := pgtest.Connect(t, s.database.Migrator)
	tx, err := holder.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	// The revocation is under way: it has changed the row and not committed.
	if _, err := tx.Exec(t.Context(), `
		UPDATE productgovernance.product_role_assignment
		SET revoked_at = now(), revoked_by_issuer = $2, revoked_by_subject = 'alice', role_assignment_version = role_assignment_version + 1
		WHERE role_assignment_id = $1`, last(role.header.Get("Location")), s.issuer.URL()); err != nil {
		t.Fatalf("revoke the role: %v", err)
	}

	answered := make(chan answer, 1)
	go func() { answered <- s.readStanding(cargopai, "frank") }()
	select {
	case got := <-answered:
		if want := standingBody(cargopai, []string{"security_admin"}); got.status != http.StatusOK || !reflect.DeepEqual(got.body, want) {
			t.Errorf("while the revocation is under way frank is told %d %v, want 200 %v", got.status, got.body, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the read waited for a revocation that had not committed")
	}

	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit the revocation: %v", err)
	}
	if got := s.readStanding(cargopai, "frank"); got.status != http.StatusNotFound || got.code() != "PRODUCT_NOT_FOUND" {
		t.Errorf("once the revocation is committed frank is told %d %v, want 404 PRODUCT_NOT_FOUND: it holds nothing else", got.status, got.body)
	}
}

// What the caller is a member of is told whole, however many workspaces that is:
// it is the caller's own, not a page of a list, and a page is a hundred at most.
// A hundred and twenty memberships of a hundred and twenty workspaces, twenty
// of them in two roles, are a hundred and twenty entries by identifier and no
// token for a next one.
func TestAMemberOfMoreWorkspacesThanAPageHoldsReadsThemAllAtOnce(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "alice")
	const workspaces, twice = 120, 20

	// The workspaces and the memberships are written as the role that owns the
	// tables: a hundred and twenty grants through the service would only be
	// slower, and say nothing more.
	issuer := s.issuer.URL()
	if _, err := s.owner.Exec(t.Context(), `
		INSERT INTO productgovernance.workspace
		    (workspace_id, product_id, workspace_key, display_name, status, aggregate_version, created_at, created_by_issuer, created_by_subject, updated_at)
		SELECT gen_random_uuid(), $1, 'bulk-' || lpad(n::text, 3, '0'), 'Workspace ' || n, 'ACTIVE', 1, now(), $2, 'alice', now()
		FROM generate_series(1, $3::int) AS n`, cargopai, issuer, workspaces); err != nil {
		t.Fatalf("write the workspaces: %v", err)
	}
	for _, role := range []struct {
		name  string
		limit string
	}{{"internal_author", ""}, {"internal_reviewer", " AND workspace_key <= 'bulk-020'"}} {
		if _, err := s.owner.Exec(t.Context(), `
			INSERT INTO productgovernance.workspace_membership
			    (membership_id, product_id, workspace_id, principal_issuer, principal_subject, role, membership_version, granted_at, granted_by_issuer, granted_by_subject)
			SELECT gen_random_uuid(), product_id, workspace_id, $2, 'wendy', $3, 1, now(), $2, 'alice'
			FROM productgovernance.workspace WHERE product_id = $1`+role.limit, cargopai, issuer, role.name); err != nil {
			t.Fatalf("write the memberships: %v", err)
		}
	}

	got := s.readStanding(cargopai, "wendy")
	if got.status != http.StatusOK {
		t.Fatalf("wendy is told %d %v, want 200", got.status, got.body)
	}
	if len(got.body) != 3 || got.body["next_page_token"] != nil {
		t.Errorf("the answer says %v, want the product, the roles and the workspaces, and no token for a next page", got.body)
	}
	listed, _ := got.body["workspaces"].([]any)
	if len(listed) != workspaces {
		t.Fatalf("wendy is told of %d workspaces, want all %d", len(listed), workspaces)
	}
	var ids []string
	roles := 0
	for _, entry := range listed {
		held := entry.(map[string]any)
		ids = append(ids, held["workspace_id"].(string))
		roles += len(held["roles"].([]any))
	}
	if !slices.IsSorted(ids) || len(slices.Compact(slices.Clone(ids))) != workspaces {
		t.Errorf("the workspaces come as %v, want each once, by identifier", ids)
	}
	if roles != workspaces+twice {
		t.Errorf("wendy is told of %d roles in them, want %d", roles, workspaces+twice)
	}
	if tables := s.heldInTables(cargopai, "wendy"); !reflect.DeepEqual(tables, got.body) {
		t.Errorf("the tables say something else than the answer of what wendy holds")
	}
}
