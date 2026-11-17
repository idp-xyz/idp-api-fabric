package http

import (
	"errors"
	"strconv"
	"strings"
)

// A change of something that changes is made from the version the caller
// read (F04 §11). The answer that shows it carries that version as its ETag,
// and the request that changes it sends the ETag back in If-Match. Whether it
// is still the version in force is decided by the use case, inside the
// transaction of the change.

// errPreconditionRequired reports a change that does not say which version
// it was made from. Made unconditionally, it would overwrite whatever was
// changed since the caller read.
var errPreconditionRequired = errors.New("the change does not say which version it was made from")

// entityTag is the ETag of a version: its number, in double quotes.
func entityTag(version int64) EntityTag {
	return `"` + strconv.FormatInt(version, 10) + `"`
}

// versionIn is the version an If-Match names. The validator lets through one
// version in double quotes and nothing else, so what is left to refuse here
// is a request that names none.
func versionIn(ifMatch *IfMatch) (int64, error) {
	if ifMatch == nil {
		return 0, errPreconditionRequired
	}
	digits, opened := strings.CutPrefix(*ifMatch, `"`)
	digits, closed := strings.CutSuffix(digits, `"`)
	version, err := strconv.ParseInt(digits, 10, 64)
	if !opened || !closed || err != nil || version < 1 {
		return 0, errors.New("the validator let through an If-Match that names no version")
	}
	return version, nil
}
