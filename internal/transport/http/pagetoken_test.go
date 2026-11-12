package http

import (
	"encoding/base64"
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

var everyList = []string{listOfProducts, listOfWorkspaces, listOfMemberships, listOfRoleAssignments}

// tokenOf is the token whose content is the JSON given, whatever it says.
func tokenOf(content string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(content))
}

// A token names the position it was made of, to the microsecond, and is a
// string the contract declares: what a page hands out as next_page_token is
// accepted back as page_token.
func TestAPageTokenNamesItsPositionAndIsOneTheContractDeclares(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatalf("load the contract: %v", err)
	}
	handed, taken := contract.Components.Schemas["NextPageToken"].Value, contract.Components.Parameters["PageToken"].Value.Schema.Value
	if handed.Pattern == "" || handed.Pattern != taken.Pattern || handed.MaxLength == nil || taken.MaxLength == nil ||
		*handed.MaxLength != *taken.MaxLength || handed.MinLength != taken.MinLength {
		t.Fatalf("the contract hands out a token as %q of %d to %v characters and takes one as %q of %d to %v",
			handed.Pattern, handed.MinLength, handed.MaxLength, taken.Pattern, taken.MinLength, taken.MaxLength)
	}
	declared := regexp.MustCompile(handed.Pattern)

	positions := map[string]ports.Position{
		"an instant with every digit":   {At: createdAt, ID: productID},
		"the last microsecond of a day": {At: time.Date(2026, 12, 31, 23, 59, 59, 999999000, time.UTC), ID: workspaceID},
		"the first microsecond":         {At: time.Date(2026, 1, 1, 0, 0, 0, 1000, time.UTC), ID: membershipID},
		"a whole second":                {At: time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC), ID: roleAssignmentID},
		"a year far from now":           {At: time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC), ID: "ffffffff-ffff-4fff-bfff-ffffffffffff"},
	}
	tokens := map[string]string{}
	for _, list := range everyList {
		for name, position := range positions {
			token := encodePageToken(list, position)
			if !declared.MatchString(token) || uint64(len(token)) < handed.MinLength || uint64(len(token)) > *handed.MaxLength {
				t.Errorf("%s, %s: the token %q is not one the contract declares", list, name, token)
			}
			got, err := decodePageToken(list, token)
			if err != nil || !got.At.Equal(position.At) || got.At.Location() != time.UTC || got.ID != position.ID {
				t.Errorf("%s, %s: the token names %+v (%v), want %+v", list, name, got, err, position)
			}
			if other, taken := tokens[token]; taken {
				t.Errorf("%s, %s: the token is that of %s as well", list, name, other)
			}
			tokens[token] = list + ", " + name

			// A token of one list names no position in another.
			for _, another := range everyList {
				if another == list {
					continue
				}
				if got, err := decodePageToken(another, token); err != errPageTokenInvalid || got != (ports.Position{}) {
					t.Errorf("%s, %s: as a token of %s it names %+v (%v), want nothing and %v", list, name, another, got, err, errPageTokenInvalid)
				}
			}
		}
	}

	// One instant has one token, in whichever zone it is given.
	shanghai := time.FixedZone("UTC+8", 8*60*60)
	if local, utc := encodePageToken(listOfProducts, ports.Position{At: createdAt.In(shanghai), ID: productID}),
		encodePageToken(listOfProducts, ports.Position{At: createdAt, ID: productID}); local != utc {
		t.Errorf("one instant has the tokens %q and %q", local, utc)
	}
}

// Anything that is not a token of the list, as this build writes one, is
// refused, and nothing says why.
func TestWhatIsNoPageTokenIsRefused(t *testing.T) {
	const list = listOfMemberships
	good := `{"v":1,"k":"memberships","at":"2026-11-11T09:00:00.123456Z","id":"` + membershipID + `"}`
	if token := encodePageToken(list, ports.Position{At: createdAt, ID: membershipID}); token != tokenOf(good) {
		t.Fatalf("a token holds %q, want %q", token, good)
	}
	with := func(old, new string) string {
		if !strings.Contains(good, old) {
			t.Fatalf("the token does not hold %q", old)
		}
		return tokenOf(strings.Replace(good, old, new, 1))
	}

	refused := map[string]string{
		"nothing":                               "",
		"one character":                         "a",
		"words":                                 "not-a-token",
		"the longest string the contract takes": strings.Repeat("A", 512),
		"base64 with padding":                   tokenOf(good) + "=",
		"base64 of the other alphabet":          "Pj4+Pz8/",
		"characters no base64 has":              "💥",

		"content that is no JSON":        tokenOf("canary"),
		"content that is null":           tokenOf("null"),
		"content that is a list":         tokenOf("[]"),
		"content that is a string":       tokenOf(`"canary"`),
		"content that is a number":       tokenOf("1"),
		"an object without members":      tokenOf("{}"),
		"an object with one member":      tokenOf(`{"v":1}`),
		"an object that is cut short":    tokenOf(good[:len(good)-1]),
		"an object followed by another":  tokenOf(good + good),
		"an object followed by a space":  tokenOf(good + " "),
		"an object that starts with one": tokenOf(" " + good),

		"another version":          with(`"v":1`, `"v":2`),
		"no version":               with(`"v":1,`, ``),
		"the version as a string":  with(`"v":1`, `"v":"1"`),
		"the version as a decimal": with(`"v":1`, `"v":1.0`),
		"another list":             with(`"k":"memberships"`, `"k":"workspaces"`),
		"a list nobody has":        with(`"k":"memberships"`, `"k":"canaries"`),
		"the list in upper case":   with(`"k":"memberships"`, `"k":"MEMBERSHIPS"`),
		"no list":                  with(`"k":"memberships",`, ``),

		"an instant without its fraction":    with(`09:00:00.123456Z`, `09:00:00Z`),
		"an instant to the millisecond":      with(`.123456Z`, `.123Z`),
		"an instant to the nanosecond":       with(`.123456Z`, `.123456789Z`),
		"an instant in another zone":         with(`.123456Z`, `.123456+08:00`),
		"an instant without a zone":          with(`.123456Z`, `.123456`),
		"an instant with a small z":          with(`.123456Z`, `.123456z`),
		"an instant with a space":            with(`T09:00`, ` 09:00`),
		"a day that is none":                 with(`2026-11-11`, `2026-02-30`),
		"a month that is none":               with(`2026-11-11`, `2026-13-11`),
		"the instant no item was created at": with(`2026-11-11T09:00:00.123456Z`, `0001-01-01T00:00:00.000000Z`),
		"an instant that is a number":        with(`"2026-11-11T09:00:00.123456Z"`, `1794387600`),
		"no instant":                         with(`"at":"2026-11-11T09:00:00.123456Z",`, ``),

		"an identifier in upper case":      with(membershipID, strings.ToUpper(membershipID)),
		"an identifier that is too long":   with(membershipID, membershipID+"0"),
		"an identifier that is too short":  with(membershipID, membershipID[1:]),
		"an identifier in braces":          with(membershipID, "{"+membershipID+"}"),
		"an identifier that is a key":      with(membershipID, "api-engineering"),
		"an identifier that is empty":      with(membershipID, ""),
		"an identifier that is SQL":        with(membershipID, "' OR ''='"),
		"no identifier":                    with(`,"id":"`+membershipID+`"`, ``),
		"a member the token does not have": with(`"v":1`, `"v":1,"canary":true`),
		"a member said twice":              with(`"v":1`, `"v":1,"v":1`),
		"the members in another order":     tokenOf(`{"k":"memberships","v":1,"at":"2026-11-11T09:00:00.123456Z","id":"` + membershipID + `"}`),
		"the members in upper case":        tokenOf(`{"V":1,"K":"memberships","AT":"2026-11-11T09:00:00.123456Z","ID":"` + membershipID + `"}`),
		"a space between two members":      with(`,"k"`, `, "k"`),
		"a line break in the object":       with(`,"k"`, ",\n\"k\""),
	}
	for name, token := range refused {
		if got, err := decodePageToken(list, token); err != errPageTokenInvalid || got != (ports.Position{}) {
			t.Errorf("%s: got %+v and %v, want nothing and exactly %v", name, got, err, errPageTokenInvalid)
		}
	}

	// A decoder that is lenient about the last character of a token reads
	// several strings as one content. Only the one this build writes is a
	// token.
	token, alike := tokenOf(good), 0
	for _, last := range "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_" {
		variant := token[:len(token)-1] + string(last)
		if variant == token {
			continue
		}
		if content, err := base64.RawURLEncoding.DecodeString(variant); err == nil && string(content) == good {
			alike++
		}
		if got, err := decodePageToken(list, variant); err != errPageTokenInvalid {
			t.Errorf("the token with %q as its last character names %+v (%v), want it refused", last, got, err)
		}
	}
	if len(token)%4 != 0 && alike == 0 {
		t.Errorf("no other string decodes to the content of the token %q; the check above proves nothing", token)
	}
}

// Whatever a caller sends as a token, decoding it ends: with a position that
// has this very token, or with a refusal.
func TestDecodingAPageTokenAlwaysEnds(t *testing.T) {
	random := rand.New(rand.NewPCG(20261112, 1))
	alphabet := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	pieces := []string{`{`, `}`, `"v"`, `:`, `1`, `,`, `"k"`, `"products"`, `"at"`, `"2026-11-11T09:00:00.123456Z"`, `"id"`, `"` + productID + `"`,
		`null`, `[`, `]`, `\`, `"`, `9999999999999999999999`, `-`, `e`, "\x00", "\xff", ` `}

	for range 20000 {
		var sent string
		switch random.IntN(3) {
		case 0:
			// A string the contract lets through.
			characters := make([]byte, random.IntN(140))
			for index := range characters {
				characters[index] = alphabet[random.IntN(len(alphabet))]
			}
			sent = string(characters)
		case 1:
			// The pieces of a token, put together at random.
			var content strings.Builder
			for range random.IntN(16) {
				content.WriteString(pieces[random.IntN(len(pieces))])
			}
			sent = tokenOf(content.String())
		default:
			// Bytes.
			content := make([]byte, random.IntN(120))
			for index := range content {
				content[index] = byte(random.IntN(256))
			}
			sent = tokenOf(string(content))
		}
		for _, list := range everyList {
			position, err := decodePageToken(list, sent)
			if err == nil && encodePageToken(list, position) != sent {
				t.Fatalf("%q is taken as a token of %s and is not the token of the position %+v it names", sent, list, position)
			}
			if err != nil && (err != errPageTokenInvalid || position != (ports.Position{})) {
				t.Fatalf("%q, as a token of %s: got %+v and %v, want nothing and exactly %v", sent, list, position, err, errPageTokenInvalid)
			}
		}
	}
}
