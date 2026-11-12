// Package postgres stores the facts Platform Operations owns in the
// platformoperations schema. Every statement runs in the transaction its
// context carries: the framework hands out no executor outside one.
package postgres

import (
	"context"
	"fmt"

	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/platformoperations/domain"
)

// AuditRecords appends to platformoperations.audit_record. The application
// role holds INSERT on that table and nothing else, so this type has no way
// to read a record back, to change one or to remove one.
type AuditRecords struct {
	framework *bentopostgres.DB
}

// NewAuditRecords builds the store on the framework database, the owner of
// the transactions of a Fabric process.
func NewAuditRecords(framework *bentopostgres.DB) *AuditRecords {
	return &AuditRecords{framework: framework}
}

const appendAuditRecord = `
INSERT INTO platformoperations.audit_record
    (audit_id, recorded_at, actor_issuer, actor_subject, action, outcome, rule_id,
     product_id, resource_type, resource_id, correlation_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

// Append writes record in the transaction ctx carries.
func (s *AuditRecords) Append(ctx context.Context, record domain.AuditRecord) error {
	executor, err := s.framework.RequireExecutor(ctx)
	if err != nil {
		return fmt.Errorf("append an audit record: %w", err)
	}
	if _, err := executor.Exec(ctx, appendAuditRecord,
		record.ID, record.RecordedAt, record.Actor.Issuer, record.Actor.Subject,
		record.Action, string(record.Outcome), record.Rule,
		optional(record.ProductID), record.ResourceType, optional(record.ResourceID), record.CorrelationID,
	); err != nil {
		return fmt.Errorf("append an audit record: %w", err)
	}
	return nil
}

// optional is NULL for a reference the record does not have.
func optional(reference string) *string {
	if reference == "" {
		return nil
	}
	return &reference
}
