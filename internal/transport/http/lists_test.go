package http

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

// listQuery is what the use case of a list was asked, whichever list it is.
type listQuery struct {
	Caller        domain.Principal
	CorrelationID string
	ProductID     string
	WorkspaceID   string
	PageSize      int
	After         *ports.Position
}

// lists are the four lists: where each is read, which list its tokens are
// of, what one item of it is shown as, and what its use case was asked.
var lists = []struct {
	name   string
	target string
	list   string
	item   map[string]any
	asked  func(g *governance) []listQuery
}{
	{"the products", "/v1/products", listOfProducts, productBody, func(g *governance) (asked []listQuery) {
		for _, query := range g.productsListed {
			asked = append(asked, listQuery{query.Caller, query.CorrelationID, "", "", query.PageSize, query.After})
		}
		return asked
	}},
	{"the workspaces of a product", "/v1/products/" + productID + "/workspaces", listOfWorkspaces, workspaceBody, func(g *governance) (asked []listQuery) {
		for _, query := range g.spacesListed {
			asked = append(asked, listQuery{query.Caller, query.CorrelationID, query.ProductID, "", query.PageSize, query.After})
		}
		return asked
	}},
	{"the members of a workspace", memberships, listOfMemberships, membershipBody, func(g *governance) (asked []listQuery) {
		for _, query := range g.membershipsListed {
			asked = append(asked, listQuery{query.Caller, query.CorrelationID, query.ProductID, query.WorkspaceID, query.PageSize, query.After})
		}
		return asked
	}},
	{"the owners of a product", roleAssignments, listOfRoleAssignments, roleAssignmentBody, func(g *governance) (asked []listQuery) {
		for _, query := range g.rolesListed {
			asked = append(asked, listQuery{query.Caller, query.CorrelationID, query.ProductID, "", query.PageSize, query.After})
		}
		return asked
	}},
}

// A list hands what its request says to its use case: who asks, of which
// product and workspace, how many items and behind which position. It
// answers the items as the single read shows each, and the token of the
// page that follows for as long as one does.
func TestAListHandsItsRequestToItsUseCaseAndAnswersAPage(t *testing.T) {
	behind := ports.Position{At: createdAt, ID: membershipID}
	next := ports.Position{At: createdAt.Add(time.Minute), ID: roleAssignmentID}

	for _, list := range lists {
		t.Run(list.name, func(t *testing.T) {
			s := newService(t)
			s.governance.items, s.governance.next = 2, &next
			want := listQuery{Caller: domain.Principal(alice)}
			if list.list != listOfProducts {
				want.ProductID = productID
			}
			if list.list == listOfMemberships {
				want.WorkspaceID = workspaceID
			}
			asked := func() listQuery {
				t.Helper()

				queries := list.asked(s.governance)
				if len(queries) != 1 || s.governance.calls() != 1 {
					t.Fatalf("the use case of the list was asked %d times of %d calls, want once and no other", len(queries), s.governance.calls())
				}
				query := queries[0]
				if !uuid.MatchString(query.CorrelationID) {
					t.Errorf("correlation identifier %q, want one the server generated", query.CorrelationID)
				}
				query.CorrelationID = ""
				return query
			}

			// A page of two items behind a position, with another to follow.
			response := s.do(t, call{Method: http.MethodGet, Token: "alice",
				Target: list.target + "?page_size=2&page_token=" + encodePageToken(list.list, behind)})
			if response.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control %q, want no-store", got)
			}
			wantBody := map[string]any{"items": []any{list.item, list.item}, "next_page_token": encodePageToken(list.list, next)}
			if got := members(t, response); !reflect.DeepEqual(got, wantBody) {
				t.Errorf("body\n %v\nwant\n %v", got, wantBody)
			}
			want.PageSize, want.After = 2, &behind
			if got := asked(); !reflect.DeepEqual(got, want) {
				t.Errorf("the use case was asked %+v behind %+v, want %+v behind %+v", got, got.After, want, want.After)
			}

			// Without a size and a token: the size the contract declares
			// as the default, which is the one of the use cases, from the
			// start. The last page carries no token.
			s = newService(t)
			response = s.do(t, call{Method: http.MethodGet, Token: "alice", Target: list.target})
			if got := members(t, response); response.Code != http.StatusOK || !reflect.DeepEqual(got, map[string]any{"items": []any{list.item}}) {
				t.Errorf("without a size and a token: %d %s, want one item and no token", response.Code, response.Body)
			}
			want.PageSize, want.After = application.DefaultPageSize, nil
			if got := asked(); !reflect.DeepEqual(got, want) {
				t.Errorf("without a size and a token the use case was asked %+v, want %+v", got, want)
			}

			// The bounds of the size are sizes like any other.
			for _, size := range []string{"1", "100"} {
				s = newService(t)
				if response := s.do(t, call{Method: http.MethodGet, Token: "alice", Target: list.target + "?page_size=" + size}); response.Code != http.StatusOK {
					t.Errorf("a page of %s: status %d, want 200: %s", size, response.Code, response.Body)
				}
				if got := asked(); size == "1" && got.PageSize != 1 || size == "100" && got.PageSize != application.MaxPageSize {
					t.Errorf("a page of %s: the use case was asked for %d items", size, got.PageSize)
				}
			}

			// A page without items is a list that is empty, not one that
			// is missing.
			s = newService(t)
			s.governance.items = 0
			response = s.do(t, call{Method: http.MethodGet, Token: "alice", Target: list.target})
			if got := strings.TrimSpace(response.Body.String()); response.Code != http.StatusOK || got != `{"items":[]}` {
				t.Errorf("a page without items: %d %s, want an empty list", response.Code, got)
			}
		})
	}
}

// A token the contract lets through that is no token of the list is
// answered before any use case is asked, and the answer does not repeat it.
func TestAPageTokenThatIsNoneOfTheListIsRefused(t *testing.T) {
	position := ports.Position{At: createdAt, ID: productID}
	const detail = "The page token is not one of this list. Read the list again from its start."

	for index, list := range lists {
		another := lists[(index+1)%len(lists)]
		for name, token := range map[string]string{
			"words in base64":                  tokenOf("canary"),
			"a token of another list":          encodePageToken(another.list, position),
			"a token of a version to come":     tokenOf(`{"v":2,"k":"` + list.list + `","at":"2026-11-11T09:00:00.123456Z","id":"` + productID + `"}`),
			"a token whose identifier is none": tokenOf(`{"v":1,"k":"` + list.list + `","at":"2026-11-11T09:00:00.123456Z","id":"canary"}`),
			"a token cut short":                encodePageToken(list.list, position)[:40],
		} {
			s := newService(t)
			for _, target := range []string{list.target + "?page_token=" + token, list.target + "?page_size=5&page_token=" + token} {
				response := s.do(t, call{Method: http.MethodGet, Token: "alice", Target: target})
				wantProblem(t, response, http.StatusBadRequest, CodePageTokenInvalid, detail)
				if strings.Contains(response.Body.String(), token) || strings.Contains(s.log.String(), token) {
					t.Errorf("%s, %s: the token was written to the answer or to the log", list.name, name)
				}
			}
			if s.governance.calls() != 0 {
				t.Errorf("%s, %s: the request reached a use case", list.name, name)
			}
		}

		// The token of the list is taken, by this list and by no other.
		s := newService(t)
		good := encodePageToken(list.list, position)
		if response := s.do(t, call{Method: http.MethodGet, Token: "alice", Target: list.target + "?page_token=" + good}); response.Code != http.StatusOK {
			t.Errorf("%s: its own token is answered %d: %s", list.name, response.Code, response.Body)
		}
		if response := s.do(t, call{Method: http.MethodGet, Token: "alice", Target: another.target + "?page_token=" + good}); response.Code != http.StatusBadRequest {
			t.Errorf("%s: its token is answered %d by %s, want 400", list.name, response.Code, another.name)
		}
	}
}
