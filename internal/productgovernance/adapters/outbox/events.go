// Package outbox announces what happened in Product Governance through the
// transactional outbox of the framework. An event is stored in the
// transaction of the write it reports and delivered later, at least once
// (INV-13, F03 §12.1).
//
// The events are the candidates of F03 §12.2, and MembershipChanged of
// F03 §3.1, in the envelope of the framework. F04 has not frozen their
// contract: the types and the payloads here are a first version, and each
// payload says which.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.idp.xyz/idp-bento-go/eventing"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

const (
	// Source names the producer of the events. With the identifier of an
	// event it is what a consumer deduplicates on.
	Source = "idp-api-fabric/productgovernance"

	TypeProductRegistered eventing.EventType = "fabric.productgovernance.product.registered"
	TypeWorkspaceCreated  eventing.EventType = "fabric.productgovernance.workspace.created"
	// TypeMembershipChanged is MembershipChanged, the one event the context
	// publishes about its members (F03 §3.1). Its payload says whether the
	// membership is in force after the change.
	TypeMembershipChanged eventing.EventType = "fabric.productgovernance.membership.changed"

	// schemaVersion is the version of the payloads below. A change a
	// consumer cannot ignore gets the next one.
	schemaVersion = 1
)

// Events writes the events of Product Governance to the outbox.
type Events struct {
	writer eventing.OutboxWriter
	clock  ports.Clock
	ids    ports.Identifiers
}

// NewEvents builds the announcer on the outbox of the framework.
func NewEvents(writer eventing.OutboxWriter, clock ports.Clock, ids ports.Identifiers) (*Events, error) {
	if writer == nil || clock == nil || ids == nil {
		return nil, errors.New("the events need an outbox, a clock and an identifier generator")
	}
	return &Events{writer: writer, clock: clock, ids: ids}, nil
}

// header is what every payload carries (F03 §9). An event holds references
// and never the content of what it reports: a consumer that needs more asks
// for it, and is authorized like anyone else.
type header struct {
	EventID          string    `json:"event_id"`
	SchemaVersion    int       `json:"schema_version"`
	OccurredAt       time.Time `json:"occurred_at"`
	CorrelationID    string    `json:"correlation_id"`
	ProductID        string    `json:"product_id"`
	AggregateID      string    `json:"aggregate_id"`
	AggregateVersion int64     `json:"aggregate_version"`
}

type organization struct {
	IdentityProvider string `json:"identity_provider"`
	Organization     string `json:"organization"`
}

// ProductRegistered announces a product that was registered.
func (e *Events) ProductRegistered(ctx context.Context, product domain.Product, correlationID string) error {
	id := e.ids.NewID()
	return e.enqueue(ctx, id, TypeProductRegistered, product.ID, "product/"+string(product.ID), product.CreatedAt, correlationID,
		struct {
			header
			ProductKey string       `json:"product_key"`
			Owner      organization `json:"owner_organization"`
		}{
			header: header{
				EventID: id, SchemaVersion: schemaVersion, OccurredAt: product.CreatedAt, CorrelationID: correlationID,
				ProductID: string(product.ID), AggregateID: string(product.ID), AggregateVersion: product.Version,
			},
			ProductKey: product.Key,
			Owner:      organization{product.Owner.IdentityProvider, product.Owner.Organization},
		})
}

// WorkspaceCreated announces a workspace that was created.
func (e *Events) WorkspaceCreated(ctx context.Context, workspace domain.Workspace, correlationID string) error {
	id := e.ids.NewID()
	return e.enqueue(ctx, id, TypeWorkspaceCreated, workspace.ProductID, "workspace/"+string(workspace.ID), workspace.CreatedAt, correlationID,
		struct {
			header
			WorkspaceID  string `json:"workspace_id"`
			WorkspaceKey string `json:"workspace_key"`
		}{
			header: header{
				EventID: id, SchemaVersion: schemaVersion, OccurredAt: workspace.CreatedAt, CorrelationID: correlationID,
				ProductID: string(workspace.ProductID), AggregateID: string(workspace.ID), AggregateVersion: workspace.Version,
			},
			WorkspaceID:  string(workspace.ID),
			WorkspaceKey: workspace.Key,
		})
}

// MembershipChanged announces a membership that was granted or revoked, as
// it is after the change.
//
// The membership is what the event is about and what its version counts: 1
// for the grant, 2 for the revocation, so a consumer that gets them out of
// order can tell. It happened when the membership was granted or, once it is
// revoked, when it was revoked.
//
// The payload names the workspace and the role and not the member. Who a
// member is identifies a person; a consumer that needs it asks for the
// membership, and is authorized like anyone else.
func (e *Events) MembershipChanged(ctx context.Context, membership domain.Membership, correlationID string) error {
	occurredAt := membership.GrantedAt
	if !membership.InForce() {
		occurredAt = membership.RevokedAt
	}
	id := e.ids.NewID()
	return e.enqueue(ctx, id, TypeMembershipChanged, membership.ProductID, "membership/"+string(membership.ID), occurredAt, correlationID,
		struct {
			header
			MembershipID string `json:"membership_id"`
			WorkspaceID  string `json:"workspace_id"`
			Role         string `json:"role"`
			InForce      bool   `json:"in_force"`
		}{
			header: header{
				EventID: id, SchemaVersion: schemaVersion, OccurredAt: occurredAt, CorrelationID: correlationID,
				ProductID: string(membership.ProductID), AggregateID: string(membership.ID), AggregateVersion: membership.Version,
			},
			MembershipID: string(membership.ID),
			WorkspaceID:  string(membership.WorkspaceID),
			Role:         string(membership.Role),
			InForce:      membership.InForce(),
		})
}

// enqueue stores one event in the transaction ctx carries.
//
// An event is scoped to its product, and the product is also its partition:
// the events of one product are delivered in the order they were stored, so
// a consumer hears of a product before it hears of its workspaces, and of a
// workspace before it hears of its members. Nothing is promised about the
// order across products.
func (e *Events) enqueue(ctx context.Context, id string, eventType eventing.EventType, productID domain.ProductID,
	subject string, occurredAt time.Time, correlationID string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode the event %s: %w", eventType, err)
	}
	if err := e.writer.Enqueue(ctx, eventing.Envelope{
		SpecVersion:   eventing.SpecVersion,
		ID:            eventing.EventID(id),
		Source:        Source,
		Type:          eventType,
		Version:       schemaVersion,
		Scope:         string(productID),
		Subject:       subject,
		PartitionKey:  string(productID),
		OccurredAt:    occurredAt,
		RecordedAt:    e.clock.Now().UTC(),
		CorrelationID: correlationID,
		ContentType:   eventing.JSONContentType,
		Payload:       encoded,
	}); err != nil {
		return fmt.Errorf("store the event %s: %w", eventType, err)
	}
	return nil
}
