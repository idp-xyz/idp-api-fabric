package outbox

import (
	"context"
	"encoding/json"
	"time"

	"go.idp.xyz/idp-bento-go/eventing"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

type Events struct {
	writer eventing.OutboxWriter
	ids    ports.Identifiers
}

func NewEvents(writer eventing.OutboxWriter, ids ports.Identifiers) *Events {
	return &Events{writer: writer, ids: ids}
}

func (e *Events) Registered(ctx context.Context, revision domain.Revision, correlationID string) error {
	const version = 1
	eventID := e.ids.NewID()
	payload, err := json.Marshal(struct {
		EventID          string    `json:"event_id"`
		SchemaVersion    int       `json:"schema_version"`
		OccurredAt       time.Time `json:"occurred_at"`
		CorrelationID    string    `json:"correlation_id"`
		ProductID        string    `json:"product_id"`
		AssetID          string    `json:"api_asset_id"`
		AggregateID      string    `json:"aggregate_id"`
		AggregateVersion int       `json:"aggregate_version"`
		Sequence         int64     `json:"revision_sequence"`
		SourceDigest     string    `json:"source_digest"`
		RefClosureDigest string    `json:"ref_closure_digest"`
	}{
		EventID: eventID, SchemaVersion: version, OccurredAt: revision.CreatedAt, CorrelationID: correlationID,
		ProductID: revision.Asset.ProductID, AssetID: revision.Asset.AssetID, AggregateID: revision.ID, AggregateVersion: 1,
		Sequence: revision.Sequence, SourceDigest: revision.SourceDigest, RefClosureDigest: revision.RefClosureDigest,
	})
	if err != nil {
		return err
	}
	return e.writer.Enqueue(ctx, eventing.Envelope{
		SpecVersion: eventing.SpecVersion, ID: eventing.EventID(eventID), Source: "idp-api-fabric/contractregistry",
		Type: "fabric.contractregistry.revision.registered", Version: version, Scope: revision.Asset.ProductID,
		Subject: "contract_revision/" + revision.ID, PartitionKey: revision.Asset.ProductID,
		OccurredAt: revision.CreatedAt, RecordedAt: revision.CreatedAt, CorrelationID: correlationID,
		ContentType: eventing.JSONContentType, Payload: payload,
	})
}
