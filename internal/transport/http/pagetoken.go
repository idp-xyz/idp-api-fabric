package http

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// A page token is how a caller says where the next page of a list starts. It
// names a position in one list: the last item of the page before. It is
// opaque to the caller, which sends it back as it got it.
//
// A token grants nothing. Anybody can make one, and it is not kept from
// that: what the caller may read is decided again with every page, from what
// the caller holds then, and a position only says where in that the page
// starts.

// The lists a page token is of. A token of one names no position in another.
const (
	listOfProducts        = "products"
	listOfWorkspaces      = "workspaces"
	listOfMemberships     = "memberships"
	listOfRoleAssignments = "role-assignments"
)

// pageTokenVersion is the version of what a token holds. A change that a
// token of before cannot survive gets the next one. The tokens of before are
// refused from then on, and a caller reads its list again from the start.
const pageTokenVersion = 1

// tokenInstant writes the instant of a position the way the store keeps it:
// in UTC, to the microsecond, with all six digits. A token that lost a digit
// would name another position, and an item would be skipped or listed twice.
const tokenInstant = "2006-01-02T15:04:05.000000Z"

// errPageTokenInvalid reports a page token that is none of the list it was
// sent to. Why it is none is not said.
var errPageTokenInvalid = errors.New("the page token is not one of this list")

// pageToken is what a token holds. The token is these members as JSON, in
// base64url without padding.
type pageToken struct {
	Version int    `json:"v"`
	List    string `json:"k"`
	At      string `json:"at"`
	ID      string `json:"id"`
}

// encodePageToken is the token of a position in a list.
func encodePageToken(list string, position ports.Position) string {
	// A number and three strings always encode.
	content, _ := json.Marshal(pageToken{
		Version: pageTokenVersion, List: list,
		At: position.At.UTC().Format(tokenInstant), ID: position.ID,
	})
	return base64.RawURLEncoding.EncodeToString(content)
}

// decodePageToken is the position a token names in the list. It fails with
// errPageTokenInvalid for anything that is not a token of this list, as this
// build writes one: what does not decode, a token of another version or of
// another list, an instant or an identifier that is none.
//
// One position has one token. Whatever else decodes to the same members,
// such as the same JSON with a space in it, is refused, so that a token is
// what was issued or nothing.
func decodePageToken(list, token string) (ports.Position, error) {
	content, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return ports.Position{}, errPageTokenInvalid
	}
	var held pageToken
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&held); err != nil {
		return ports.Position{}, errPageTokenInvalid
	}
	if held.Version != pageTokenVersion || held.List != list {
		return ports.Position{}, errPageTokenInvalid
	}
	at, err := time.Parse(tokenInstant, held.At)
	if err != nil || at.IsZero() {
		return ports.Position{}, errPageTokenInvalid
	}
	// Every identifier has the one form the contract declares, so the parser
	// of any of them tells whether this is one.
	if _, err := domain.ParseProductID(held.ID); err != nil {
		return ports.Position{}, errPageTokenInvalid
	}
	position := ports.Position{At: at, ID: held.ID}
	if encodePageToken(list, position) != token {
		return ports.Position{}, errPageTokenInvalid
	}
	return position, nil
}

// paging reads what a list request says of its page: how many items it
// holds at most, and the position its token names in the list. A request
// without a token asks for the start of the list. One without a size gets
// the size the contract declares as the default, which the validator fills
// in; should it still name none here, the use case chooses.
func paging(list string, size *PageSize, token *PageToken) (int, *ports.Position, error) {
	var pageSize int
	if size != nil {
		pageSize = *size
	}
	if token == nil {
		return pageSize, nil, nil
	}
	position, err := decodePageToken(list, *token)
	if err != nil {
		return 0, nil, err
	}
	return pageSize, &position, nil
}

// nextPageToken is the token of the page that starts behind the position.
// The last page of a list has no position to follow, and no token.
func nextPageToken(list string, next *ports.Position) *NextPageToken {
	if next == nil {
		return nil
	}
	token := encodePageToken(list, *next)
	return &token
}

// listed is the items of a page as the contract shows each of them. A page
// without items is an empty list, never a missing one.
func listed[T, D any](items []T, show func(T) (D, error)) ([]D, error) {
	shown := make([]D, 0, len(items))
	for _, item := range items {
		dto, err := show(item)
		if err != nil {
			return nil, err
		}
		shown = append(shown, dto)
	}
	return shown, nil
}
