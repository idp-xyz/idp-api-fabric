package domain

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// An identifier is stable and never reused. It is the identity of an object:
// a key, a display name or a URL never is (F03 §1.3). It has the canonical
// form of a UUID.
var identifier = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ProductID identifies a product.
type ProductID string

// ParseProductID accepts the canonical form of an identifier.
func ParseProductID(value string) (ProductID, error) {
	if !identifier.MatchString(value) {
		return "", invalid("a product identifier is a UUID in its canonical form")
	}
	return ProductID(value), nil
}

// WorkspaceID identifies a workspace.
type WorkspaceID string

// ParseWorkspaceID accepts the canonical form of an identifier.
func ParseWorkspaceID(value string) (WorkspaceID, error) {
	if !identifier.MatchString(value) {
		return "", invalid("a workspace identifier is a UUID in its canonical form")
	}
	return WorkspaceID(value), nil
}

// KeyPattern is the pattern of a key. A key is the name people and
// configuration use for an object: unique in its scope, short, and safe in a
// URL or a file name. It is 2 to 63 lower case letters, digits and hyphens,
// starts with a letter and does not end with a hyphen. A key is not the
// identity of the object; the identifier is.
//
// The contract of the API states the same pattern, and a test holds the two
// together.
const KeyPattern = `^[a-z][a-z0-9-]{0,61}[a-z0-9]$`

var key = regexp.MustCompile(KeyPattern)

// DisplayNameMaxLength bounds a display name, in characters. The contract of
// the API states the same bound.
const DisplayNameMaxLength = 120

func checkKey(what, value string) error {
	if !key.MatchString(value) {
		return invalid("%s is 2 to 63 lower case letters, digits and hyphens, starts with a letter and does not end with a hyphen", what)
	}
	return nil
}

// checkDisplayName accepts the name an object is shown under. A name is for
// people: it identifies nothing and is not unique.
func checkDisplayName(what, value string) error {
	switch {
	case value == "":
		return invalid("%s is empty", what)
	case !utf8.ValidString(value):
		return invalid("%s is not valid UTF-8", what)
	case utf8.RuneCountInString(value) > DisplayNameMaxLength:
		return invalid("%s is longer than %d characters", what, DisplayNameMaxLength)
	case strings.ContainsFunc(value, unicode.IsControl):
		return invalid("%s contains a control character", what)
	case strings.TrimSpace(value) != value:
		return invalid("%s has surrounding white space", what)
	}
	return nil
}
