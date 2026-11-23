package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platformoperations/domain"
	"go.idp.xyz/idp-api-fabric/internal/platformoperations/ports"
)

// The fakes stand in for the ports. The real store is tested against
// PostgreSQL in adapters/postgres, and the whole of it in cmd/fabric-api. The
// transactor of recordaudit_test.go says whether a call was made inside a
// transaction.

type scopeAsked struct {
	caller     domain.Actor
	product    string
	transacted bool
}

// scopes says what the callers are to the product, whoever asks, and keeps
// what it was asked.
type scopes struct {
	scope ports.AuditScope
	err   error
	asked []scopeAsked
}

func (s *scopes) AuditScope(ctx context.Context, caller domain.Actor, product string) (ports.AuditScope, error) {
	s.asked = append(s.asked, scopeAsked{caller, product, ctx.Value(inTransaction{}) == true})
	return s.scope, s.err
}

type readAsked struct {
	product    string
	filter     domain.AuditFilter
	page       ports.Page
	transacted bool
}

// reader answers the records it was given, whatever it is asked, and keeps
// what it was asked.
type reader struct {
	records []domain.AuditRecord
	err     error
	asked   []readAsked
}

func (r *reader) List(ctx context.Context, product string, filter domain.AuditFilter, page ports.Page) ([]domain.AuditRecord, error) {
	r.asked = append(r.asked, readAsked{product, filter, page, ctx.Value(inTransaction{}) == true})
	return r.records, r.err
}

// refusals keeps what was recorded of a refusal, and whether a transaction was
// open then.
type refusals struct {
	entries    []AuditEntry
	transacted []bool
	fail       error
}

func (r *refusals) Record(ctx context.Context, entry AuditEntry) error {
	if r.fail != nil {
		return r.fail
	}
	r.entries = append(r.entries, entry)
	r.transacted = append(r.transacted, ctx.Value(inTransaction{}) == true)
	return nil
}

const (
	trailedProduct = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
	readRequest    = "7c8d9e0f-1a2b-4c3d-8e4f-5a6b7c8d9e0f"
)

var (
	frank   = domain.Actor{Issuer: "https://issuer.example", Subject: "frank"}
	mallory = domain.Actor{Issuer: "https://issuer.example", Subject: "mallory"}
)

// reading is the use case with its fakes.
type reading struct {
	use        *ReadAudit
	transactor *transactor
	scopes     *scopes
	reader     *reader
	refusals   *refusals
}

// newReading is the use case wired to fakes that make the caller an auditor of
// the product and find nothing in the trail.
func newReading(t *testing.T) reading {
	t.Helper()

	r := reading{
		transactor: &transactor{}, reader: &reader{}, refusals: &refusals{},
		scopes: &scopes{scope: ports.AuditScope{Found: true, Known: true, Auditor: true}},
	}
	use, err := NewReadAudit(ReadAuditDependencies{Transactor: r.transactor, Scopes: r.scopes, Reader: r.reader, Refusals: r.refusals})
	if err != nil {
		t.Fatalf("wire the use case: %v", err)
	}
	r.use = use
	return r
}

// list asks as the caller for the audit records of the product, after
// whatever change makes of the query.
func (r reading) list(caller domain.Actor, change ...func(*ListAuditRecords)) (AuditPage, error) {
	query := ListAuditRecords{Caller: caller, CorrelationID: readRequest, ProductID: trailedProduct}
	for _, apply := range change {
		apply(&query)
	}
	return r.use.List(context.Background(), query)
}

// trail is n records of the product, newest first, a minute apart.
func trail(n int) []domain.AuditRecord {
	var records []domain.AuditRecord
	for index := range n {
		records = append(records, domain.AuditRecord{
			ID:         fmt.Sprintf("00000000-0000-4000-8000-%012d", n-index),
			RecordedAt: time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC).Add(time.Duration(n-index) * time.Minute),
			Actor:      domain.Actor{Issuer: "https://issuer.example", Subject: "alice"},
			Action:     "product_role.grant", Outcome: domain.OutcomeSucceeded, Rule: "AC-10", ProductID: trailedProduct,
			ResourceType: "RoleAssignment", ResourceID: fmt.Sprintf("00000000-0000-4000-8000-%012d", 1000+index),
			CorrelationID: "3b0f6a2d-8c41-4e7f-a5d9-0e1c2b3a4d5e",
		})
	}
	return records
}

// Whoever may know the product exists and is no auditor of it is told it may
// not; to anyone else there is no such product. Either way the refusal is
// recorded, once, under the product, after the transaction that found it: it
// is the trail of the product that holds who tried to read it.
func TestAReadOfTheAuditTrailIsRefusedAndRecordedForWhoeverIsNoAuditor(t *testing.T) {
	cases := map[string]struct {
		scope ports.AuditScope
		want  error
	}{
		"somebody who holds nothing in the product":        {ports.AuditScope{Found: true}, ErrProductNotFound},
		"somebody who knows the product and is no auditor": {ports.AuditScope{Found: true, Known: true}, ErrForbidden},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newReading(t)
			r.scopes.scope = c.scope

			page, err := r.list(mallory)
			if err != c.want || len(page.Items) != 0 || page.Next != nil {
				t.Fatalf("got %+v and %v, want nothing and exactly %v", page, err, c.want)
			}
			wantEntry := AuditEntry{
				Actor: mallory, Action: "audit.search", Outcome: domain.OutcomeDenied, Rule: "DENY-03",
				ProductID: trailedProduct, ResourceType: "AuditRecord", CorrelationID: readRequest,
			}
			if !slices.Equal(r.refusals.entries, []AuditEntry{wantEntry}) || !slices.Equal(r.refusals.transacted, []bool{false}) {
				t.Errorf("recorded\n %+v\nwant, outside the transaction,\n %+v", r.refusals.entries, wantEntry)
			}
			// The record is one the trail takes: it says enough to be evidence.
			if err := (domain.AuditRecord{
				ID: recordID, RecordedAt: recordedAt, Actor: wantEntry.Actor, Action: wantEntry.Action, Outcome: wantEntry.Outcome,
				Rule: wantEntry.Rule, ProductID: wantEntry.ProductID, ResourceType: wantEntry.ResourceType, CorrelationID: wantEntry.CorrelationID,
			}).Validate(); err != nil {
				t.Errorf("the refusal is no record the trail takes: %v", err)
			}
			if len(r.reader.asked) != 0 {
				t.Errorf("the trail was read for somebody who is no auditor: %+v", r.reader.asked)
			}
			if r.transactor.runs != 1 || len(r.scopes.asked) != 1 || !r.scopes.asked[0].transacted ||
				r.scopes.asked[0].caller != mallory || r.scopes.asked[0].product != trailedProduct {
				t.Errorf("the scope was read %+v in %d transactions, want once, inside one, of the caller and the product", r.scopes.asked, r.transactor.runs)
			}
		})
	}
}

// A refusal for a product that is not there is recorded under nothing: there is
// no product whose trail could hold it, and nobody could ever read it.
func TestAReadOfAProductThatIsNotThereLeavesNoRecord(t *testing.T) {
	r := newReading(t)
	r.scopes.scope = ports.AuditScope{}

	if page, err := r.list(frank); err != ErrProductNotFound || len(page.Items) != 0 {
		t.Fatalf("got %+v and %v, want nothing and exactly %v", page, err, ErrProductNotFound)
	}
	if len(r.refusals.entries) != 0 || len(r.reader.asked) != 0 {
		t.Errorf("recorded %+v and read %+v, want neither", r.refusals.entries, r.reader.asked)
	}
}

// The auditor reads the page, in the transaction that read what it is, and
// nothing is recorded of it: no read is, and the trail would hold the reading
// of itself.
func TestASuccessfulReadOfTheAuditTrailLeavesNoRecord(t *testing.T) {
	r := newReading(t)
	r.reader.records = trail(2)
	since := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	behind := ports.Position{At: time.Date(2026, 11, 23, 9, 3, 0, 0, time.UTC), ID: "00000000-0000-4000-8000-000000000003"}

	page, err := r.list(frank, func(q *ListAuditRecords) {
		q.PageSize, q.After = 5, &behind
		q.Filter = domain.AuditFilter{RecordedSince: &since, Action: "product_role.grant", Outcome: domain.OutcomeSucceeded}
	})
	if err != nil || !slices.Equal(page.Items, trail(2)) || page.Next != nil {
		t.Fatalf("got %+v and %v, want the two records and no token", page, err)
	}
	if len(r.refusals.entries) != 0 {
		t.Errorf("the read left the records %+v, want none", r.refusals.entries)
	}
	if len(r.reader.asked) != 1 {
		t.Fatalf("the trail was read %d times, want once", len(r.reader.asked))
	}
	got := r.reader.asked[0]
	if !got.transacted || r.transactor.runs != 1 || len(r.scopes.asked) != 1 || !r.scopes.asked[0].transacted {
		t.Errorf("the scope was read in %d transactions and the records read inside one: %t, want one transaction for both", r.transactor.runs, got.transacted)
	}
	if got.product != trailedProduct || got.page.Limit != 6 || got.page.After == nil || *got.page.After != behind ||
		got.filter.Action != "product_role.grant" || got.filter.Outcome != domain.OutcomeSucceeded ||
		got.filter.RecordedSince == nil || !got.filter.RecordedSince.Equal(since) || got.filter.RecordedBefore != nil {
		t.Errorf("the store was asked %+v, want the product, one record more than 5, behind the position, under the filter", got)
	}
}

// The refusal stands whether or not it could be recorded: the caller is
// refused all the same, and the service is told that the trail has a gap.
func TestAReadIsRefusedEvenWhenItsRefusalCannotBeRecorded(t *testing.T) {
	for name, c := range map[string]struct {
		scope ports.AuditScope
		want  error
	}{
		"a stranger":         {ports.AuditScope{Found: true}, ErrProductNotFound},
		"somebody who knows": {ports.AuditScope{Found: true, Known: true}, ErrForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			r := newReading(t)
			r.scopes.scope = c.scope
			r.refusals.fail = errors.New("the trail cannot be written")

			page, err := r.list(mallory)
			if !errors.Is(err, c.want) || !errors.Is(err, ErrRefusalNotAudited) || len(page.Items) != 0 {
				t.Errorf("got %+v and %v, want nothing, %v and %v", page, err, c.want, ErrRefusalNotAudited)
			}
			if len(r.reader.asked) != 0 {
				t.Errorf("the trail was read for somebody who is no auditor")
			}
		})
	}
}

// What the request says of the records is looked at once the caller is known
// to read them: whoever is refused is told nothing else, and is told the same
// whatever the request says. A request that asks what no record can answer is
// no refusal of the caller and leaves no record.
func TestTheCallerIsJudgedBeforeTheFilterIsLookedAt(t *testing.T) {
	now := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	later := now.Add(time.Hour)
	invalid := map[string]func(*ListAuditRecords){
		"a range that ends before it begins": func(q *ListAuditRecords) {
			q.Filter = domain.AuditFilter{RecordedSince: &later, RecordedBefore: &now}
		},
		"an outcome nobody defined":     func(q *ListAuditRecords) { q.Filter.Outcome = "MAYBE" },
		"a size below one":              func(q *ListAuditRecords) { q.PageSize = -1 },
		"a size above the largest":      func(q *ListAuditRecords) { q.PageSize = MaxPageSize + 1 },
		"a position with no instant":    func(q *ListAuditRecords) { q.After = &ports.Position{ID: "00000000-0000-4000-8000-000000000001"} },
		"a position with no identifier": func(q *ListAuditRecords) { q.After = &ports.Position{At: now, ID: "x"} },
	}
	for name, change := range invalid {
		t.Run(name, func(t *testing.T) {
			// Whoever may not read the records is refused as if it asked for
			// the list as it is.
			for refused, c := range map[string]struct {
				scope ports.AuditScope
				want  error
			}{
				"a stranger":         {ports.AuditScope{Found: true}, ErrProductNotFound},
				"somebody who knows": {ports.AuditScope{Found: true, Known: true}, ErrForbidden},
			} {
				r := newReading(t)
				r.scopes.scope = c.scope
				if _, err := r.list(mallory, change); err != c.want {
					t.Errorf("%s: got %v, want exactly %v", refused, err, c.want)
				}
				if len(r.refusals.entries) != 1 || len(r.reader.asked) != 0 {
					t.Errorf("%s: recorded %+v and read %+v, want one refusal and no read", refused, r.refusals.entries, r.reader.asked)
				}
			}

			// The auditor is told what is wrong, and nothing is recorded.
			r := newReading(t)
			if page, err := r.list(frank, change); !errors.Is(err, domain.ErrInvalidAuditQuery) || len(page.Items) != 0 {
				t.Errorf("the auditor: got %+v and %v, want nothing and %v", page, err, domain.ErrInvalidAuditQuery)
			}
			if len(r.refusals.entries) != 0 || len(r.reader.asked) != 0 {
				t.Errorf("the auditor: recorded %+v and read %+v, want neither", r.refusals.entries, r.reader.asked)
			}
		})
	}
}

// A page holds at most what was asked, and the store is asked for one record
// more, to know whether a page follows without counting what the caller may
// read. A page that is full and is the last has no next: a caller does not
// follow a token to an empty page.
func TestAPageNamesTheNextOnlyWhenOneFollows(t *testing.T) {
	cases := map[string]struct {
		size, stored, limit, items int
		next                       bool
	}{
		"one record more than the page holds":    {3, 4, 4, 3, true},
		"exactly what the page holds":            {3, 3, 4, 3, false},
		"fewer than the page holds":              {3, 2, 4, 2, false},
		"nothing":                                {3, 0, 4, 0, false},
		"the default size, one record too many":  {0, DefaultPageSize + 1, DefaultPageSize + 1, DefaultPageSize, true},
		"the default size, a full last page":     {0, DefaultPageSize, DefaultPageSize + 1, DefaultPageSize, false},
		"the largest size, one record too many":  {MaxPageSize, MaxPageSize + 1, MaxPageSize + 1, MaxPageSize, true},
		"the smallest size, two records waiting": {1, 2, 2, 1, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newReading(t)
			r.reader.records = trail(c.stored)

			page, err := r.list(frank, func(q *ListAuditRecords) { q.PageSize = c.size })
			if err != nil || len(page.Items) != c.items || (page.Next != nil) != c.next {
				t.Fatalf("got %d records and the next %+v (%v), want %d and a next: %t", len(page.Items), page.Next, err, c.items, c.next)
			}
			if len(r.reader.asked) != 1 || r.reader.asked[0].page.Limit != c.limit {
				t.Errorf("the store was asked %+v, want a limit of %d", r.reader.asked, c.limit)
			}
			if c.next {
				// The next page starts behind the last record of this one.
				last := page.Items[len(page.Items)-1]
				if want := (ports.Position{At: last.RecordedAt, ID: last.ID}); *page.Next != want {
					t.Errorf("the next page starts behind %+v, want %+v", *page.Next, want)
				}
			}
			if !slices.Equal(page.Items, trail(c.stored)[:c.items]) {
				t.Errorf("the page holds %d records out of order or changed", len(page.Items))
			}
		})
	}
}

// A position is handed to the store as a copy: what the store does with it is
// no business of the caller's.
func TestAPositionIsHandedToTheStoreAsACopy(t *testing.T) {
	r := newReading(t)
	behind := ports.Position{At: time.Date(2026, 11, 23, 9, 3, 0, 0, time.UTC), ID: "00000000-0000-4000-8000-000000000003"}
	if _, err := r.list(frank, func(q *ListAuditRecords) { q.After = &behind }); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := r.reader.asked[0].page.After; got == &behind || *got != behind {
		t.Errorf("the store was handed %p and %+v, want a copy of %+v", got, *got, behind)
	}
}

// The trail keeps an instant in UTC, to the microsecond, and is asked for one
// the way it keeps it.
func TestTheInstantsOfAFilterAreAskedForAsTheTrailKeepsThem(t *testing.T) {
	r := newReading(t)
	shanghai := time.FixedZone("UTC+8", 8*60*60)
	since := time.Date(2026, 11, 23, 10, 30, 0, 123456789, shanghai)
	before := time.Date(2026, 11, 23, 11, 30, 0, 0, shanghai)

	if _, err := r.list(frank, func(q *ListAuditRecords) {
		q.Filter = domain.AuditFilter{RecordedSince: &since, RecordedBefore: &before}
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	got := r.reader.asked[0].filter
	if want := time.Date(2026, 11, 23, 2, 30, 0, 123456000, time.UTC); got.RecordedSince == nil || !got.RecordedSince.Equal(want) ||
		got.RecordedSince.Location() != time.UTC {
		t.Errorf("the store was asked from %v, want %v in UTC", got.RecordedSince, want)
	}
	if want := time.Date(2026, 11, 23, 3, 30, 0, 0, time.UTC); got.RecordedBefore == nil || !got.RecordedBefore.Equal(want) {
		t.Errorf("the store was asked until %v, want %v", got.RecordedBefore, want)
	}
}

// What cannot be answered is no refusal of the caller: a failure to say what
// it is to the product, and a failure to read the records, are failures of the
// service. Nothing is recorded of them, and the caller is told nothing of them.
func TestAFailureToReadIsNoRefusal(t *testing.T) {
	scopeDown := errors.New("the roles cannot be read")
	storeDown := errors.New("the trail cannot be read")

	r := newReading(t)
	r.scopes.err = scopeDown
	if _, err := r.list(frank); !errors.Is(err, scopeDown) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrProductNotFound) {
		t.Errorf("a scope that cannot be read: got %v, want %v alone", err, scopeDown)
	}
	if len(r.refusals.entries) != 0 || len(r.reader.asked) != 0 {
		t.Errorf("a scope that cannot be read: recorded %+v and read %+v, want neither", r.refusals.entries, r.reader.asked)
	}

	r = newReading(t)
	r.reader.err = storeDown
	if page, err := r.list(frank); !errors.Is(err, storeDown) || len(page.Items) != 0 || page.Next != nil {
		t.Errorf("a trail that cannot be read: got %+v and %v, want nothing and %v", page, err, storeDown)
	}
	if len(r.refusals.entries) != 0 {
		t.Errorf("a trail that cannot be read: recorded %+v, want nothing", r.refusals.entries)
	}
}

// Before anything is read, a request that names no principal, and one that
// names no product, are answered.
func TestARequestThatNamesNoPrincipalAndNoProductReadsNothing(t *testing.T) {
	for name, c := range map[string]struct {
		caller  domain.Actor
		product string
		want    error
	}{
		"no principal":                {domain.Actor{}, trailedProduct, ErrForbidden},
		"no subject":                  {domain.Actor{Issuer: "https://issuer.example"}, trailedProduct, ErrForbidden},
		"no issuer":                   {domain.Actor{Subject: "frank"}, trailedProduct, ErrForbidden},
		"a product that is none":      {frank, "cargopai", ErrProductNotFound},
		"an identifier in upper case": {frank, "1C9A4E7B-52D0-4F8E-B6A1-90C3D2E4F5A6", ErrProductNotFound},
		"no product":                  {frank, "", ErrProductNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			r := newReading(t)
			_, err := r.list(c.caller, func(q *ListAuditRecords) { q.ProductID = c.product })
			if !errors.Is(err, c.want) {
				t.Errorf("got %v, want %v", err, c.want)
			}
			if r.transactor.runs != 0 || len(r.scopes.asked) != 0 || len(r.reader.asked) != 0 || len(r.refusals.entries) != 0 {
				t.Errorf("something was done for a request that names no principal or no product")
			}
		})
	}
}

func TestReadingTheAuditTrailNeedsAllItsPorts(t *testing.T) {
	complete := ReadAuditDependencies{Transactor: &transactor{}, Scopes: &scopes{}, Reader: &reader{}, Refusals: &refusals{}}
	if _, err := NewReadAudit(complete); err != nil {
		t.Fatalf("a complete wiring was refused: %v", err)
	}
	for name, remove := range map[string]func(*ReadAuditDependencies){
		"a transactor": func(d *ReadAuditDependencies) { d.Transactor = nil },
		"the scopes":   func(d *ReadAuditDependencies) { d.Scopes = nil },
		"a reader":     func(d *ReadAuditDependencies) { d.Reader = nil },
		"the refusals": func(d *ReadAuditDependencies) { d.Refusals = nil },
	} {
		incomplete := complete
		remove(&incomplete)
		if _, err := NewReadAudit(incomplete); err == nil {
			t.Errorf("a wiring without %s was accepted", name)
		}
	}
}
