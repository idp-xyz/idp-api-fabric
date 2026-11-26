package domain

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	// ErrCheckStale reports a check that was not made under the policy and
	// by the tools a check is made with now. What it concluded is evidence
	// of then, and a release is prepared on a check of now (F04 §7.2).
	ErrCheckStale = errors.New("the check was not made under the current policy and tools")
	// ErrReleaseBlocked reports a check with a rule nothing waives that
	// failed or could not be judged. No release is prepared on such a check.
	ErrReleaseBlocked = errors.New("rules that nothing waives did not pass")
	// ErrReleaseCheckMissing reports a check in which a rule that blocks was
	// not run, and that rule is not one enforced outside a check.
	ErrReleaseCheckMissing = errors.New("a required contract check did not run")
	// ErrWaiverForbidden reports a waiver of a rule that nothing waives
	// (F04 §7.2).
	ErrWaiverForbidden = errors.New("a rule that nothing waives cannot be waived")
	// ErrWaiverMissing reports a release that leaves a rule that did not
	// pass without its waiver.
	ErrWaiverMissing = errors.New("rules that did not pass are not waived")
	// ErrInvalidWaiver reports a waiver the model refuses for what it says:
	// of a rule the policy does not have, of a rule twice, of a rule that
	// did not fail, or without a reason of one line.
	ErrInvalidWaiver = errors.New("the waiver is invalid")
)

func invalidWaiver(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidWaiver, reason)
}

// Current reports whether a check was made under the policy and by tools a
// check is made with now.
func Current(run CheckRun, policy Policy, tools []Tool) bool {
	if run.PolicyDigest != policy.Digest() || len(run.Tools) == 0 {
		return false
	}
	for _, tool := range run.Tools {
		if !slices.Contains(tools, tool) {
			return false
		}
	}
	return true
}

// Blockers returns the rules of a check that stand between its revision and
// a release: the ones that failed or could not be judged. unwaivable are
// those nothing waives, and no release is prepared on a check that has one;
// waivable are those whoever prepares the release may waive, each for a
// reason.
//
// A rule that warns blocks nothing. A rule no tool evaluates is not among
// them either: the check says of it that it was not run, and GateRelease
// decides whether that is a rule enforced elsewhere.
func Blockers(run CheckRun) (unwaivable, waivable []string) {
	for _, outcome := range run.Outcomes {
		if outcome.Status != StatusFail && outcome.Status != StatusUnknown {
			continue
		}
		if outcome.Rule.Waivable {
			waivable = append(waivable, outcome.Rule.ID)
		} else {
			unwaivable = append(unwaivable, outcome.Rule.ID)
		}
	}
	return unwaivable, waivable
}

// ReleaseWaiver lets one rule that did not pass in the check of a release
// stand, for the reason whoever prepares the release gives (F04 §7.2). It
// is part of the release it was given with and holds for nothing else.
type ReleaseWaiver struct {
	RuleID string
	Reason string
}

// GateRelease decides whether a release is prepared on a check, with the
// waivers whoever prepares it gives, and returns those waivers in the order
// of their rules. No person approves the revision: the check, under the
// policy and the tools of today, is what a release rests on.
//
// The check is one made under the policy and by the tools of today
// (ErrCheckStale). Every rule that failed or could not be judged is waived
// by name and for a reason (ErrWaiverMissing names those that are not); a
// rule that nothing waives is not waived (ErrWaiverForbidden), and if one
// of those did not pass no release is prepared at all (ErrReleaseBlocked).
// A waiver of a rule that passed, of a rule twice or of a rule the policy
// does not have is refused (ErrInvalidWaiver), so the waivers cover exactly
// what needs one. A rule that blocks and was not run is accepted only when
// it is enforced outside a check (ErrReleaseCheckMissing).
func GateRelease(run CheckRun, policy Policy, tools []Tool, waivers []ReleaseWaiver) ([]ReleaseWaiver, error) {
	if !Current(run, policy, tools) {
		return nil, ErrCheckStale
	}
	unwaivable, waivable := Blockers(run)
	ordered := slices.Clone(waivers)
	slices.SortFunc(ordered, func(a, b ReleaseWaiver) int { return strings.Compare(a.RuleID, b.RuleID) })
	for i, waiver := range ordered {
		rule, known := policy.Rule(waiver.RuleID)
		switch {
		case !known:
			return nil, invalidWaiver("a waiver names a rule the policy does not have")
		case i > 0 && ordered[i-1].RuleID == waiver.RuleID:
			return nil, invalidWaiver("rule " + rule.ID + " is waived twice")
		case !rule.Waivable:
			return nil, fmt.Errorf("%w: %s", ErrWaiverForbidden, rule.ID)
		case !slices.Contains(waivable, rule.ID):
			return nil, invalidWaiver("rule " + rule.ID + " did not fail and is not waived")
		case !plainText(waiver.Reason, MaxReviewReason):
			return nil, invalidWaiver("the waiver of rule " + rule.ID + " gives no reason of one line")
		}
	}
	// A rule that nothing waives and that did not pass ends the matter,
	// whatever else is waived.
	if len(unwaivable) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrReleaseBlocked, strings.Join(unwaivable, ", "))
	}
	var unwaived []string
	for _, id := range waivable {
		if !slices.ContainsFunc(ordered, func(waiver ReleaseWaiver) bool { return waiver.RuleID == id }) {
			unwaived = append(unwaived, id)
		}
	}
	if len(unwaived) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrWaiverMissing, strings.Join(unwaived, ", "))
	}
	for _, outcome := range run.Outcomes {
		if outcome.Rule.Severity == SeverityBlock && outcome.Status == StatusNotRun && !outsideContractChecks(outcome.Rule.Evaluator) {
			return nil, fmt.Errorf("%w: %s", ErrReleaseCheckMissing, outcome.Rule.ID)
		}
	}
	if len(ordered) == 0 {
		return nil, nil
	}
	return ordered, nil
}

// These rules are enforced outside check runs. The registry and publication
// enforce their own invariants, and rendering and external delivery have
// independent gates. A missing evaluator for any other mandatory rule fails
// release preparation.
func outsideContractChecks(evaluator string) bool {
	switch evaluator {
	case "semantic_diff", "release_digest", "publication_authorization", "approval_binding", "sanitized_render",
		"tool_provenance", "policy_monotonicity", "capability_registry", "four_eyes", "publication_leak_scan", "external_dependency_state":
		return true
	default:
		return false
	}
}
