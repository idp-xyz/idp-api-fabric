package outbox

import (
	"context"
	"encoding/json"
	"time"

	"go.idp.xyz/idp-bento-go/eventing"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

func (e *Events) Prepared(ctx context.Context, release domain.Release, correlationID string) error {
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
		RevisionID       string    `json:"revision_id"`
		ArtifactDigest   string    `json:"artifact_digest"`
	}{eventID, version, release.PreparedAt, correlationID, release.Asset.ProductID, release.Asset.AssetID,
		release.ID, 1, release.RevisionID, release.ArtifactDigest})
	if err != nil {
		return err
	}
	return e.writer.Enqueue(ctx, eventing.Envelope{
		SpecVersion: eventing.SpecVersion, ID: eventing.EventID(eventID), Source: "idp-api-fabric/contractregistry",
		Type: "fabric.contractregistry.release.prepared", Version: version, Scope: release.Asset.ProductID,
		Subject: "contract_release/" + release.ID, PartitionKey: release.Asset.ProductID,
		OccurredAt: release.PreparedAt, RecordedAt: release.PreparedAt, CorrelationID: correlationID,
		ContentType: eventing.JSONContentType, Payload: payload,
	})
}
