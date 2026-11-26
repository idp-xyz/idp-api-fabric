package application

import (
	"context"
	"errors"
	"fmt"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

var ErrBaselineMismatch = errors.New("the check did not compare against the active revision")

// ReleaseCheck names the check a release rests on and the content it has to
// have read. Preparing asks what a release asks of its check when it is
// prepared; otherwise the check is the one a release recorded, and is asked
// only to be that check. Baseline is the revision a slot served last, when
// an activation replaces a release of another revision with this one.
type ReleaseCheck struct {
	Revision    domain.RevisionRef
	CheckRunID  string
	InputDigest string
	// Waivers are what whoever prepares the release lets stand of the
	// rules that did not pass. They are asked for only while preparing.
	Waivers   []domain.ReleaseWaiver
	Preparing bool
	Baseline  *domain.ComparisonBaseline
}

// VerifiedCheck is the check a release rests on, as the release records it,
// with the waivers it was prepared with.
type VerifiedCheck struct {
	CheckRunID       string
	PolicySnapshotID string
	PolicyDigest     string
	Waivers          []domain.ReleaseWaiver
}

// VerifyReleaseCheck verifies the check of a release for the registry and
// publication. The check is a recorded check of the revision that read the
// content the release is of. One that is not there is not found while a
// release is prepared on it (ErrCheckRunNotFound), and is a record that is not
// what a release recorded once the release names it (domain.ErrCheckRunDamaged),
// as is a check that read other content.
//
// While a release is prepared the check is held to domain.GateRelease: made
// under the policy and by the tools of today, with every rule that did not
// pass waived, and none that nothing waives among them. Once a release is
// prepared it rests on that check for good: activating and restoring it ask
// nothing of the policy and the tools of today, only that its check is the
// one it recorded and, where a slot replaces a release of another revision,
// that the check compared the revision with that one (ErrBaselineMismatch).
func (c *Checks) VerifyReleaseCheck(ctx context.Context, input ReleaseCheck) (VerifiedCheck, error) {
	run, found, err := c.deps.Checks.Find(ctx, input.Revision, input.CheckRunID)
	if err != nil {
		return VerifiedCheck{}, err
	}
	if !found && input.Preparing {
		return VerifiedCheck{}, ErrCheckRunNotFound
	}
	if !found {
		return VerifiedCheck{}, fmt.Errorf("the check run %s a release names: %w", input.CheckRunID, domain.ErrCheckRunDamaged)
	}
	if run.InputDigest != input.InputDigest {
		return VerifiedCheck{}, fmt.Errorf("check run %s: %w", run.ID, domain.ErrCheckRunDamaged)
	}
	verified := VerifiedCheck{CheckRunID: run.ID, PolicySnapshotID: run.PolicySnapshotID, PolicyDigest: run.PolicyDigest}
	if input.Preparing {
		tools := []domain.Tool{c.deps.Evaluator.Tool(), c.deps.Comparator.Tool()}
		if verified.Waivers, err = domain.GateRelease(run, c.policy, tools, input.Waivers); err != nil {
			return VerifiedCheck{}, err
		}
	}
	if input.Baseline != nil && (run.Compatibility == nil || run.Compatibility.Baseline != *input.Baseline) {
		return VerifiedCheck{}, ErrBaselineMismatch
	}
	return verified, nil
}
