package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"go.idp.xyz/idp-bento-go/eventing"
	bentopostgres "go.idp.xyz/idp-bento-go/postgres"
	bentooutbox "go.idp.xyz/idp-bento-go/postgres/outbox"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/adapters/outbox"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
)

const (
	productID    = domain.ProductID("1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6")
	workspaceID  = domain.WorkspaceID("7d2e9f10-3a4b-4c5d-8e6f-0a1b2c3d4e5f")
	membershipID = domain.MembershipID("3f8b1c2d-4e5a-4b6c-9d7e-8f9a0b1c2d3e")
)

var (
	admin      = domain.Principal{Issuer: "https://issuer.example", Subject: "platform-admin"}
	alice      = domain.Principal{Issuer: "https://issuer.example", Subject: "alice"}
	bob        = domain.Principal{Issuer: "https://issuer.example", Subject: "bob"}
	cargo      = domain.OrganizationRef{IdentityProvider: "https://issuer.example", Organization: "org-cargopai"}
	created    = time.Date(2026, 11, 11, 9, 0, 0, 123456000, time.UTC)
	recordedAt = created.Add(2 * time.Second)
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return recordedAt }

// eventIDs hands out the identifiers of the events of a test, in order: the
// first ends in e1, the second in e2.
type eventIDs struct{ next int }

func (i *eventIDs) NewID() string {
	i.next++
	return fmt.Sprintf("00000000-0000-4000-8000-0000000000e%d", i.next)
}

func cargopai(t *testing.T) (domain.Product, domain.Workspace) {
	t.Helper()

	product, err := domain.NewProduct(productID, "cargopai", "CargoPai", cargo, admin, created)
	if err != nil {
		t.Fatalf("build the product: %v", err)
	}
	workspace, err := domain.NewWorkspace(workspaceID, product, "api-engineering", "API engineering", alice, created.Add(time.Minute))
	if err != nil {
		t.Fatalf("build the workspace: %v", err)
	}
	return product, workspace
}

// member is bob's membership of the workspace as alice granted it, and as it
// is once the administrator has revoked it.
func member(t *testing.T) (granted, revoked domain.Membership) {
	t.Helper()

	product, workspace := cargopai(t)
	granted, err := domain.GrantMembership(membershipID, product, workspace, bob, domain.RoleWorkspaceMaintainer, alice, created.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("build the membership: %v", err)
	}
	revoked, err = granted.Revoke(admin, created.Add(time.Hour))
	if err != nil {
		t.Fatalf("revoke the membership: %v", err)
	}
	return granted, revoked
}

// stored is one row of the outbox, as far as these events fill it.
type stored struct {
	Source        string
	EventID       string
	EventType     string
	EventVersion  int64
	Scope         string
	Subject       string
	PartitionKey  string
	Status        string
	OccurredAt    time.Time
	RecordedAt    time.Time
	CorrelationID string
	ContentType   string
	Payload       map[string]any
}

func outboxRows(t *testing.T, owner *pgx.Conn) []stored {
	t.Helper()

	rows, err := owner.Query(t.Context(), `
		SELECT source, event_id, event_type, event_version, scope, subject, partition_key, status,
		       occurred_at, recorded_at, correlation_id, content_type, payload
		FROM bento.outbox ORDER BY enqueued_seq`)
	if err != nil {
		t.Fatalf("read the outbox: %v", err)
	}
	found, err := pgx.CollectRows(rows, pgx.RowToStructByPos[stored])
	if err != nil {
		t.Fatalf("read the outbox: %v", err)
	}
	for index := range found {
		found[index].OccurredAt, found[index].RecordedAt = found[index].OccurredAt.UTC(), found[index].RecordedAt.UTC()
	}
	return found
}

// INV-13: an event is stored in the transaction of the write it reports. It
// is there if the write committed and gone if it did not.
func TestAnEventSharesTheFateOfTheTransactionItWasStoredIn(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	t.Cleanup(fabric.Close)
	owner := pgtest.Connect(t, database.Migrator)

	store, err := bentooutbox.NewStore(fabric.Framework)
	if err != nil {
		t.Fatalf("build the outbox: %v", err)
	}
	product, workspace := cargopai(t)

	events, err := outbox.NewEvents(store, fixedClock{}, &eventIDs{})
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("the write failed after its event was stored")
	err = fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
		if err := events.ProductRegistered(ctx, product, "rolled-back"); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("got %v, want the failure of the write", err)
	}
	if rows := outboxRows(t, owner); len(rows) != 0 {
		t.Fatalf("a rolled back write left %d events, want none", len(rows))
	}

	events, _ = outbox.NewEvents(store, fixedClock{}, &eventIDs{})
	err = fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
		if err := events.ProductRegistered(ctx, product, "correlation-1"); err != nil {
			return err
		}
		return events.WorkspaceCreated(ctx, workspace, "correlation-2")
	})
	if err != nil {
		t.Fatalf("store two events: %v", err)
	}

	// The product is the scope and the partition of both, so a consumer hears
	// of the product before it hears of its workspace.
	want := []stored{
		{
			Source: "idp-api-fabric/productgovernance", EventID: "00000000-0000-4000-8000-0000000000e1",
			EventType: "fabric.productgovernance.product.registered", EventVersion: 1,
			Scope: string(productID), Subject: "product/" + string(productID), PartitionKey: string(productID),
			Status: "PENDING", OccurredAt: created, RecordedAt: recordedAt,
			CorrelationID: "correlation-1", ContentType: "application/json",
			Payload: map[string]any{
				"event_id": "00000000-0000-4000-8000-0000000000e1", "schema_version": float64(1),
				"occurred_at": "2026-11-11T09:00:00.123456Z", "correlation_id": "correlation-1",
				"product_id": string(productID), "aggregate_id": string(productID), "aggregate_version": float64(1),
				"product_key":        "cargopai",
				"owner_organization": map[string]any{"identity_provider": "https://issuer.example", "organization": "org-cargopai"},
			},
		},
		{
			Source: "idp-api-fabric/productgovernance", EventID: "00000000-0000-4000-8000-0000000000e2",
			EventType: "fabric.productgovernance.workspace.created", EventVersion: 1,
			Scope: string(productID), Subject: "workspace/" + string(workspaceID), PartitionKey: string(productID),
			Status: "PENDING", OccurredAt: created.Add(time.Minute), RecordedAt: recordedAt,
			CorrelationID: "correlation-2", ContentType: "application/json",
			Payload: map[string]any{
				"event_id": "00000000-0000-4000-8000-0000000000e2", "schema_version": float64(1),
				"occurred_at": "2026-11-11T09:01:00.123456Z", "correlation_id": "correlation-2",
				"product_id": string(productID), "aggregate_id": string(workspaceID), "aggregate_version": float64(1),
				"workspace_id": string(workspaceID), "workspace_key": "api-engineering",
			},
		},
	}
	if got := outboxRows(t, owner); !reflect.DeepEqual(got, want) {
		t.Errorf("the outbox holds\n %+v\nwant\n %+v", got, want)
	}

	// Outside a transaction nothing is stored: an event without the write it
	// reports would be a report of nothing.
	events, _ = outbox.NewEvents(store, fixedClock{}, &eventIDs{})
	if err := events.ProductRegistered(t.Context(), product, "no-transaction"); !errors.Is(err, bentopostgres.ErrTransactionRequired) {
		t.Errorf("outside a transaction: got %v, want %v", err, bentopostgres.ErrTransactionRequired)
	}
	if rows := outboxRows(t, owner); len(rows) != 2 {
		t.Errorf("the outbox holds %d events, want 2", len(rows))
	}
}

// MembershipChanged is one event for both ends of a membership (F03 §3.1).
// The membership is its subject and what its version counts, and the payload
// says whether the membership is in force after the change.
func TestAMembershipIsAnnouncedWhenItIsGrantedAndWhenItIsRevoked(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	t.Cleanup(fabric.Close)
	owner := pgtest.Connect(t, database.Migrator)

	store, err := bentooutbox.NewStore(fabric.Framework)
	if err != nil {
		t.Fatalf("build the outbox: %v", err)
	}
	events, err := outbox.NewEvents(store, fixedClock{}, &eventIDs{})
	if err != nil {
		t.Fatal(err)
	}
	granted, revoked := member(t)

	err = fabric.WithinTransaction(t.Context(), func(ctx context.Context) error {
		if err := events.MembershipChanged(ctx, granted, "correlation-3"); err != nil {
			return err
		}
		return events.MembershipChanged(ctx, revoked, "correlation-4")
	})
	if err != nil {
		t.Fatalf("store two events: %v", err)
	}

	want := []stored{
		{
			Source: "idp-api-fabric/productgovernance", EventID: "00000000-0000-4000-8000-0000000000e1",
			EventType: "fabric.productgovernance.membership.changed", EventVersion: 1,
			Scope: string(productID), Subject: "membership/" + string(membershipID), PartitionKey: string(productID),
			// It happened when the membership was granted.
			Status: "PENDING", OccurredAt: created.Add(2 * time.Minute), RecordedAt: recordedAt,
			CorrelationID: "correlation-3", ContentType: "application/json",
			Payload: map[string]any{
				"event_id": "00000000-0000-4000-8000-0000000000e1", "schema_version": float64(1),
				"occurred_at": "2026-11-11T09:02:00.123456Z", "correlation_id": "correlation-3",
				"product_id": string(productID), "aggregate_id": string(membershipID), "aggregate_version": float64(1),
				"membership_id": string(membershipID), "workspace_id": string(workspaceID),
				"role": "workspace_maintainer", "in_force": true,
			},
		},
		{
			Source: "idp-api-fabric/productgovernance", EventID: "00000000-0000-4000-8000-0000000000e2",
			EventType: "fabric.productgovernance.membership.changed", EventVersion: 1,
			Scope: string(productID), Subject: "membership/" + string(membershipID), PartitionKey: string(productID),
			// It happened when the membership was revoked.
			Status: "PENDING", OccurredAt: created.Add(time.Hour), RecordedAt: recordedAt,
			CorrelationID: "correlation-4", ContentType: "application/json",
			Payload: map[string]any{
				"event_id": "00000000-0000-4000-8000-0000000000e2", "schema_version": float64(1),
				"occurred_at": "2026-11-11T10:00:00.123456Z", "correlation_id": "correlation-4",
				"product_id": string(productID), "aggregate_id": string(membershipID), "aggregate_version": float64(2),
				"membership_id": string(membershipID), "workspace_id": string(workspaceID),
				"role": "workspace_maintainer", "in_force": false,
			},
		},
	}
	if got := outboxRows(t, owner); !reflect.DeepEqual(got, want) {
		t.Errorf("the outbox holds\n %+v\nwant\n %+v", got, want)
	}

	events, _ = outbox.NewEvents(store, fixedClock{}, &eventIDs{})
	if err := events.MembershipChanged(t.Context(), granted, "no-transaction"); !errors.Is(err, bentopostgres.ErrTransactionRequired) {
		t.Errorf("outside a transaction: got %v, want %v", err, bentopostgres.ErrTransactionRequired)
	}
	if rows := outboxRows(t, owner); len(rows) != 2 {
		t.Errorf("the outbox holds %d events, want 2", len(rows))
	}
}

// capture keeps what is enqueued, without a database.
type capture struct {
	envelopes []eventing.Envelope
	fail      error
}

func (c *capture) Enqueue(_ context.Context, envelope eventing.Envelope) error {
	if c.fail != nil {
		return c.fail
	}
	c.envelopes = append(c.envelopes, envelope)
	return nil
}

// An event carries references and never the content of what it reports: no
// display name, no principal. That holds for the member of a membership too:
// who it is identifies a person.
func TestAnEventIsAValidEnvelopeOfReferences(t *testing.T) {
	product, workspace := cargopai(t)
	granted, revoked := member(t)
	writer := &capture{}
	events, err := outbox.NewEvents(writer, fixedClock{}, &eventIDs{})
	if err != nil {
		t.Fatal(err)
	}
	if err := events.ProductRegistered(t.Context(), product, "correlation-1"); err != nil {
		t.Fatalf("announce the product: %v", err)
	}
	if err := events.WorkspaceCreated(t.Context(), workspace, "correlation-2"); err != nil {
		t.Fatalf("announce the workspace: %v", err)
	}
	if err := events.MembershipChanged(t.Context(), granted, "correlation-3"); err != nil {
		t.Fatalf("announce the membership: %v", err)
	}
	if err := events.MembershipChanged(t.Context(), revoked, "correlation-4"); err != nil {
		t.Fatalf("announce the revocation: %v", err)
	}

	if len(writer.envelopes) != 4 {
		t.Fatalf("%d envelopes, want 4", len(writer.envelopes))
	}
	for _, envelope := range writer.envelopes {
		if err := envelope.Validate(); err != nil {
			t.Errorf("the envelope of %s is refused by the framework: %v", envelope.Type, err)
		}
		var payload map[string]any
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			t.Fatalf("the payload of %s is not a JSON object: %v", envelope.Type, err)
		}
		for _, member := range []string{"event_id", "schema_version", "occurred_at", "correlation_id", "product_id", "aggregate_id", "aggregate_version"} {
			if _, present := payload[member]; !present {
				t.Errorf("the payload of %s lacks %s (F03 §9)", envelope.Type, member)
			}
		}
		for _, withheld := range []string{"CargoPai", "API engineering", admin.Subject, alice.Subject, bob.Subject} {
			if json.Valid(envelope.Payload) && containsValue(payload, withheld) {
				t.Errorf("the payload of %s carries %q", envelope.Type, withheld)
			}
		}
	}
}

func containsValue(value any, wanted string) bool {
	switch typed := value.(type) {
	case string:
		return typed == wanted
	case map[string]any:
		for _, member := range typed {
			if containsValue(member, wanted) {
				return true
			}
		}
	case []any:
		for _, element := range typed {
			if containsValue(element, wanted) {
				return true
			}
		}
	}
	return false
}

func TestAnEventThatCannotBeStoredFailsTheWrite(t *testing.T) {
	product, _ := cargopai(t)
	down := errors.New("the outbox is down")
	events, err := outbox.NewEvents(&capture{fail: down}, fixedClock{}, &eventIDs{})
	if err != nil {
		t.Fatal(err)
	}
	if err := events.ProductRegistered(t.Context(), product, "correlation-1"); !errors.Is(err, down) {
		t.Fatalf("got %v, want the failure of the outbox", err)
	}

	if _, err := outbox.NewEvents(nil, fixedClock{}, &eventIDs{}); err == nil {
		t.Error("events without an outbox were built")
	}
	if _, err := outbox.NewEvents(&capture{}, nil, &eventIDs{}); err == nil {
		t.Error("events without a clock were built")
	}
	if _, err := outbox.NewEvents(&capture{}, fixedClock{}, nil); err == nil {
		t.Error("events without identifiers were built")
	}
}
