package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	operationsdomain "go.idp.xyz/idp-api-fabric/internal/platformoperations/domain"
	operationsports "go.idp.xyz/idp-api-fabric/internal/platformoperations/ports"
	governance "go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
)

// These tests run the reading of the audit trail as fabric-api assembles it:
// the transport, the validator, the use cases of Product Governance and
// Platform Operations, and a real PostgreSQL reached as the application role.

// auditPath is where the audit records of the product are read.
func auditPath(product string) string { return "/v1/products/" + product + "/audit-records" }

var auditIdentifier = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// grantRole makes the subject hold the role in the product, as the grantor,
// and answers as the service does.
func (s *stack) grantRole(product, grantor, subject, role string) answer {
	s.t.Helper()

	body, _ := json.Marshal(map[string]any{
		"principal": map[string]string{"issuer": s.issuer.URL(), "subject": subject}, "role": role,
	})
	return s.call(http.MethodPost, "/v1/products/"+product+"/role-assignments", grantor, string(body))
}

// requireThat fails the test unless the answer is the status with the error
// code, which is empty for an answer that is none.
func (s *stack) requireThat(got answer, status int, code string) {
	s.t.Helper()

	if got.status != status || got.code() != code {
		s.t.Fatalf("got %d %v, want status %d error code %q\n%s", got.status, got.body, status, code, s.log.String())
	}
}

// auditorOf makes the subject an auditor of the product, as its owner, and
// returns where the role is read and revoked.
func (s *stack) auditorOf(product, owner, subject string) string {
	s.t.Helper()

	got := s.grantRole(product, owner, subject, "internal_auditor")
	if got.status != http.StatusCreated {
		s.t.Fatalf("make %s an auditor: status %d %v", subject, got.status, got.body)
	}
	return got.header.Get("Location")
}

// trailed is one row of the audit trail as the table holds it.
type trailed struct {
	ID, Issuer, Subject, Action, Outcome, Rule string
	At                                         time.Time
	Product, ResourceType, ResourceID          string
	Correlation                                string
}

// trail is every record of the product as the table holds it, newest first:
// what the list is to answer, read as the role that owns the table.
func (s *stack) trail(product string) []trailed {
	s.t.Helper()

	rows, err := s.owner.Query(s.t.Context(), `
		SELECT audit_id::text, actor_issuer, actor_subject, action, outcome, rule_id, recorded_at,
		       coalesce(product_id::text, ''), resource_type, coalesce(resource_id::text, ''), correlation_id
		FROM platformoperations.audit_record WHERE product_id = $1
		ORDER BY recorded_at DESC, audit_id DESC`, product)
	if err != nil {
		s.t.Fatalf("read the trail of the product: %v", err)
	}
	found, err := pgx.CollectRows(rows, pgx.RowToStructByPos[trailed])
	if err != nil {
		s.t.Fatalf("read the trail of the product: %v", err)
	}
	for index := range found {
		found[index].At = found[index].At.UTC()
	}
	return found
}

// auditPagesOf reads a list to its end as the subject, a page of size records
// at a time, each page with the token the one before handed out. The target may
// have a query of its own: the page is asked for after it.
func (s *stack) auditPagesOf(target, subject string, size int) [][]map[string]any {
	s.t.Helper()

	separator := "?"
	if strings.Contains(target, "?") {
		separator = "&"
	}
	var pages [][]map[string]any
	next := ""
	for len(pages) < 500 {
		request := fmt.Sprintf("%s%spage_size=%d", target, separator, size)
		if next != "" {
			request += "&page_token=" + next
		}
		var items []map[string]any
		items, next = s.page(request, subject)
		pages = append(pages, items)
		if next == "" {
			return pages
		}
	}
	s.t.Fatalf("%s lists %s: the list does not end", subject, target)
	return nil
}

// listed is every record of the list as the subject reads it.
func (s *stack) listed(target, subject string, size int) []map[string]any {
	s.t.Helper()
	return slices.Concat(s.auditPagesOf(target, subject, size)...)
}

// idsOfItems is the audit identifiers of the records a list shows.
func idsOfItems(items []map[string]any) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		id, _ := item["audit_id"].(string)
		ids = append(ids, id)
	}
	return ids
}

func idsOfTrail(rows []trailed) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

// shows says what a record of a list shows otherwise than the row of the table
// it is, and is empty if it shows it as it is: every fact, and no resource
// where the row has none.
func shows(item map[string]any, row trailed) string {
	at, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(item["recorded_at"]))
	actor, _ := item["actor"].(map[string]any)
	resource, hasResource := item["resource_id"]
	members := 9
	if row.ResourceID != "" {
		members++
	}
	switch {
	case item["audit_id"] != row.ID:
		return "audit_id"
	case !at.Equal(row.At):
		return "recorded_at"
	case actor["issuer"] != row.Issuer || actor["subject"] != row.Subject || len(actor) != 2:
		return "actor"
	case item["action"] != row.Action || item["outcome"] != row.Outcome || item["rule_id"] != row.Rule:
		return "action, outcome or rule_id"
	case item["product_id"] != row.Product || item["resource_type"] != row.ResourceType || item["correlation_id"] != row.Correlation:
		return "product_id, resource_type or correlation_id"
	case row.ResourceID == "" && hasResource, row.ResourceID != "" && resource != row.ResourceID:
		return "resource_id"
	case len(item) != members:
		return "the members of the record"
	}
	return ""
}

// Only a principal that holds the role of an auditor in the product reads its
// audit records: not its owner, a member of its workspaces, whoever holds a
// security role in it, the administrator of the platform nor the auditor of
// another product. Whoever knows the product is told it may not; to anyone
// else the product is not there, as it is not for whoever asks for a product
// that is not. Each refusal is on record, once, in the trail of the product,
// and what was read leaves nothing.
func TestOnlyAnAuditorOfTheProductReadsItsAuditRecords(t *testing.T) {
	s := publicationFor(t)
	s.require(s.securityRole("security", "security_admin", "owner"), http.StatusCreated, "")
	s.require(s.securityRole("clearer", "security_reviewer", "owner"), http.StatusCreated, "")
	s.auditorOf(s.product, "owner", "auditor")
	parcel := s.register("platform-admin", "parcel", "parcel-owner")
	s.auditorOf(parcel, "parcel-owner", "other-auditor")
	path := auditPath(s.product)
	const missing = "3e1c6a9d-74f2-4b0a-98c3-b2e5f4a6c7d8"

	// The auditor reads. What it reads leaves no record.
	before := len(s.audit())
	read := s.call(http.MethodGet, path, "auditor", "")
	if read.status != http.StatusOK || read.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the auditor reads: %d %v, want 200 and no-store", read.status, read.body)
	}
	if items, _ := read.body["items"].([]any); len(items) == 0 {
		t.Fatalf("the auditor reads no record of a product that has a history: %v", read.body)
	}
	s.listed(path, "auditor", 2)
	if after := len(s.audit()); after != before {
		t.Errorf("reading the audit trail left %d records, want none", after-before)
	}

	// Whoever knows the product and is no auditor of it.
	knows := []string{"owner", "maintainer", "author", "coauthor", "reviewer", "publisher", "security", "clearer", "platform-admin"}
	// Whoever does not: nothing in the product, or something in another.
	strangers := []string{"stranger", "parcel-owner", "other-auditor"}
	traces := map[string]string{}
	for _, subject := range knows {
		got := s.call(http.MethodGet, path, subject, "")
		s.require(got, http.StatusForbidden, "FORBIDDEN")
		traces[subject], _ = got.body["trace_id"].(string)
	}
	for _, subject := range strangers {
		got := s.call(http.MethodGet, path, subject, "")
		s.require(got, http.StatusNotFound, "PRODUCT_NOT_FOUND")
		traces[subject], _ = got.body["trace_id"].(string)
		absent := s.call(http.MethodGet, auditPath(missing), subject, "")
		s.require(absent, http.StatusNotFound, "PRODUCT_NOT_FOUND")
		if !sameAnswer(got, absent) {
			t.Errorf("%s is told of a product that is withheld %v and of one that is missing %v", subject, got.body, absent.body)
		}
	}
	// A product that is not there is not there for an auditor and an
	// administrator either.
	for _, subject := range []string{"auditor", "platform-admin"} {
		s.require(s.call(http.MethodGet, auditPath(missing), subject, ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	}
	s.require(s.call(http.MethodGet, path, "", ""), http.StatusUnauthorized, "CREDENTIALS_MISSING")

	// Every refusal of a product that is there is on record, once, as a refusal
	// for lack of a role, of the audit records, without a resource. The
	// products that are not there, and the request that came without
	// credentials, left none.
	want := map[audited]int{}
	for _, subject := range slices.Concat(knows, strangers) {
		want[audited{subject, "audit.search", "DENIED", "DENY-03", s.product, "AuditRecord", ""}] = 1
	}
	if got := s.recorded(func(record audited) bool { return record.Action == "audit.search" }); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("the audit trail holds the refusals\n %v\nwant\n %v", got, want)
	}

	// The trail of the product holds who tried to read it, and the auditor reads
	// that: by the identifier of the request, which is the trace of the problem.
	for subject, trace := range traces {
		found := s.listed(path+"?correlation_id="+url.QueryEscape(trace), "auditor", 100)
		if len(found) != 1 {
			t.Errorf("the request of %s, %s, is %d records of the trail, want 1", subject, trace, len(found))
			continue
		}
		if actor, _ := found[0]["actor"].(map[string]any); actor["subject"] != subject || found[0]["outcome"] != "DENIED" ||
			found[0]["action"] != "audit.search" || found[0]["rule_id"] != "DENY-03" || found[0]["resource_type"] != "AuditRecord" {
			t.Errorf("the record of the request of %s is %v", subject, found[0])
		}
		if _, hasResource := found[0]["resource_id"]; hasResource {
			t.Errorf("the refusal of %s names the resource %v, want none", subject, found[0]["resource_id"])
		}
	}
	if refusals := s.listed(path+"?action=audit.search&outcome=DENIED", "auditor", 5); len(refusals) != len(traces) {
		t.Errorf("the auditor reads %d refusals to read, want %d", len(refusals), len(traces))
	}
}

// An auditor reads the records of its product and of no other, every fact of
// each, as the trail holds them. What belongs to no product, such as the
// refusal of a registration, is read by nobody here.
func TestAnAuditorReadsTheRecordsOfItsProductAndNoOther(t *testing.T) {
	s := start(t, "platform-admin")
	cargopai := s.register("platform-admin", "cargopai", "owner")
	parcel := s.register("platform-admin", "parcel", "parcel-owner")
	engineering := s.createWorkspace(cargopai, "owner", "engineering")
	sorting := s.createWorkspace(parcel, "parcel-owner", "sorting")
	s.createWorkspace(cargopai, "owner", "operations")
	s.createWorkspace(parcel, "parcel-owner", "dispatch")
	s.join(cargopai, engineering, "owner", "author", "internal_author")
	s.join(parcel, sorting, "parcel-owner", "sorter", "internal_author")
	s.auditorOf(cargopai, "owner", "auditor")
	s.auditorOf(parcel, "parcel-owner", "parcel-auditor")
	// A refusal that concerns no product: nobody but the administrator
	// registers one. It is the newest record there is, and it is in no list.
	s.requireThat(s.call(http.MethodPost, "/v1/products", "owner", registration("cargopai-2", "owner")(s)), http.StatusForbidden, "FORBIDDEN")
	s.requireThat(s.call(http.MethodGet, "/v1/products/"+parcel, "intruder", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	s.requireThat(s.call(http.MethodGet, "/v1/products/"+cargopai, "intruder", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")

	rows, err := s.owner.Query(t.Context(), `SELECT audit_id::text FROM platformoperations.audit_record WHERE product_id IS NULL`)
	var platformScope []string
	if err == nil {
		platformScope, err = pgx.CollectRows(rows, pgx.RowTo[string])
	}
	if err != nil || len(platformScope) == 0 {
		t.Fatalf("the records of no product: %v (%v), want the refusal of a registration", platformScope, err)
	}

	for product, auditor := range map[string]string{cargopai: "auditor", parcel: "parcel-auditor"} {
		other := cargopai
		if product == cargopai {
			other = parcel
		}
		want := s.trail(product)
		if len(want) < 6 {
			t.Fatalf("the trail of %s holds %d records, want a history", product, len(want))
		}
		for _, size := range []int{1, 3, 100} {
			got := s.listed(auditPath(product), auditor, size)
			if !slices.Equal(idsOfItems(got), idsOfTrail(want)) {
				t.Errorf("in pages of %d the auditor of %s reads\n %v\nwant the trail, newest first,\n %v", size, product, idsOfItems(got), idsOfTrail(want))
				continue
			}
			for index, item := range got {
				if what := shows(item, want[index]); what != "" {
					t.Errorf("the record %d of %s shows %s as the trail does not hold it: %v, want %+v", index, product, what, item, want[index])
				}
			}
			for _, id := range slices.Concat(idsOfTrail(s.trail(other)), platformScope) {
				if slices.Contains(idsOfItems(got), id) {
					t.Errorf("the auditor of %s reads the record %s of another product or of none", product, id)
				}
			}
		}
		// Nor does a filter reach what is not in the product.
		if got := s.listed(auditPath(product)+"?action=product.register&outcome=DENIED", auditor, 10); len(got) != 0 {
			t.Errorf("the auditor of %s reads the refusal of a registration: %v", product, got)
		}
	}

	// An auditor of one product is no auditor of the other.
	s.requireThat(s.call(http.MethodGet, auditPath(parcel), "auditor", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	s.requireThat(s.call(http.MethodGet, auditPath(cargopai), "parcel-auditor", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")

	// The oldest record of a product is its registration, by the administrator.
	history := s.listed(auditPath(cargopai), "auditor", 100)
	if first := history[len(history)-1]; first["action"] != "product.register" || first["outcome"] != "SUCCEEDED" || first["rule_id"] != "AC-08" ||
		first["resource_type"] != "Product" || first["resource_id"] != cargopai || first["actor"].(map[string]any)["subject"] != "platform-admin" {
		t.Errorf("the oldest record of the product is %v, want its registration by the administrator", first)
	}
}

// Every filter keeps the records that are what it says, exactly, whichever
// way it is asked for, and the filters narrow the list together. What the list
// answers is what the trail holds that is so, in the order of the list.
func TestTheAuditRecordsAreFilteredByWhatTheyRecord(t *testing.T) {
	s := publicationFor(t)
	s.auditorOf(s.product, "owner", "auditor")
	s.require(s.securityRole("security", "security_admin", "owner"), http.StatusCreated, "")
	// Refusals of several kinds, and what the request of one of them is.
	refusal := s.call(http.MethodGet, "/v1/products/"+s.product, "mallory", "")
	s.require(refusal, http.StatusNotFound, "PRODUCT_NOT_FOUND")
	s.require(s.call(http.MethodPost, "/v1/products/"+s.product+"/workspaces", "author", `{"workspace_key":"mine","display_name":"Mine"}`), http.StatusForbidden, "FORBIDDEN")
	s.require(s.call(http.MethodGet, auditPath(s.product), "owner", ""), http.StatusForbidden, "FORBIDDEN")
	trace, _ := refusal.body["trace_id"].(string)

	path := auditPath(s.product)
	all := s.trail(s.product)
	if len(all) < 12 {
		t.Fatalf("the trail holds %d records, want a history", len(all))
	}
	// expected is what the trail holds that is so, newest first.
	expected := func(keep func(trailed) bool) []string {
		ids := []string{}
		for _, row := range all {
			if keep(row) {
				ids = append(ids, row.ID)
			}
		}
		return ids
	}
	issuer := s.issuer.URL()
	other := url.QueryEscape("https://other.example")
	// Instants the records themselves name: the second oldest, and the fifth
	// newest.
	since, before := all[len(all)-2].At, all[4].At
	instant := func(at time.Time) string { return at.UTC().Format("2006-01-02T15:04:05.000000Z") }

	cases := map[string]struct {
		query string
		keep  func(trailed) bool
	}{
		"nothing":                        {"", func(trailed) bool { return true }},
		"an action":                      {"action=member.grant", func(r trailed) bool { return r.Action == "member.grant" }},
		"an outcome that was denied":     {"outcome=DENIED", func(r trailed) bool { return r.Outcome == "DENIED" }},
		"an outcome that succeeded":      {"outcome=SUCCEEDED", func(r trailed) bool { return r.Outcome == "SUCCEEDED" }},
		"a subject":                      {"actor_subject=owner", func(r trailed) bool { return r.Subject == "owner" }},
		"a subject and its issuer":       {"actor_subject=owner&actor_issuer=" + url.QueryEscape(issuer), func(r trailed) bool { return r.Subject == "owner" && r.Issuer == issuer }},
		"an issuer alone":                {"actor_issuer=" + url.QueryEscape(issuer), func(r trailed) bool { return r.Issuer == issuer }},
		"another issuer":                 {"actor_issuer=" + other, func(trailed) bool { return false }},
		"a subject under another issuer": {"actor_subject=owner&actor_issuer=" + other, func(trailed) bool { return false }},
		"a kind of resource":             {"resource_type=Member", func(r trailed) bool { return r.ResourceType == "Member" }},
		"a resource":                     {"resource_id=" + s.workspace, func(r trailed) bool { return r.ResourceID == s.workspace }},
		"a request":                      {"correlation_id=" + url.QueryEscape(trace), func(r trailed) bool { return r.Correlation == trace }},
		"a request that is none":         {"correlation_id=no-such-request", func(trailed) bool { return false }},
		"from an instant":                {"recorded_since=" + instant(since), func(r trailed) bool { return !r.At.Before(since) }},
		"until an instant":               {"recorded_before=" + instant(before), func(r trailed) bool { return r.At.Before(before) }},
		"between two instants": {"recorded_since=" + instant(since) + "&recorded_before=" + instant(before), func(r trailed) bool {
			return !r.At.Before(since) && r.At.Before(before)
		}},
		"an action that was denied": {"action=workspace.manage&outcome=DENIED", func(r trailed) bool {
			return r.Action == "workspace.manage" && r.Outcome == "DENIED"
		}},
		"a subject, a kind and an outcome": {"actor_subject=mallory&resource_type=Product&outcome=DENIED", func(r trailed) bool {
			return r.Subject == "mallory" && r.ResourceType == "Product" && r.Outcome == "DENIED"
		}},
		"filters that nothing satisfies": {"action=member.grant&outcome=DENIED&actor_subject=owner", func(trailed) bool { return false }},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			want := expected(c.keep)
			target := path
			if c.query != "" {
				target += "?" + c.query
			}
			for _, size := range []int{1, 4, 100} {
				if got := idsOfItems(s.listed(target, "auditor", size)); !slices.Equal(got, want) {
					t.Errorf("in pages of %d the records are %v, want %v", size, got, want)
				}
			}
		})
	}
	// The filters select something in the cases above, or the cases say nothing.
	for name, query := range map[string]string{
		"an action": "action=member.grant", "an outcome that was denied": "outcome=DENIED", "a subject": "actor_subject=owner",
		"a kind of resource": "resource_type=Member", "a resource": "resource_id=" + s.workspace, "a request": "correlation_id=" + url.QueryEscape(trace),
		"between two instants": "recorded_since=" + instant(since) + "&recorded_before=" + instant(before),
	} {
		got := s.listed(path+"?"+query, "auditor", 100)
		if len(got) == 0 || len(got) == len(all) && name != "a subject" {
			t.Errorf("%s keeps %d of the %d records of the trail: the case would prove nothing", name, len(got), len(all))
		}
	}

	// An instant is the same instant in whichever form it is written. The one
	// with an offset has its + written as %2B, as a query needs it.
	local := since.In(time.FixedZone("UTC+8", 8*60*60))
	for name, written := range map[string]string{
		"in UTC with a Z":        since.Format("2006-01-02T15:04:05.000000Z07:00"),
		"in UTC to a nanosecond": since.Format("2006-01-02T15:04:05.000000000Z"),
		"with an offset":         strings.ReplaceAll(local.Format("2006-01-02T15:04:05.000000-07:00"), "+", "%2B"),
	} {
		want := expected(func(r trailed) bool { return !r.At.Before(since) })
		if got := idsOfItems(s.listed(path+"?recorded_since="+written, "auditor", 100)); !slices.Equal(got, want) {
			t.Errorf("from the instant %s: the records are %v, want %v", name, got, want)
		}
	}
	// A + that is left as it is, is a space, and no instant.
	s.require(s.call(http.MethodGet, path+"?recorded_since=2026-11-23T10:30:00+08:00", "auditor", ""), http.StatusBadRequest, "REQUEST_INVALID")

	// A range that ends where it begins or before is for the model to refuse,
	// and is refused to the auditor only: to anybody else the product is what
	// it is to whoever asks for the list as it is.
	reversed := path + "?recorded_since=" + instant(before) + "&recorded_before=" + instant(since)
	s.require(s.call(http.MethodGet, reversed, "auditor", ""), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE")
	s.require(s.call(http.MethodGet, path+"?recorded_since="+instant(since)+"&recorded_before="+instant(since), "auditor", ""),
		http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE")
	s.require(s.call(http.MethodGet, reversed, "owner", ""), http.StatusForbidden, "FORBIDDEN")
	s.require(s.call(http.MethodGet, reversed, "stranger", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
}

// A list is read a page at a time, newest first. Every page but the last is
// full and hands out the token of the next; the last one hands out none, also
// when it is full. What is recorded while the list is read comes before every
// page that was handed out: it repeats no record that was read and takes none
// from what is behind.
func TestAuditRecordsAreReadNewestFirstAndPagesSurviveNewRecords(t *testing.T) {
	s := publicationFor(t)
	s.auditorOf(s.product, "owner", "auditor")
	path := auditPath(s.product)
	original := s.trail(s.product)
	if len(original) < 8 {
		t.Fatalf("the trail holds %d records, want a history", len(original))
	}
	for index := 1; index < len(original); index++ {
		if original[index].At.After(original[index-1].At) {
			t.Fatalf("the trail is not newest first at %d: %v after %v", index, original[index].At, original[index-1].At)
		}
	}

	for _, size := range []int{1, 2, 3, 7, len(original) - 1, len(original), len(original) + 1, 100} {
		pages := s.auditPagesOf(path, "auditor", size)
		if got := idsOfItems(slices.Concat(pages...)); !slices.Equal(got, idsOfTrail(original)) {
			t.Errorf("in pages of %d the records are %v, want %v", size, got, idsOfTrail(original))
		}
		if want := (len(original) + size - 1) / size; len(pages) != want {
			t.Errorf("in pages of %d the list has %d pages, want %d", size, len(pages), want)
		}
		for index, page := range pages[:len(pages)-1] {
			if len(page) != size {
				t.Errorf("in pages of %d, page %d holds %d records and is not the last", size, index+1, len(page))
			}
		}
	}
	// Without a size a page holds fifty records.
	for index := range 60 {
		s.require(s.call(http.MethodGet, "/v1/products/"+s.product, fmt.Sprintf("visitor-%d", index), ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	}
	first, next := s.page(path, "auditor")
	if len(first) != 50 || next == "" {
		t.Fatalf("without a size the first page holds %d records and the token %q, want 50 and a token", len(first), next)
	}

	// The list is read to its end while records are added: by what happens in
	// the product, and not by the auditor, which leaves none.
	snapshot := s.trail(s.product)
	one, token := s.page(path+"?page_size=5", "auditor")
	if !slices.Equal(idsOfItems(one), idsOfTrail(snapshot[:5])) || token == "" {
		t.Fatalf("the first page holds %v and the token %q, want the five newest and a token", idsOfItems(one), token)
	}
	s.createWorkspace(s.product, "owner", "later-one")
	s.join(s.product, s.workspace, "owner", "late-author", "internal_author")
	s.require(s.call(http.MethodGet, "/v1/products/"+s.product, "latecomer", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	added := len(s.trail(s.product)) - len(snapshot)
	if added < 3 {
		t.Fatalf("%d records were added while the list was read, want some", added)
	}
	rest := idsOfItems(one)
	for token != "" {
		var items []map[string]any
		items, token = s.page(path+"?page_size=5&page_token="+token, "auditor")
		rest = append(rest, idsOfItems(items)...)
	}
	if !slices.Equal(rest, idsOfTrail(snapshot)) {
		t.Errorf("read while records were added, the list is\n %v\nwant what was there when it was begun\n %v", rest, idsOfTrail(snapshot))
	}
	// A list read again holds them at its head.
	again := idsOfItems(s.listed(path, "auditor", 100))
	if len(again) != len(snapshot)+added || !slices.Equal(again[added:], idsOfTrail(snapshot)) {
		t.Errorf("read again, the list holds %d records, the new ones at its head: %v", len(again), again[:min(added, len(again))])
	}
}

// What the role of an auditor gives is the records of the product and nothing
// else: no contract, no revision, no check, no review, no release, no origin of
// one, no slot of a publication and no list of them, no artifact and no block.
// The product itself, its audiences and its targets are known to whoever holds
// a role in it, as to the holder of a security role, and the auditor is told no
// more and no less.
func TestAnAuditorReadsNoContractNoRevisionAndNoPublication(t *testing.T) {
	s := publicationFor(t)
	s.require(s.securityRole("security", "security_admin", "owner"), http.StatusCreated, "")
	s.auditorOf(s.product, "owner", "auditor")
	// A product with what an auditor must not read: a contract with its checks
	// and its review, a release, a target with a slot that serves it, and a
	// block of it.
	revision, review, release := s.released(revisionSource, "", "1.0.0")
	target, config := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, release, "", "first"), http.StatusOK, "")
	// The artifact is served to whoever may read the slot, an author for one.
	if artifact := s.call(http.MethodGet, slot+"/artifact", "author", ""); artifact.status != http.StatusOK {
		t.Fatalf("an author reads the artifact: %d %v, want it served", artifact.status, artifact.body)
	}
	// So are the slots of the target, the slots of the asset, and the release by
	// its identifier with its origin: the paths the auditor is refused below are
	// served to an author.
	for _, path := range []string{target + "/assets", s.asset + "/publications", s.releaseAt(release) + "/provenance"} {
		if got := s.call(http.MethodGet, path, "author", ""); got.status != http.StatusOK {
			t.Fatalf("an author reads %s: %d %v, want it read", path, got.status, got.body)
		}
	}
	if got := s.download(s.releaseAt(release)+"/artifact", "author"); got.status != http.StatusOK {
		t.Fatalf("an author downloads the release: %d %s, want it served", got.status, got.body)
	}
	placed := s.call(http.MethodPost, target+"/access-blocks", "security", jsonBody(map[string]string{"api_asset_id": last(s.asset), "reason": "an audit"}))
	s.require(placed, http.StatusCreated, "")
	block := target + "/access-blocks/" + placed.id("access_block_id")
	product := "/v1/products/" + s.product
	revisionPath := s.revisions + "/" + revision

	// What the auditor reads: the product, as whoever holds a role in it does,
	// and what is told to whoever knows the product.
	for _, path := range []string{
		product, "/v1/products", product + "/audience-configs", config, product + "/publication-targets", target,
		auditPath(s.product),
	} {
		if got := s.call(http.MethodGet, path, "auditor", ""); got.status != http.StatusOK {
			t.Errorf("the auditor reads %s: %d %v, want 200", path, got.status, got.body)
		}
	}
	if items := s.listed("/v1/products", "auditor", 10); len(items) != 1 || items[0]["product_id"] != s.product {
		t.Errorf("the auditor lists the products %v, want the one it holds a role in", items)
	}
	if items := s.listed(product+"/workspaces", "auditor", 10); len(items) != 0 {
		t.Errorf("the auditor lists the workspaces %v, want none: it is a member of none", items)
	}

	// What it does not: who is in the workspaces and who holds a role, the
	// assets, the contract and what was made of it, the releases, the slot and
	// what it serves, and the blocks; and what it may not do.
	refused := map[string]struct {
		method, path, body string
		status             int
		code               string
	}{
		"a workspace":                {http.MethodGet, product + "/workspaces/" + s.workspace, "", http.StatusNotFound, "WORKSPACE_NOT_FOUND"},
		"the members of a workspace": {http.MethodGet, product + "/workspaces/" + s.workspace + "/memberships", "", http.StatusNotFound, "WORKSPACE_NOT_FOUND"},
		"the roles of the product":   {http.MethodGet, product + "/role-assignments", "", http.StatusForbidden, "FORBIDDEN"},
		"the assets":                 {http.MethodGet, product + "/assets", "", http.StatusForbidden, "FORBIDDEN"},
		"an asset":                   {http.MethodGet, s.asset, "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"the revisions of an asset":  {http.MethodGet, s.revisions, "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"a revision":                 {http.MethodGet, revisionPath, "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"the source of a revision":   {http.MethodGet, revisionPath + "/source", "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"the checks of a revision":   {http.MethodGet, revisionPath + "/check-runs", "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"the reviews of a revision":  {http.MethodGet, revisionPath + "/reviews", "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"a review":                   {http.MethodGet, revisionPath + "/reviews/" + review, "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"the releases of an asset":   {http.MethodGet, s.asset + "/releases", "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"a release":                  {http.MethodGet, s.asset + "/releases/" + release, "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"a slot":                     {http.MethodGet, slot, "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"the operations of a slot":   {http.MethodGet, slot + "/operations", "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"the artifact of a slot":     {http.MethodGet, slot + "/artifact", "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"the slots of a target":      {http.MethodGet, target + "/assets", "", http.StatusForbidden, "FORBIDDEN"},
		"the slots an asset is in":   {http.MethodGet, s.asset + "/publications", "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"the artifact of a release":  {http.MethodGet, s.releaseAt(release) + "/artifact", "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"the origin of a release":    {http.MethodGet, s.releaseAt(release) + "/provenance", "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"the blocks of a target":     {http.MethodGet, target + "/access-blocks", "", http.StatusForbidden, "FORBIDDEN"},
		"a block":                    {http.MethodGet, block, "", http.StatusForbidden, "FORBIDDEN"},
		"a contract imported":        {http.MethodPost, s.revisions, uploadRevision("openapi.yaml", revisionSource), http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		"a block placed":             {http.MethodPost, target + "/access-blocks", jsonBody(map[string]string{"api_asset_id": last(s.asset), "reason": "no"}), http.StatusForbidden, "FORBIDDEN"},
		"a role named":               {http.MethodPost, product + "/role-assignments", s.role("friend"), http.StatusForbidden, "FORBIDDEN"},
		"a workspace created":        {http.MethodPost, product + "/workspaces", `{"workspace_key":"mine","display_name":"Mine"}`, http.StatusForbidden, "FORBIDDEN"},
		"an audience configured":     {http.MethodPost, product + "/audience-configs", `{"audience":"public","environment":"production"}`, http.StatusForbidden, "FORBIDDEN"},
	}
	for what, c := range refused {
		got := s.call(c.method, c.path, "auditor", c.body)
		if got.status != c.status || got.code() != c.code {
			t.Errorf("the auditor asks for %s, %s %s: %d %v, want %d %s", what, c.method, c.path, got.status, got.body, c.status, c.code)
		}
		if encoded, _ := json.Marshal(got.body); strings.Contains(string(encoded), release) || strings.Contains(string(encoded), revision) {
			t.Errorf("the answer to the auditor asking for %s holds the identifier of the release or of the revision: %s", what, encoded)
		}
	}
	// Nor does owning the product, or publishing under it, read the records.
	s.require(s.call(http.MethodGet, auditPath(s.product), "owner", ""), http.StatusForbidden, "FORBIDDEN")
	s.require(s.call(http.MethodGet, auditPath(s.product), "publisher", ""), http.StatusForbidden, "FORBIDDEN")
}

// A role is held from the moment it is granted to the moment it is revoked,
// and nothing a caller holds from before keeps it: a page token grants
// nothing, and a role that was revoked reads no page after it. An owner that
// is to read the records makes itself an auditor, and that is on record, for
// the auditor to read.
func TestTheAuditorRoleTakesEffectAtOnceAndEndsAtOnce(t *testing.T) {
	s := publicationFor(t)
	path := auditPath(s.product)

	// Not yet an auditor: the product is not there for it.
	s.require(s.call(http.MethodGet, path, "auditor", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	// An owner names itself, as a platform administrator may not.
	s.require(s.grantRole(s.product, "platform-admin", "auditor", "internal_auditor"), http.StatusForbidden, "FORBIDDEN")
	s.require(s.call(http.MethodGet, path, "owner", ""), http.StatusForbidden, "FORBIDDEN")
	itself := s.grantRole(s.product, "owner", "owner", "internal_auditor")
	s.require(itself, http.StatusCreated, "")
	mine := s.listed(path+"?actor_subject=owner&action=product_role.grant&outcome=SUCCEEDED", "owner", 10)
	if len(mine) != 1 || mine[0]["resource_id"] != last(itself.header.Get("Location")) || mine[0]["rule_id"] != "AC-10" ||
		mine[0]["resource_type"] != "RoleAssignment" {
		t.Errorf("the owner that made itself an auditor reads %v of its grant, want the one record of it, under the rule of the owners", mine)
	}
	// What the administrator was refused is on record too, and the owner reads it.
	refused := s.listed(path+"?actor_subject=platform-admin&action=product_role.grant&outcome=DENIED", "owner", 10)
	if len(refused) != 1 || refused[0]["rule_id"] != "DENY-03" {
		t.Errorf("the owner reads %v of the refusal of the administrator, want the one record of it", refused)
	}

	// The role is granted: the next request reads.
	location := s.auditorOf(s.product, "owner", "auditor")
	if pages := s.auditPagesOf(path, "auditor", 2); len(pages) < 3 {
		t.Fatalf("the trail is read in %d pages of two, want some", len(pages))
	}
	items, token := s.page(path+"?page_size=2", "auditor")
	if len(items) != 2 || token == "" {
		t.Fatalf("the first page holds %d records and the token %q, want two and a token", len(items), token)
	}
	// Ending the role ends what it was read with: the token names a position and
	// grants nothing, and the list is none of the caller's any more.
	s.revoke(location, "owner")
	for _, target := range []string{path + "?page_size=2&page_token=" + token, path} {
		s.require(s.call(http.MethodGet, target, "auditor", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	}
	// The record of the revocation is in the trail that the owner, which is an
	// auditor, reads.
	revoked := s.listed(path+"?action=product_role.revoke", "owner", 10)
	if len(revoked) != 1 || revoked[0]["resource_id"] != last(location) {
		t.Errorf("the owner reads %v of the revocation, want the one record of it", revoked)
	}
	// Granted again, as an assignment of its own, it reads again, from the same
	// position.
	again := s.auditorOf(s.product, "owner", "auditor")
	if last(again) == last(location) {
		t.Errorf("the role was granted again under the identifier of the one that was revoked")
	}
	if got, _ := s.page(path+"?page_size=2&page_token="+token, "auditor"); len(got) != 2 {
		t.Errorf("behind the token, once the role is granted again: %d records, want 2", len(got))
	}
}

// A revocation that commits while a read waits for the role it is made with
// decides that read: the role of the caller is read under a lock, so a request
// does not read with a role that is revoked by the time it reads. The
// revocation is committed here the way another request would commit it, from
// another session, while the read waits.
func TestARoleRevokedWhileAReadWaitsIsNoRoleToReadWith(t *testing.T) {
	s := publicationFor(t)
	s.auditorOf(s.product, "owner", "auditor")
	path := auditPath(s.product)

	holder := pgtest.Connect(t, s.database.Migrator)
	tx, err := holder.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	var assignment string
	if err := tx.QueryRow(t.Context(), `
		SELECT role_assignment_id::text FROM productgovernance.product_role_assignment
		WHERE product_id = $1 AND principal_subject = 'auditor' AND role = 'internal_auditor' AND revoked_at IS NULL
		FOR UPDATE`, s.product).Scan(&assignment); err != nil {
		t.Fatalf("lock the role of the auditor: %v", err)
	}

	answered := make(chan answer, 1)
	go func() { answered <- s.call(http.MethodGet, path, "auditor", "") }()
	s.waiting(1)
	select {
	case got := <-answered:
		t.Fatalf("the read was answered %d %v while the role of the auditor was held by another transaction", got.status, got.body)
	default:
	}

	// The revocation commits, as that of the owner does: the assignment ends,
	// who ended it is kept.
	if _, err := tx.Exec(t.Context(), `
		UPDATE productgovernance.product_role_assignment
		SET revoked_at = now(), revoked_by_issuer = $2, revoked_by_subject = 'owner', role_assignment_version = role_assignment_version + 1
		WHERE role_assignment_id = $1`, assignment, s.issuer.URL()); err != nil {
		t.Fatalf("revoke the role: %v", err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatalf("commit the revocation: %v", err)
	}
	select {
	case got := <-answered:
		// The caller knew the product when it was asked, and holds no role in
		// it when it decides: it may not read.
		s.require(got, http.StatusForbidden, "FORBIDDEN")
	case <-time.After(15 * time.Second):
		t.Fatal("the read was not answered once the role was revoked")
	}
	if refused := s.recorded(func(record audited) bool {
		return record.Subject == "auditor" && record.Action == "audit.search" && record.Outcome == "DENIED"
	}); len(refused) != 1 {
		t.Errorf("the refusal is on record %d times, want once: %v", len(refused), refused)
	}
}

// A product that takes no new work keeps what it holds: its auditor still reads
// the trail, which is when it matters, and the role is still ended. It takes no
// new auditor.
func TestTheAuditRecordsOfASuspendedProductAreStillRead(t *testing.T) {
	s := publicationFor(t)
	location := s.auditorOf(s.product, "owner", "auditor")
	path := auditPath(s.product)
	if _, err := s.owner.Exec(t.Context(), `UPDATE productgovernance.product SET status = 'SUSPENDED' WHERE product_id = $1`, s.product); err != nil {
		t.Fatalf("suspend the product: %v", err)
	}

	if got := s.listed(path, "auditor", 10); len(got) == 0 {
		t.Errorf("the auditor of a suspended product reads no record")
	}
	s.require(s.grantRole(s.product, "owner", "second-auditor", "internal_auditor"), http.StatusConflict, "PRODUCT_NOT_ACTIVE")
	s.require(s.call(http.MethodGet, path, "second-auditor", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	s.revoke(location, "owner")
	s.require(s.call(http.MethodGet, path, "auditor", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
}

// The trail holds references and no more: no token, no request body, and no
// name of a private object. Requests are made with a token and with bodies that
// carry a marker, as an attacker or a careless caller would, and neither the
// table nor the answers of the list say any of it.
func TestAuditRecordsHoldNoTokenAndNoRequestBody(t *testing.T) {
	s := publicationFor(t)
	s.auditorOf(s.product, "owner", "auditor")
	const marker = "marker-4f1c9e27-private-name"
	path := "/v1/products/" + s.product

	// Each caller has a token of its own, and the requests are made with it.
	tokens := map[string]string{}
	var secrets []string
	for _, subject := range []string{"owner", "stranger", "author", "auditor"} {
		tokens[subject] = s.token(subject)
		secrets = append(secrets, tokens[subject])
		secrets = append(secrets, strings.Split(tokens[subject], ".")...)
	}
	secrets = append(secrets, marker)
	do := func(method, target, subject, body string) answer {
		return s.callWith(method, target, tokens[subject], body)
	}

	// Writes that succeed, with the marker in what they carry, and refusals of
	// the same.
	s.require(do(http.MethodPost, path+"/workspaces", "owner", `{"workspace_key":"secret-room","display_name":"`+marker+`"}`), http.StatusCreated, "")
	s.require(do(http.MethodPost, path+"/workspaces", "stranger", `{"workspace_key":"intruded","display_name":"`+marker+`"}`), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	s.require(do(http.MethodPost, path+"/workspaces", "author", `{"workspace_key":"refused","display_name":"`+marker+`"}`), http.StatusForbidden, "FORBIDDEN")
	s.require(do(http.MethodPost, path+"/audience-configs", "author",
		`{"audience":"partner","environment":"sandbox","docs_url":"https://docs.example/`+marker+`"}`), http.StatusForbidden, "FORBIDDEN")
	s.require(do(http.MethodGet, path+"/workspaces/"+marker, "author", ""), http.StatusBadRequest, "REQUEST_INVALID")
	s.require(do(http.MethodGet, auditPath(s.product), "stranger", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")

	// The table, every column of every row, as the migrating role reads it.
	var inTable int
	if err := s.owner.QueryRow(t.Context(), `
		SELECT count(*) FROM platformoperations.audit_record r
		WHERE EXISTS (SELECT 1 FROM unnest($1::text[]) AS secret WHERE strpos(row_to_json(r)::text, secret) > 0)`, secrets).Scan(&inTable); err != nil {
		t.Fatalf("search the trail: %v", err)
	}
	if inTable != 0 {
		t.Errorf("%d records of the trail hold a token or a marker of a request", inTable)
	}
	// What was recorded of those requests is there, and says what it should: the
	// refusals, once each, as who made them.
	for _, subject := range []string{"stranger", "author"} {
		if got := s.listed(auditPath(s.product)+"?actor_subject="+subject+"&outcome=DENIED", "auditor", 100); len(got) == 0 {
			t.Errorf("the refusals of %s are not in the trail", subject)
		}
	}

	// What the list says of it.
	listing := do(http.MethodGet, auditPath(s.product)+"?page_size=100", "auditor", "")
	s.require(listing, http.StatusOK, "")
	encoded, _ := json.Marshal(listing.body)
	for _, secret := range secrets {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("the list holds %q", secret)
		}
	}
	// What is in a record is a reference: an identifier the server made, a
	// principal, a name of an action and a rule.
	for _, item := range s.listed(auditPath(s.product), "auditor", 100) {
		for _, member := range []string{"audit_id", "correlation_id"} {
			if id, _ := item[member].(string); !auditIdentifier.MatchString(id) {
				t.Errorf("the record shows %s %q, want an identifier the server made: %v", member, id, item)
			}
		}
	}
}

// An identifier that names no product is no product to know: the scope of an
// auditor over it is empty, and asking for it is no failure of the service.
// The use case refuses such an identifier before it asks, so no request gets
// here with one; this is where the answer of Product Governance to it, that
// there is no such product, is told apart from a failure, were the use case
// ever to ask. A request that names no principal is a failure all the same,
// whichever the product: it is not read as there being no product.
func TestAnIdentifierThatNamesNoProductIsNoFailureOfTheScopeOfAnAuditor(t *testing.T) {
	// Nothing is read for an identifier that names no product, so the scopes
	// need no store.
	scopes := auditScopes{governance: &governance.ProductGovernance{}}
	caller := operationsdomain.Actor{Issuer: "https://issuer.example", Subject: "alice"}
	const absent = "3e1c6a9d-74f2-4b0a-98c3-b2e5f4a6c7d8"

	for name, product := range map[string]string{
		"words":                       "not-an-identifier",
		"nothing":                     "",
		"an identifier in upper case": strings.ToUpper(absent),
		"an identifier cut short":     absent[:len(absent)-1],
		"an identifier with a suffix": absent + "0",
	} {
		got, err := scopes.AuditScope(t.Context(), caller, product)
		if err != nil || got != (operationsports.AuditScope{}) {
			t.Errorf("%s: the scope is %+v with the failure %v, want the empty scope and no failure", name, got, err)
		}
	}

	for _, product := range []string{"not-an-identifier", absent} {
		if _, err := scopes.AuditScope(t.Context(), operationsdomain.Actor{}, product); !errors.Is(err, governance.ErrForbidden) {
			t.Errorf("a request with no principal, for %q: the failure is %v, want a refusal", product, err)
		}
	}
}
