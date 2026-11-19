package ports

import (
	"context"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// Position is the place of a check run in the history of a revision: when it
// was concluded and, among runs concluded at the same instant, its
// identifier.
type Position struct {
	At time.Time
	ID string
}

// CheckRuns keeps check runs. A run is written once and never changed.
//
// What it reads back it reads through domain.RecordedPolicy and
// domain.Recorded: a run whose rows no longer agree with each other, or
// whose policy is not the document its digest was computed from, is an
// error (domain.ErrCheckRunDamaged, domain.ErrPolicyDamaged) and not a run.
type CheckRuns interface {
	// Recorded returns the run of the revision under the policy with the
	// digest, by the tools with the digest, when there is one. It makes the
	// transaction it is called in the only one that records a check of the
	// revision until it ends, so that a caller who finds none and records
	// one is the only one to do so.
	Recorded(ctx context.Context, revision domain.RevisionRef, policyDigest, toolDigest string) (domain.CheckRun, bool, error)
	// Snapshot returns the identifier of the snapshot of the policy for
	// the product, and records the snapshot under id when the product has
	// none of that policy yet.
	Snapshot(ctx context.Context, productID string, policy domain.Policy, id string, at time.Time) (string, error)
	// Record stores a run, the outcomes of its rules and its findings.
	Record(context.Context, domain.CheckRun) error
	// Find returns the run of the revision with the identifier, and none
	// for a run of another revision.
	Find(ctx context.Context, revision domain.RevisionRef, id string) (domain.CheckRun, bool, error)
	// List returns the runs of the revision behind the position, oldest
	// first, at most limit of them.
	List(ctx context.Context, revision domain.RevisionRef, after *Position, limit int) ([]domain.Overview, error)
}
