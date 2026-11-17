package domain

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

const (
	MaxSourceBytes = 40 << 10
	MaxSourceFiles = 16
	MaxSourcePath  = 200
)

var (
	ErrInvalidSource          = errors.New("the contract source is invalid")
	ErrUnsupportedSpecVersion = errors.New("the contract specification version is unsupported")
	sourcePath                = regexp.MustCompile(`^[a-zA-Z0-9._-]+(/[a-zA-Z0-9._-]+)*$`)
)

type SourceFile struct {
	Path    string
	Content string
}

// ValidSourcePath names a file in the uploaded bundle's virtual root.
func ValidSourcePath(name string) bool {
	if len(name) > MaxSourcePath || !sourcePath.MatchString(name) ||
		path.Clean(name) != name || strings.HasPrefix(name, "../") {
		return false
	}
	switch path.Ext(name) {
	case ".yaml", ".yml", ".json":
		return true
	default:
		return false
	}
}

// SourceSnapshot holds the exact bytes of the entrypoint and its local
// references. The sorted manifest is the input to RefClosureDigest.
type SourceSnapshot struct {
	Entrypoint       string
	SpecVersion      string
	SourceDigest     string
	RefClosureDigest string

	manifest string
	files    []SourceFile
}

type manifestFile struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

type sourceManifest struct {
	Entrypoint string         `json:"entrypoint"`
	Files      []manifestFile `json:"files"`
}

// CaptureSource records a resolved file set and its content digests.
func CaptureSource(entrypoint, version string, files []SourceFile) (SourceSnapshot, error) {
	ordered := slices.Clone(files)
	slices.SortFunc(ordered, func(a, b SourceFile) int { return strings.Compare(a.Path, b.Path) })
	manifest := sourceManifest{Entrypoint: entrypoint, Files: make([]manifestFile, 0, len(ordered))}
	snapshot := SourceSnapshot{Entrypoint: entrypoint, SpecVersion: version, files: ordered}
	for i, file := range ordered {
		if i > 0 && ordered[i-1].Path == file.Path {
			return SourceSnapshot{}, ErrInvalidSource
		}
		digest := sourceDigest(file.Content)
		manifest.Files = append(manifest.Files, manifestFile{Path: file.Path, Digest: digest})
		if file.Path == entrypoint {
			snapshot.SourceDigest = digest
		}
	}
	if snapshot.SourceDigest == "" {
		return SourceSnapshot{}, ErrInvalidSource
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return SourceSnapshot{}, fmt.Errorf("encode the reference manifest: %w", err)
	}
	snapshot.manifest = string(encoded)
	snapshot.RefClosureDigest = sourceDigest(snapshot.manifest)
	return snapshot, nil
}

func (s SourceSnapshot) Manifest() string    { return s.manifest }
func (s SourceSnapshot) Files() []SourceFile { return slices.Clone(s.files) }

func sourceDigest(content string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content)))
}
