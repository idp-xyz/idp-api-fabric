package domain

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

const SourceUpload = "UPLOAD"

// The statuses of a revision (F03 §8.1). The content of a revision never
// changes; its status says where it stands in its review. A revision is
// recorded as a draft, is in review from the moment it is submitted, and
// leaves the review approved, rejected or withdrawn.
//
// The status is not the approval: what approves a revision is the decision
// Policy & Review records, for the content, the policy and the scope it
// names. The status says that there is one.
const (
	RevisionDraft     = "DRAFT"
	RevisionInReview  = "IN_REVIEW"
	RevisionApproved  = "APPROVED"
	RevisionRejected  = "REJECTED"
	RevisionWithdrawn = "WITHDRAWN"
)

// ErrStatusConflict reports a revision that is not at a status the change
// asked for starts from.
var ErrStatusConflict = errors.New("the revision is not at a status this starts from")

// moves are the changes of status there are. A revision is submitted as a
// draft, or again once it was withdrawn: its content is recorded once for a
// source, so a withdrawal that ended there would end what can be asked of
// that content. Approved and rejected are where a review ends, and nothing
// leaves them: what is wrong with a rejected revision is mended in another.
var moves = map[string][]string{
	RevisionDraft:     {RevisionInReview},
	RevisionWithdrawn: {RevisionInReview},
	RevisionInReview:  {RevisionApproved, RevisionRejected, RevisionWithdrawn},
}

// Move checks that a revision at the status from can take the status to. It
// fails with ErrStatusConflict, naming the status the revision is at.
func Move(from, to string) error {
	if !slices.Contains(moves[from], to) {
		return fmt.Errorf("%w: it is %s", ErrStatusConflict, from)
	}
	return nil
}

type Principal struct {
	Issuer  string
	Subject string
}

type AssetRef struct {
	ProductID   string
	WorkspaceID string
	AssetID     string
}

// Draft is the registration of a source as a revision of one API asset.
type Draft struct {
	ID               string
	SourceSnapshotID string
	Asset            AssetRef
	// Origin is the Git origin the submitter declared, or the zero value
	// for a source uploaded without one.
	Origin     GitOrigin
	CapturedAt time.Time
	CapturedBy Principal
}

// Revision carries the immutable metadata of a recorded draft. Its source
// bytes live with the source snapshot.
type Revision struct {
	ID               string
	SourceSnapshotID string
	Asset            AssetRef
	Sequence         int64
	Status           string
	// StatusVersion counts the changes of the status: 1 for a revision as
	// it was recorded, one more with every move.
	StatusVersion int64
	SourceKind    string
	// Origin is declared for a GIT source and zero for an UPLOAD.
	Origin           GitOrigin
	Entrypoint       string
	SpecVersion      string
	SourceDigest     string
	RefClosureDigest string
	CreatedAt        time.Time
}
