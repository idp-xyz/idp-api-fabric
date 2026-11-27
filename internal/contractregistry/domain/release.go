package domain

import (
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

// The formats of an artifact, by the specification of its contract. The
// bytes of an OpenAPI artifact are what they were before AsyncAPI was taken.
const (
	ArtifactFormat      = "fabric-openapi-source/v1"
	EventArtifactFormat = "fabric-asyncapi-source/v1"
)

// ArtifactFormatOf is the format of the artifact of a contract written in
// specification.
func ArtifactFormatOf(specification Specification) string {
	if specification == AsyncAPI {
		return EventArtifactFormat
	}
	return ArtifactFormat
}

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

// Release is a READY artifact bound to the immutable check evidence for its
// revision, and to the waivers it was prepared with. This record does not
// change with publication.
type Release struct {
	ID         string
	Asset      AssetRef
	RevisionID string
	// Specification is the language of the contract of the revision, which
	// decides the format of the artifact.
	Specification Specification
	// ReviewID names the review that approved the revision, for a release
	// prepared under one. A release prepared since reviews are no longer
	// decided (CR-0001) rests on its check alone and names none.
	ReviewID       string
	Evidence       CheckEvidence
	Waivers        []Waiver
	Version        string
	InputDigest    string
	ArtifactDigest string
	PreparedAt     time.Time
	PreparedBy     Principal
}

// CheckEvidence is the check a release rests on and the policy it was held
// to.
type CheckEvidence struct {
	CheckRunID       string
	PolicySnapshotID string
	PolicyDigest     string
}

// Waiver is a rule that did not pass in the check of a release and that
// whoever prepared the release let stand, and why. The waivers of a release
// are in the order of their rules.
type Waiver struct {
	RuleID string
	Reason string
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
	}{ArtifactFormatOf(revision.Specification), revision.Entrypoint, revision.SpecVersion, revision.RefClosureDigest, make([]artifactFile, 0, len(recorded))}
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
