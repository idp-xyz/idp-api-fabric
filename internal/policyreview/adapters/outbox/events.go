package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"go.idp.xyz/idp-bento-go/eventing"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

const CheckCompleted = "fabric.policyreview.check_run.completed"

type Events struct {
	writer eventing.OutboxWriter
	ids    ports.Identifiers
}

func NewEvents(writer eventing.OutboxWriter, ids ports.Identifiers) *Events {
	return &Events{writer: writer, ids: ids}
}

// The events of a review. A review is asked for once and concluded once, so
// its request is version 1 of it and its conclusion version 2.
const (
	ReviewRequested = "fabric.policyreview.review.requested"
	ReviewWithdrawn = "fabric.policyreview.review.withdrawn"
)

// reviewEvent is what an event of a review carries: what the review is of
// and what it rests on. It carries no word anybody wrote: a note and a
// reason are read from the review, by whoever may read it.
type reviewEvent struct {
	EventID          string    `json:"event_id"`
	SchemaVersion    int       `json:"schema_version"`
	OccurredAt       time.Time `json:"occurred_at"`
	CorrelationID    string    `json:"correlation_id"`
	ProductID        string    `json:"product_id"`
	AssetID          string    `json:"api_asset_id"`
	RevisionID       string    `json:"revision_id"`
	AggregateID      string    `json:"aggregate_id"`
	AggregateVersion int       `json:"aggregate_version"`
	CheckRunID       string    `json:"check_run_id"`
	InputDigest      string    `json:"input_digest"`
	Outcome          string    `json:"outcome,omitempty"`
}

func (e *Events) review(ctx context.Context, kind string, version int, request domain.ReviewRequest, outcome string, at time.Time, correlationID string) error {
	const schema = 1
	id := e.ids.NewID()
	payload, err := json.Marshal(reviewEvent{
		EventID: id, SchemaVersion: schema, OccurredAt: at, CorrelationID: correlationID,
		ProductID: request.Revision.ProductID, AssetID: request.Revision.AssetID, RevisionID: request.Revision.RevisionID,
		AggregateID: request.ID, AggregateVersion: version, CheckRunID: request.CheckRunID, InputDigest: request.InputDigest,
		Outcome: outcome,
	})
	if err != nil {
		return err
	}
	return e.writer.Enqueue(ctx, eventing.Envelope{
		SpecVersion: eventing.SpecVersion, ID: eventing.EventID(id), Source: "idp-api-fabric/policyreview",
		Type: eventing.EventType(kind), Version: schema, Scope: request.Revision.ProductID,
		Subject: "review/" + request.ID, PartitionKey: request.Revision.ProductID,
		OccurredAt: at, RecordedAt: at, CorrelationID: correlationID,
		ContentType: eventing.JSONContentType, Payload: payload,
	})
}

// Requested announces that a decision was asked for.
func (e *Events) Requested(ctx context.Context, request domain.ReviewRequest, correlationID string) error {
	return e.review(ctx, ReviewRequested, 1, request, "", request.RequestedAt, correlationID)
}

// Concluded announces how a review ended.
func (e *Events) Concluded(ctx context.Context, review domain.Review, correlationID string) error {
	conclusion := review.Conclusion
	if conclusion == nil {
		return errors.New("a review that is not concluded has no conclusion to announce")
	}
	return e.review(ctx, ReviewWithdrawn, 2, review.Request, string(conclusion.Outcome), conclusion.ConcludedAt, correlationID)
}

func (e *Events) Completed(ctx context.Context, run domain.CheckRun, correlationID string) error {
	const version = 1
	id := e.ids.NewID()
	payload, err := json.Marshal(struct {
		EventID          string        `json:"event_id"`
		SchemaVersion    int           `json:"schema_version"`
		OccurredAt       time.Time     `json:"occurred_at"`
		CorrelationID    string        `json:"correlation_id"`
		ProductID        string        `json:"product_id"`
		AssetID          string        `json:"api_asset_id"`
		RevisionID       string        `json:"revision_id"`
		AggregateID      string        `json:"aggregate_id"`
		AggregateVersion int           `json:"aggregate_version"`
		InputDigest      string        `json:"input_digest"`
		PolicyDigest     string        `json:"policy_digest"`
		Status           domain.Status `json:"status"`
	}{
		EventID: id, SchemaVersion: version, OccurredAt: run.CompletedAt, CorrelationID: correlationID,
		ProductID: run.Revision.ProductID, AssetID: run.Revision.AssetID, RevisionID: run.Revision.RevisionID,
		AggregateID: run.ID, AggregateVersion: 1, InputDigest: run.InputDigest, PolicyDigest: run.PolicyDigest, Status: run.Status,
	})
	if err != nil {
		return err
	}
	return e.writer.Enqueue(ctx, eventing.Envelope{
		SpecVersion: eventing.SpecVersion, ID: eventing.EventID(id), Source: "idp-api-fabric/policyreview",
		Type: CheckCompleted, Version: version, Scope: run.Revision.ProductID,
		Subject: "check_run/" + run.ID, PartitionKey: run.Revision.ProductID,
		OccurredAt: run.CompletedAt, RecordedAt: run.CompletedAt, CorrelationID: correlationID,
		ContentType: eventing.JSONContentType, Payload: payload,
	})
}
