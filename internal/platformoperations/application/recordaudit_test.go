package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platformoperations/domain"
)

// The fakes stand in for the ports. The real store is tested against
// PostgreSQL in adapters/postgres.

type inTransaction struct{}

// transactor runs fn with a context that says a transaction is open.
type transactor struct{ runs int }

func (t *transactor) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	t.runs++
	return fn(context.WithValue(ctx, inTransaction{}, true))
}

// records keeps what was appended and whether a transaction was open then.
type records struct {
	appended   []domain.AuditRecord
	transacted []bool
	fail       error
}

func (r *records) Append(ctx context.Context, record domain.AuditRecord) error {
	if r.fail != nil {
		return r.fail
	}
	r.appended = append(r.appended, record)
	r.transacted = append(r.transacted, ctx.Value(inTransaction{}) == true)
	return nil
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type fixedIDs struct{ id string }

func (i fixedIDs) NewID() string { return i.id }

var (
	// The clock answers in another zone than UTC: a record is kept in UTC
	// whatever the clock says.
	recordedAt = time.Date(2026, 11, 11, 17, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	recordID   = "6f1d2c3a-0b7e-4c59-9a43-5e2f8d1c7b60"
)

func entry() AuditEntry {
	return AuditEntry{
		Actor:         domain.Actor{Issuer: "https://issuer.example", Subject: "alice"},
		Action:        "product.register",
		Outcome:       domain.OutcomeSucceeded,
		Rule:          "AC-08",
		ProductID:     "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6",
		ResourceType:  "Product",
		ResourceID:    "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6",
		CorrelationID: "3b0f6a2d-8c41-4e7f-a5d9-0e1c2b3a4d5e",
	}
}

func TestRecordAppendsTheEntryWithAnIdentifierAndTheTimeInUTC(t *testing.T) {
	transactor, store := &transactor{}, &records{}
	useCase := NewRecordAudit(transactor, store, fixedClock{recordedAt}, fixedIDs{recordID})

	if err := useCase.Record(context.Background(), entry()); err != nil {
		t.Fatalf("record: %v", err)
	}
	if len(store.appended) != 1 {
		t.Fatalf("%d records were appended, want 1", len(store.appended))
	}
	if !store.transacted[0] || transactor.runs != 1 {
		t.Error("the record was not appended inside one transaction")
	}

	got, given := store.appended[0], entry()
	want := domain.AuditRecord{
		ID: recordID, RecordedAt: recordedAt.UTC(),
		Actor: given.Actor, Action: given.Action, Outcome: given.Outcome, Rule: given.Rule,
		ProductID: given.ProductID, ResourceType: given.ResourceType, ResourceID: given.ResourceID,
		CorrelationID: given.CorrelationID,
	}
	if got != want {
		t.Errorf("appended\n %+v\nwant\n %+v", got, want)
	}
	if got.RecordedAt.Location() != time.UTC {
		t.Errorf("the record is timed in %s, want UTC", got.RecordedAt.Location())
	}
}

func TestRecordRefusesAnEntryThatSaysTooLittle(t *testing.T) {
	transactor, store := &transactor{}, &records{}
	useCase := NewRecordAudit(transactor, store, fixedClock{recordedAt}, fixedIDs{recordID})

	incomplete := entry()
	incomplete.Rule = ""
	if err := useCase.Record(context.Background(), incomplete); !errors.Is(err, domain.ErrAuditRecordIncomplete) {
		t.Fatalf("got %v, want %v", err, domain.ErrAuditRecordIncomplete)
	}
	if len(store.appended) != 0 || transactor.runs != 0 {
		t.Error("an incomplete entry reached the store")
	}
}

func TestRecordReportsARecordThatCouldNotBeWritten(t *testing.T) {
	down := errors.New("the database is gone")
	useCase := NewRecordAudit(&transactor{}, &records{fail: down}, fixedClock{recordedAt}, fixedIDs{recordID})

	if err := useCase.Record(context.Background(), entry()); !errors.Is(err, down) {
		t.Fatalf("got %v, want the failure of the store", err)
	}
}
