package outbox

import (
	"context"
	"encoding/json"
	"time"

	"go.idp.xyz/idp-bento-go/eventing"

	"go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	"go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
)

type Events struct {
	writer eventing.OutboxWriter
	ids    ports.Identifiers
}

func NewEvents(writer eventing.OutboxWriter, ids ports.Identifiers) *Events {
	return &Events{writer: writer, ids: ids}
}

func (e *Events) Registered(ctx context.Context, asset domain.Asset, correlationID string) error {
	const version = 1
	eventID := e.ids.NewID()
	payload, err := json.Marshal(struct {
		EventID          string             `json:"event_id"`
		SchemaVersion    int                `json:"schema_version"`
		OccurredAt       time.Time          `json:"occurred_at"`
		CorrelationID    string             `json:"correlation_id"`
		ProductID        domain.ProductID   `json:"product_id"`
		AggregateID      domain.AssetID     `json:"aggregate_id"`
		AggregateVersion int64              `json:"aggregate_version"`
		AssetID          domain.AssetID     `json:"api_asset_id"`
		WorkspaceID      domain.WorkspaceID `json:"workspace_id"`
	}{
		EventID: eventID, SchemaVersion: version, OccurredAt: asset.CreatedAt, CorrelationID: correlationID,
		ProductID: asset.ProductID, AggregateID: asset.ID, AggregateVersion: asset.Version,
		AssetID: asset.ID, WorkspaceID: asset.WorkspaceID,
	})
	if err != nil {
		return err
	}
	return e.writer.Enqueue(ctx, eventing.Envelope{
		SpecVersion: eventing.SpecVersion, ID: eventing.EventID(eventID), Source: "idp-api-fabric/apicatalog",
		Type: "fabric.apicatalog.asset.registered", Version: version, Scope: string(asset.ProductID),
		Subject: "api_asset/" + string(asset.ID), PartitionKey: string(asset.ProductID),
		OccurredAt: asset.CreatedAt, RecordedAt: asset.CreatedAt,
		CorrelationID: correlationID, ContentType: eventing.JSONContentType, Payload: payload,
	})
}
