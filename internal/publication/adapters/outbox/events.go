package outbox

import (
	"context"
	"encoding/json"
	"time"

	"go.idp.xyz/idp-bento-go/eventing"

	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
	"go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

type Events struct {
	writer eventing.OutboxWriter
	ids    ports.Identifiers
}

func NewEvents(writer eventing.OutboxWriter, ids ports.Identifiers) *Events {
	return &Events{writer: writer, ids: ids}
}

func (e *Events) Enqueue(ctx context.Context, request domain.Request, event ports.Event) error {
	const version = 1
	id := e.ids.NewID()
	payload, err := json.Marshal(struct {
		EventID          string    `json:"event_id"`
		SchemaVersion    int       `json:"schema_version"`
		OccurredAt       time.Time `json:"occurred_at"`
		CorrelationID    string    `json:"correlation_id"`
		ProductID        string    `json:"product_id"`
		AggregateID      string    `json:"aggregate_id"`
		AggregateVersion int64     `json:"aggregate_version"`
		TargetID         string    `json:"target_id"`
		AssetID          string    `json:"api_asset_id,omitempty"`
		ReleaseID        string    `json:"release_id,omitempty"`
		BlockGeneration  int64     `json:"block_generation,omitempty"`
	}{id, version, event.At, request.CorrelationID, request.ProductID, event.AggregateID, event.AggregateVersion,
		event.TargetID, event.AssetID, event.ReleaseID, event.BlockGeneration})
	if err != nil {
		return err
	}
	return e.writer.Enqueue(ctx, eventing.Envelope{
		SpecVersion: eventing.SpecVersion, ID: eventing.EventID(id), Source: "idp-api-fabric/publication",
		Type: eventing.EventType("fabric.publication." + event.Name), Version: version, Scope: request.ProductID,
		Subject: event.Aggregate + "/" + event.AggregateID, PartitionKey: event.TargetID,
		OccurredAt: event.At, RecordedAt: event.At, CorrelationID: request.CorrelationID,
		ContentType: eventing.JSONContentType, Payload: payload,
	})
}
