package ports

import (
	"context"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// Target identifies a revision under the current caller's product and asset.
type Target struct {
	Caller        domain.Principal
	ProductID     string
	AssetID       string
	RevisionID    string
	CorrelationID string
}

type Revisions interface {
	// Revision authorizes reading the revision and returns its reference closure digest.
	Revision(context.Context, Target) (revision domain.RevisionRef, inputDigest string, err error)
	// Source authorizes reading the revision and verifies its recorded files.
	Source(context.Context, Target) (domain.RevisionRef, domain.Source, error)
	// LockAuthor rechecks the current author membership while recording a check.
	LockAuthor(context.Context, domain.Principal, domain.RevisionRef) (author, productActive bool, err error)
}

type Transactions interface {
	WithinTransaction(context.Context, func(context.Context) error) error
}

type AuditEntry struct {
	Target     Target
	CheckRunID string
	Action     string
	Rule       string
	Allowed    bool
}

type Events interface {
	Completed(context.Context, domain.CheckRun, string) error
}

type Clock interface{ Now() time.Time }
type Identifiers interface{ NewID() string }
