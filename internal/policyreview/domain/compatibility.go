package domain

import (
	"regexp"
	"slices"
)

type Compatibility string

const (
	Compatible Compatibility = "COMPATIBLE"
	Risky      Compatibility = "RISKY"
	Unknown    Compatibility = "UNKNOWN"
	Breaking   Compatibility = "BREAKING"
)

// Priority orders compatibility by the most restrictive conclusion.
func (c Compatibility) Priority() int {
	switch c {
	case Breaking:
		return 3
	case Unknown:
		return 2
	case Risky:
		return 1
	case Compatible:
		return 0
	default:
		return -1
	}
}

type CompatibilityChange struct {
	Classification Compatibility `json:"classification"`
	Code           string        `json:"code"`
	Pointer        string        `json:"pointer,omitempty"`
}

const MaxCompatibilityChanges = 100

type Comparison struct {
	Classification Compatibility         `json:"classification"`
	Changes        []CompatibilityChange `json:"changes"`
	Truncated      bool                  `json:"truncated"`
}

// ComparisonBaseline names the verified input selected for comparison.
type ComparisonBaseline struct {
	RevisionID  string `json:"revision_id"`
	InputDigest string `json:"input_digest"`
}

// ComparisonEvidence is the bounded result the comparator produced from
// the check's candidate and the baseline named here.
type ComparisonEvidence struct {
	Baseline ComparisonBaseline `json:"baseline"`
	Tool     Tool               `json:"tool"`
	Comparison
}

const comparisonEvaluator = "semantic_diff"

var changeCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

func (e *ComparisonEvidence) validFor(revisionID string) bool {
	if e.Baseline.RevisionID == "" || e.Baseline.RevisionID == revisionID ||
		!digestForm.MatchString(e.Baseline.InputDigest) || !e.Tool.valid() ||
		len(e.Changes) > MaxCompatibilityChanges || (e.Truncated && len(e.Changes) != MaxCompatibilityChanges) {
		return false
	}
	worst := Compatible
	for _, change := range e.Changes {
		if change.Classification.Priority() < 0 || !changeCode.MatchString(change.Code) || !ValidPointer(change.Pointer) {
			return false
		}
		if change.Classification.Priority() > worst.Priority() {
			worst = change.Classification
		}
	}
	return e.Classification == worst
}

func (e *ComparisonEvidence) clone() *ComparisonEvidence {
	if e == nil {
		return nil
	}
	copy := *e
	copy.Changes = slices.Clone(e.Changes)
	return &copy
}

func (e *ComparisonEvidence) observation(rule Rule, entrypoint string) Observation {
	observation := Observation{RuleID: rule.ID}
	switch e.Classification {
	case Breaking:
		for _, change := range e.Changes {
			if change.Classification == Breaking {
				observation.Findings = append(observation.Findings, Finding{
					RuleID: rule.ID, Path: entrypoint, Pointer: change.Pointer,
					Message: "the change is backward-incompatible (" + change.Code + ")",
				})
			}
		}
	case Unknown, Risky:
		// Risky changes require additional evidence before the blocking
		// compatibility rule can pass. The result retains the classification.
		observation.Undecided = true
	}
	return observation
}

func (e *ComparisonEvidence) matches(rule Rule, outcome Outcome) bool {
	if e == nil {
		return outcome.Status == StatusNotRun
	}
	observed := e.observation(rule, "")
	return outcome.Tool == e.Tool && outcome.Status == statusOf(rule, observed) && outcome.Findings == len(observed.Findings)
}
