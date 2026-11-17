package domain

import "time"

const (
	SourceUpload  = "UPLOAD"
	RevisionDraft = "DRAFT"
)

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
	CapturedAt       time.Time
	CapturedBy       Principal
}

// Revision carries the immutable metadata of a recorded draft. Its source
// bytes live with the source snapshot.
type Revision struct {
	ID               string
	SourceSnapshotID string
	Asset            AssetRef
	Sequence         int64
	Status           string
	SourceKind       string
	Entrypoint       string
	SpecVersion      string
	SourceDigest     string
	RefClosureDigest string
	CreatedAt        time.Time
}
