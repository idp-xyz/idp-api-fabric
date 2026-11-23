package ports

import (
	"context"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platformoperations/domain"
)

// Position is where a page of the audit records of a product starts behind:
// the instant the last record of the page before was recorded, and its
// identifier, which tells apart the records of one instant. The list is
// newest first, so what is behind a position is what was recorded before it.
type Position struct {
	At time.Time
	ID string
}

// Page is the part of a list a store is asked for: at most Limit records, and
// those behind After if it is set. Nil starts the list.
type Page struct {
	After *Position
	Limit int
}

// AuditReader reads the audit trail back. It never changes a record: the
// trail only grows.
type AuditReader interface {
	// List returns one page of the records of the product that the filter
	// lets through, newest first, by the instant they were recorded and then
	// by identifier. A record of another product is not among them, and
	// neither is one that belongs to no product. The transaction ctx
	// carries, if any, is the one it reads in.
	List(ctx context.Context, productID string, filter domain.AuditFilter, page Page) ([]domain.AuditRecord, error)
}

// AuditScope is what a caller is to a product, as far as reading the audit
// records of the product goes.
type AuditScope struct {
	// Found is whether there is such a product.
	Found bool
	// Known is whether the caller may know that there is.
	Known bool
	// Auditor is whether the caller holds the role of an auditor of the
	// product now.
	Auditor bool
}

// AuditScopes tells what a caller is to a product. Who holds which role in a
// product is for Product Governance to say; Platform Operations decides what
// it lets them read.
type AuditScopes interface {
	// AuditScope reads it in the transaction ctx carries, and keeps the role
	// of the caller from being revoked until that transaction ends.
	AuditScope(ctx context.Context, caller domain.Actor, productID string) (AuditScope, error)
}
