package domain

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ReviewStatuses are the statuses a review has, in the order the contract
// names them: IN_REVIEW until it is concluded, then the way it ended. A queue
// of reviews is kept to one of them by name.
func ReviewStatuses() []string {
	return []string{ReviewOpen, string(ReviewWithdrawn), string(ReviewApproved), string(ReviewRejected)}
}

// ValidReviewStatus reports whether status is one a review has.
func ValidReviewStatus(status string) bool {
	return slices.Contains(ReviewStatuses(), status)
}

// MaxReference bounds an issuer and a subject a queue is kept to, in
// characters, as the contract bounds a reference into another system.
const MaxReference = 255

// ValidReference reports whether reference is one a queue can be kept to: of
// valid UTF-8, of 1 to MaxReference characters and without a control
// character, as the contract states a reference. Something else names no
// principal that asked for a review.
func ValidReference(reference string) bool {
	return reference != "" && utf8.ValidString(reference) && utf8.RuneCountInString(reference) <= MaxReference &&
		!strings.ContainsFunc(reference, unicode.IsControl)
}

var identifier = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ValidIdentifier reports whether value is an identifier in the one form the
// contract declares: a UUID in lower case.
func ValidIdentifier(value string) bool { return identifier.MatchString(value) }
