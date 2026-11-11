package application

import (
	"context"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/platformoperations/domain"
	"go.idp.xyz/idp-api-fabric/internal/platformoperations/ports"
)

// AuditEntry is what the caller of RecordAudit knows about an attempt to act.
// The identifier of the record and its time are added here.
type AuditEntry struct {
	Actor         domain.Actor
	Action        string
	Outcome       domain.Outcome
	Rule          string
	ProductID     string
	ResourceType  string
	ResourceID    string
	CorrelationID string
}

// RecordAudit appends facts to the audit trail.
type RecordAudit struct {
	transactor ports.Transactor
	records    ports.AuditRecords
	clock      ports.Clock
	ids        ports.Identifiers
}

// NewRecordAudit builds the use case.
func NewRecordAudit(transactor ports.Transactor, records ports.AuditRecords, clock ports.Clock, ids ports.Identifiers) *RecordAudit {
	return &RecordAudit{transactor: transactor, records: records, clock: clock, ids: ids}
}

// Record appends entry to the audit trail.
//
// Called inside a transaction, it joins it: the record commits with the write
// it is evidence of, and a record that cannot be written fails that write
// (INV-13). Called outside one, as for a refusal that changed nothing, the
// record commits on its own.
func (u *RecordAudit) Record(ctx context.Context, entry AuditEntry) error {
	record := domain.AuditRecord{
		ID:            u.ids.NewID(),
		RecordedAt:    u.clock.Now().UTC(),
		Actor:         entry.Actor,
		Action:        entry.Action,
		Outcome:       entry.Outcome,
		Rule:          entry.Rule,
		ProductID:     entry.ProductID,
		ResourceType:  entry.ResourceType,
		ResourceID:    entry.ResourceID,
		CorrelationID: entry.CorrelationID,
	}
	if err := record.Validate(); err != nil {
		return err
	}
	if err := u.transactor.WithinTransaction(ctx, func(ctx context.Context) error {
		return u.records.Append(ctx, record)
	}); err != nil {
		return fmt.Errorf("record the audit fact of %s: %w", entry.Action, err)
	}
	return nil
}
