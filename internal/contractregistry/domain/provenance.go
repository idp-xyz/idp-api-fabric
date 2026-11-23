package domain

import "time"

// ReleaseProvenance is what a release was prepared on, read at once: the
// release, the revision it is of, and the check and the approval it names.
//
// Each part was recorded before the release was and is never changed, so
// this says the same whatever is recorded after. That the revision is
// reviewed again and approved anew changes none of it: a release keeps the
// review and the check it was prepared under. It does not say whether that
// approval holds now (that is for a slot to say), and the status of the
// revision is not part of it.
type ReleaseProvenance struct {
	Release  Release
	Revision Revision
	Evidence ReleaseEvidence
}

// ReleaseEvidence is the check and the approval a release names. Policy &
// Review keeps both; the registry reads them back through the port that
// hands them over, and has no say in what they are.
type ReleaseEvidence struct {
	Check    ReleaseCheck
	Approval ReleaseApproval
}

// ReleaseCheck is the check the approval of a release rests on: which one it
// was, the policy it was held to, the tools that evaluated it and what it
// concluded.
type ReleaseCheck struct {
	ID           string
	PolicySetID  string
	PolicyDigest string
	Tools        []CheckTool
	// Status is what the check concluded. Summary counts the rules by what
	// was concluded of them, which is what tells a conclusion of UNKNOWN
	// from another.
	Status      string
	Summary     CheckSummary
	CompletedAt time.Time
}

// CheckTool is a tool that evaluated rules in a check, by name and version.
type CheckTool struct {
	Name    string
	Version string
}

// CheckSummary counts the rules of a check by what was concluded of them,
// and the findings of all of them.
type CheckSummary struct {
	Pass, Warn, Fail, Unknown, NotRun int
	Findings                          int
}

// ReleaseApproval is the approval a release was prepared under: the review,
// who approved, when, for what and until when, and the rules it let stand.
// Until when is as it was given. Whether the approval holds now is not
// something it knows.
type ReleaseApproval struct {
	ReviewID string
	// Supplementary says that the revision was approved already when the
	// review was asked for.
	Supplementary bool
	Scope         []ReviewTarget
	ConcludedBy   Principal
	ConcludedAt   time.Time
	ValidUntil    time.Time
	// Waivers are in the order of their rules. A waiver says no more than
	// the rule and until when: why is for the review to say.
	Waivers []ApprovalWaiver
}

// ReviewTarget is one audience in one environment, which an approval covers.
type ReviewTarget struct {
	Audience    string
	Environment string
}

// ApprovalWaiver is a rule an approval let stand although it did not pass.
type ApprovalWaiver struct {
	RuleID    string
	ExpiresAt time.Time
}
