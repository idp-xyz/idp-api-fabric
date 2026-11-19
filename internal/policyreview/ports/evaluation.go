package ports

import "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"

// Evaluator is a tool of a check: it judges a source by the rules whose
// evaluator it implements (F04 §8). Which rules those are is its own
// business. A check asks it about every rule of the policy and records the
// rules it says nothing of as not run.
type Evaluator interface {
	// Tool names the evaluator and the version of its behaviour.
	Tool() domain.Tool
	// Evaluate reports on each of the rules it implements the evaluator of,
	// in the order they are given, and on no other. It reads nothing but
	// the source and does not fail: a rule it cannot judge on this source
	// is reported as undecided.
	Evaluate(domain.Source, []domain.Rule) []domain.Observation
}
