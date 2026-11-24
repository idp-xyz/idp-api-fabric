package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ErrInvalidAuditQuery reports a question of the audit trail that no record
// can answer: a range that ends before it begins, a value that is none of
// what the trail holds. It is wrapped with what is wrong, in words that
// repeat nothing of the value.
var ErrInvalidAuditQuery = errors.New("the audit query is not one the trail can answer")

func invalidQuery(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidAuditQuery, fmt.Sprintf(format, args...))
}

// The bounds of what a query names, in characters. The contract of the API
// states the same ones, and a test holds the two together.
const (
	// NameMaxLength bounds an action and a kind of resource.
	NameMaxLength = 64
	// ReferenceMaxLength bounds an issuer, a subject and a correlation
	// identifier.
	ReferenceMaxLength = 255
)

// The shapes of an action, such as product_role.grant, and of a kind of
// resource, such as RoleAssignment. The contract states the same patterns,
// and a test holds the two together.
const (
	ActionPattern       = `^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`
	ResourceTypePattern = `^[A-Za-z][A-Za-z0-9]*$`
)

var (
	action       = regexp.MustCompile(ActionPattern)
	resourceType = regexp.MustCompile(ResourceTypePattern)
	identifier   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// ValidIdentifier reports whether value is an identifier in its canonical
// form: a UUID in lower case.
func ValidIdentifier(value string) bool { return identifier.MatchString(value) }

// AuditFilter is what a reader asks of the audit records of a product, beyond
// the product. Every member narrows the list and none widens it: a record is
// let through when it is what each member that is set says.
//
// Apart from the time, a member is matched exactly. A member that is not set
// matches everything.
type AuditFilter struct {
	// RecordedSince and RecordedBefore bound the instant a record was
	// recorded: at or after the first, and before the second. Neither is
	// set to the zero time: a bound that is nil is no bound.
	RecordedSince  *time.Time
	RecordedBefore *time.Time

	Action  string
	Outcome Outcome
	// ActorIssuer and ActorSubject are the parts of the principal that
	// acted. Either may be asked for alone: the subject then matches under
	// any issuer.
	ActorIssuer  string
	ActorSubject string

	ResourceType  string
	ResourceID    string
	CorrelationID string
}

// Normalized returns the filter with its instants as the trail keeps them: in
// UTC, to the microsecond. An instant finer than that names no record the
// trail could hold.
func (f AuditFilter) Normalized() AuditFilter {
	f.RecordedSince, f.RecordedBefore = stored(f.RecordedSince), stored(f.RecordedBefore)
	return f
}

func stored(instant *time.Time) *time.Time {
	if instant == nil {
		return nil
	}
	at := instant.UTC().Truncate(time.Microsecond)
	return &at
}

// Validate reports whether the filter asks what the trail can answer.
func (f AuditFilter) Validate() error {
	if f.RecordedSince != nil && f.RecordedBefore != nil && !f.RecordedSince.Before(*f.RecordedBefore) {
		return invalidQuery("the range of time ends where it begins or before")
	}
	switch f.Outcome {
	case "", OutcomeSucceeded, OutcomeDenied:
	default:
		return invalidQuery("an outcome is succeeded or denied")
	}
	if f.Action != "" && (len(f.Action) > NameMaxLength || !action.MatchString(f.Action)) {
		return invalidQuery("an action is words of lower case letters, digits and underscores, joined by dots")
	}
	if f.ResourceType != "" && (len(f.ResourceType) > NameMaxLength || !resourceType.MatchString(f.ResourceType)) {
		return invalidQuery("a kind of resource is letters and digits")
	}
	if f.ResourceID != "" && !ValidIdentifier(f.ResourceID) {
		return invalidQuery("a resource identifier is a UUID in its canonical form")
	}
	for _, reference := range []struct{ what, value string }{
		{"the issuer of an actor", f.ActorIssuer}, {"the subject of an actor", f.ActorSubject}, {"a correlation identifier", f.CorrelationID},
	} {
		if err := checkReference(reference.what, reference.value); err != nil {
			return err
		}
	}
	return nil
}

// checkReference accepts a reference to ask for: not set, or of a bounded
// length and without control characters, as the trail holds one.
func checkReference(what, value string) error {
	switch {
	case value == "":
		return nil
	case !utf8.ValidString(value):
		return invalidQuery("%s is not valid UTF-8", what)
	case utf8.RuneCountInString(value) > ReferenceMaxLength:
		return invalidQuery("%s is longer than %d characters", what, ReferenceMaxLength)
	case strings.ContainsFunc(value, unicode.IsControl):
		return invalidQuery("%s contains a control character", what)
	}
	return nil
}
