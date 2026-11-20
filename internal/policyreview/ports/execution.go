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
	// LockReviewer rechecks the current reviewer membership while a
	// decision on a review is recorded.
	LockReviewer(context.Context, domain.Principal, domain.RevisionRef) (reviewer, productActive bool, err error)
	// Recorder authorizes reading the revision and reports who recorded
	// it: the author of its content, who does not decide on its review.
	Recorder(context.Context, Target) (domain.Principal, error)
	// Move changes the status the registry keeps of the revision, within
	// the transaction it is called in, and returns the version that status
	// has then. The registry decides which moves there are and refuses the
	// others; of several moves that arrive together it makes one.
	Move(ctx context.Context, revision domain.RevisionRef, to string) (statusVersion int64, err error)
	// Hold keeps the revision at the status the registry has of it until
	// the transaction it is called in ends, and returns that status and
	// its version. Nothing moves the revision meanwhile, and of several
	// holds that arrive together one is given at a time.
	Hold(ctx context.Context, revision domain.RevisionRef) (status string, statusVersion int64, err error)
}

type Comparator interface {
	Tool() domain.Tool
	Compare(baseline, candidate domain.Source) domain.Comparison
}

type Transactions interface {
	WithinTransaction(context.Context, func(context.Context) error) error
}

type AuditEntry struct {
	Target     Target
	CheckRunID string
	// ReviewID names the review an entry is about, for the entries of a
	// review. It is empty for the entry of a check.
	ReviewID string
	Action   string
	Rule     string
	Allowed  bool
}

type Events interface {
	Completed(context.Context, domain.CheckRun, string) error
}

type Clock interface{ Now() time.Time }
type Identifiers interface{ NewID() string }
