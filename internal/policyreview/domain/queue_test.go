package domain_test

import (
	"slices"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// A queue of reviews is kept to the status a review stands at, and these are
// the four it can: open until it is concluded, and then as it ended. They are
// what Status says of a review in each of its states, in the order the
// contract names them, so that a status that Status gives is one a queue can
// be kept to, and the other way round.
func TestTheStatusesAQueueIsKeptToAreThoseAReviewStandsAt(t *testing.T) {
	open := domain.Review{Request: domain.ReviewRequest{ID: "review"}}
	statuses := []string{open.Status()}
	for _, outcome := range []domain.ReviewOutcome{domain.ReviewWithdrawn, domain.ReviewApproved, domain.ReviewRejected} {
		statuses = append(statuses, domain.Review{Request: open.Request, Conclusion: &domain.Conclusion{Outcome: outcome}}.Status())
	}
	if got := domain.ReviewStatuses(); !slices.Equal(got, statuses) || !slices.Equal(got, []string{"IN_REVIEW", "WITHDRAWN", "APPROVED", "REJECTED"}) {
		t.Errorf("the statuses are %v, want %v, which are what a review stands at", got, statuses)
	}
	// What is handed out is the caller's to change.
	handed := domain.ReviewStatuses()
	handed[0] = "CHANGED"
	if domain.ReviewStatuses()[0] != "IN_REVIEW" {
		t.Error("a caller changed the statuses a review has")
	}

	for _, status := range statuses {
		if !domain.ValidReviewStatus(status) {
			t.Errorf("%s is the status of a review and is refused", status)
		}
	}
	for _, status := range []string{
		"", "in_review", "Approved", "OPEN", "DRAFT", "ESCALATED", " IN_REVIEW", "APPROVED ", "IN_REVIEW\n", "APPROVED,REJECTED", "*",
	} {
		if domain.ValidReviewStatus(status) {
			t.Errorf("%q is no status of a review and is accepted", status)
		}
	}
}

// A queue is kept to the principals that asked for its reviews by an issuer or
// a subject, which are references: at least one character and at most
// MaxReference of them, of valid UTF-8 and without a control character. The
// longest is the length of that many characters and not of that many bytes.
func TestAReferenceToKeepAQueueToIsOneAPrincipalCanHave(t *testing.T) {
	for name, reference := range map[string]string{
		"a subject":                 "carol",
		"an issuer":                 "https://issuer.example",
		"one character":             "c",
		"spaces inside":             "carol of the team",
		"the longest":               strings.Repeat("a", domain.MaxReference),
		"the longest, of two bytes": strings.Repeat("é", domain.MaxReference),
		"255 characters":            strings.Repeat("a", 255),
		"255 of two bytes":          strings.Repeat("é", 255),
		"a space around it":         " carol ",
		"what is not Latin":         "卡罗尔",
		"a piece of SQL":            "carol' OR '1'='1",
	} {
		if !domain.ValidReference(reference) {
			t.Errorf("%s: %q is refused", name, reference)
		}
	}
	for name, reference := range map[string]string{
		"nothing":                 "",
		"one character too many":  strings.Repeat("a", domain.MaxReference+1),
		"one more of two bytes":   strings.Repeat("é", domain.MaxReference+1),
		"256 characters":          strings.Repeat("a", 256),
		"256 of two bytes":        strings.Repeat("é", 256),
		"a line feed":             "carol\n",
		"a tab":                   "ca\trol",
		"a null":                  "carol\x00",
		"an escape":               "\x1b[31mcarol",
		"a control of Unicode":    "carol\u0085",
		"a byte that is no UTF-8": "carol\xff",
	} {
		if domain.ValidReference(reference) {
			t.Errorf("%s: %q is accepted", name, reference)
		}
	}
}

// An identifier is in the one form the contract declares, a UUID in lower
// case with its hyphens; the identifier of a review or of a product that is
// in another form names none.
func TestAnIdentifierIsInTheOneFormTheContractDeclares(t *testing.T) {
	for _, value := range []string{
		"1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6", "00000000-0000-0000-0000-000000000000", "ffffffff-ffff-4fff-bfff-ffffffffffff",
	} {
		if !domain.ValidIdentifier(value) {
			t.Errorf("%s is refused", value)
		}
	}
	for _, value := range []string{
		"", "cargopai", "1C9A4E7B-52D0-4F8E-B6A1-90C3D2E4F5A6", "1c9a4e7b52d04f8eb6a190c3d2e4f5a6", "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a",
		"1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a60", " 1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6", "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6\n",
		"{1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6}", "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5ag", "urn:uuid:1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6",
	} {
		if domain.ValidIdentifier(value) {
			t.Errorf("%q is accepted", value)
		}
	}
	// Every character is held to its form, one at a time: a digit of an
	// identifier is of hexadecimal and in lower case, and a hyphen is a hyphen.
	const valid = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
	for position := range len(valid) {
		changes := []byte{'g', 'G', ' ', '-'}
		if valid[position] == '-' {
			changes = []byte{'a', '0', ' ', '_'}
		}
		if upper := strings.ToUpper(string(valid[position])); upper != string(valid[position]) {
			changes = append(changes, upper[0])
		}
		for _, change := range changes {
			if change == valid[position] {
				continue
			}
			value := valid[:position] + string(change) + valid[position+1:]
			if domain.ValidIdentifier(value) {
				t.Errorf("%q is accepted, which has %q at position %d", value, change, position)
			}
		}
	}
}
