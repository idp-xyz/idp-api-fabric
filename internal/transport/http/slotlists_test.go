package http

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
	publication "go.idp.xyz/idp-api-fabric/internal/publication/application"
	"go.idp.xyz/idp-api-fabric/internal/publication/domain"
	publicationports "go.idp.xyz/idp-api-fabric/internal/publication/ports"
)

const (
	otherSlotID  = "8192a3b4-c5d6-4e7f-8a9b-0c1d2e3f4a5b"
	otherAssetID = "a3b4c5d6-e7f8-4a9b-8c0d-1e2f3a4b5c6d"
	otherTarget  = "b4c5d6e7-f8a9-4b0c-9d1e-2f3a4b5c6d7e"
	otherProduct = "c5d6e7f8-a9b0-4c1d-8e2f-3a4b5c6d7e8f"

	// slotsOfTarget is where the slots of the target are listed, and
	// publicationsOfAsset where the slots of the asset are.
	slotsOfTarget       = "/v1/products/" + productID + "/publication-targets/" + publicationTargetID + "/assets"
	publicationsOfAsset = "/v1/products/" + productID + "/assets/" + apiAssetID + "/publications"

	// highestIdentifier is the greatest identifier there is.
	highestIdentifier = "ffffffff-ffff-4fff-bfff-ffffffffffff"
)

// slotLists stands in for the use cases that read slots: the one that reads a
// slot and the two that list them. It answers what the test set and keeps
// what it was asked.
type slotLists struct {
	Publications

	slot domain.SlotView
	page publication.SlotPage
	err  error

	read         []domain.Request
	slots        []listedSlots
	publications []listedSlots
}

// listedSlots is what a use case that lists slots was asked.
type listedSlots struct {
	request domain.Request
	after   *publicationports.SlotPosition
	limit   int
}

func (p *slotLists) GetSlot(_ context.Context, request domain.Request) (domain.SlotView, error) {
	p.read = append(p.read, request)
	return p.slot, p.err
}

func (p *slotLists) ListSlots(_ context.Context, request domain.Request, after *publicationports.SlotPosition, limit int) (publication.SlotPage, error) {
	p.slots = append(p.slots, listedSlots{request, after, limit})
	return p.page, p.err
}

func (p *slotLists) ListAssetPublications(_ context.Context, request domain.Request, after *publicationports.SlotPosition, limit int) (publication.SlotPage, error) {
	p.publications = append(p.publications, listedSlots{request, after, limit})
	return p.page, p.err
}

func (p *slotLists) calls() int { return len(p.read) + len(p.slots) + len(p.publications) }

func newSlotListService(t *testing.T, fake *slotLists) *service {
	t.Helper()

	s := &service{governance: newGovernance(), log: &bytes.Buffer{}}
	handler, err := NewHandler(Config{
		Logger: slog.New(slog.NewTextHandler(s.log, nil)), Build: testBuild, Verifier: tokens{}, Governance: s.governance,
		Catalog: unusedCatalog{}, Registry: unusedRegistry{}, PolicyReview: unusedPolicyReview{},
		Releases: unusedReleases{}, Publication: fake, Audit: unusedAudit{},
	})
	if err != nil {
		t.Fatalf("build the handler: %v", err)
	}
	s.handler = handler
	return s
}

// slotList is a list of slots: where it is read, which list its tokens are
// of, which lists of other scopes it is not, what its use case is asked of,
// and where that is kept.
type slotList struct {
	name    string
	target  string
	list    string
	others  map[string]string
	request domain.Request
	asked   func(*slotLists) []listedSlots
}

var slotListsOfTheAPI = []slotList{
	{
		name: "the slots of a target", target: slotsOfTarget, list: "publication-slots/" + productID + "/" + publicationTargetID,
		others: map[string]string{
			"the same list of another target":  "publication-slots/" + productID + "/" + otherTarget,
			"the same list of another product": "publication-slots/" + otherProduct + "/" + publicationTargetID,
		},
		request: domain.Request{Caller: domain.Principal(alice), ProductID: productID, TargetID: publicationTargetID},
		asked:   func(l *slotLists) []listedSlots { return l.slots },
	},
	{
		name: "the publications of an asset", target: publicationsOfAsset, list: "asset-publications/" + productID + "/" + apiAssetID,
		others: map[string]string{
			"the same list of another asset":   "asset-publications/" + productID + "/" + otherAssetID,
			"the same list of another product": "asset-publications/" + otherProduct + "/" + apiAssetID,
		},
		request: domain.Request{Caller: domain.Principal(alice), ProductID: productID, AssetID: apiAssetID},
		asked:   func(l *slotLists) []listedSlots { return l.publications },
	},
}

// withdrawnSlot is the slot of another asset on the target, withdrawn at its
// second generation.
func withdrawnSlot() domain.SlotView {
	return domain.SlotView{Slot: domain.Slot{
		ID: otherSlotID, ProductID: productID, TargetID: publicationTargetID, AssetID: otherAssetID, Generation: 2,
	}}
}

// shownActive and shownWithdrawn are the two slots as the contract shows them.
func shownActive() map[string]any {
	return map[string]any{
		"slot_id": publicationSlotID, "publication_target_id": publicationTargetID, "api_asset_id": apiAssetID,
		"active_release_id": releaseID, "generation": float64(3), "visibility": "ACTIVE", "blocked": false,
		"delivery_status": "UNKNOWN",
	}
}

func shownWithdrawn() map[string]any {
	return map[string]any{
		"slot_id": otherSlotID, "publication_target_id": publicationTargetID, "api_asset_id": otherAssetID,
		"generation": float64(2), "visibility": "WITHDRAWN", "blocked": false, "delivery_status": "UNKNOWN",
	}
}

// tokenAt is the token of a position in the list.
func tokenAt(list string, at time.Time, id string) string {
	return encodePageToken(list, governanceports.Position{At: at, ID: id})
}

// A list of slots hands what its request says to its use case: who asks, of
// which target or asset, how many slots and behind which position. It answers
// the slots as the read of one shows each, and the token of the page that
// follows for as long as one does.
func TestASlotListHandsItsRequestToItsUseCaseAndAnswersAPage(t *testing.T) {
	next := publicationports.SlotPosition{At: createdAt.Add(time.Minute), ID: otherSlotID}

	for _, list := range slotListsOfTheAPI {
		t.Run(list.name, func(t *testing.T) {
			asked := func(fake *slotLists) listedSlots {
				t.Helper()

				queries := list.asked(fake)
				if len(queries) != 1 || fake.calls() != 1 {
					t.Fatalf("the use case of the list was asked %d times of %d calls, want once and no other", len(queries), fake.calls())
				}
				query := queries[0]
				if !uuid.MatchString(query.request.CorrelationID) {
					t.Errorf("correlation identifier %q, want one the server generated", query.request.CorrelationID)
				}
				query.request.CorrelationID = ""
				return query
			}

			// A page of two slots behind a position, with another to follow.
			fake := &slotLists{page: publication.SlotPage{Items: []domain.SlotView{activeSlot(), withdrawnSlot()}, Next: &next}}
			response := newSlotListService(t, fake).do(t, call{
				Method: http.MethodGet, Token: "alice",
				Target: list.target + "?page_size=2&page_token=" + tokenAt(list.list, createdAt, publicationSlotID),
			})
			if response.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
			}
			if got := response.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control %q, want no-store", got)
			}
			wantBody := map[string]any{
				"items": []any{shownActive(), shownWithdrawn()}, "next_page_token": tokenAt(list.list, next.At, next.ID),
			}
			if got := members(t, response); !reflect.DeepEqual(got, wantBody) {
				t.Errorf("body\n %v\nwant\n %v", got, wantBody)
			}
			behind := &publicationports.SlotPosition{At: createdAt, ID: publicationSlotID}
			if got := asked(fake); got.limit != 2 || !reflect.DeepEqual(got.after, behind) || got.request != list.request {
				t.Errorf("the use case was asked %+v behind %+v for %d slots, want %+v behind %+v for 2", got.request, got.after, got.limit, list.request, behind)
			}

			// Without a size and a token: the default size, from the start.
			// The last page carries no token.
			fake = &slotLists{page: publication.SlotPage{Items: []domain.SlotView{activeSlot()}}}
			response = newSlotListService(t, fake).do(t, call{Method: http.MethodGet, Token: "alice", Target: list.target})
			if got := members(t, response); response.Code != http.StatusOK ||
				!reflect.DeepEqual(got, map[string]any{"items": []any{shownActive()}}) {
				t.Errorf("without a size and a token: %d %s, want one slot and no token", response.Code, response.Body)
			}
			if got := asked(fake); got.limit != 50 || got.after != nil || got.request != list.request {
				t.Errorf("without a size and a token the use case was asked %+v behind %+v for %d slots, want the default of 50 from the start",
					got.request, got.after, got.limit)
			}

			// The bounds of the size are sizes like any other, and what is
			// beyond them never reaches the use case.
			for size, limit := range map[string]int{"1": 1, "100": 100} {
				fake = &slotLists{}
				if response := newSlotListService(t, fake).do(t, call{Method: http.MethodGet, Token: "alice", Target: list.target + "?page_size=" + size}); response.Code != http.StatusOK {
					t.Errorf("a page of %s: status %d, want 200: %s", size, response.Code, response.Body)
				}
				if got := asked(fake); got.limit != limit {
					t.Errorf("a page of %s: the use case was asked for %d slots", size, got.limit)
				}
			}
			for _, size := range []string{"0", "101", "-1", "many"} {
				fake = &slotLists{}
				wantProblem(t, newSlotListService(t, fake).do(t, call{Method: http.MethodGet, Token: "alice", Target: list.target + "?page_size=" + size}),
					http.StatusBadRequest, CodeRequestInvalid, "The query parameter page_size does not match the contract.")
				if fake.calls() != 0 {
					t.Errorf("a page of %s reached the use case", size)
				}
			}
			// A page and nothing else is asked for: no filter is declared.
			fake = &slotLists{}
			wantProblem(t, newSlotListService(t, fake).do(t, call{Method: http.MethodGet, Token: "alice", Target: list.target + "?visibility=ACTIVE"}),
				http.StatusBadRequest, CodeRequestInvalid, "The request has a query parameter the operation does not declare.")
			if fake.calls() != 0 {
				t.Error("a request with a filter reached the use case")
			}

			// A page without slots is a list that is empty, not one that is
			// missing.
			response = newSlotListService(t, &slotLists{}).do(t, call{Method: http.MethodGet, Token: "alice", Target: list.target})
			if got := strings.TrimSpace(response.Body.String()); response.Code != http.StatusOK || got != `{"items":[]}` {
				t.Errorf("a page without slots: %d %s, want an empty list", response.Code, got)
			}
		})
	}
}

// The validator fills in the default size of a page before a handler is
// reached, so no request through the router shows what a handler does when it
// is not told one. The handlers are called here as if the validator had not
// run, and a list that names no size is asked of its use case for the default
// page, from the start. The size is the one constant of the handlers, and the
// contract declares it as the default of the size of a page of each list: the
// two are held together here, so that neither can be changed, or the fallback
// dropped, without the other.
func TestASlotListThatNamesNoPageSizeIsAskedForTheDefaultPageOfTheContract(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatalf("load the contract: %v", err)
	}
	ctx := context.WithValue(context.Background(), stateKey{}, &requestState{correlationID: "correlation-1", principal: alice, authenticated: true})

	for _, c := range []struct {
		list slotList
		path string
		read func(operations) error
	}{
		{slotListsOfTheAPI[0], "/v1/products/{product_id}/publication-targets/{publication_target_id}/assets", func(o operations) error {
			_, err := o.ListPublicationSlots(ctx, ListPublicationSlotsRequestObject{ProductID: productID, PublicationTargetID: publicationTargetID})
			return err
		}},
		{slotListsOfTheAPI[1], "/v1/products/{product_id}/assets/{api_asset_id}/publications", func(o operations) error {
			_, err := o.ListAssetPublications(ctx, ListAssetPublicationsRequestObject{ProductID: productID, APIAssetID: apiAssetID})
			return err
		}},
	} {
		t.Run(c.list.name, func(t *testing.T) {
			fake := &slotLists{}
			if err := c.read(operations{publication: fake}); err != nil {
				t.Fatalf("list the slots without a page size: %v", err)
			}
			queries := c.list.asked(fake)
			if len(queries) != 1 || fake.calls() != 1 {
				t.Fatalf("the use case of the list was asked %d times of %d calls, want once and no other", len(queries), fake.calls())
			}
			if got := queries[0]; got.limit != defaultSlotPage || got.after != nil {
				t.Errorf("the use case was asked for %d slots behind %+v, want the default page of %d from the start", got.limit, got.after, defaultSlotPage)
			}

			var declared any
			for _, parameter := range contract.Paths.Find(c.path).Get.Parameters {
				if parameter.Value.Name == "page_size" {
					declared = parameter.Value.Schema.Value.Default
				}
			}
			if fmt.Sprint(declared) != fmt.Sprint(defaultSlotPage) {
				t.Errorf("the contract declares the default size of a page of this list as %v, the handlers take %d", declared, defaultSlotPage)
			}
		})
	}
}

// What a slot says of itself it says the same wherever it is read: the slot
// that is read alone, and the same slot in the list of its target and in the
// list of its asset, are shown by one function.
func TestAListedSlotIsShownAsTheSlotIsRead(t *testing.T) {
	blocked := activeSlot()
	blocked.Blocked = true
	empty := domain.SlotView{Slot: domain.Slot{ProductID: productID, TargetID: publicationTargetID, AssetID: apiAssetID}}
	states := map[string]domain.SlotView{
		"an active release": activeSlot(), "a blocked slot": blocked, "a withdrawn slot": withdrawnSlot(),
		"a slot that took no operation": empty,
	}

	read := call{Method: http.MethodGet, Target: publicationSlot, Token: alice.Subject}
	for name, slot := range states {
		t.Run(name, func(t *testing.T) {
			s := newSlotListService(t, &slotLists{slot: slot, page: publication.SlotPage{Items: []domain.SlotView{slot}}})
			single := s.do(t, read)
			if single.Code != http.StatusOK {
				t.Fatalf("the slot read alone: %d %s", single.Code, single.Body)
			}
			for _, list := range slotListsOfTheAPI {
				listed := s.do(t, call{Method: http.MethodGet, Target: list.target, Token: alice.Subject})
				items, _ := members(t, listed)["items"].([]any)
				if listed.Code != http.StatusOK || len(items) != 1 || !reflect.DeepEqual(items[0], members(t, single)) {
					t.Errorf("%s: %d %v, want the slot as it is read alone: %v", list.name, listed.Code, items, members(t, single))
				}
			}
		})
	}
}

// A token names a position in one list of slots: the slots of one target, or
// the publications of one asset, of one product. It is no token of another
// list, of another target or asset, of another product, or of the lists of
// targets and of assets, and it is refused before any use case is asked.
func TestAPageTokenOfAnotherListOrScopeIsRefused(t *testing.T) {
	const detail = "The page token is not one of this list. Read the list again from its start."

	for index, list := range slotListsOfTheAPI {
		another := slotListsOfTheAPI[(index+1)%len(slotListsOfTheAPI)]
		scopes := map[string]string{
			"the other list of slots": another.list,
			"the list of targets":     "publication-targets/" + productID,
			"the list of assets":      listOfAssets,
		}
		for name, scope := range list.others {
			scopes[name] = scope
		}
		for name, scope := range scopes {
			token := tokenAt(scope, createdAt, publicationSlotID)
			fake := &slotLists{}
			s := newSlotListService(t, fake)
			response := s.do(t, call{Method: http.MethodGet, Token: "alice", Target: list.target + "?page_token=" + token})
			wantProblem(t, response, http.StatusBadRequest, CodePageTokenInvalid, detail)
			if strings.Contains(response.Body.String(), token) || strings.Contains(s.log.String(), token) {
				t.Errorf("%s, a token of %s: the token was written to the answer or to the log", list.name, name)
			}
			if fake.calls() != 0 {
				t.Errorf("%s, a token of %s: the request reached a use case", list.name, name)
			}
		}

		// The token of the list is taken, by this list and by no other.
		good := tokenAt(list.list, createdAt, publicationSlotID)
		if response := newSlotListService(t, &slotLists{}).do(t, call{Method: http.MethodGet, Token: "alice", Target: list.target + "?page_token=" + good}); response.Code != http.StatusOK {
			t.Errorf("%s: its own token is answered %d: %s", list.name, response.Code, response.Body)
		}
		if response := newSlotListService(t, &slotLists{}).do(t, call{Method: http.MethodGet, Token: "alice", Target: another.target + "?page_token=" + good}); response.Code != http.StatusBadRequest {
			t.Errorf("%s: its token is answered %d by %s, want 400", list.name, response.Code, another.name)
		}
	}
}

// What a page hands out as the token of the next page is a string the
// contract takes back, also for the longest position a list can have.
func TestThePageTokenOfASlotListIsOneTheContractTakes(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatalf("load the contract: %v", err)
	}
	taken := contract.Components.Parameters["PageToken"].Value.Schema.Value
	declared := regexp.MustCompile(taken.Pattern)
	latest := governanceports.Position{At: time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC), ID: highestIdentifier}
	for _, list := range slotListsOfTheAPI {
		token := encodePageToken(list.list, latest)
		if !declared.MatchString(token) || uint64(len(token)) < taken.MinLength || uint64(len(token)) > *taken.MaxLength {
			t.Errorf("%s: the token %q of %d characters is not one the contract takes", list.name, token, len(token))
		}
		if got, err := decodePageToken(list.list, token); err != nil || !got.At.Equal(latest.At) || got.ID != latest.ID {
			t.Errorf("%s: the token names %+v (%v), want %+v", list.name, got, err, latest)
		}
	}
}

// A list of slots is read by a caller that has been told apart from the
// others: without one, or with a token nobody accepts, it is refused before
// any use case is asked.
func TestTheSlotListsNeedAVerifiedCaller(t *testing.T) {
	for _, list := range slotListsOfTheAPI {
		for name, c := range map[string]struct {
			token, challenge, detail string
			status                   int
			code                     string
		}{
			"no credentials":         {"", "Bearer", "", http.StatusUnauthorized, CodeCredentialsMissing},
			"a token nobody accepts": {"mallory", `Bearer error="invalid_token"`, "", http.StatusUnauthorized, CodeTokenInvalid},
			"a token that cannot be judged now": {"down", "", "The token could not be verified at the moment. It may be valid.",
				http.StatusServiceUnavailable, CodeIdentityProviderUnavailable},
		} {
			fake := &slotLists{}
			recorder := newSlotListService(t, fake).do(t, call{Method: http.MethodGet, Token: c.token, Target: list.target})
			wantProblem(t, recorder, c.status, c.code, c.detail)
			if got := recorder.Header().Get("WWW-Authenticate"); got != c.challenge {
				t.Errorf("%s, %s: WWW-Authenticate %q, want %q", list.name, name, got, c.challenge)
			}
			if fake.calls() != 0 {
				t.Errorf("%s, %s: the request reached a use case", list.name, name)
			}
		}
	}
}

// The contract shows the list of slots as it shows the slot: every item is a
// PublicationSlot, whatever else the slot says is said in every place it is
// read. A list takes a page and nothing else.
func TestTheContractListsSlotsAsItShowsOne(t *testing.T) {
	contract, err := loadContract()
	if err != nil {
		t.Fatal(err)
	}
	list := contract.Components.Schemas["PublicationSlotList"].Value
	items := list.Properties["items"]
	if items == nil || items.Value.Items.Ref != "#/components/schemas/PublicationSlot" || items.Value.MaxItems == nil || *items.Value.MaxItems != 100 {
		t.Errorf("the items of a list of slots are not at most 100 of PublicationSlot: %+v", items)
	}
	if next := list.Properties["next_page_token"]; next == nil || next.Ref != "#/components/schemas/NextPageToken" {
		t.Errorf("a list of slots does not hand out the token of the page that follows as NextPageToken: %+v", next)
	}
	if !reflect.DeepEqual(list.Required, []string{"items"}) || len(list.Properties) != 2 {
		t.Errorf("a list of slots requires %v of %d members, want its items, and besides them the token", list.Required, len(list.Properties))
	}
	// A list carries what it names and nothing else.
	if extra := list.AdditionalProperties; extra.Has == nil || *extra.Has || extra.Schema != nil {
		t.Errorf("a list of slots may carry members the contract does not name: %+v", extra)
	}

	for path, parameters := range map[string][]string{
		"/v1/products/{product_id}/publication-targets/{publication_target_id}/assets": {"product_id", "publication_target_id", "page_size", "page_token"},
		"/v1/products/{product_id}/assets/{api_asset_id}/publications":                 {"product_id", "api_asset_id", "page_size", "page_token"},
	} {
		operation := contract.Paths.Find(path).Get
		var declared []string
		for _, parameter := range operation.Parameters {
			declared = append(declared, parameter.Value.Name)
		}
		if !reflect.DeepEqual(declared, parameters) {
			t.Errorf("%s declares %v, want %v: a list of slots takes a page and nothing else", path, declared, parameters)
		}
		ok := operation.Responses.Value("200").Value
		if ok.Content.Get("application/json").Schema.Ref != "#/components/schemas/PublicationSlotList" ||
			ok.Headers["Cache-Control"] == nil || !ok.Headers["Cache-Control"].Value.Required {
			t.Errorf("%s does not answer a PublicationSlotList that is not to be kept", path)
		}
		// Not to be kept is the one thing the contract lets the page say of it.
		if header := ok.Headers["Cache-Control"]; header != nil {
			if schema := header.Value.Schema; schema == nil || !schema.Value.Type.Is("string") || !reflect.DeepEqual(schema.Value.Enum, []any{"no-store"}) {
				t.Errorf("%s lets the page say of its keeping something other than no-store: %+v", path, schema)
			}
		}
		// What else is answered: a request that cannot be read, a caller who
		// is not known, one who may not read, and what is not there.
		for status, ref := range map[string]string{
			"400": "#/components/responses/Problem", "401": "#/components/responses/Unauthenticated",
			"403": "#/components/responses/Problem", "404": "#/components/responses/Problem",
		} {
			if answer := operation.Responses.Value(status); answer == nil || answer.Ref != ref {
				t.Errorf("%s declares %s as %+v, want %s", path, status, answer, ref)
			}
		}
		if answer := operation.Responses.Default(); answer == nil || answer.Ref != "#/components/responses/Problem" {
			t.Errorf("%s declares anything else as %+v, want #/components/responses/Problem", path, answer)
		}
		for _, status := range []string{"409", "412", "422", "503"} {
			if operation.Responses.Value(status) != nil {
				t.Errorf("%s declares %s, which a read does not answer", path, status)
			}
		}
	}
}
