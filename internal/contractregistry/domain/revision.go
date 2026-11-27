package domain

import "time"

const SourceUpload = "UPLOAD"

// The statuses of a revision (F03 §8.1). The content of a revision never
// changes; its status says where it stood in its review. A revision was
// recorded as a draft, was in review from the moment it was submitted, and
// left the review approved, rejected or withdrawn.
//
// No review is asked for any more (CR-0001): a revision is recorded as a
// draft and stays where it is, and a release is prepared from a revision
// whatever its status, on a check of it. The statuses of before are kept
// as they were recorded.
const RevisionDraft = "DRAFT"

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
	Specification    Specification
	SpecVersion      string
	SourceDigest     string
	RefClosureDigest string
	CreatedAt        time.Time
	// CreatedBy is who recorded the revision: the author of its content as
	// far as the registry knows.
	CreatedBy Principal
}
