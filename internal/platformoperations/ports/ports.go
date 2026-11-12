package ports

import (
	"context"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platformoperations/domain"
)

// AuditRecords is the store of the audit trail. It only ever appends.
type AuditRecords interface {
	// Append writes record in the transaction ctx carries.
	Append(ctx context.Context, record domain.AuditRecord) error
}

// Transactor owns the transaction boundary. If ctx already carries a
// transaction, fn joins it; otherwise fn runs in one of its own.
type Transactor interface {
	WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error
}

// Clock tells the time. No use case reads it from anywhere else (F03 §1.3).
type Clock interface {
	Now() time.Time
}

// Identifiers generates the identifier of a new record.
type Identifiers interface {
	NewID() string
}
