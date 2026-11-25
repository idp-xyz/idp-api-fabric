package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.idp.xyz/idp-bento-go/eventing"
	"go.idp.xyz/idp-bento-go/postgres/outbox"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

func TestWorkerRecoversLeaseAndRetriesBeforeDeliveringNextEvent(t *testing.T) {
	config := pgtest.MigratedDatabase(t)
	database, err := db.Open(t.Context(), config.Application)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(database.Close)
	store, err := outbox.NewStore(database.Framework)
	if err != nil {
		t.Fatal(err)
	}

	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	first := publicationEvent("first", "access_block.created", at)
	second := publicationEvent("second", "slot.withdrawn", at)
	if err := database.WithinTransaction(t.Context(), func(ctx context.Context) error {
		if err := store.Enqueue(ctx, first); err != nil {
			return err
		}
		return store.Enqueue(ctx, second)
	}); err != nil {
		t.Fatalf("enqueue test events: %v", err)
	}
	oldLease, err := store.Claim(t.Context(), eventing.OutboxClaim{
		Now: at, Limit: 1, LeaseFor: time.Second, MaxAttempts: 12,
	})
	if err != nil || len(oldLease) != 1 || oldLease[0].Ref.EventID != first.ID {
		t.Fatalf("claim expired lease: %#v, %v", oldLease, err)
	}

	received := make(chan eventing.Envelope, 3)
	var firstRefused atomic.Bool
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/events" ||
			r.Header.Get("Authorization") != "Bearer local-test-token" ||
			r.Header.Get("Content-Type") != eventing.JSONContentType {
			t.Error("worker sent an unexpected request")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var event eventing.Envelope
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Errorf("decode event: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case received <- event:
		default:
			t.Error("received an unexpected additional delivery")
		}
		if event.ID == first.ID && firstRefused.CompareAndSwap(false, true) {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close)
	t.Setenv(pgtest.ApplicationDSNVariable, config.Application)
	t.Setenv("IDP_FABRIC_OUTBOX_URL", receiver.URL+"/events")
	t.Setenv("IDP_FABRIC_OUTBOX_TOKEN", "local-test-token")
	ctx, stop := context.WithTimeout(t.Context(), 15*time.Second)
	done := make(chan error, 1)
	go func() { done <- run(ctx, slog.New(slog.DiscardHandler)) }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("worker failed: %v", err)
		}
	})

	for _, want := range []eventing.Envelope{first, first, second} {
		select {
		case got := <-received:
			if got.ID != want.ID || got.Type != want.Type || got.Source != want.Source || got.Scope != want.Scope ||
				got.PartitionKey != want.PartitionKey || !got.OccurredAt.Equal(want.OccurredAt) ||
				got.CorrelationID != want.CorrelationID || string(got.Payload) != string(want.Payload) {
				t.Fatalf("delivered event = %#v, want %#v", got, want)
			}
		case <-ctx.Done():
			t.Fatalf("waiting for delivery of %s: %v", want.ID, ctx.Err())
		}
	}
	waitForPublishedEvents(t, ctx, database)

	var attempt, failures int
	var code eventing.FailureCode
	if err := database.Pool.QueryRow(ctx, `SELECT attempt, failures, failure_code FROM bento.outbox
		WHERE source = $1 AND event_id = $2`, first.Source, first.ID).Scan(&attempt, &failures, &code); err != nil {
		t.Fatal(err)
	}
	if attempt != 3 || failures != 1 || code != "outbox.receiver_rejected" {
		t.Fatalf("first event has attempt %d, failures %d and failure code %q", attempt, failures, code)
	}
	if err := store.MarkPublished(ctx, oldLease[0].Ref, time.Now()); !errors.Is(err, eventing.ErrLeaseLost) {
		t.Fatalf("expired worker lease was not fenced: %v", err)
	}
}

func waitForPublishedEvents(t *testing.T, ctx context.Context, database *db.Database) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		err := database.Pool.QueryRow(ctx, "SELECT count(*) FROM bento.outbox WHERE status = 'PUBLISHED'").Scan(&count)
		if err != nil {
			t.Fatal(err)
		}
		if count == 2 {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("waiting for both Outbox acknowledgments: %v", ctx.Err())
		}
	}
}

func TestHTTPPublisherDoesNotFollowRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			t.Errorf("followed redirect to %s", r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Redirect(w, r, "/other", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	publisher, err := newHTTPPublisher(server.URL+"/events", "local-test-token")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(publisher.client.CloseIdleConnections)
	if err := publisher.Publish(t.Context(), publicationEvent("event", "slot.withdrawn", time.Now())); !errors.Is(err, eventing.ErrPublishRejected) {
		t.Fatalf("redirect was not rejected: %v", err)
	}
}

func publicationEvent(id, name string, at time.Time) eventing.Envelope {
	return eventing.Envelope{
		SpecVersion: eventing.SpecVersion, ID: eventing.EventID(id), Source: "idp-api-fabric/publication",
		Type: eventing.EventType("fabric.publication." + name), Version: 1, Scope: "cargopai",
		Subject: "PublicationSlot/slot", PartitionKey: "target", OccurredAt: at, RecordedAt: at,
		CorrelationID: "publication-request", ContentType: eventing.JSONContentType,
		Payload: json.RawMessage(`{"generation":1}`),
	}
}
