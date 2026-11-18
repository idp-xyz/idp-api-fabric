package domain

import (
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"
)

// SourceGit is the kind of a source that was submitted with a Git origin.
const SourceGit = "GIT"

// ErrInvalidOrigin reports a Git origin the model refuses.
var ErrInvalidOrigin = errors.New("the Git origin is invalid")

// RepositoryPattern is the pattern of the repository of a Git origin: an
// https URL written one way. The host is in lower case, nothing stands in
// front of it, so the URL carries no credentials, and the path is one or
// more segments of letters, digits, ".", "_", "~" and "-" without a trailing
// slash, a query or a fragment. A segment of dots alone is refused: "." and
// ".." name no repository.
//
// A repository is compared as it is written, so a URL that can be written two
// ways would be two sources. What is left to the submitter is the ".git"
// suffix some hosts put on the clone URL.
//
// The contract of the API states the same pattern, and a test holds the two
// together.
const RepositoryPattern = `^https://` + repositoryHostPattern + `(:` + repositoryPortPattern + `)?` +
	`(/[A-Za-z0-9._~-]*[A-Za-z0-9_~-][A-Za-z0-9._~-]*)+$`

const repositoryHostPattern = `[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*`

// repositoryPortPattern is a port from 1 to 65535, without leading zeros.
const repositoryPortPattern = `([1-9][0-9]{0,3}|[1-5][0-9]{4}|6[0-4][0-9]{3}|65[0-4][0-9]{2}|655[0-2][0-9]|6553[0-5])`

// RepositoryMaxLength bounds the repository of a Git origin, in characters.
// The contract of the API states the same bound.
const RepositoryMaxLength = 300

// CommitPattern is the pattern of the commit of a Git origin: the full name
// of the object, 40 hexadecimal digits for SHA-1 or 64 for SHA-256, in lower
// case. An abbreviation, a branch or a tag is refused: each of them can name
// another commit tomorrow. The contract of the API states the same pattern.
const CommitPattern = `^([0-9a-f]{40}|[0-9a-f]{64})$`

// RefPattern is the pattern of the reference of a Git origin: its full name,
// as in refs/heads/main or refs/tags/v1.2.0, without white space or control
// characters. The full name says whether it was a branch or a tag. The
// contract of the API states the same pattern.
const RefPattern = `^refs/[^\p{Z}\p{Cc}]+$`

// RefMaxLength bounds the reference of a Git origin, in characters. The
// contract of the API states the same bound.
const RefMaxLength = 200

var (
	repository = regexp.MustCompile(RepositoryPattern)
	commit     = regexp.MustCompile(CommitPattern)
	reference  = regexp.MustCompile(RefPattern)
)

// GitOrigin is where the submitter of a source says its files came from: a
// repository, the commit the files were read at and, when the submitter names
// one, the branch or tag that pointed at the commit then.
//
// It is a declaration. The registry keeps it as evidence of what was said and
// by whom; it does not read the repository, so it cannot tell whether the
// commit exists or holds these files. The zero GitOrigin stands for a source
// that was uploaded without one.
type GitOrigin struct {
	Repository string
	Commit     string
	// Ref is informational: a reference moves, the commit does not.
	Ref string
}

// Declared reports whether the submitter named an origin at all.
func (o GitOrigin) Declared() bool { return o != GitOrigin{} }

// SourceKind is the kind of a source submitted with this origin.
func (o GitOrigin) SourceKind() string {
	if o.Declared() {
		return SourceGit
	}
	return SourceUpload
}

// Validate checks a declared origin. What it refuses is not repeated in the
// error: a repository URL that is refused may be one that carries a token.
func (o GitOrigin) Validate() error {
	switch {
	case len(o.Repository) > RepositoryMaxLength || !repository.MatchString(o.Repository):
		return invalidOrigin("the repository is not an https URL of a repository, written without credentials, query or trailing slash")
	case !commit.MatchString(o.Commit):
		return invalidOrigin("the commit is not the full name of a commit in lower case")
	case o.Ref == "":
		return nil
	case !utf8.ValidString(o.Ref) || utf8.RuneCountInString(o.Ref) > RefMaxLength || !reference.MatchString(o.Ref):
		return invalidOrigin("the reference is not the full name of a reference")
	}
	return nil
}

func invalidOrigin(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidOrigin, reason)
}
