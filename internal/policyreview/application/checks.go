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
	ErrInvalidBaseline   = errors.New("the baseline must be a different revision of the same API asset")
	// ErrNoTools reports a revision of a specification this build has no
	// tools for. Contract Registry records revisions of the ones it has.
	ErrNoTools = errors.New("no tools check a contract of this specification")
)

type Dependencies struct {
	Transactions ports.Transactions
	Revisions    ports.Revisions
	Checks       ports.CheckRuns
	// Evaluators and Comparators are the tools of a check by the
	// specification of the contract they read, one of each for every
	// specification a revision is recorded in.
	Evaluators  map[domain.Specification]ports.Evaluator
	Comparators map[domain.Specification]ports.Comparator
	Audit       func(context.Context, ports.AuditEntry) error
	Events      ports.Events
	Clock       ports.Clock
	Identifiers ports.Identifiers
}

type Checks struct {
	deps   Dependencies
	policy domain.Policy
}

func NewChecks(deps Dependencies) (*Checks, error) {
	if deps.Transactions == nil || deps.Revisions == nil || deps.Checks == nil ||
		len(deps.Evaluators) == 0 || len(deps.Evaluators) != len(deps.Comparators) || deps.Audit == nil || deps.Events == nil ||
		deps.Clock == nil || deps.Identifiers == nil {
		return nil, errors.New("Policy & Review needs transactions, revisions, check storage, evaluators and comparators, audit, events, clock and identifiers")
	}
	for specification, evaluator := range deps.Evaluators {
		if comparator := deps.Comparators[specification]; evaluator == nil || comparator == nil {
			return nil, fmt.Errorf("Policy & Review needs an evaluator and a comparator for %s", specification)
		}
	}
	return &Checks{deps: deps, policy: domain.Baseline()}, nil
}

// toolsFor are the evaluator and the comparator a source is checked with.
func (c *Checks) toolsFor(source domain.Source) (ports.Evaluator, ports.Comparator, error) {
	evaluator, found := c.deps.Evaluators[source.Specification]
	if !found {
		return nil, nil, fmt.Errorf("%w: %q", ErrNoTools, source.Specification)
	}
	return evaluator, c.deps.Comparators[source.Specification], nil
}

// tools are the tools a check is made with today, of every specification.
func (c *Checks) tools() []domain.Tool {
	var tools []domain.Tool
	for specification, evaluator := range c.deps.Evaluators {
		tools = append(tools, evaluator.Tool(), c.deps.Comparators[specification].Tool())
	}
	return tools
}

type Execution struct {
	Run     domain.CheckRun
	Created bool
}

// Run evaluates a verified revision under the current policy. The optional
// baseline selects a second verified revision of the same API asset.
func (c *Checks) Run(ctx context.Context, target ports.Target, baselineID string) (Execution, error) {
	revision, source, err := c.deps.Revisions.Source(ctx, target)
	if err != nil {
		return Execution{}, err
	}
	evaluator, compare, err := c.toolsFor(source)
	if err != nil {
		return Execution{}, err
	}
	tool := evaluator.Tool()
	tools := []domain.Tool{tool}
	var baseline domain.Source
	var comparator domain.Tool
	if baselineID != "" {
		if baselineID == revision.RevisionID {
			return Execution{}, ErrInvalidBaseline
		}
		baselineTarget := target
		baselineTarget.RevisionID = baselineID
		_, baseline, err = c.deps.Revisions.Source(ctx, baselineTarget)
		if err != nil {
			return Execution{}, err
		}
		if baseline.Specification != source.Specification {
			return Execution{}, ErrInvalidBaseline
		}
		comparator = compare.Tool()
		tools = append(tools, comparator)
	}
	toolDigest := domain.ToolDigest(tools)
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
		recorded, found, err := c.deps.Checks.Recorded(tx, revision, policyDigest, toolDigest, baselineID)
		if err != nil {
			return err
		}
		if found {
			if recorded.InputDigest != source.Digest {
				return domain.ErrCheckRunDamaged
			}
			if baselineID != "" {
				if recorded.Compatibility == nil ||
					recorded.Compatibility.Baseline != (domain.ComparisonBaseline{RevisionID: baselineID, InputDigest: baseline.Digest}) {
					return domain.ErrCheckRunDamaged
				}
			}
			result = Execution{Run: recorded}
			return nil
		}

		started := c.deps.Clock.Now()
		observations := evaluator.Evaluate(source, c.policy.Rules())
		var comparison *domain.ComparisonEvidence
		if baselineID != "" {
			comparison = &domain.ComparisonEvidence{
				Baseline:   domain.ComparisonBaseline{RevisionID: baselineID, InputDigest: baseline.Digest},
				Tool:       comparator,
				Comparison: compare.Compare(baseline, source),
			}
		}
		completed := c.deps.Clock.Now()
		snapshot, err := c.deps.Checks.Snapshot(tx, revision.ProductID, c.policy, c.deps.Identifiers.NewID(), started)
		if err != nil {
			return err
		}
		run, err := domain.Complete(domain.Draft{
			ID: c.deps.Identifiers.NewID(), PolicySnapshotID: snapshot, Revision: revision,
			RequestedBy: target.Caller, StartedAt: started, Compatibility: comparison,
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
