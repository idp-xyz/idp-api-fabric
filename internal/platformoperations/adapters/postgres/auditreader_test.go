package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platformoperations/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/platformoperations/domain"
	"go.idp.xyz/idp-api-fabric/internal/platformoperations/ports"
)

const (
	productA  = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
	productB  = "2d0b5f8c-63e1-4a9f-8b72-01d4e3f5a6b7"
	issuerOne = "https://issuer.example"
	issuerTwo = "https://other-issuer.example"
)

// idOf is an identifier that is told apart at a glance and orders as its
// number does.
func idOf(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

// at is an instant of the day the tests are set on: the minute and the
// microsecond of the hour of nine.
func at(minute, microsecond int) time.Time {
	return time.Date(2026, 11, 23, 9, minute, 0, microsecond*1000, time.UTC)
}

// seeded is a row of the audit trail as the test wants it, down to the
// microsecond and the identifier. An empty product or resource is NULL.
type seeded struct {
	ID                     string
	At                     time.Time
	Issuer, Subject        string
	Action, Outcome, Rule  string
	Product                string
	ResourceType, Resource string
	Correlation            string
}

// record is the row as the reader hands it out.
func (s seeded) record() domain.AuditRecord {
	return domain.AuditRecord{
		ID: s.ID, RecordedAt: s.At, Actor: domain.Actor{Issuer: s.Issuer, Subject: s.Subject}, Action: s.Action,
		Outcome: domain.Outcome(s.Outcome), Rule: s.Rule, ProductID: s.Product,
		ResourceType: s.ResourceType, ResourceID: s.Resource, CorrelationID: s.Correlation,
	}
}

// seed puts the rows into the trail as the migrating role, which can: the
// application role only appends, and only through the use case that stamps the
// time and the identifier of what it records.
func (f fixture) seed(t *testing.T, rows ...seeded) {
	t.Helper()

	for _, row := range rows {
		if _, err := f.owner.Exec(t.Context(), `
			INSERT INTO platformoperations.audit_record
			    (audit_id, recorded_at, actor_issuer, actor_subject, action, outcome, rule_id,
			     product_id, resource_type, resource_id, correlation_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, '')::uuid, $9, NULLIF($10, '')::uuid, $11)`,
			row.ID, row.At, row.Issuer, row.Subject, row.Action, row.Outcome, row.Rule,
			row.Product, row.ResourceType, row.Resource, row.Correlation); err != nil {
			t.Fatalf("seed the record %s: %v", row.ID, err)
		}
	}
}

// page reads one page the way the use case asks for it: one record more than
// the page holds, behind the position.
func (f fixture) page(t *testing.T, product string, filter domain.AuditFilter, after *ports.Position, size int) []domain.AuditRecord {
	t.Helper()

	records, err := postgres.NewAuditReader(f.fabric.Framework).List(t.Context(), product, filter, ports.Page{After: after, Limit: size + 1})
	if err != nil {
		t.Fatalf("list the audit records of %s: %v", product, err)
	}
	return records
}

// pageUnder is page in a transaction whose planner is told, by the settings,
// what it may not use: the way the statement is planned is not the order the
// list is in.
func (f fixture) pageUnder(t *testing.T, settings []string, product string, filter domain.AuditFilter, after *ports.Position, size int) []domain.AuditRecord {
	t.Helper()

	var records []domain.AuditRecord
	err := f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
		executor, err := f.fabric.Framework.RequireExecutor(ctx)
		if err != nil {
			return err
		}
		for _, setting := range settings {
			if _, err := executor.Exec(ctx, setting); err != nil {
				return err
			}
		}
		records, err = postgres.NewAuditReader(f.fabric.Framework).List(ctx, product, filter, ports.Page{After: after, Limit: size + 1})
		return err
	})
	if err != nil {
		t.Fatalf("list the audit records of %s under %v: %v", product, settings, err)
	}
	return records
}

// read reads the list to its end, a page of size records at a time, each page
// behind the last record of the one before, and returns the identifiers of
// each page.
func (f fixture) read(t *testing.T, product string, filter domain.AuditFilter, size int) [][]string {
	t.Helper()
	return f.readUnder(t, nil, product, filter, size)
}

// readUnder is read under the settings of the planner.
func (f fixture) readUnder(t *testing.T, settings []string, product string, filter domain.AuditFilter, size int) [][]string {
	t.Helper()

	var pages [][]string
	var after *ports.Position
	for len(pages) < 1000 {
		var records []domain.AuditRecord
		if settings == nil {
			records = f.page(t, product, filter, after, size)
		} else {
			records = f.pageUnder(t, settings, product, filter, after, size)
		}
		more := len(records) > size
		if more {
			records = records[:size]
		}
		pages = append(pages, idsOf(records))
		if !more {
			return pages
		}
		last := records[len(records)-1]
		after = &ports.Position{At: last.RecordedAt, ID: last.ID}
	}
	t.Fatalf("the list of %s does not end", product)
	return nil
}

func idsOf(records []domain.AuditRecord) []string {
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	return ids
}

// A list is the records of one product, newest first, as the trail holds them:
// every fact of a record, and nothing of another product or of none.
func TestTheStoreListsOnlyTheRecordsOfTheProductNewestFirst(t *testing.T) {
	f := newFixture(t)
	a1 := seeded{ID: idOf(1), At: at(0, 0), Issuer: issuerOne, Subject: "alice", Action: "product.register", Outcome: "SUCCEEDED",
		Rule: "AC-08", Product: productA, ResourceType: "Product", Resource: productA, Correlation: "request-1"}
	a2 := seeded{ID: idOf(2), At: at(2, 0), Issuer: issuerOne, Subject: "alice", Action: "product_role.grant", Outcome: "SUCCEEDED",
		Rule: "AC-10", Product: productA, ResourceType: "RoleAssignment", Resource: idOf(101), Correlation: "request-2"}
	// A refusal leaves no resource behind.
	a3 := seeded{ID: idOf(3), At: at(4, 0), Issuer: issuerTwo, Subject: "mallory", Action: "audit.search", Outcome: "DENIED",
		Rule: "DENY-03", Product: productA, ResourceType: "AuditRecord", Correlation: "request-3"}
	b1 := seeded{ID: idOf(4), At: at(1, 0), Issuer: issuerOne, Subject: "bob", Action: "product.register", Outcome: "SUCCEEDED",
		Rule: "AC-08", Product: productB, ResourceType: "Product", Resource: productB, Correlation: "request-4"}
	b2 := seeded{ID: idOf(5), At: at(3, 0), Issuer: issuerOne, Subject: "bob", Action: "audit.search", Outcome: "DENIED",
		Rule: "DENY-03", Product: productB, ResourceType: "AuditRecord", Correlation: "request-5"}
	// A record of the platform scope: a refusal of a registration, which
	// concerns no product yet. It is the newest there is, and in no list.
	platform := seeded{ID: idOf(6), At: at(5, 0), Issuer: issuerOne, Subject: "mallory", Action: "product.register", Outcome: "DENIED",
		Rule: "DENY-03", ResourceType: "Product", Correlation: "request-6"}
	f.seed(t, a2, b2, platform, a1, b1, a3)

	for product, want := range map[string][]seeded{productA: {a3, a2, a1}, productB: {b2, b1}} {
		got := f.page(t, product, domain.AuditFilter{}, nil, 10)
		var wanted []domain.AuditRecord
		for _, row := range want {
			wanted = append(wanted, row.record())
		}
		if !reflect.DeepEqual(got, wanted) {
			t.Errorf("the records of %s are\n %+v\nwant\n %+v", product, got, wanted)
		}
	}
	if got := f.page(t, "3e1c6a9d-74f2-4b0a-98c3-b2e5f4a6c7d8", domain.AuditFilter{}, nil, 10); len(got) != 0 {
		t.Errorf("a product with no record has the records %v, want none", idsOf(got))
	}

	// The record of no product is in the list of nobody, whatever the filter
	// and whatever the page.
	for _, product := range []string{productA, productB} {
		for _, size := range []int{1, 2, 10} {
			for _, page := range f.read(t, product, domain.AuditFilter{}, size) {
				if slices.Contains(page, platform.ID) {
					t.Errorf("the record of the platform scope is in the list of %s, in pages of %d", product, size)
				}
			}
		}
	}
	if got := f.page(t, productA, domain.AuditFilter{Action: "product.register", Outcome: domain.OutcomeDenied}, nil, 10); len(got) != 0 {
		t.Errorf("the refusal of the platform scope is in the list of %s: %v", productA, idsOf(got))
	}
}

// The records of one instant are told apart by their identifier, newest
// identifier first, and the position of a page is exact to the microsecond: a
// page behind a record repeats none that was read and skips none that was not,
// in whatever size the list is read.
func TestRecordsOfOneInstantAreOrderedByIdentifierAndNoneIsRepeatedOrSkipped(t *testing.T) {
	f := newFixture(t)
	row := func(n int, when time.Time) seeded {
		return seeded{ID: idOf(n), At: when, Issuer: issuerOne, Subject: "alice", Action: "asset.create", Outcome: "SUCCEEDED",
			Rule: "AC-11", Product: productA, ResourceType: "APIAsset", Resource: idOf(100 + n), Correlation: fmt.Sprintf("request-%d", n)}
	}
	// Seven records of one instant, entered in no order, one a microsecond
	// after them and one a microsecond before.
	var rows []seeded
	for _, n := range []int{4, 7, 1, 5, 2, 6, 3} {
		rows = append(rows, row(n, at(30, 500)))
	}
	rows = append(rows, row(8, at(30, 501)), row(9, at(30, 499)))
	f.seed(t, rows...)

	want := []string{idOf(8), idOf(7), idOf(6), idOf(5), idOf(4), idOf(3), idOf(2), idOf(1), idOf(9)}
	for _, size := range []int{1, 2, 3, 4, 9, 10} {
		pages := f.read(t, productA, domain.AuditFilter{}, size)
		if got := slices.Concat(pages...); !slices.Equal(got, want) {
			t.Errorf("in pages of %d the records are %v, want %v", size, got, want)
		}
		if wantPages := (len(want) + size - 1) / size; len(pages) != wantPages {
			t.Errorf("in pages of %d the list has %d pages, want %d", size, len(pages), wantPages)
		}
		for index, page := range pages[:len(pages)-1] {
			if len(page) != size {
				t.Errorf("in pages of %d, page %d holds %d records and is not the last", size, index+1, len(page))
			}
		}
	}

	// The order is the statement's, and not the one of the index it happens to
	// be planned with: a plan that reads the table whole and sorts it, and one
	// that reads the index in a bitmap, give the same list. A statement that
	// was prepared and has been run a few times keeps the plan it was given
	// (a generic one), whatever the planner is told afterwards, so each run is
	// planned anew.
	for name, settings := range map[string][]string{
		"the table read whole": {
			"SET LOCAL plan_cache_mode = force_custom_plan", "SET LOCAL enable_indexscan = off", "SET LOCAL enable_bitmapscan = off"},
		"the index read as a bitmap": {
			"SET LOCAL plan_cache_mode = force_custom_plan", "SET LOCAL enable_indexscan = off", "SET LOCAL enable_seqscan = off"},
	} {
		for _, size := range []int{1, 3} {
			if got := slices.Concat(f.readUnder(t, settings, productA, domain.AuditFilter{}, size)...); !slices.Equal(got, want) {
				t.Errorf("with %s, in pages of %d the records are %v, want %v", name, size, got, want)
			}
		}
	}

	// A page behind a record of that instant starts behind that identifier:
	// the records of the instant with a smaller one, and then the older ones.
	behind := ports.Position{At: at(30, 500), ID: idOf(5)}
	if got := idsOf(f.page(t, productA, domain.AuditFilter{}, &behind, 10)); !slices.Equal(got, []string{idOf(4), idOf(3), idOf(2), idOf(1), idOf(9)}) {
		t.Errorf("behind %+v the records are %v", behind, got)
	}
	// And one behind a position no record is at: between two instants.
	between := ports.Position{At: at(30, 500).Add(300 * time.Nanosecond), ID: "ffffffff-ffff-4fff-bfff-ffffffffffff"}
	if got := idsOf(f.page(t, productA, domain.AuditFilter{}, &between, 20)); !slices.Equal(got, want[1:]) {
		t.Errorf("behind %+v the records are %v, want %v", between, got, want[1:])
	}
}

// The list is read from what is there when a page is read. What is recorded
// while it is read comes before the position of every page that was handed
// out, so it repeats nothing and takes nothing from what is behind it. A
// record that was committed late, with an instant older than the position, is
// behind it, and is read once with the others; it is no record that was read.
func TestAPageSurvivesRecordsAddedWhileItIsRead(t *testing.T) {
	f := newFixture(t)
	row := func(n int, when time.Time) seeded {
		return seeded{ID: idOf(n), At: when, Issuer: issuerOne, Subject: "alice", Action: "asset.create", Outcome: "SUCCEEDED",
			Rule: "AC-11", Product: productA, ResourceType: "APIAsset", Resource: idOf(100 + n), Correlation: fmt.Sprintf("request-%d", n)}
	}
	var first []seeded
	for n := 1; n <= 10; n++ {
		first = append(first, row(n, at(n, 0)))
	}
	f.seed(t, first...)

	// The first page: the three newest.
	one := f.page(t, productA, domain.AuditFilter{}, nil, 3)
	if got := idsOf(one[:3]); !slices.Equal(got, []string{idOf(10), idOf(9), idOf(8)}) || len(one) != 4 {
		t.Fatalf("the first page holds %v of %d records read, want the three newest and one more", got, len(one))
	}
	cursor := ports.Position{At: one[2].RecordedAt, ID: one[2].ID}

	// While the list is read, a record is added that is newer than all of
	// them, one that is between the first page and the cursor of the second
	// (already read past), and one that was recorded earlier and is committed
	// only now, between the fifth record and the sixth.
	f.seed(t,
		row(11, at(11, 0)),
		row(12, at(8, 500)),
		row(13, at(5, 500)),
	)

	var second []string
	after := &cursor
	for {
		records := f.page(t, productA, domain.AuditFilter{}, after, 3)
		more := len(records) > 3
		if more {
			records = records[:3]
		}
		second = append(second, idsOf(records)...)
		if !more {
			break
		}
		last := records[len(records)-1]
		after = &ports.Position{At: last.RecordedAt, ID: last.ID}
	}
	// Behind the cursor there is what was there, once, and the record that
	// came late and belongs there. What came before the cursor is not read,
	// and nothing that was read is read again.
	want := []string{idOf(7), idOf(6), idOf(13), idOf(5), idOf(4), idOf(3), idOf(2), idOf(1)}
	if !slices.Equal(second, want) {
		t.Errorf("behind the cursor the records are %v, want %v", second, want)
	}
	for _, id := range []string{idOf(11), idOf(12)} {
		if slices.Contains(second, id) {
			t.Errorf("the record %s came before the cursor and was read behind it", id)
		}
	}
	for _, id := range idsOf(one[:3]) {
		if slices.Contains(second, id) {
			t.Errorf("the record %s was read on the first page and again behind it", id)
		}
	}

	// The list as it is now holds all of them, once, newest first.
	whole := slices.Concat(f.read(t, productA, domain.AuditFilter{}, 3)...)
	if want := []string{
		idOf(11), idOf(10), idOf(9), idOf(12), idOf(8), idOf(7), idOf(6), idOf(13), idOf(5), idOf(4), idOf(3), idOf(2), idOf(1),
	}; !slices.Equal(whole, want) {
		t.Errorf("the list now holds %v, want %v", whole, want)
	}
}

// Every filter narrows the list to the records that are what it says, exactly,
// and the filters narrow it together. The ends of a time are an interval that
// includes the first and not the second, to the microsecond. What is not set
// lets everything through, and what is set is no pattern.
func TestEachFilterNarrowsTheListAndTheFiltersAddUp(t *testing.T) {
	f := newFixture(t)
	r1 := seeded{ID: idOf(1), At: at(0, 0), Issuer: issuerOne, Subject: "alice", Action: "product.register", Outcome: "SUCCEEDED",
		Rule: "AC-08", Product: productA, ResourceType: "Product", Resource: productA, Correlation: "request-1"}
	r2 := seeded{ID: idOf(2), At: at(1, 0), Issuer: issuerOne, Subject: "alice", Action: "product_role.grant", Outcome: "SUCCEEDED",
		Rule: "AC-10", Product: productA, ResourceType: "RoleAssignment", Resource: idOf(101), Correlation: "request-2"}
	r3 := seeded{ID: idOf(3), At: at(2, 0), Issuer: issuerOne, Subject: "bob", Action: "product_role.grant", Outcome: "DENIED",
		Rule: "DENY-03", Product: productA, ResourceType: "RoleAssignment", Correlation: "request-3"}
	// alice again, under another issuer: the same subject is another principal.
	r4 := seeded{ID: idOf(4), At: at(3, 0), Issuer: issuerTwo, Subject: "alice", Action: "member.grant", Outcome: "SUCCEEDED",
		Rule: "AC-10", Product: productA, ResourceType: "Member", Resource: idOf(102), Correlation: "request-4"}
	r5 := seeded{ID: idOf(5), At: at(4, 0), Issuer: issuerOne, Subject: "mallory", Action: "audit.search", Outcome: "DENIED",
		Rule: "DENY-03", Product: productA, ResourceType: "AuditRecord", Correlation: "request-5"}
	// The same request as r4 recorded another fact, a microsecond past the
	// minute.
	r6 := seeded{ID: idOf(6), At: at(5, 1), Issuer: issuerOne, Subject: "bob", Action: "member.grant", Outcome: "SUCCEEDED",
		Rule: "AC-10", Product: productA, ResourceType: "Member", Resource: idOf(103), Correlation: "request-4"}
	// Another product, with every fact the filters ask for: none is read here.
	other := seeded{ID: idOf(7), At: at(2, 500), Issuer: issuerOne, Subject: "alice", Action: "product_role.grant", Outcome: "SUCCEEDED",
		Rule: "AC-10", Product: productB, ResourceType: "RoleAssignment", Resource: idOf(101), Correlation: "request-2"}
	f.seed(t, r1, r2, r3, r4, r5, r6, other)
	names := map[string]string{r1.ID: "r1", r2.ID: "r2", r3.ID: "r3", r4.ID: "r4", r5.ID: "r5", r6.ID: "r6", other.ID: "other"}
	named := func(ids []string) []string {
		var shown []string
		for _, id := range ids {
			shown = append(shown, names[id])
		}
		return shown
	}
	instant := func(row seeded, shift time.Duration) *time.Time {
		moment := row.At.Add(shift)
		return &moment
	}

	cases := map[string]struct {
		filter domain.AuditFilter
		want   []string
	}{
		"nothing":                                        {domain.AuditFilter{}, []string{"r6", "r5", "r4", "r3", "r2", "r1"}},
		"an action":                                      {domain.AuditFilter{Action: "product_role.grant"}, []string{"r3", "r2"}},
		"an action of no record":                         {domain.AuditFilter{Action: "product_role.revoke"}, nil},
		"a succeeded outcome":                            {domain.AuditFilter{Outcome: domain.OutcomeSucceeded}, []string{"r6", "r4", "r2", "r1"}},
		"a denied outcome":                               {domain.AuditFilter{Outcome: domain.OutcomeDenied}, []string{"r5", "r3"}},
		"a subject, under any issuer":                    {domain.AuditFilter{ActorSubject: "alice"}, []string{"r4", "r2", "r1"}},
		"a subject under an issuer":                      {domain.AuditFilter{ActorIssuer: issuerOne, ActorSubject: "alice"}, []string{"r2", "r1"}},
		"an issuer alone":                                {domain.AuditFilter{ActorIssuer: issuerTwo}, []string{"r4"}},
		"an issuer that acted a lot":                     {domain.AuditFilter{ActorIssuer: issuerOne}, []string{"r6", "r5", "r3", "r2", "r1"}},
		"a subject under the wrong issuer":               {domain.AuditFilter{ActorIssuer: issuerTwo, ActorSubject: "bob"}, nil},
		"a kind of resource":                             {domain.AuditFilter{ResourceType: "Member"}, []string{"r6", "r4"}},
		"a resource":                                     {domain.AuditFilter{ResourceID: idOf(101)}, []string{"r2"}},
		"a resource no record names":                     {domain.AuditFilter{ResourceID: idOf(999)}, nil},
		"a resource another product has a record of too": {domain.AuditFilter{ResourceID: other.Resource, ResourceType: "RoleAssignment"}, []string{"r2"}},
		"a request":                                      {domain.AuditFilter{CorrelationID: "request-4"}, []string{"r6", "r4"}},
		"a request of a single record":                   {domain.AuditFilter{CorrelationID: "request-5"}, []string{"r5"}},
		"a request of no record":                         {domain.AuditFilter{CorrelationID: "request-9"}, nil},

		"from a record, which is included":      {domain.AuditFilter{RecordedSince: instant(r3, 0)}, []string{"r6", "r5", "r4", "r3"}},
		"until a record, which is not":          {domain.AuditFilter{RecordedBefore: instant(r3, 0)}, []string{"r2", "r1"}},
		"between two records":                   {domain.AuditFilter{RecordedSince: instant(r2, 0), RecordedBefore: instant(r4, 0)}, []string{"r3", "r2"}},
		"from the instant of the last":          {domain.AuditFilter{RecordedSince: instant(r6, 0)}, []string{"r6"}},
		"from the microsecond after the last":   {domain.AuditFilter{RecordedSince: instant(r6, time.Microsecond)}, nil},
		"until the instant of the last":         {domain.AuditFilter{RecordedBefore: instant(r6, 0)}, []string{"r5", "r4", "r3", "r2", "r1"}},
		"until the microsecond after the last":  {domain.AuditFilter{RecordedBefore: instant(r6, time.Microsecond)}, []string{"r6", "r5", "r4", "r3", "r2", "r1"}},
		"from the microsecond before the first": {domain.AuditFilter{RecordedSince: instant(r1, -time.Microsecond)}, []string{"r6", "r5", "r4", "r3", "r2", "r1"}},
		"until the first":                       {domain.AuditFilter{RecordedBefore: instant(r1, 0)}, nil},
		"a range of one microsecond":            {domain.AuditFilter{RecordedSince: instant(r6, 0), RecordedBefore: instant(r6, time.Microsecond)}, []string{"r6"}},

		"an action and a subject":                         {domain.AuditFilter{Action: "member.grant", ActorSubject: "bob"}, []string{"r6"}},
		"an action and an outcome":                        {domain.AuditFilter{Action: "product_role.grant", Outcome: domain.OutcomeSucceeded}, []string{"r2"}},
		"an outcome and a kind of resource":               {domain.AuditFilter{Outcome: domain.OutcomeDenied, ResourceType: "AuditRecord"}, []string{"r5"}},
		"a request and a time":                            {domain.AuditFilter{CorrelationID: "request-4", RecordedSince: instant(r5, 0)}, []string{"r6"}},
		"a subject, an issuer and a range":                {domain.AuditFilter{ActorIssuer: issuerOne, ActorSubject: "alice", RecordedSince: instant(r2, 0)}, []string{"r2"}},
		"every filter, of one record":                     {domain.AuditFilter{RecordedSince: instant(r2, 0), RecordedBefore: instant(r3, 0), Action: "product_role.grant", Outcome: domain.OutcomeSucceeded, ActorIssuer: issuerOne, ActorSubject: "alice", ResourceType: "RoleAssignment", ResourceID: idOf(101), CorrelationID: "request-2"}, []string{"r2"}},
		"filters that no record satisfies together":       {domain.AuditFilter{Action: "member.grant", Outcome: domain.OutcomeDenied}, nil},
		"a request and the wrong kind of resource":        {domain.AuditFilter{CorrelationID: "request-4", ResourceType: "RoleAssignment"}, nil},
		"an action that both records of the request have": {domain.AuditFilter{CorrelationID: "request-4", Action: "member.grant"}, []string{"r6", "r4"}},

		// What is set is matched exactly: no pattern, no case folding, no
		// prefix, and nothing that reads as SQL.
		"a prefix of a subject":       {domain.AuditFilter{ActorSubject: "ali"}, nil},
		"a subject in upper case":     {domain.AuditFilter{ActorSubject: "ALICE"}, nil},
		"a wildcard for a subject":    {domain.AuditFilter{ActorSubject: "%"}, nil},
		"a wildcard inside a subject": {domain.AuditFilter{ActorSubject: "a_ice"}, nil},
		"a pattern for an action":     {domain.AuditFilter{Action: "product_role.%"}, nil},
		"an action with a wildcard":   {domain.AuditFilter{Action: "product%role.grant"}, nil},
		"SQL for a request":           {domain.AuditFilter{CorrelationID: "request-4' OR '1'='1"}, nil},
		"a request in another case":   {domain.AuditFilter{CorrelationID: "REQUEST-4"}, nil},
		"an issuer with a suffix":     {domain.AuditFilter{ActorIssuer: issuerOne + "/"}, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := named(idsOf(f.page(t, productA, c.filter, nil, 20)))
			if !slices.Equal(got, c.want) {
				t.Errorf("the records are %v, want %v", got, c.want)
			}
			// However it is read, the filters hold on every page: a filter
			// is applied before paging, not to the page.
			for _, size := range []int{1, 2} {
				if pages := slices.Concat(f.read(t, productA, c.filter, size)...); !slices.Equal(named(pages), c.want) {
					t.Errorf("in pages of %d the records are %v, want %v", size, named(pages), c.want)
				}
			}
		})
	}

	// An empty answer is an empty list and not a missing one.
	if got := f.page(t, productA, domain.AuditFilter{Action: "product_role.revoke"}, nil, 20); got == nil {
		t.Errorf("a list with no record is nil, want an empty list")
	}
}

// A page takes at least one record: it is asked for with one more than the page
// holds, and nothing is read for a limit that is none.
func TestThePageOfTheStoreTakesAtLeastOneRecord(t *testing.T) {
	f := newFixture(t)
	for _, limit := range []int{0, -1} {
		if got, err := postgres.NewAuditReader(f.fabric.Framework).List(t.Context(), productA, domain.AuditFilter{}, ports.Page{Limit: limit}); err == nil {
			t.Errorf("a limit of %d: got %v and no error", limit, got)
		}
	}
}

// What a page costs is what it returns, whatever the trail holds: the index of
// the list is read where the page starts, and a request is found by its
// identifier without reading the trail of its product. This guards the work
// performed, not the time it takes, and not the type of the join: the number
// of rows the statement visits in the table.
func TestAPageOfTheAuditTrailCostsWhatItReturns(t *testing.T) {
	f := newFixture(t)
	// 20,000 records of each of two products, written alternately: a request
	// of the product under test is named by its number.
	for product, tag := range map[string]string{productA: "request", productB: "other"} {
		if _, err := f.owner.Exec(t.Context(), `
			INSERT INTO platformoperations.audit_record
			    (audit_id, recorded_at, actor_issuer, actor_subject, action, outcome, rule_id,
			     product_id, resource_type, resource_id, correlation_id)
			SELECT gen_random_uuid(), $1::timestamptz + i * interval '1 microsecond', $2, 'user-' || (i % 50),
			       'asset.read', CASE WHEN i % 10 = 0 THEN 'DENIED' ELSE 'SUCCEEDED' END, 'DENY-03',
			       $3::uuid, 'APIAsset', gen_random_uuid(), $4 || '-' || i
			FROM generate_series(1, 20000) AS i`, at(0, 0), issuerOne, product, tag); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.owner.Exec(t.Context(), "ANALYZE platformoperations.audit_record"); err != nil {
		t.Fatal(err)
	}

	middle := f.page(t, productA, domain.AuditFilter{RecordedBefore: ptr(at(0, 12000))}, nil, 1)
	if len(middle) < 1 {
		t.Fatalf("a record in the middle of the trail: %d records", len(middle))
	}
	behind := ports.Position{At: middle[0].RecordedAt, ID: middle[0].ID}
	from, until := at(0, 9000), at(0, 9100)

	// visited is the number of rows the statement reads from the table to
	// answer one page, as PostgreSQL says it did, and whether it had to sort
	// them: a list that is read from the index in its own order is not sorted.
	visited := func(position *ports.Position, filter domain.AuditFilter) (rows int, sorted bool, plan string) {
		t.Helper()
		var at, id any
		if position != nil {
			at, id = position.At, position.ID
		}
		var encoded []byte
		if err := f.app.QueryRow(t.Context(), "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+postgres.ListAuditRecordsForTest,
			at, id, 101, productA, parameterOf(filter.RecordedSince), parameterOf(filter.RecordedBefore),
			filter.Action, string(filter.Outcome), filter.ActorIssuer, filter.ActorSubject,
			filter.ResourceType, filter.ResourceID, filter.CorrelationID).Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		type planNode struct {
			Type     string     `json:"Node Type"`
			Relation string     `json:"Relation Name"`
			Rows     float64    `json:"Actual Rows"`
			Loops    float64    `json:"Actual Loops"`
			Removed  float64    `json:"Rows Removed by Filter"`
			Rechecks float64    `json:"Rows Removed by Index Recheck"`
			Children []planNode `json:"Plans"`
		}
		var plans []struct{ Plan planNode }
		if err := json.Unmarshal(encoded, &plans); err != nil || len(plans) != 1 {
			t.Fatalf("decode the execution plan: %v: %s", err, encoded)
		}
		var total float64
		var walk func(planNode)
		walk = func(node planNode) {
			if node.Relation == "audit_record" {
				total += (node.Rows + node.Removed + node.Rechecks) * node.Loops
			}
			if strings.Contains(node.Type, "Sort") {
				sorted = true
			}
			for _, child := range node.Children {
				walk(child)
			}
		}
		walk(plans[0].Plan)
		return int(total), sorted, string(encoded)
	}

	// A list that is read through the index of the list is in its order
	// already; a request is read through the index of the requests, and its
	// few records are sorted. A range of a hundred records is read either way,
	// whichever the planner finds cheaper on the statistics of the day: both
	// visit a hundred rows, and that is all that is asked of it.
	for name, c := range map[string]struct {
		position *ports.Position
		filter   domain.AuditFilter
		most     int
		inOrder  bool
	}{
		"the first page":                 {nil, domain.AuditFilter{}, 400, true},
		"a page in the middle":           {&behind, domain.AuditFilter{}, 400, true},
		"a range of time":                {nil, domain.AuditFilter{RecordedSince: &from, RecordedBefore: &until}, 400, false},
		"one request among 20,000":       {nil, domain.AuditFilter{CorrelationID: "request-7000"}, 20, false},
		"a request that is none":         {nil, domain.AuditFilter{CorrelationID: "request-0"}, 20, false},
		"the denied records, one in ten": {nil, domain.AuditFilter{Outcome: domain.OutcomeDenied}, 2000, true},
	} {
		got, sorted, plan := visited(c.position, c.filter)
		if got < 1 && name != "a request that is none" || got > c.most {
			t.Errorf("%s: %d rows of 40,000 were visited, want at most %d: %s", name, got, c.most, plan)
		}
		if c.inOrder && sorted {
			t.Errorf("%s: the records are sorted, and not read in the order of the index of the list: %s", name, plan)
		}
		t.Logf("%s: %d rows visited of 40,000, sorted: %t", name, got, sorted)
	}
	if !strings.Contains(postgres.ListAuditRecordsForTest, "ORDER BY audit_record.recorded_at DESC, audit_record.audit_id DESC") {
		t.Errorf("the statement does not order the records as the list is ordered")
	}
}

func ptr(t time.Time) *time.Time { return &t }

// parameterOf is an end of a range of time as a statement takes it: no instant
// is NULL.
func parameterOf(at *time.Time) any {
	if at == nil {
		return nil
	}
	return *at
}
