package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	auditing "go.idp.xyz/idp-api-fabric/internal/platformoperations/application"
	auditdomain "go.idp.xyz/idp-api-fabric/internal/platformoperations/domain"
	auditports "go.idp.xyz/idp-api-fabric/internal/platformoperations/ports"
	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

const (
	auditRecordID    = "8a9b0c1d-2e3f-4a50-8b61-7c8d9e0f1a2b"
	auditedRequestID = "0b1c2d3e-4f50-4a61-9b72-8c9d0e1f2a3b"
	// auditRecords is where the audit records of the product are read.
	auditRecords = "/v1/products/" + productID + "/audit-records"
)

// unusedAudit is the use case of reading the audit records for the tests that
// never reach it.
type unusedAudit struct{ AuditReading }

// audits stands in for the use case of reading the audit records. It keeps
// what it was asked and answers what the test set. The real use case is
// tested where it lives, and the whole of it in cmd/fabric-api.
type audits struct {
	page auditing.AuditPage
	err  error

	asked []auditing.ListAuditRecords
}

func (a *audits) List(_ context.Context, query auditing.ListAuditRecords) (auditing.AuditPage, error) {
	a.asked = append(a.asked, query)
	return a.page, a.err
}

func newAuditService(t *testing.T, fake *audits) *service {
	t.Helper()

	s := &service{governance: newGovernance(), log: &bytes.Buffer{}}
	handler, err := NewHandler(Config{
		Logger: slog.New(slog.NewTextHandler(s.log, nil)), Build: testBuild, Verifier: tokens{}, Governance: s.governance,
		Catalog: unusedCatalog{}, Registry: unusedRegistry{}, PolicyReview: unusedPolicyReview{},
		Releases: unusedReleases{}, Publication: unusedPublications{}, Audit: fake,
	})
	if err != nil {
		t.Fatalf("build the handler: %v", err)
	}
	s.handler = handler
	return s
}

// grantRecorded is the record of alice making bob an owner: a success, with
// the role assignment it made.
func grantRecorded() auditdomain.AuditRecord {
	return auditdomain.AuditRecord{
		ID: auditRecordID, RecordedAt: createdAt, Actor: auditdomain.Actor{Issuer: issuer, Subject: "alice"},
		Action: "product_role.grant", Outcome: auditdomain.OutcomeSucceeded, Rule: "AC-10", ProductID: productID,
		ResourceType: "RoleAssignment", ResourceID: roleAssignmentID, CorrelationID: auditedRequestID,
	}
}

// refusalRecorded is the record of mallory being refused the audit records: a
// refusal leaves no resource behind.
func refusalRecorded() auditdomain.AuditRecord {
	return auditdomain.AuditRecord{
		ID: "9b0c1d2e-3f40-4b51-8c62-8d9e0f1a2b3c", RecordedAt: createdAt.Add(-time.Minute),
		Actor:  auditdomain.Actor{Issuer: issuer, Subject: "mallory"},
		Action: "audit.search", Outcome: auditdomain.OutcomeDenied, Rule: "DENY-03", ProductID: productID,
		ResourceType: "AuditRecord", CorrelationID: "1c2d3e4f-5061-4b72-8d83-9e0f1a2b3c4d",
	}
}

// grantShown and refusalShown are the two as the contract shows them: every
// fact the trail holds, and no resource where there is none.
func grantShown() map[string]any {
	return map[string]any{
		"audit_id": auditRecordID, "recorded_at": "2026-11-11T09:00:00.123456Z",
		"actor":  map[string]any{"issuer": issuer, "subject": "alice"},
		"action": "product_role.grant", "outcome": "SUCCEEDED", "rule_id": "AC-10", "product_id": productID,
		"resource_type": "RoleAssignment", "resource_id": roleAssignmentID, "correlation_id": auditedRequestID,
	}
}

func refusalShown() map[string]any {
	return map[string]any{
		"audit_id": "9b0c1d2e-3f40-4b51-8c62-8d9e0f1a2b3c", "recorded_at": "2026-11-11T08:59:00.123456Z",
		"actor":  map[string]any{"issuer": issuer, "subject": "mallory"},
		"action": "audit.search", "outcome": "DENIED", "rule_id": "DENY-03", "product_id": productID,
		"resource_type": "AuditRecord", "correlation_id": "1c2d3e4f-5061-4b72-8d83-9e0f1a2b3c4d",
	}
}

// The list hands what its request says to its use case: who asks, of which
// product, under which filters, how many records and behind which position.
// It answers the records as the contract shows each, and the token of the page
// that follows for as long as one does. The token is of the audit records of
// this product, and of no other list.
func TestAListOfAuditRecordsHandsItsRequestToItsUseCaseAndAnswersAPage(t *testing.T) {
	// A position is the one of the use case, and a token names it.
	behind := auditports.Position{At: createdAt, ID: auditRecordID}
	next := auditports.Position{At: createdAt.Add(-time.Hour), ID: "9b0c1d2e-3f40-4b51-8c62-8d9e0f1a2b3c"}
	since, before := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	list := listOfAuditRecords(productID)
	tokenOfPosition := func(position auditports.Position) string {
		return encodePageToken(list, governanceports.Position{At: position.At, ID: position.ID})
	}

	fake := &audits{page: auditing.AuditPage{Items: []auditdomain.AuditRecord{grantRecorded(), refusalRecorded()}, Next: &next}}
	s := newAuditService(t, fake)
	asked := func() auditing.ListAuditRecords {
		t.Helper()
		if len(fake.asked) != 1 {
			t.Fatalf("the use case was asked %d times, want once", len(fake.asked))
		}
		query := fake.asked[0]
		if !uuid.MatchString(query.CorrelationID) {
			t.Errorf("correlation identifier %q, want one the server generated", query.CorrelationID)
		}
		query.CorrelationID = ""
		fake.asked = nil
		return query
	}

	// A page of two records behind a position, under every filter there is,
	// with another to follow.
	filters := url.Values{
		"page_size": {"2"}, "page_token": {tokenOfPosition(behind)},
		"recorded_since": {"2026-11-11T00:00:00Z"}, "recorded_before": {"2026-11-12T00:00:00Z"},
		"action": {"product_role.grant"}, "outcome": {"SUCCEEDED"}, "actor_issuer": {issuer}, "actor_subject": {"alice"},
		"resource_type": {"RoleAssignment"}, "resource_id": {roleAssignmentID}, "correlation_id": {auditedRequestID},
	}
	response := s.do(t, call{Method: http.MethodGet, Token: "alice", Target: auditRecords + "?" + filters.Encode()})
	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", got)
	}
	wantBody := map[string]any{"items": []any{grantShown(), refusalShown()}, "next_page_token": tokenOfPosition(next)}
	if got := members(t, response); !reflect.DeepEqual(got, wantBody) {
		t.Errorf("body\n %v\nwant\n %v", got, wantBody)
	}
	got := asked()
	if got.PageSize != 2 || !reflect.DeepEqual(got.After, &behind) {
		t.Errorf("the use case was asked for %d records behind %+v, want 2 behind %+v", got.PageSize, got.After, &behind)
	}
	if got.Filter.RecordedSince == nil || !got.Filter.RecordedSince.Equal(since) ||
		got.Filter.RecordedBefore == nil || !got.Filter.RecordedBefore.Equal(before) {
		t.Errorf("the use case was asked for the range %v to %v, want %v to %v",
			got.Filter.RecordedSince, got.Filter.RecordedBefore, since, before)
	}
	got.Filter.RecordedSince, got.Filter.RecordedBefore, got.PageSize, got.After = nil, nil, 0, nil
	want := auditing.ListAuditRecords{
		Caller: auditdomain.Actor{Issuer: issuer, Subject: "alice"}, ProductID: productID,
		Filter: auditdomain.AuditFilter{
			Action: "product_role.grant", Outcome: auditdomain.OutcomeSucceeded, ActorIssuer: issuer, ActorSubject: "alice",
			ResourceType: "RoleAssignment", ResourceID: roleAssignmentID, CorrelationID: auditedRequestID,
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the use case was asked\n %+v\nwant\n %+v", got, want)
	}

	// Without a size, a token and filters: the size the contract declares as
	// the default, which is the one of the use case, from the start. The last
	// page carries no token, and a record with no resource shows none.
	fake.page = auditing.AuditPage{Items: []auditdomain.AuditRecord{refusalRecorded()}}
	response = s.do(t, call{Method: http.MethodGet, Token: "alice", Target: auditRecords})
	if got := members(t, response); response.Code != http.StatusOK || !reflect.DeepEqual(got, map[string]any{"items": []any{refusalShown()}}) {
		t.Errorf("without a size and a token: %d %s, want one record and no token", response.Code, response.Body)
	}
	want = auditing.ListAuditRecords{
		Caller: auditdomain.Actor{Issuer: issuer, Subject: "alice"}, ProductID: productID, PageSize: auditing.DefaultPageSize,
	}
	if got := asked(); !reflect.DeepEqual(got, want) {
		t.Errorf("without a size and a token the use case was asked %+v, want %+v", got, want)
	}

	// The bounds of the size are sizes like any other.
	for _, size := range []string{"1", "100"} {
		if response := s.do(t, call{Method: http.MethodGet, Token: "alice", Target: auditRecords + "?page_size=" + size}); response.Code != http.StatusOK {
			t.Errorf("a page of %s: status %d, want 200: %s", size, response.Code, response.Body)
		}
		if got := asked(); size == "1" && got.PageSize != 1 || size == "100" && got.PageSize != auditing.MaxPageSize {
			t.Errorf("a page of %s: the use case was asked for %d records", size, got.PageSize)
		}
	}

	// A page without records is a list that is empty, not one that is missing.
	fake.page = auditing.AuditPage{}
	response = s.do(t, call{Method: http.MethodGet, Token: "alice", Target: auditRecords})
	if got := strings.TrimSpace(response.Body.String()); response.Code != http.StatusOK || got != `{"items":[]}` {
		t.Errorf("a page without records: %d %s, want an empty list", response.Code, got)
	}
}

// What the request says that the contract does not allow is answered before
// any use case is asked, as it is for every operation: a parameter it does not
// declare, one without a value, a value it does not take. Nothing of the
// request is repeated. A page token that matches the contract and is none of
// this list, the list of another product included, is answered the same way
// whoever sends it.
func TestAnAuditQueryTheContractDoesNotDeclareIsRefused(t *testing.T) {
	position := governanceports.Position{At: createdAt, ID: auditRecordID}
	foreign := "7a8b9c0d-1e2f-4a30-8b41-5c6d7e8f9a0b"
	long := func(n int) string { return strings.Repeat("a", n) }

	cases := map[string]struct {
		query string
		code  string
	}{
		"a parameter the contract does not declare": {"offset=1", CodeRequestInvalid},
		"the size in camel case":                    {"pageSize=1", CodeRequestInvalid},
		"a size of none":                            {"page_size=0", CodeRequestInvalid},
		"a size above the largest":                  {"page_size=101", CodeRequestInvalid},
		"a size in words":                           {"page_size=ten", CodeRequestInvalid},
		"a filter without a value":                  {"action=", CodeRequestInvalid},
		"an instant without a value":                {"recorded_since=", CodeRequestInvalid},
		"an outcome nobody defined":                 {"outcome=MAYBE", CodeRequestInvalid},
		"an outcome in lower case":                  {"outcome=denied", CodeRequestInvalid},
		"an action in upper case":                   {"action=Product.Register", CodeRequestInvalid},
		"an action of one word":                     {"action=register", CodeRequestInvalid},
		"an action that is too long":                {"action=" + long(32) + "." + long(32), CodeRequestInvalid},
		"a kind of resource with a hyphen":          {"resource_type=Role-Assignment", CodeRequestInvalid},
		"a resource that is no identifier":          {"resource_id=not-an-identifier", CodeRequestInvalid},
		"an identifier in upper case":               {"resource_id=" + strings.ToUpper(roleAssignmentID), CodeRequestInvalid},
		"a correlation identifier with a control":   {"correlation_id=a%00b", CodeRequestInvalid},
		"a subject that is too long":                {"actor_subject=" + long(256), CodeRequestInvalid},
		"an instant in words":                       {"recorded_since=yesterday", CodeRequestInvalid},
		"an instant that is a date":                 {"recorded_before=2026-11-23", CodeRequestInvalid},
		"an instant that is a number":               {"recorded_since=1700000000", CodeRequestInvalid},
		"a token in words":                          {"page_token=" + tokenOf("canary"), CodePageTokenInvalid},
		"a token of another list":                   {"page_token=" + encodePageToken(listOfRoleAssignments, position), CodePageTokenInvalid},
		"a token of the same list of another product": {
			"page_token=" + encodePageToken(listOfAuditRecords(foreign), position), CodePageTokenInvalid},
		"a token of the audit records of no product": {"page_token=" + encodePageToken("audit-records", position), CodePageTokenInvalid},
		"a token cut short":                          {"page_token=" + encodePageToken(listOfAuditRecords(productID), position)[:40], CodePageTokenInvalid},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			fake := &audits{}
			s := newAuditService(t, fake)
			// What is wrong with the request is said to whoever sends it, an
			// auditor or not, and the answer repeats none of it.
			for _, token := range []string{"alice", "platform-admin"} {
				response := s.do(t, call{Method: http.MethodGet, Token: token, Target: auditRecords + "?" + c.query})
				wantRefusal(t, response, http.StatusBadRequest, c.code)
				if _, value, found := strings.Cut(c.query, "="); found && len(value) > 8 &&
					(strings.Contains(response.Body.String(), value) || strings.Contains(s.log.String(), value)) {
					t.Errorf("%q was written to the answer or to the log", value)
				}
			}
			if len(fake.asked) != 0 {
				t.Errorf("the request reached the use case")
			}
		})
	}

	// Whoever sends the request is asked first.
	fake := &audits{}
	s := newAuditService(t, fake)
	wantProblem(t, s.do(t, call{Method: http.MethodGet, Target: auditRecords + "?page_size=0"}), http.StatusUnauthorized, CodeCredentialsMissing, "")
	wantProblem(t, s.do(t, call{Method: http.MethodGet, Token: "nobody", Target: auditRecords}), http.StatusUnauthorized, CodeTokenInvalid, "")
	if len(fake.asked) != 0 {
		t.Errorf("a request without a verified caller reached the use case")
	}

	// The token of the list of the product is taken, by this product and by no
	// other.
	good := encodePageToken(listOfAuditRecords(productID), position)
	if response := s.do(t, call{Method: http.MethodGet, Token: "alice", Target: auditRecords + "?page_token=" + good}); response.Code != http.StatusOK {
		t.Errorf("its own token is answered %d: %s", response.Code, response.Body)
	}
	other := "/v1/products/" + foreign + "/audit-records"
	wantProblem(t, s.do(t, call{Method: http.MethodGet, Token: "alice", Target: other + "?page_token=" + good}),
		http.StatusBadRequest, CodePageTokenInvalid, "The page token is not one of this list. Read the list again from its start.")
}

// An instant is read in the forms that name one, and refused in the others,
// whatever the offset: the one of a request that writes it in UTC with a Z, one
// that gives an offset and the fraction of a second. A + that is left as it is
// is a space in a query, which is no instant.
func TestAnInstantIsReadInTheFormsThatNameOneAndRefusedInTheOthers(t *testing.T) {
	instant := time.Date(2026, 11, 23, 2, 30, 0, 0, time.UTC)

	cases := map[string]struct {
		value string
		want  time.Time // the zero time is a refusal
	}{
		"in UTC, ending in Z":                        {"2026-11-23T02:30:00Z", instant},
		"with an offset, the + written as %2B":       {"2026-11-23T10:30:00%2B08:00", instant},
		"with a negative offset":                     {"2026-11-22T21:30:00-05:00", instant},
		"with an offset of zero":                     {"2026-11-23T02:30:00%2B00:00", instant},
		"with a fraction of a second":                {"2026-11-23T02:30:00.5Z", instant.Add(500 * time.Millisecond)},
		"with the microsecond":                       {"2026-11-23T02:30:00.123456Z", instant.Add(123456 * time.Microsecond)},
		"with a fraction finer than the microsecond": {"2026-11-23T02:30:00.123456789Z", instant.Add(123456789 * time.Nanosecond)},
		"with an offset and a fraction":              {"2026-11-23T10:30:00.25%2B08:00", instant.Add(250 * time.Millisecond)},
		"with the + left as it is":                   {"2026-11-23T10:30:00+08:00", time.Time{}},
		"with no zone":                               {"2026-11-23T02:30:00", time.Time{}},
		"with a space for the T":                     {"2026-11-23%2002:30:00Z", time.Time{}},
		"with a t and a z in lower case":             {"2026-11-23t02:30:00z", time.Time{}},
		"as a date":                                  {"2026-11-23", time.Time{}},
		"with an hour that is none":                  {"2026-11-23T25:30:00Z", time.Time{}},
		"with a month that is none":                  {"2026-13-23T02:30:00Z", time.Time{}},
		"as seconds since the epoch":                 {"1700000000", time.Time{}},
	}
	for _, parameter := range []string{"recorded_since", "recorded_before"} {
		for name, c := range cases {
			t.Run(parameter+" "+name, func(t *testing.T) {
				fake := &audits{}
				response := newAuditService(t, fake).do(t, call{Method: http.MethodGet, Token: "alice", Target: auditRecords + "?" + parameter + "=" + c.value})
				if c.want.IsZero() {
					wantRefusal(t, response, http.StatusBadRequest, CodeRequestInvalid)
					if len(fake.asked) != 0 {
						t.Errorf("the request reached the use case")
					}
					return
				}
				if response.Code != http.StatusOK || len(fake.asked) != 1 {
					t.Fatalf("answered %d %s, with %d asks of the use case, want 200 and one", response.Code, response.Body, len(fake.asked))
				}
				got := fake.asked[0].Filter.RecordedSince
				if parameter == "recorded_before" {
					got = fake.asked[0].Filter.RecordedBefore
				}
				if got == nil || !got.Equal(c.want) {
					t.Errorf("the use case was asked for %v, want the instant %v", got, c.want)
				}
			})
		}
	}
}

// A refusal of the use case is answered as the use case said it, with the
// problem of that and nothing of what the request asked. A product that is not
// there and one the caller may not know are answered alike.
func TestARefusedReadingOfTheAuditRecordsIsAnsweredAsTheUseCaseSaidIt(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
		detail string
	}{
		"no such product":             {auditing.ErrProductNotFound, http.StatusNotFound, CodeProductNotFound, ""},
		"a caller that is no auditor": {auditing.ErrForbidden, http.StatusForbidden, CodeForbidden, "The caller may not perform this action."},
		"a range that ends before it begins": {
			fmt.Errorf("%w: the range of time ends where it begins or before", auditdomain.ErrInvalidAuditQuery),
			http.StatusUnprocessableEntity, CodeRequestUnprocessable, ""},
		// The caller is refused all the same, and whoever operates the
		// service is told that the trail has a gap.
		"a refusal that could not be recorded": {
			fmt.Errorf("%w; %w: the database is down", auditing.ErrForbidden, auditing.ErrRefusalNotAudited),
			http.StatusForbidden, CodeForbidden, "The caller may not perform this action."},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newAuditService(t, &audits{err: c.err})
			response := s.do(t, call{Method: http.MethodGet, Token: "alice", Target: auditRecords})
			wantProblem(t, response, c.status, c.code, c.detail)
			if audited := strings.Contains(s.log.String(), "a refusal could not be audited"); audited != errors.Is(c.err, auditing.ErrRefusalNotAudited) {
				t.Errorf("the log says the refusal could not be audited: %t\n%s", audited, s.log)
			}
			if strings.Contains(response.Body.String(), "database") {
				t.Errorf("the answer repeats what the use case said of the failure: %s", response.Body)
			}
		})
	}

	// A failure nobody expected is an internal error whose cause is logged and
	// never sent.
	s := newAuditService(t, &audits{err: errors.New("the audit trail cannot be read: canary")})
	response := s.do(t, call{Method: http.MethodGet, Token: "alice", Target: auditRecords})
	wantProblem(t, response, http.StatusInternalServerError, CodeInternalError, "")
	if strings.Contains(response.Body.String(), "canary") || !strings.Contains(s.log.String(), "canary") {
		t.Errorf("the cause was sent: %t, logged: %t", strings.Contains(response.Body.String(), "canary"), strings.Contains(s.log.String(), "canary"))
	}

	// A record the contract has no word for is a failure of the service, not
	// an answer.
	broken := grantRecorded()
	broken.Outcome = "MAYBE"
	s = newAuditService(t, &audits{page: auditing.AuditPage{Items: []auditdomain.AuditRecord{broken}}})
	wantProblem(t, s.do(t, call{Method: http.MethodGet, Token: "alice", Target: auditRecords}), http.StatusInternalServerError, CodeInternalError, "")
}

// The contract states the bounds of the model, and the model the bounds of the
// contract: what one lets through the other does not refuse, and a list says
// what the trail holds of a record and no more.
func TestTheBoundsOfAnAuditQueryAreTheOnesTheContractDeclares(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatalf("load the contract: %v", err)
	}
	schemas := contract.Components.Schemas

	size := contract.Components.Parameters["PageSize"].Value.Schema.Value
	if size.Min == nil || *size.Min != 1 || size.Max == nil || *size.Max != auditing.MaxPageSize ||
		fmt.Sprint(size.Default) != fmt.Sprint(auditing.DefaultPageSize) {
		t.Errorf("the contract bounds a page at %v to %v records, by default %v, the use case at 1 to %d, by default %d",
			size.Min, size.Max, size.Default, auditing.MaxPageSize, auditing.DefaultPageSize)
	}
	if got := schemas["AuditRecordList"].Value.Properties["items"].Value.MaxItems; got == nil || *got != auditing.MaxPageSize {
		t.Errorf("the contract bounds the list at %v records, the use case a page at %d", got, auditing.MaxPageSize)
	}
	for name, c := range map[string]struct {
		pattern string
		length  uint64
	}{
		"AuditAction":       {auditdomain.ActionPattern, auditdomain.NameMaxLength},
		"AuditResourceType": {auditdomain.ResourceTypePattern, auditdomain.NameMaxLength},
	} {
		got := schemas[name].Value
		if got.Pattern != c.pattern || got.MaxLength == nil || *got.MaxLength != c.length {
			t.Errorf("the contract declares %s as %q of at most %v characters, the model as %q of %d", name, got.Pattern, got.MaxLength, c.pattern, c.length)
		}
	}
	if got := schemas["Reference"].Value.MaxLength; got == nil || *got != auditdomain.ReferenceMaxLength {
		t.Errorf("the contract bounds a reference at %v characters, the model at %d", got, auditdomain.ReferenceMaxLength)
	}

	var declared []string
	for _, value := range schemas["AuditOutcome"].Value.Enum {
		outcome, _ := value.(string)
		declared = append(declared, outcome)
	}
	slices.Sort(declared)
	if want := []string{string(auditdomain.OutcomeDenied), string(auditdomain.OutcomeSucceeded)}; !slices.Equal(declared, want) {
		t.Errorf("the contract declares the outcomes %v, the model %v", declared, want)
	}

	// A record shows every fact the trail holds of it, and the one that is not
	// always there is the only one that is not required.
	record := schemas["AuditRecord"].Value
	shown := slices.Sorted(maps.Keys(grantShown()))
	if got := slices.Sorted(maps.Keys(record.Properties)); !slices.Equal(got, shown) {
		t.Errorf("the contract declares the members %v of a record, a record shows %v", got, shown)
	}
	required := slices.Sorted(maps.Keys(refusalShown()))
	if got := slices.Sorted(slices.Values(record.Required)); !slices.Equal(got, required) {
		t.Errorf("the contract requires the members %v of a record, a refusal shows %v", got, required)
	}

	// The filters of the operation are those the use case takes, and the page
	// is asked for as every list is.
	operation := contract.Paths.Find("/v1/products/{product_id}/audit-records").Get
	var asked []string
	for _, parameter := range operation.Parameters {
		if parameter.Value.In == "query" {
			asked = append(asked, parameter.Value.Name)
		}
	}
	slices.Sort(asked)
	want := []string{
		"action", "actor_issuer", "actor_subject", "correlation_id", "outcome", "page_size", "page_token",
		"recorded_before", "recorded_since", "resource_id", "resource_type",
	}
	if !slices.Equal(asked, want) {
		t.Errorf("the operation takes the query parameters %v, want %v", asked, want)
	}
}

// What the service answers for the audit records satisfies the schemas the
// contract declares for them, and those schemas are as closed as the answer:
// the list, a record and its actor take no member that is not declared. The
// trail holds references only, so an answer that carried a token or the body
// of a request is one the contract does not take, and neither is a record that
// lacks a fact the trail always holds or shows one that is none.
func TestTheContractStatesTheAuditRecordsAsTheyAreAnswered(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	declared := contract.Components.Schemas["AuditRecordList"]
	if declared == nil {
		t.Fatal("the contract declares no list of audit records")
	}

	// A record that left a resource behind and one that left none, with the
	// token of the page that follows; and a list with nothing in it.
	next := auditports.Position{At: createdAt.Add(-time.Hour), ID: "9b0c1d2e-3f40-4b51-8c62-8d9e0f1a2b3c"}
	fake := &audits{page: auditing.AuditPage{Items: []auditdomain.AuditRecord{grantRecorded(), refusalRecorded()}, Next: &next}}
	s := newAuditService(t, fake)
	recorder := s.do(t, call{Method: http.MethodGet, Target: auditRecords, Token: "alice"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", recorder.Code, recorder.Body)
	}
	if err := declared.Value.VisitJSON(members(t, recorder)); err != nil {
		t.Fatalf("the contract refuses what the service answers: %v\n%s", err, recorder.Body)
	}
	fake.page = auditing.AuditPage{}
	if empty := s.do(t, call{Method: http.MethodGet, Target: auditRecords, Token: "alice"}); declared.Value.VisitJSON(members(t, empty)) != nil {
		t.Errorf("the contract refuses a list with no record: %s", empty.Body)
	}

	record := func(change func(map[string]any)) func(map[string]any) {
		return func(body map[string]any) { change(body["items"].([]any)[0].(map[string]any)) }
	}
	without := func(member string) func(map[string]any) {
		return record(func(r map[string]any) { delete(r, member) })
	}
	set := func(member string, value any) func(map[string]any) {
		return record(func(r map[string]any) { r[member] = value })
	}
	refused := map[string]func(map[string]any){
		"a member the list does not declare":   func(body map[string]any) { body["total"] = 2 },
		"no items":                             func(body map[string]any) { delete(body, "items") },
		"a record that carries a token":        set("token", "eyJhbGciOiJSUzI1NiJ9"),
		"a record that carries a request body": set("request_body", `{"role":"internal_auditor"}`),
		"a record that carries a name":         set("actor_name", "Alice"),
		"an actor that says more":              record(func(r map[string]any) { r["actor"].(map[string]any)["email"] = "alice@example.com" }),
		"an actor that is a name":              set("actor", "alice"),
		"more records than a page holds": func(body map[string]any) {
			body["items"] = slices.Repeat(body["items"].([]any)[:1], auditing.MaxPageSize+1)
		},
		"no identifier":                      without("audit_id"),
		"no time it was recorded at":         without("recorded_at"),
		"nobody who acted":                   without("actor"),
		"no action":                          without("action"),
		"no outcome":                         without("outcome"),
		"no rule":                            without("rule_id"),
		"no product":                         without("product_id"),
		"no kind of resource":                without("resource_type"),
		"no request":                         without("correlation_id"),
		"an identifier that is none":         set("audit_id", "not-an-identifier"),
		"a time that is none":                set("recorded_at", "yesterday"),
		"an action that is no name":          set("action", "Product Register"),
		"an outcome there is not":            set("outcome", "ESCALATED"),
		"a kind of resource that is no name": set("resource_type", "Role Assignment"),
		"a resource that is no identifier":   set("resource_id", "none"),
	}
	for name, change := range refused {
		t.Run(name, func(t *testing.T) {
			body := members(t, recorder)
			change(body)
			if err := declared.Value.VisitJSON(body); err == nil {
				t.Errorf("the contract accepts a list with %s: %v", name, body)
			}
		})
	}
}
