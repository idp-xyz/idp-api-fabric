package http

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	catalog "go.idp.xyz/idp-api-fabric/internal/apicatalog/application"
	catalogdomain "go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	catalogports "go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
	registrydomain "go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	governanceports "go.idp.xyz/idp-api-fabric/internal/productgovernance/ports"
)

const (
	assetsOfProduct = "/v1/products/" + productID + "/assets"
	assetRead       = assetsOfProduct + "/" + apiAssetID

	secondAssetID   = "8e9f0a1b-2c3d-4e4f-8a5b-6c7d8e9f0a1b"
	thirdAssetID    = "9f0a1b2c-3d4e-4f50-9b6c-7d8e9f0a1b2c"
	earlierRevision = "5e6f7081-92a3-4b4c-8d5e-6f708192a3b4"
)

// theContract is the contract this build carries, loaded once.
var theContract = sync.OnceValues(loadContract)

// schemaOf is a schema of the components of the contract.
func schemaOf(t *testing.T, name string) *openapi3.Schema {
	t.Helper()

	contract, err := theContract()
	if err != nil {
		t.Fatal(err)
	}
	schema := contract.Components.Schemas[name]
	if schema == nil || schema.Value == nil {
		t.Fatalf("the contract has no schema %s", name)
	}
	return schema.Value
}

// memberOf is a member of a schema of the contract.
func memberOf(t *testing.T, schema, member string) *openapi3.SchemaRef {
	t.Helper()

	declared := schemaOf(t, schema).Properties[member]
	if declared == nil || declared.Value == nil {
		t.Fatalf("the schema %s has no member %s", schema, member)
	}
	return declared
}

// shape is what a schema says of the values it accepts, and not what it says
// to explain them.
type shape struct {
	Types     []string
	Format    string
	Enum      []any
	Pattern   string
	Min, Max  *float64
	MinLength uint64
	MaxLength *uint64
	Const     any
}

func shapeOf(declared *openapi3.SchemaRef) shape {
	value := declared.Value
	var types []string
	if value.Type != nil {
		types = value.Type.Slice()
	}
	return shape{
		Types: types, Format: value.Format, Enum: value.Enum, Pattern: value.Pattern, Min: value.Min, Max: value.Max,
		MinLength: value.MinLength, MaxLength: value.MaxLength, Const: value.Const,
	}
}

// What an overview shows of a revision, a check and a release is what the
// reads of them show, under the names they show it by: a client that reads an
// overview reads each member as it reads the same member of the revision, of
// the check or of the release itself.
func TestEveryMemberOfAnOverviewIsDeclaredAsTheMemberItIsTakenFrom(t *testing.T) {
	for _, c := range []struct{ schema, member, from string }{
		{"OverviewRevision", "revision_id", "ContractRevision"},
		{"OverviewRevision", "revision_sequence", "ContractRevision"},
		{"OverviewRevision", "status", "ContractRevision"},
		{"OverviewRevision", "created_at", "ContractRevision"},
		{"OverviewCheck", "check_run_id", "CheckRunOverview"},
		{"OverviewCheck", "status", "CheckRunOverview"},
		{"OverviewCheck", "completed_at", "CheckRunOverview"},
		{"OverviewRelease", "release_id", "ContractRelease"},
		{"OverviewRelease", "revision_id", "ContractRelease"},
		{"OverviewRelease", "version", "ContractRelease"},
		{"OverviewRelease", "prepared_at", "ContractRelease"},
	} {
		got, want := shapeOf(memberOf(t, c.schema, c.member)), shapeOf(memberOf(t, c.from, c.member))
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s.%s is declared as\n %+v\nbut %s.%s as\n %+v", c.schema, c.member, got, c.from, c.member, want)
		}
	}
}

// The words the Catalog has for the status of a check and of a revision are the
// words the contract has, and the overview of an asset is closed: it has the
// members it has and says so, so that what is not there is not guessed at.
func TestTheWordsAndTheMembersOfAnOverviewAreThoseTheContractDeclares(t *testing.T) {
	statuses := func(declared *openapi3.Schema) (words []string) {
		for _, word := range declared.Enum {
			words = append(words, word.(string))
		}
		return words
	}
	var checks []string
	for _, status := range catalogdomain.CheckStatuses() {
		checks = append(checks, string(status))
	}
	if got := statuses(schemaOf(t, "CheckRunStatus")); !slices.Equal(got, checks) {
		t.Errorf("a check concludes %v, the Catalog filters by %v", got, checks)
	}
	revisions := []string{
		registrydomain.RevisionDraft, registrydomain.RevisionInReview, registrydomain.RevisionApproved,
		registrydomain.RevisionRejected, registrydomain.RevisionWithdrawn,
	}
	if got := statuses(memberOf(t, "OverviewRevision", "status").Value); !slices.Equal(got, revisions) {
		t.Errorf("an overview shows the status of a revision as %v, a revision has %v", got, revisions)
	}

	closed := func(declared *openapi3.Schema) bool {
		return declared.AdditionalProperties.Has != nil && !*declared.AdditionalProperties.Has
	}
	names := func(declared *openapi3.Schema) []string {
		var named []string
		for name := range declared.Properties {
			named = append(named, name)
		}
		slices.Sort(named)
		return named
	}
	for _, name := range []string{"AssetOverview", "OverviewRevision", "OverviewCheck", "OverviewRelease"} {
		if !closed(schemaOf(t, name)) {
			t.Errorf("%s is open: it must say that it has no member but its own", name)
		}
	}
	for _, name := range []string{"OverviewRevision", "OverviewCheck", "OverviewRelease"} {
		declared := schemaOf(t, name)
		required := slices.Clone(declared.Required)
		slices.Sort(required)
		if !slices.Equal(required, names(declared)) {
			t.Errorf("%s requires %v, want every member it has: %v", name, required, names(declared))
		}
	}

	overview := schemaOf(t, "AssetOverview")
	if want := []string{"latest_check", "latest_release", "latest_revision", "release_pending"}; !slices.Equal(names(overview), want) {
		t.Errorf("an overview has the members %v, want %v", names(overview), want)
	}
	if !slices.Equal(overview.Required, []string{"release_pending"}) {
		t.Errorf("an overview requires %v, want release_pending alone: an asset may have no revision, no check and no release", overview.Required)
	}
	if got := shapeOf(memberOf(t, "AssetOverview", "release_pending")); !reflect.DeepEqual(got.Types, []string{"boolean"}) {
		t.Errorf("release_pending is declared as %+v, want a boolean", got)
	}
	for member, schema := range map[string]string{
		"latest_revision": "OverviewRevision", "latest_check": "OverviewCheck", "latest_release": "OverviewRelease",
	} {
		if got := memberOf(t, "AssetOverview", member).Ref; got != "#/components/schemas/"+schema {
			t.Errorf("%s is %q, want %s", member, got, schema)
		}
	}

	// An asset carries an overview or none: a caller that may not read one
	// reads the asset as it always did.
	asset := schemaOf(t, "APIAsset")
	if got := memberOf(t, "APIAsset", "overview").Ref; got != "#/components/schemas/AssetOverview" {
		t.Errorf("the overview of an asset is %q, want AssetOverview", got)
	}
	if slices.Contains(asset.Required, "overview") {
		t.Errorf("an asset requires an overview, which only a reader of its revisions has: %v", asset.Required)
	}
}

// The list of the assets of a product takes the two filters as the contract
// declares them, neither of them required, and may refuse a caller.
func TestTheFiltersOfTheListAreDeclaredAsOptionalAndTheListMayRefuse(t *testing.T) {
	contract, err := theContract()
	if err != nil {
		t.Fatal(err)
	}
	operation := contract.Paths.Find("/v1/products/{product_id}/assets").Get
	if operation == nil || operation.OperationID != "listAssets" {
		t.Fatalf("the assets of a product are listed by %+v", operation)
	}
	declared := map[string]*openapi3.Parameter{}
	for _, parameter := range operation.Parameters {
		declared[parameter.Value.Name] = parameter.Value
	}
	status, pending := declared["check_status"], declared["release_pending"]
	if status == nil || pending == nil {
		t.Fatalf("the list declares the parameters %v, want check_status and release_pending among them", slices.Collect(maps.Keys(declared)))
	}
	for _, parameter := range []*openapi3.Parameter{status, pending} {
		if parameter.In != openapi3.ParameterInQuery || parameter.Required {
			t.Errorf("%s is %+v, want an optional parameter of the query", parameter.Name, parameter)
		}
	}
	if got := status.Schema.Ref; got != "#/components/schemas/CheckRunStatus" {
		t.Errorf("check_status is %q, want the statuses a check concludes", got)
	}
	if got := shapeOf(pending.Schema); !reflect.DeepEqual(got, shape{Types: []string{"boolean"}, Const: true}) {
		t.Errorf("release_pending is declared as %+v, want the boolean true alone: there is no filter for the others", got)
	}
	if operation.Responses.Status(http.StatusForbidden) == nil {
		t.Error("the list does not declare 403, which a caller that reads no overview is answered when it filters")
	}
}

// assetCatalog stands in for the use cases of the Catalog the reads of an
// asset are made through. It keeps what it was asked and answers what the
// test set.
type assetCatalog struct {
	APICatalog
	view   catalog.AssetView
	page   catalog.Page
	err    error
	read   []catalog.GetAsset
	listed []catalog.ListAssets
}

func (c *assetCatalog) ReadAsset(_ context.Context, query catalog.GetAsset) (catalog.AssetView, error) {
	c.read = append(c.read, query)
	return c.view, c.err
}

func (c *assetCatalog) ListAssets(_ context.Context, query catalog.ListAssets) (catalog.Page, error) {
	c.listed = append(c.listed, query)
	return c.page, c.err
}

func newAssetService(t *testing.T, fake APICatalog) *service {
	t.Helper()

	s := &service{governance: newGovernance(), log: &bytes.Buffer{}}
	handler, err := NewHandler(Config{
		Logger: slog.New(slog.NewTextHandler(s.log, nil)), Build: testBuild, Verifier: tokens{}, Governance: s.governance,
		Catalog: fake, Registry: unusedRegistry{}, PolicyReview: unusedPolicyReview{},
		Releases: unusedReleases{}, Publication: unusedPublications{}, Audit: unusedAudit{},
	})
	if err != nil {
		t.Fatalf("build the handler: %v", err)
	}
	s.handler = handler
	return s
}

func assetOfTheCatalog(id string) catalogdomain.Asset {
	return catalogdomain.Asset{
		ID: catalogdomain.AssetID(id), ProductID: productID, WorkspaceID: workspaceID, Key: "payments", DisplayName: "Payments",
		DomainKey: "payments", Kind: catalogdomain.KindHTTP, Lifecycle: catalogdomain.Experimental, Version: 1,
		CreatedAt: createdAt, CreatedBy: catalogdomain.Principal(alice),
	}
}

func assetBody(id string, overview map[string]any) map[string]any {
	body := map[string]any{
		"api_asset_id": id, "product_id": productID, "workspace_id": workspaceID, "asset_key": "payments",
		"display_name": "Payments", "domain_key": "payments", "api_kind": "HTTP", "lifecycle": "EXPERIMENTAL",
		"created_at": stamp(0),
	}
	if overview != nil {
		body["overview"] = overview
	}
	return body
}

// stamp is the instant that many minutes after the one everything is created
// at, as the contract writes it.
func stamp(after time.Duration) string {
	return createdAt.Add(after * time.Minute).Format(time.RFC3339Nano)
}

// everything is an overview with every member: the last revision approved, and
// checked and failed, and a release prepared from an earlier revision.
func everything() catalogdomain.Overview {
	return catalogdomain.Overview{
		LatestRevision: &catalogdomain.OverviewRevision{
			ID: revisionID, Sequence: 3, Status: registrydomain.RevisionApproved, CreatedAt: createdAt.Add(10 * time.Minute),
		},
		LatestCheck: &catalogdomain.OverviewCheck{ID: checkRunID, Status: catalogdomain.CheckFail, CompletedAt: createdAt.Add(20 * time.Minute)},
		LatestRelease: &catalogdomain.OverviewRelease{
			ID: releaseID, RevisionID: earlierRevision, Version: "1.2.0", PreparedAt: createdAt.Add(30 * time.Minute),
		},
		ReleasePending: true,
	}
}

func everythingBody() map[string]any {
	return map[string]any{
		"latest_revision": map[string]any{"revision_id": revisionID, "revision_sequence": float64(3), "status": "APPROVED", "created_at": stamp(10)},
		"latest_check":    map[string]any{"check_run_id": checkRunID, "status": "FAIL", "completed_at": stamp(20)},
		"latest_release": map[string]any{
			"release_id": releaseID, "revision_id": earlierRevision, "version": "1.2.0", "prepared_at": stamp(30),
		},
		"release_pending": true,
	}
}

// An asset is shown with the overview the use case gave it, every member as
// the use case gave it and no member it did not give: a revision with no
// check shows none, an asset with no release shows none, and an asset with no
// revision shows only that nothing is pending. An asset the use case gave no
// overview is shown as it was shown before overviews were.
func TestAnAssetIsShownWithTheOverviewTheUseCaseGaveItAndWithoutOneIfItGaveNone(t *testing.T) {
	revised := everything()
	revised.LatestCheck, revised.LatestRelease, revised.ReleasePending = nil, nil, false
	released := everything()
	released.LatestCheck, released.ReleasePending = nil, false
	for name, c := range map[string]struct {
		overview *catalogdomain.Overview
		want     map[string]any
	}{
		"every member": {ptr(everything()), everythingBody()},
		"a revision that has no check and no release": {&revised, map[string]any{
			"latest_revision": everythingBody()["latest_revision"], "release_pending": false,
		}},
		"a release and a revision, and no check": {&released, map[string]any{
			"latest_revision": everythingBody()["latest_revision"], "latest_release": everythingBody()["latest_release"], "release_pending": false,
		}},
		"no revision": {&catalogdomain.Overview{}, map[string]any{"release_pending": false}},
		"no overview": {nil, nil},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &assetCatalog{view: catalog.AssetView{Asset: assetOfTheCatalog(apiAssetID), Overview: c.overview}}
			response := newAssetService(t, fake).do(t, call{Method: http.MethodGet, Target: assetRead, Token: alice.Subject})

			if response.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
			}
			if got, want := members(t, response), assetBody(apiAssetID, c.want); !reflect.DeepEqual(got, want) {
				t.Errorf("the asset is shown as\n %v\nwant\n %v", got, want)
			}
			if len(fake.read) != 1 || fake.read[0].AssetID != apiAssetID || fake.read[0].ProductID != productID ||
				fake.read[0].Caller != catalogdomain.Principal(alice) {
				t.Errorf("the use case was asked %+v, want one read of the asset by alice", fake.read)
			}
		})
	}
}

func ptr[T any](value T) *T { return &value }

// positionOf is a position of the list as its token holds it.
func positionOf(position catalogports.Position) governanceports.Position {
	return governanceports.Position{At: position.At, ID: string(position.ID)}
}

// A page shows each asset as the single read does: the one that has an
// overview with it and the one that has none without, in the order the use
// case gave them, and the token of the page that follows when there is one.
func TestAListShowsEachAssetWithTheOverviewItHasIfItHasOne(t *testing.T) {
	pending := catalogdomain.Overview{ReleasePending: false}
	next := catalogports.Position{At: createdAt.Add(time.Minute), ID: thirdAssetID}
	fake := &assetCatalog{page: catalog.Page{
		Items: []catalog.AssetView{
			{Asset: assetOfTheCatalog(apiAssetID), Overview: ptr(everything())},
			{Asset: assetOfTheCatalog(secondAssetID), Overview: &pending},
			{Asset: assetOfTheCatalog(thirdAssetID)},
		},
		Next: &next,
	}}
	response := newAssetService(t, fake).do(t, call{Method: http.MethodGet, Target: assetsOfProduct, Token: alice.Subject})

	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
	}
	want := map[string]any{
		"items": []any{
			assetBody(apiAssetID, everythingBody()),
			assetBody(secondAssetID, map[string]any{"release_pending": false}),
			assetBody(thirdAssetID, nil),
		},
		"next_page_token": encodePageToken(listOfAssets, positionOf(next)),
	}
	if got := members(t, response); !reflect.DeepEqual(got, want) {
		t.Errorf("the page is\n %v\nwant\n %v", got, want)
	}
}

// filtersOf is what a list was asked to keep.
type filtersOf struct {
	WorkspaceID, DomainKey, Query string
	Kind                          catalogdomain.Kind
	CheckStatus                   catalogdomain.CheckStatus
	ReleasePending                bool
	PageSize                      int
}

func askedFor(query catalog.ListAssets) filtersOf {
	return filtersOf{
		WorkspaceID: string(query.WorkspaceID), DomainKey: query.DomainKey, Query: query.Query, Kind: query.Kind,
		CheckStatus: query.CheckStatus, ReleasePending: query.ReleasePending, PageSize: query.PageSize,
	}
}

// What a request filters by is what the use case is asked to keep, as it was
// sent: the status a check concluded, the pending release, and the other
// filters with them, and nothing where the request says nothing.
func TestTheFiltersOfAListReachTheUseCaseAsTheyWereSent(t *testing.T) {
	const anyOther = "&workspace_id=" + workspaceID + "&api_kind=EVENT&domain_key=payments&q=pay&page_size=7"
	otherFilters := filtersOf{WorkspaceID: workspaceID, Kind: catalogdomain.KindEvent, DomainKey: "payments", Query: "pay", PageSize: 7}
	failed, pending := otherFilters, otherFilters
	failed.CheckStatus = catalogdomain.CheckFail
	pending.ReleasePending = true
	both := failed
	both.ReleasePending = true

	for name, c := range map[string]struct {
		query string
		want  filtersOf
	}{
		"none":                              {"", filtersOf{PageSize: application.DefaultPageSize}},
		"a check that passed":               {"?check_status=PASS", filtersOf{CheckStatus: catalogdomain.CheckPass, PageSize: application.DefaultPageSize}},
		"a check that warned":               {"?check_status=WARN", filtersOf{CheckStatus: catalogdomain.CheckWarn, PageSize: application.DefaultPageSize}},
		"a check that failed":               {"?check_status=FAIL", filtersOf{CheckStatus: catalogdomain.CheckFail, PageSize: application.DefaultPageSize}},
		"a check that was unsure":           {"?check_status=UNKNOWN", filtersOf{CheckStatus: catalogdomain.CheckUnknown, PageSize: application.DefaultPageSize}},
		"a release pending":                 {"?release_pending=true", filtersOf{ReleasePending: true, PageSize: application.DefaultPageSize}},
		"the others and no overview filter": {"?" + anyOther[1:], otherFilters},
		"the others and a failed check":     {"?check_status=FAIL" + anyOther, failed},
		"the others and a release pending":  {"?release_pending=true" + anyOther, pending},
		"both of them and the others":       {"?release_pending=true&check_status=FAIL" + anyOther, both},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &assetCatalog{}
			response := newAssetService(t, fake).do(t, call{Method: http.MethodGet, Target: assetsOfProduct + c.query, Token: alice.Subject})

			if response.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
			}
			if len(fake.listed) != 1 || !reflect.DeepEqual(askedFor(fake.listed[0]), c.want) {
				t.Errorf("the use case was asked %+v, want once %+v", fake.listed, c.want)
			}
			if body := members(t, response); !reflect.DeepEqual(body, map[string]any{"items": []any{}}) {
				t.Errorf("the page is %v, want no item and no token", body)
			}
		})
	}
}

// A page token the filtered list answered is followed with the same filters,
// from the position the token holds.
func TestAFilteredListIsFollowedFromTheTokenWithTheFiltersItWasSentWith(t *testing.T) {
	behind := catalogports.Position{At: createdAt.Add(5 * time.Minute), ID: secondAssetID}
	fake := &assetCatalog{}
	target := assetsOfProduct + "?check_status=UNKNOWN&release_pending=true&page_size=2&page_token=" + encodePageToken(listOfAssets, positionOf(behind))
	response := newAssetService(t, fake).do(t, call{Method: http.MethodGet, Target: target, Token: alice.Subject})

	if response.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", response.Code, response.Body)
	}
	if len(fake.listed) != 1 {
		t.Fatalf("the use case was asked %d times, want once", len(fake.listed))
	}
	if got, want := askedFor(fake.listed[0]), (filtersOf{CheckStatus: catalogdomain.CheckUnknown, ReleasePending: true, PageSize: 2}); got != want {
		t.Errorf("the use case was asked %+v, want %+v", got, want)
	}
	if got := fake.listed[0].After; got == nil || !got.At.Equal(behind.At) || got.ID != behind.ID {
		t.Errorf("the list starts behind %+v, want %+v", got, behind)
	}
}

// A request that filters by what the contract does not have is refused, and
// the use case is not asked: neither a status no check concludes nor the
// absence of a status, neither the other of the two values of a pending
// release, which has no filter, nor a filter of another read.
func TestAFilterTheContractDoesNotHaveIsRefusedBeforeTheUseCaseIsAsked(t *testing.T) {
	for name, target := range map[string]string{
		"a status of no check":                       assetsOfProduct + "?check_status=NOT_RUN",
		"a status in lower case":                     assetsOfProduct + "?check_status=fail",
		"a status of a revision":                     assetsOfProduct + "?check_status=APPROVED",
		"no status":                                  assetsOfProduct + "?check_status=",
		"the releases that are not pending":          assetsOfProduct + "?release_pending=false",
		"a word for a pending release":               assetsOfProduct + "?release_pending=yes",
		"a pending release twice":                    assetsOfProduct + "?release_pending=true&release_pending=true",
		"two statuses":                               assetsOfProduct + "?check_status=FAIL&check_status=PASS",
		"no value for a pending release":             assetsOfProduct + "?release_pending=",
		"a filter the list has not":                  assetsOfProduct + "?overview=true",
		"a filter of the asset read":                 assetsOfProduct + "?latest_check=FAIL",
		"a status for the read of an asset":          assetRead + "?check_status=FAIL",
		"a pending release for the read of an asset": assetRead + "?release_pending=true",
	} {
		t.Run(name, func(t *testing.T) {
			fake := &assetCatalog{}
			wantRefusal(t, newAssetService(t, fake).do(t, call{Method: http.MethodGet, Target: target, Token: alice.Subject}),
				http.StatusBadRequest, CodeRequestInvalid)

			if len(fake.listed)+len(fake.read) != 0 {
				t.Errorf("the use case was asked %+v %+v of a request the contract refuses", fake.listed, fake.read)
			}
		})
	}
}

// A caller that may not read what a filter asks about is refused as the use
// case says, and told no more than that: not an empty page, which would say
// what the filter keeps.
func TestAListTheUseCaseRefusesIsRefusedAndShowsNoAsset(t *testing.T) {
	fake := &assetCatalog{err: catalog.ErrForbidden, page: catalog.Page{Items: []catalog.AssetView{{Asset: assetOfTheCatalog(apiAssetID)}}}}
	response := newAssetService(t, fake).do(t, call{Method: http.MethodGet, Target: assetsOfProduct + "?release_pending=true", Token: alice.Subject})

	wantProblem(t, response, http.StatusForbidden, CodeForbidden, "The caller may not perform this action.")
	if len(fake.listed) != 1 {
		t.Errorf("the use case was asked %d times, want once", len(fake.listed))
	}
}

// A Catalog whose answers to the rounds of a filtered list were out of the
// order of the list, or from another place than the rounds had read to, is a
// fault of the service and says nothing of itself to the caller: the answer is
// the internal error of any fault, with no member that tells the cause, and the
// log has the cause, with the identifier of the request. The failure is no
// refusal, so it is not a 403, and the use case is asked the once.
func TestAFilteredListThatTheCatalogAnsweredOutOfOrderIsAnInternalErrorThatSaysNothing(t *testing.T) {
	cause := fmt.Errorf("%w: the asset marker-of-the-cause, created at 2026-11-11T09:00:00Z, does not come after another", catalog.ErrAssetsOutOfOrder)
	for name, target := range map[string]string{
		"a release pending":       assetsOfProduct + "?release_pending=true",
		"a status":                assetsOfProduct + "?check_status=FAIL",
		"both, from a page token": assetsOfProduct + "?release_pending=true&check_status=UNKNOWN&page_size=3&page_token=" + encodePageToken(listOfAssets, governanceports.Position{At: createdAt, ID: secondAssetID}),
	} {
		fake := &assetCatalog{err: cause}
		s := newAssetService(t, fake)
		response := s.do(t, call{Method: http.MethodGet, Target: target, Token: alice.Subject})

		traceID := wantProblem(t, response, http.StatusInternalServerError, CodeInternalError, "")
		for _, told := range []string{"marker-of-the-cause", "order", "place", "Catalog"} {
			if strings.Contains(response.Body.String(), told) {
				t.Errorf("%s: the answer says %q of the cause: %s", name, told, response.Body)
			}
		}
		if !strings.Contains(s.log.String(), "marker-of-the-cause") || !strings.Contains(s.log.String(), "correlation_id="+traceID) {
			t.Errorf("%s: the cause must be logged with the identifier of the request, got %q", name, s.log.String())
		}
		if len(fake.listed) != 1 {
			t.Errorf("%s: the use case was asked %d times, want once", name, len(fake.listed))
		}
	}
}

// A word that the contract has not for the status of a revision or of a check
// is a fault of the server and not something to send: the answer is the
// internal error of any fault, and shows nothing of the asset.
func TestAnOverviewWithAWordTheContractDoesNotHaveIsNotSent(t *testing.T) {
	badRevision, badCheck := everything(), everything()
	badRevision.LatestRevision.Status = "ARCHIVED"
	badCheck.LatestCheck.Status = "NOT_RUN"
	for name, overview := range map[string]catalogdomain.Overview{"a revision": badRevision, "a check": badCheck} {
		view := catalog.AssetView{Asset: assetOfTheCatalog(apiAssetID), Overview: &overview}
		wantProblem(t, newAssetService(t, &assetCatalog{view: view}).do(t, call{Method: http.MethodGet, Target: assetRead, Token: alice.Subject}),
			http.StatusInternalServerError, CodeInternalError, "")

		listed := newAssetService(t, &assetCatalog{page: catalog.Page{Items: []catalog.AssetView{view}}}).do(t,
			call{Method: http.MethodGet, Target: assetsOfProduct, Token: alice.Subject})
		if listed.Code != http.StatusInternalServerError {
			t.Errorf("a page with %s of a word the contract has not: status %d, want 500: %s", name, listed.Code, listed.Body)
		}
	}
}
