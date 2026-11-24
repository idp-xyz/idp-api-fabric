package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	bentopostgres "go.idp.xyz/idp-bento-go/postgres"

	"go.idp.xyz/idp-api-fabric/internal/platformoperations/domain"
	"go.idp.xyz/idp-api-fabric/internal/platformoperations/ports"
)

// AuditReader reads platformoperations.audit_record back. The application role
// holds SELECT on the table next to INSERT, and no other privilege on it: what
// is read here can be neither changed nor removed through this role, and the
// type has no statement that does.
//
// It runs in the transaction its context carries, if there is one, and on the
// pool otherwise, as every read does.
type AuditReader struct {
	framework *bentopostgres.DB
}

// NewAuditReader builds the reader on the framework database.
func NewAuditReader(framework *bentopostgres.DB) *AuditReader {
	return &AuditReader{framework: framework}
}

// A page of the records of one product, newest first: by the instant a record
// was recorded and then by its identifier, which tells apart the records of
// one instant. Neither column ever changes, so a record keeps its place for as
// long as it is listed, and a page starts behind the last record of the page
// before, wherever the records around it are by then. What is recorded while
// the list is read is newer than any position that was handed out and comes
// before it: it repeats nothing and takes nothing from what is behind.
//
// The index of the list (migration 002) is in the order of its columns and is
// read backward, so a page costs what it returns. The position is compared as
// a row, the way the order compares it; a page that names none starts behind
// the end of time and the largest identifier, so one statement reads the first
// page as it reads the others.
//
// A filter that is not set is the empty string, or no instant, and lets
// everything through; one that is set is an exact match, apart from the two
// ends of the time, which are an interval that includes the first and not the
// second. The records of no product have no product to match: they are in no
// list.
//
// $1 and $2 are the position, $3 how many records the page takes and $4 the
// product. The filters follow, in the order of AuditFilter.
//
// The order names the columns of the table. A bare audit_id in ORDER BY would
// be the column the statement hands out under that name, which is the text of
// the identifier: the records of one instant would be sorted as text, and not
// as the position compares them, and the index could not give the order.
const listAuditRecords = `
SELECT audit_id::text, recorded_at, actor_issuer, actor_subject, action, outcome, rule_id,
       product_id::text, resource_type, resource_id::text, correlation_id
FROM platformoperations.audit_record
WHERE product_id = $4::uuid
  AND ($5::timestamptz IS NULL OR recorded_at >= $5)
  AND ($6::timestamptz IS NULL OR recorded_at < $6)
  AND ($7 = '' OR action = $7)
  AND ($8 = '' OR outcome = $8)
  AND ($9 = '' OR actor_issuer = $9)
  AND ($10 = '' OR actor_subject = $10)
  AND ($11 = '' OR resource_type = $11)
  AND ($12 = '' OR resource_id = NULLIF($12, '')::uuid)
  AND ($13 = '' OR correlation_id = $13)
  AND (recorded_at, audit_id) < (coalesce($1, 'infinity'::timestamptz),
                                 coalesce($2, 'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid))
ORDER BY audit_record.recorded_at DESC, audit_record.audit_id DESC
LIMIT $3`

// List returns one page of the records of the product that the filter lets
// through, newest first.
func (s *AuditReader) List(ctx context.Context, productID string, filter domain.AuditFilter, page ports.Page) ([]domain.AuditRecord, error) {
	if page.Limit < 1 {
		return nil, errors.New("list the audit records of a product: a page takes at least one record")
	}
	// A page that names no position starts behind the end of time.
	var at, id any
	if page.After != nil {
		at, id = page.After.At, page.After.ID
	}
	reader, err := s.framework.ReadExecutor(ctx)
	if err != nil {
		return nil, fmt.Errorf("list the audit records of a product: %w", err)
	}
	rows, err := reader.Query(ctx, listAuditRecords, at, id, page.Limit, productID,
		instant(filter.RecordedSince), instant(filter.RecordedBefore),
		filter.Action, string(filter.Outcome), filter.ActorIssuer, filter.ActorSubject,
		filter.ResourceType, filter.ResourceID, filter.CorrelationID)
	if err != nil {
		return nil, fmt.Errorf("list the audit records of a product: %w", err)
	}
	records, err := pgx.CollectRows(rows, scanAuditRecord)
	if err != nil {
		return nil, fmt.Errorf("list the audit records of a product: %w", err)
	}
	return records, nil
}

// instant is an end of a range of time as a statement takes it: no instant is
// NULL.
func instant(at *time.Time) any {
	if at == nil {
		return nil
	}
	return *at
}

// scanAuditRecord reads one row of listAuditRecords.
func scanAuditRecord(row pgx.CollectableRow) (domain.AuditRecord, error) {
	var record domain.AuditRecord
	var outcome string
	var productID, resourceID *string
	if err := row.Scan(&record.ID, &record.RecordedAt, &record.Actor.Issuer, &record.Actor.Subject,
		&record.Action, &outcome, &record.Rule, &productID, &record.ResourceType, &resourceID, &record.CorrelationID); err != nil {
		return domain.AuditRecord{}, err
	}
	record.Outcome = domain.Outcome(outcome)
	if productID != nil {
		record.ProductID = *productID
	}
	if resourceID != nil {
		record.ResourceID = *resourceID
	}
	return record, nil
}
