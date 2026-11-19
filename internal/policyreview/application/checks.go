package application

import (
	"context"
	"errors"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/ports"
)

var (
	ErrForbidden         = errors.New("the caller may not check this revision")
	ErrProductSuspended  = errors.New("the product is suspended")
	ErrRefusalNotAudited = errors.New("a check refusal could not be audited")
)

type Dependencies struct {
	Transactions ports.Transactions
	Revisions    ports.Revisions
	Checks       ports.CheckRuns
	Evaluator    ports.Evaluator
	Audit        func(context.Context, ports.AuditEntry) error
	Events       ports.Events
	Clock        ports.Clock
	Identifiers  ports.Identifiers
}

type Checks struct {
	deps   Dependencies
	policy domain.Policy
}

func NewChecks(deps Dependencies) (*Checks, error) {
	if deps.Transactions == nil || deps.Revisions == nil || deps.Checks == nil ||
		deps.Evaluator == nil || deps.Audit == nil || deps.Events == nil ||
		deps.Clock == nil || deps.Identifiers == nil {
		return nil, errors.New("Policy & Review needs transactions, revisions, check storage, an evaluator, audit, events, clock and identifiers")
	}
	return &Checks{deps: deps, policy: domain.Baseline()}, nil
}

type Execution struct {
	Run     domain.CheckRun
	Created bool
}

// Run evaluates a verified revision under the current baseline. Identical
// policy and tool inputs reuse the evidence already recorded for the revision.
func (c *Checks) Run(ctx context.Context, target ports.Target) (Execution, error) {
	revision, source, err := c.deps.Revisions.Source(ctx, target)
	if err != nil {
		return Execution{}, err
	}
	tool := c.deps.Evaluator.Tool()
	toolDigest := domain.ToolDigest([]domain.Tool{tool})
	policyDigest := c.policy.Digest()

	const action = "revision.check"
	var result Execution
	err = c.deps.Transactions.WithinTransaction(ctx, func(tx context.Context) error {
		author, active, err := c.deps.Revisions.LockAuthor(tx, target.Caller, revision)
		if err != nil {
			return err
		}
		if !author {
			return ErrForbidden
		}
		if !active {
			return ErrProductSuspended
		}
		recorded, found, err := c.deps.Checks.Recorded(tx, revision, policyDigest, toolDigest)
		if err != nil {
			return err
		}
		if found {
			if recorded.InputDigest != source.Digest {
				return domain.ErrCheckRunDamaged
			}
			result = Execution{Run: recorded}
			return nil
		}

		started := c.deps.Clock.Now()
		observations := c.deps.Evaluator.Evaluate(source, c.policy.Rules())
		completed := c.deps.Clock.Now()
		snapshot, err := c.deps.Checks.Snapshot(tx, revision.ProductID, c.policy, c.deps.Identifiers.NewID(), started)
		if err != nil {
			return err
		}
		run, err := domain.Complete(domain.Draft{
			ID: c.deps.Identifiers.NewID(), PolicySnapshotID: snapshot, Revision: revision,
			RequestedBy: target.Caller, StartedAt: started,
		}, c.policy, source, tool, observations, completed)
		if err != nil {
			return err
		}
		if err := c.deps.Checks.Record(tx, run); err != nil {
			return err
		}
		if err := c.deps.Audit(tx, ports.AuditEntry{
			Target: target, CheckRunID: run.ID, Action: action, Rule: "AC-02", Allowed: true,
		}); err != nil {
			return err
		}
		if err := c.deps.Events.Completed(tx, run, target.CorrelationID); err != nil {
			return err
		}
		result = Execution{Run: run, Created: true}
		return nil
	})
	if err == nil {
		return result, nil
	}
	if errors.Is(err, ErrForbidden) {
		if audit := c.deps.Audit(ctx, ports.AuditEntry{Target: target, Action: action, Rule: "DENY-03"}); audit != nil {
			return Execution{}, fmt.Errorf("%w: %w: %v", err, ErrRefusalNotAudited, audit)
		}
	}
	return Execution{}, err
}
