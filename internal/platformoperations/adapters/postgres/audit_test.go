package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/platformoperations/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/platformoperations/application"
	"go.idp.xyz/idp-api-fabric/internal/platformoperations/domain"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

var recordedAt = time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

// fixture is a migrated database opened the way a Fabric process opens it,
// as the application role, and a connection of the migrating role to read
// what the process itself cannot.
type fixture struct {
	fabric   *db.Database
	recorder *application.RecordAudit
	store    *postgres.AuditRecords
	owner    *pgx.Conn
	app      *pgx.Conn
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	t.Cleanup(fabric.Close)

	store := postgres.NewAuditRecords(fabric.Framework)
	return fixture{
		fabric:   fabric,
		recorder: application.NewRecordAudit(fabric, store, fixedClock{recordedAt}, ids.UUIDs{}),
		store:    store,
		owner:    pgtest.Connect(t, database.Migrator),
		app:      pgtest.Connect(t, database.Application),
	}
}

// row is one audit record as the table holds it.
type row struct {
	RecordedAt    time.Time
	ActorIssuer   string
	ActorSubject  string
	Action        string
	Outcome       string
	Rule          string
	ProductID     *string
	ResourceType  string
	ResourceID    *string
	CorrelationID string
}

func (f fixture) rows(t *testing.T) []row {
	t.Helper()

	rows, err := f.owner.Query(t.Context(), `
		SELECT recorded_at, actor_issuer, actor_subject, action, outcome, rule_id,
		       product_id::text, resource_type, resource_id::text, correlation_id
		FROM platformoperations.audit_record ORDER BY correlation_id`)
	if err != nil {
		t.Fatalf("read the audit trail: %v", err)
	}
	recorded, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		t.Fatalf("read the audit trail: %v", err)
	}
	// This connection is not a Fabric pool and reads a timestamp in the zone
	// of the host. The instant is what is compared.
	for index := range recorded {
		recorded[index].RecordedAt = recorded[index].RecordedAt.UTC()
	}
	return recorded
}

const productID = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"

func registration(correlationID string) application.AuditEntry {
	return application.AuditEntry{
		Actor:         domain.Actor{Issuer: "https://issuer.example", Subject: "alice"},
		Action:        "product.register",
		Outcome:       domain.OutcomeSucceeded,
		Rule:          "AC-08",
		ProductID:     productID,
		ResourceType:  "Product",
		ResourceID:    productID,
		CorrelationID: correlationID,
	}
}

func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// The record of a write and the write itself are one transaction (INV-13):
// what rolls the write back takes the record with it.
func TestARecordSharesTheFateOfTheTransactionItJoined(t *testing.T) {
	f := newFixture(t)

	failure := errors.New("the write failed after its audit fact was recorded")
	err := f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
		if err := f.recorder.Record(ctx, registration("rolled-back")); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("got %v, want the failure of the write", err)
	}
	if recorded := f.rows(t); len(recorded) != 0 {
		t.Fatalf("a rolled back write left %d audit records, want none", len(recorded))
	}

	err = f.fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
		return f.recorder.Record(ctx, registration("committed"))
	})
	if err != nil {
		t.Fatalf("record inside a transaction that commits: %v", err)
	}
	recorded := f.rows(t)
	if len(recorded) != 1 {
		t.Fatalf("%d audit records, want 1", len(recorded))
	}
	got, product := recorded[0], productID
	if got.ProductID == nil || got.ResourceID == nil || *got.ProductID != product || *got.ResourceID != product {
		t.Errorf("product %v and resource %v, want both %s", got.ProductID, got.ResourceID, product)
	}
	got.ProductID, got.ResourceID = nil, nil
	want := row{
		RecordedAt: recordedAt, ActorIssuer: "https://issuer.example", ActorSubject: "alice",
		Action: "product.register", Outcome: "SUCCEEDED", Rule: "AC-08", ResourceType: "Product", CorrelationID: "committed",
	}
	if got != want {
		t.Errorf("recorded\n %+v\nwant\n %+v", got, want)
	}
}

// A refusal changes nothing, so there is no transaction to join. Its record
// commits on its own. A refusal in the platform scope names no product and no
// resource.
func TestARecordOutsideATransactionCommitsOnItsOwn(t *testing.T) {
	f := newFixture(t)

	refusal := registration("refused")
	refusal.Outcome, refusal.Rule, refusal.ProductID, refusal.ResourceID = domain.OutcomeDenied, "DENY-03", "", ""
	if err := f.recorder.Record(t.Context(), refusal); err != nil {
		t.Fatalf("record: %v", err)
	}

	recorded := f.rows(t)
	if len(recorded) != 1 {
		t.Fatalf("%d audit records, want 1", len(recorded))
	}
	if got := recorded[0]; got.Outcome != "DENIED" || got.Rule != "DENY-03" || got.ProductID != nil || got.ResourceID != nil {
		t.Errorf("recorded %+v, want a refusal under DENY-03 with no product and no resource", got)
	}
}

func TestTheStoreWritesOnlyInsideATransaction(t *testing.T) {
	f := newFixture(t)

	record := domain.AuditRecord{
		ID: ids.NewUUID(), RecordedAt: recordedAt, Actor: domain.Actor{Issuer: "https://issuer.example", Subject: "alice"},
		Action: "product.register", Outcome: domain.OutcomeSucceeded, Rule: "AC-08",
		ResourceType: "Product", ResourceID: productID, CorrelationID: "no-transaction",
	}
	if err := f.store.Append(t.Context(), record); !errors.Is(err, bentopostgres.ErrTransactionRequired) {
		t.Fatalf("got %v, want %v", err, bentopostgres.ErrTransactionRequired)
	}
	if recorded := f.rows(t); len(recorded) != 0 {
		t.Fatalf("%d audit records were written outside a transaction", len(recorded))
	}
}

// The audit trail is append-only for a Fabric process (FR-062): the role it
// connects with can add a record and can neither read, change nor remove one.
func TestTheApplicationRoleCanAppendAndNothingElse(t *testing.T) {
	f := newFixture(t)
	if err := f.recorder.Record(t.Context(), registration("kept")); err != nil {
		t.Fatalf("record: %v", err)
	}

	refused := []string{
		`SELECT count(*) FROM platformoperations.audit_record`,
		`UPDATE platformoperations.audit_record SET outcome = 'DENIED'`,
		`DELETE FROM platformoperations.audit_record`,
		`TRUNCATE platformoperations.audit_record`,
		`ALTER TABLE platformoperations.audit_record DROP COLUMN rule_id`,
		`DROP TABLE platformoperations.audit_record`,
		`CREATE TABLE platformoperations.intruder (id integer)`,
	}
	for _, statement := range refused {
		if _, err := f.app.Exec(t.Context(), statement); sqlState(err) != "42501" {
			t.Errorf("%s\n got %v, want it refused for lack of privilege", statement, err)
		}
	}
	if recorded := f.rows(t); len(recorded) != 1 || recorded[0].Outcome != "SUCCEEDED" {
		t.Errorf("the audit trail holds %+v, want the one record as it was written", recorded)
	}
}

func TestTheSchemaKnowsTwoOutcomes(t *testing.T) {
	f := newFixture(t)

	_, err := f.owner.Exec(t.Context(), `
		INSERT INTO platformoperations.audit_record
		    (audit_id, recorded_at, actor_issuer, actor_subject, action, outcome, rule_id, resource_type, correlation_id)
		VALUES (gen_random_uuid(), now(), 'https://issuer.example', 'alice', 'product.register', 'MAYBE', 'AC-08', 'Product', 'c')`)
	if sqlState(err) != "23514" {
		t.Fatalf("got %v, want the outcome refused by a check constraint", err)
	}
}
