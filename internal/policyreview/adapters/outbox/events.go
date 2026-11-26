package outbox

import (
	"context"
	"encoding/json"
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
