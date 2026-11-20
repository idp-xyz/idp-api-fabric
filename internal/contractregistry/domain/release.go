package domain

import (
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

const ArtifactFormat = "fabric-openapi-source/v1"

// MaxArtifactBytes bounds the bytes of an artifact, as the table that keeps
// them does. The largest source a revision is recorded from (MaxSourceBytes
// in MaxSourceFiles files under paths of MaxSourcePath) makes an artifact
// within it, and a test holds the two together.
const MaxArtifactBytes = 64 << 10

// ReleaseVersionPattern is the form of the version a release is prepared
// under. The contract of the API and the table of releases state the same
// pattern, and tests hold the three together.
const ReleaseVersionPattern = `^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`

var (
	ErrReleaseVersion  = errors.New("the release version is invalid")
	ErrArtifactDamaged = errors.New("the release artifact does not match its digest")
	releaseVersion     = regexp.MustCompile(ReleaseVersionPattern)
)

func ValidateReleaseVersion(version string) error {
	if !releaseVersion.MatchString(version) {
		return ErrReleaseVersion
	}
	return nil
}

// Release is a READY artifact bound to the immutable approval and check
// evidence for its revision. This record does not change with publication.
type Release struct {
	ID             string
	Asset          AssetRef
	RevisionID     string
	ReviewID       string
	Approval       ApprovalEvidence
	Version        string
	InputDigest    string
	ArtifactDigest string
	PreparedAt     time.Time
	PreparedBy     Principal
}

type ApprovalEvidence struct {
	CheckRunID       string
	PolicySnapshotID string
	PolicyDigest     string
}

type artifactFile struct {
	Path    string `json:"path"`
	Digest  string `json:"digest"`
	Content []byte `json:"content_base64"`
}

// BuildArtifact produces the versioned JSON representation of the verified
// bundle. The bytes and their digest are stored together and served verbatim.
func BuildArtifact(revision Revision, files []SourceFile) ([]byte, string, error) {
	recorded, err := RecordedSource(revision, files)
	if err != nil {
		return nil, "", err
	}
	artifact := struct {
		Format      string         `json:"format"`
		Entrypoint  string         `json:"entrypoint"`
		SpecVersion string         `json:"spec_version"`
		InputDigest string         `json:"ref_closure_digest"`
		Files       []artifactFile `json:"files"`
	}{ArtifactFormat, revision.Entrypoint, revision.SpecVersion, revision.RefClosureDigest, make([]artifactFile, 0, len(recorded))}
	for _, file := range recorded {
		artifact.Files = append(artifact.Files, artifactFile{Path: file.Path, Digest: file.Digest, Content: []byte(file.Content)})
	}
	payload, err := json.Marshal(artifact)
	if err != nil {
		return nil, "", err
	}
	return payload, sourceDigest(string(payload)), nil
}

func VerifyArtifact(digest string, payload []byte) error {
	if len(payload) == 0 || sourceDigest(string(payload)) != digest {
		return ErrArtifactDamaged
	}
	return nil
}
