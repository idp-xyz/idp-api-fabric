package domain

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrInvalid reports a value that cannot be part of the Product Governance
// model: a key that is not a key, a name that is not a name. It is wrapped
// with what is wrong, in words that repeat nothing of the value.
var ErrInvalid = errors.New("the value is not valid")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// ReferenceMaxLength bounds every reference into another system, in
// characters: an issuer, a subject, an identity provider, an organization.
// They are stored and audited as they are given, so their length has to be
// known. The contract of the API states the same bound, and the token
// verifier puts it on a subject.
const ReferenceMaxLength = 255

// checkReference accepts a reference into another system. Fabric cannot know
// its format, only that it is something to compare for equality: not empty,
// of a bounded length and without control characters. That is exactly what
// the token verifier accepts as a subject, so every principal that can
// authenticate can also be named here.
//
// The length is counted in characters, as the contract counts it. Counted in
// bytes, a reference in a script that takes several bytes a character would
// pass the contract and be refused here.
func checkReference(what, value string) error {
	switch {
	case value == "":
		return invalid("%s is empty", what)
	case !utf8.ValidString(value):
		return invalid("%s is not valid UTF-8", what)
	case utf8.RuneCountInString(value) > ReferenceMaxLength:
		return invalid("%s is longer than %d characters", what, ReferenceMaxLength)
	case strings.ContainsFunc(value, unicode.IsControl):
		return invalid("%s contains a control character", what)
	}
	return nil
}

// Principal is a user, a service account or a workload that can be
// authorized: a subject as one issuer names it (F03 §2). It is a reference
// into an identity provider and carries no credential. The same subject under
// another issuer is another principal, and a name or an email address never
// identifies one.
type Principal struct {
	Issuer  string
	Subject string
}

// Validate reports whether p can name a principal.
func (p Principal) Validate() error {
	if err := checkReference("the issuer of a principal", p.Issuer); err != nil {
		return err
	}
	return checkReference("the subject of a principal", p.Subject)
}

// OrganizationRef refers to an internal organization, the research or
// operations unit a product belongs to (F03 §2). The organization itself
// lives in the identity or organization system named by IdentityProvider;
// Fabric keeps the reference and no copy.
type OrganizationRef struct {
	IdentityProvider string
	Organization     string
}

// Validate reports whether r can refer to an organization.
func (r OrganizationRef) Validate() error {
	if err := checkReference("the identity provider of an organization", r.IdentityProvider); err != nil {
		return err
	}
	return checkReference("the reference of an organization", r.Organization)
}
