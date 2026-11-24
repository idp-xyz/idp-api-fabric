package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func instantAt(hour int) *time.Time {
	at := time.Date(2026, 11, 23, hour, 0, 0, 0, time.UTC)
	return &at
}

// A filter that asks for nothing is a list of everything, and each member that
// is set narrows it to what the trail can hold.
func TestAnAuditFilterThatAsksWhatTheTrailCanAnswerIsValid(t *testing.T) {
	asks := map[string]AuditFilter{
		"nothing":                       {},
		"a range of time":               {RecordedSince: instantAt(1), RecordedBefore: instantAt(2)},
		"only the start of a range":     {RecordedSince: instantAt(1)},
		"only the end of a range":       {RecordedBefore: instantAt(2)},
		"an action":                     {Action: "product_role.grant"},
		"an action of three words":      {Action: "publication.target.create"},
		"an action with an underscore":  {Action: "audience_config.change"},
		"an outcome that succeeded":     {Outcome: OutcomeSucceeded},
		"an outcome that was denied":    {Outcome: OutcomeDenied},
		"an issuer alone":               {ActorIssuer: "https://issuer.example"},
		"a subject alone":               {ActorSubject: "alice"},
		"a subject in another script":   {ActorSubject: "用户-艾丽丝"},
		"a kind of resource":            {ResourceType: "RoleAssignment"},
		"a resource":                    {ResourceID: "6f1d2c3a-0b7e-4c59-9a43-5e2f8d1c7b60"},
		"a correlation identifier":      {CorrelationID: "3b0f6a2d-8c41-4e7f-a5d9-0e1c2b3a4d5e"},
		"a reference of the longest":    {ActorSubject: strings.Repeat("a", ReferenceMaxLength)},
		"an action of the longest":      {Action: strings.Repeat("a", NameMaxLength/2-1) + "." + strings.Repeat("b", NameMaxLength/2-1)},
		"a kind of resource, longest":   {ResourceType: strings.Repeat("R", NameMaxLength)},
		"a reference of 255 characters": {ActorSubject: strings.Repeat("艾", ReferenceMaxLength)},
	}
	for name, filter := range asks {
		t.Run(name, func(t *testing.T) {
			if err := filter.Validate(); err != nil {
				t.Errorf("the filter was refused: %v", err)
			}
		})
	}
	if err := (AuditFilter{
		RecordedSince: instantAt(1), RecordedBefore: instantAt(2), Action: "member.grant", Outcome: OutcomeDenied,
		ActorIssuer: "https://issuer.example", ActorSubject: "alice", ResourceType: "Member",
		ResourceID: "6f1d2c3a-0b7e-4c59-9a43-5e2f8d1c7b60", CorrelationID: "c",
	}).Validate(); err != nil {
		t.Errorf("a filter that sets every member was refused: %v", err)
	}
}

// What no record can match is refused, and the words say what is wrong and
// repeat nothing of the value.
func TestAnAuditFilterIsRefusedWhenItsValuesAreNotWhatTheTrailHolds(t *testing.T) {
	const canary = "canary-value"
	refused := map[string]AuditFilter{
		"a range that ends where it begins":           {RecordedSince: instantAt(1), RecordedBefore: instantAt(1)},
		"a range that ends before it begins":          {RecordedSince: instantAt(2), RecordedBefore: instantAt(1)},
		"an outcome nobody defined":                   {Outcome: "MAYBE"},
		"an outcome in lower case":                    {Outcome: "denied"},
		"an action of one word":                       {Action: canary},
		"an action in upper case":                     {Action: "Product.Register"},
		"an action that starts with a digit":          {Action: "1product.register"},
		"an action with a hyphen":                     {Action: "product-role.grant"},
		"an action that ends with a dot":              {Action: "product_role."},
		"an action with a space":                      {Action: "product_role. grant"},
		"an action that is too long":                  {Action: strings.Repeat("a", NameMaxLength/2) + "." + strings.Repeat("b", NameMaxLength/2)},
		"a kind of resource with a hyphen":            {ResourceType: "Role-Assignment"},
		"a kind of resource that starts with a digit": {ResourceType: "1Role"},
		"a kind of resource that is too long":         {ResourceType: strings.Repeat("R", NameMaxLength+1)},
		"a resource that is no identifier":            {ResourceID: canary},
		"an identifier in upper case":                 {ResourceID: "6F1D2C3A-0B7E-4C59-9A43-5E2F8D1C7B60"},
		"an identifier without hyphens":               {ResourceID: "6f1d2c3a0b7e4c599a435e2f8d1c7b60"},
		"an identifier with a trailing space":         {ResourceID: "6f1d2c3a-0b7e-4c59-9a43-5e2f8d1c7b60 "},
		"an issuer with a control character":          {ActorIssuer: "https://issuer.example\x00"},
		"a subject with a line break":                 {ActorSubject: "ali\nce"},
		"a subject that is not UTF-8":                 {ActorSubject: "ali\xffce"},
		"a subject that is too long":                  {ActorSubject: strings.Repeat("a", ReferenceMaxLength+1)},
		"a subject of too many characters":            {ActorSubject: strings.Repeat("艾", ReferenceMaxLength+1)},
		"a correlation identifier with a control":     {CorrelationID: "a\x1bb"},
	}
	for name, filter := range refused {
		t.Run(name, func(t *testing.T) {
			err := filter.Validate()
			if !errors.Is(err, ErrInvalidAuditQuery) {
				t.Fatalf("got %v, want %v", err, ErrInvalidAuditQuery)
			}
			for _, value := range []string{canary, "\x00", "\xff", "\x1b"} {
				if strings.Contains(err.Error(), value) {
					t.Errorf("the words repeat the value: %q", err)
				}
			}
			if !utf8.ValidString(err.Error()) {
				t.Errorf("the words are not UTF-8: %q", err)
			}
		})
	}
}

// The trail keeps an instant in UTC, to the microsecond, and asks for one the
// way it keeps it: an instant finer than that names none the trail could
// hold. A bound that is not set stays unset, and the instant of the first
// microsecond of the year one is a bound like any other.
func TestAnInstantOfAFilterIsAskedForAsTheTrailKeepsIt(t *testing.T) {
	shanghai := time.FixedZone("UTC+8", 8*60*60)
	since := time.Date(2026, 11, 23, 10, 30, 0, 123456789, shanghai)
	before := time.Date(2026, 11, 23, 2, 31, 0, 999999999, time.UTC)
	epoch := time.Time{}

	got := AuditFilter{RecordedSince: &since, RecordedBefore: &before}.Normalized()
	if want := time.Date(2026, 11, 23, 2, 30, 0, 123456000, time.UTC); got.RecordedSince == nil || !got.RecordedSince.Equal(want) ||
		got.RecordedSince.Location() != time.UTC || got.RecordedSince.Nanosecond() != 123456000 {
		t.Errorf("an instant with an offset and nanoseconds is %v, want %v in UTC", got.RecordedSince, want)
	}
	if want := time.Date(2026, 11, 23, 2, 31, 0, 999999000, time.UTC); got.RecordedBefore == nil || !got.RecordedBefore.Equal(want) {
		t.Errorf("an instant of nine digits is %v, want %v", got.RecordedBefore, want)
	}
	// What was asked is not changed under the caller's feet.
	if since.Nanosecond() != 123456789 || since.Location() != shanghai {
		t.Errorf("the instant of the caller was changed to %v", since)
	}

	if got := (AuditFilter{}).Normalized(); got.RecordedSince != nil || got.RecordedBefore != nil {
		t.Errorf("a filter that sets no bound has the bounds %v and %v, want none", got.RecordedSince, got.RecordedBefore)
	}
	if got := (AuditFilter{RecordedBefore: &epoch}).Normalized(); got.RecordedBefore == nil || got.RecordedSince != nil {
		t.Errorf("the first instant there is, as the end of a range: %v and %v, want it kept as a bound", got.RecordedSince, got.RecordedBefore)
	}

	// Two instants a nanosecond apart are one for the trail: a range between
	// them is empty, and is refused as one.
	first, second := time.Date(2026, 11, 23, 2, 30, 0, 100, time.UTC), time.Date(2026, 11, 23, 2, 30, 0, 200, time.UTC)
	if err := (AuditFilter{RecordedSince: &first, RecordedBefore: &second}).Validate(); err != nil {
		t.Fatalf("a range of a hundred nanoseconds as it is asked for: %v", err)
	}
	if err := (AuditFilter{RecordedSince: &first, RecordedBefore: &second}).Normalized().Validate(); !errors.Is(err, ErrInvalidAuditQuery) {
		t.Errorf("a range of a hundred nanoseconds as the trail keeps it: got %v, want %v", err, ErrInvalidAuditQuery)
	}
}

func TestAnIdentifierIsInItsCanonicalFormOrIsNone(t *testing.T) {
	for value, want := range map[string]bool{
		"6f1d2c3a-0b7e-4c59-9a43-5e2f8d1c7b60":   true,
		"00000000-0000-0000-0000-000000000000":   true,
		"6F1D2C3A-0B7E-4C59-9A43-5E2F8D1C7B60":   false,
		"6f1d2c3a0b7e4c599a435e2f8d1c7b60":       false,
		"6f1d2c3a-0b7e-4c59-9a43-5e2f8d1c7b6":    false,
		"6f1d2c3a-0b7e-4c59-9a43-5e2f8d1c7b600":  false,
		"6f1d2c3a-0b7e-4c59-9a43-5e2f8d1c7b6g":   false,
		"6f1d2c3a-0b7e-4c59-9a43-5e2f8d1c7b60\n": false,
		"":                                       false,
	} {
		if got := ValidIdentifier(value); got != want {
			t.Errorf("ValidIdentifier(%q) is %t, want %t", value, got, want)
		}
	}
}
