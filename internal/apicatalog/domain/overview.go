package domain

import (
	"slices"
	"time"
)

// CheckStatus is what a check concluded. The Catalog does not reach it: it
// holds the four words a check can conclude, to tell a filter that names one
// from a filter that names none, and the contract of the API states the same
// four, which a test holds together.
type CheckStatus string

const (
	CheckPass    CheckStatus = "PASS"
	CheckWarn    CheckStatus = "WARN"
	CheckFail    CheckStatus = "FAIL"
	CheckUnknown CheckStatus = "UNKNOWN"
)

// CheckStatuses returns the statuses a check can conclude.
func CheckStatuses() []CheckStatus {
	return []CheckStatus{CheckPass, CheckWarn, CheckFail, CheckUnknown}
}

func (s CheckStatus) Valid() bool { return slices.Contains(CheckStatuses(), s) }

// OverviewRevision is the last revision of an asset: the one of the greatest
// sequence. Its members are those of the revision as Contract Registry gives
// it.
type OverviewRevision struct {
	ID        string
	Sequence  int64
	Status    string
	CreatedAt time.Time
}

// OverviewCheck is the check of that revision that was concluded last. Its
// members are those of the check as Policy & Review lists it.
type OverviewCheck struct {
	ID          string
	Status      CheckStatus
	CompletedAt time.Time
}

// OverviewRelease is the release of the asset that was prepared last,
// whichever of its revisions it was prepared from. Its members are those of
// the release as Contract Registry gives it.
type OverviewRelease struct {
	ID         string
	RevisionID string
	Version    string
	PreparedAt time.Time
}

// Overview says where an asset stands on its way from a revision to a
// release: a reader of the revisions of its Workspace is shown it with the
// asset. It is made of what other contexts record and keeps nothing of its
// own.
type Overview struct {
	LatestRevision *OverviewRevision
	// LatestCheck is nil while the last revision has not been checked.
	LatestCheck *OverviewCheck
	// LatestRelease is nil while the asset has no release.
	LatestRelease *OverviewRelease
	// ReleasePending says that the last revision is approved and that no
	// release was prepared from it. It says nothing of whether the approval
	// still holds: a release asks that when it is prepared.
	ReleasePending bool
}
