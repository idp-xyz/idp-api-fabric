package application

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	"go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
)

const (
	financeWorkspace = domain.WorkspaceID("b0000000-0000-4000-8000-000000000003")
)

var caller = domain.Principal{Issuer: "https://issuer.example", Subject: "alice"}

func assetNumber(n int) domain.AssetID {
	return domain.AssetID(fmt.Sprintf("a0000000-0000-4000-8000-%012d", n))
}

// assetsOf makes the assets 1 to count of the product, oldest first, each
// owned by the workspace the function says.
func assetsOf(count int, workspace func(n int) domain.WorkspaceID) []domain.Asset {
	assets := make([]domain.Asset, 0, count)
	for n := 1; n <= count; n++ {
		assets = append(assets, domain.Asset{
			ID: assetNumber(n), ProductID: someProduct, WorkspaceID: workspace(n), Key: fmt.Sprintf("asset-%d", n),
			DisplayName: fmt.Sprintf("Asset %d", n), DomainKey: "shipping", Kind: domain.KindHTTP, Lifecycle: domain.Experimental,
			Version: 1, CreatedAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Second),
		})
	}
	return assets
}

func inWorkspace(workspace domain.WorkspaceID) func(int) domain.WorkspaceID {
	return func(int) domain.WorkspaceID { return workspace }
}

// scopes stands in for what Product Governance says of the caller.
type scopes struct {
	ports.Scopes
	scope ports.ProductScope
	read  int
}

func (s *scopes) Product(context.Context, domain.ProductID, domain.Principal) (ports.ProductScope, error) {
	s.read++
	return s.scope, nil
}

// askedAtMost is how many times a fake of this file answers before it stops
// the test that asks it. A test asks a fake a few times, and a list that is
// read page by page a few hundred; a loop that does not stop asks without end,
// and a fake keeps what it was asked, so that a loop that was broken once used
// all the memory of the machine and restarted it. Ten thousand questions are
// a few hundred megabytes at the most, and the test fails where it is asked
// the next.
const askedAtMost = 10000

// reporter is what a fake needs of a test to stop it: a *testing.T is one.
type reporter interface {
	Helper()
	Fatalf(format string, args ...any)
}

// counted counts the questions a fake is asked, and stops the test at the one
// after askedAtMost: the test fails there, and goes no further.
type counted struct {
	test      reporter
	questions int
}

func (c *counted) count(of string) {
	c.questions++
	if c.questions <= askedAtMost {
		return
	}
	if c.test == nil {
		panic(fmt.Sprintf("%s was asked more than %d times", of, askedAtMost))
	}
	c.test.Helper()
	c.test.Fatalf("%s was asked more than %d times: a loop does not stop, and the test is stopped here", of, askedAtMost)
}

// assetStore stands in for the assets the Catalog keeps: it answers as the
// statement of the real one does, from the assets it was given, which are
// those of one product, and keeps what it was asked.
type assetStore struct {
	ports.Assets
	counted
	assets  []domain.Asset
	asked   []ports.Selection
	finds   int
	failure error
	// answering, if there is one, is what the Catalog answers to a list instead
	// of what its assets say: a Catalog that does not do what the real one does.
	answering func(ports.Selection) []domain.Asset
}

func (s *assetStore) Find(_ context.Context, product domain.ProductID, id domain.AssetID) (domain.Asset, bool, error) {
	s.finds++
	for _, asset := range s.assets {
		if asset.ProductID == product && asset.ID == id {
			return asset, true, s.failure
		}
	}
	return domain.Asset{}, false, s.failure
}

func (s *assetStore) List(_ context.Context, product domain.ProductID, selection ports.Selection) ([]domain.Asset, error) {
	s.count("the Catalog")
	asked := selection
	asked.Workspaces = slices.Clone(selection.Workspaces)
	s.asked = append(s.asked, asked)
	if s.failure != nil {
		return nil, s.failure
	}
	if s.answering != nil {
		return slices.Clone(s.answering(selection)), nil
	}
	var found []domain.Asset
	for _, asset := range s.assets {
		switch {
		case asset.ProductID != product,
			!selection.All && !slices.Contains(selection.Workspaces, asset.WorkspaceID),
			selection.Workspace != "" && asset.WorkspaceID != selection.Workspace,
			selection.Kind != "" && asset.Kind != selection.Kind,
			selection.DomainKey != "" && asset.DomainKey != selection.DomainKey,
			selection.After != nil && !(asset.CreatedAt.After(selection.After.At) ||
				asset.CreatedAt.Equal(selection.After.At) && asset.ID > selection.After.ID):
			continue
		}
		found = append(found, asset)
	}
	sort.Slice(found, func(i, j int) bool {
		if !found[i].CreatedAt.Equal(found[j].CreatedAt) {
			return found[i].CreatedAt.Before(found[j].CreatedAt)
		}
		return found[i].ID < found[j].ID
	})
	if len(found) > selection.Limit {
		found = found[:selection.Limit]
	}
	return found, nil
}

// overviewsOf stands in for what the other contexts record: it says of each
// asset what the test says, as the real one does, and keeps what it was
// asked.
type overviewsOf struct {
	ports.Overviews
	counted
	of func(domain.AssetID) (domain.Overview, bool) // false for an asset without a revision
	// silent leaves an asset without a revision out of what it says, instead
	// of saying that it has the empty overview.
	silent  bool
	asked   []asking
	failure error
}

type asking struct {
	product domain.ProductID
	places  []ports.AssetPlace
	filter  ports.OverviewFilter
}

func (o *overviewsOf) Of(_ context.Context, product domain.ProductID, places []ports.AssetPlace, filter ports.OverviewFilter) (map[domain.AssetID]domain.Overview, error) {
	o.count("the overviews")
	o.asked = append(o.asked, asking{product, slices.Clone(places), filter})
	if o.failure != nil {
		return nil, o.failure
	}
	found := make(map[domain.AssetID]domain.Overview, len(places))
	for _, place := range places {
		overview, has := o.of(place.ID)
		switch {
		case !filter.Any():
			if has || !o.silent {
				found[place.ID] = overview
			}
		case has && (filter.CheckStatus == "" || overview.LatestCheck != nil && overview.LatestCheck.Status == filter.CheckStatus) &&
			(!filter.ReleasePending || overview.ReleasePending):
			found[place.ID] = overview
		}
	}
	return found, nil
}

// pending is the overview of an asset whose last revision is approved and not
// released, and checked: the one every asset has to a test that does not
// care.
func pending(id domain.AssetID) (domain.Overview, bool) {
	return domain.Overview{
		LatestRevision: &domain.OverviewRevision{ID: "r-" + string(id), Sequence: 1, Status: "APPROVED"},
		LatestCheck:    &domain.OverviewCheck{ID: "c-" + string(id), Status: domain.CheckUnknown},
		ReleasePending: true,
	}, true
}

// never is what an overview says of an asset that nothing is asked for.
func never(domain.AssetID) (domain.Overview, bool) {
	return domain.Overview{LatestRevision: &domain.OverviewRevision{Status: "DRAFT"}}, true
}

// world is a Catalog over what is above, and what the test does to it.
type world struct {
	t         *testing.T
	scopes    *scopes
	assets    *assetStore
	overviews *overviewsOf
	audited   []ports.AuditEntry
	catalog   *Catalog
}

func newWorld(t *testing.T, scope ports.ProductScope, assets []domain.Asset, of func(domain.AssetID) (domain.Overview, bool)) *world {
	t.Helper()

	w := &world{
		t: t, scopes: &scopes{scope: scope}, assets: &assetStore{assets: assets, counted: counted{test: t}},
		overviews: &overviewsOf{of: of, counted: counted{test: t}},
	}
	scope.Exists, scope.Known = true, true
	w.scopes.scope = scope
	catalog, err := New(Dependencies{
		Transactions: unusedTransactions{}, Scopes: w.scopes, Assets: w.assets,
		Audit: func(_ context.Context, entry ports.AuditEntry) error {
			w.audited = append(w.audited, entry)
			return nil
		},
		Events: unusedEvents{}, Clock: unusedClock{}, Identifiers: unusedIdentifiers{}, Overviews: w.overviews,
	})
	if err != nil {
		t.Fatalf("wire the use cases: %v", err)
	}
	w.catalog = catalog
	return w
}

func (w *world) request() Request {
	return Request{Caller: caller, ProductID: someProduct, CorrelationID: "correlation-1"}
}

func (w *world) list(query ListAssets) (Page, error) {
	w.t.Helper()

	query.Request = w.request()
	return w.catalog.ListAssets(w.t.Context(), query)
}

// askedAbout are the assets the overviews were asked about, in the order they were asked.
func (w *world) askedAbout() []domain.AssetID {
	var named []domain.AssetID
	for _, asked := range w.overviews.asked {
		for _, place := range asked.places {
			named = append(named, place.ID)
		}
	}
	return named
}

func identifiersOf(views []AssetView) []domain.AssetID {
	named := make([]domain.AssetID, 0, len(views))
	for _, view := range views {
		named = append(named, view.Asset.ID)
	}
	return named
}

// The assets that are looked in are those of the Workspaces whose revisions the
// caller reads, and nothing is asked about any other asset: a Workspace the
// caller is a member of and reads no revisions in, and a Workspace it reads
// only by owning the product, are not looked in, and the question that is
// asked names the assets of the first alone. A Workspace that is asked for
// and is not one of those is looked in no more for it.
func TestAFilteredListLooksOnlyInTheWorkspacesTheCallerReadsRevisionsInAndAsksOnlyAboutTheirAssets(t *testing.T) {
	workspaces := []domain.WorkspaceID{engineering, operations, financeWorkspace}
	assets := assetsOf(30, func(n int) domain.WorkspaceID { return workspaces[n%3] })
	scope := ports.ProductScope{Owner: true, Workspaces: []domain.WorkspaceID{engineering, operations}, Readers: []domain.WorkspaceID{engineering}}

	for name, filter := range map[string]ListAssets{
		"a status":      {CheckStatus: domain.CheckUnknown},
		"a release":     {ReleasePending: true},
		"both":          {CheckStatus: domain.CheckUnknown, ReleasePending: true},
		"with a search": {ReleasePending: true, Query: "asset"},
	} {
		w := newWorld(t, scope, assets, pending)
		filter.PageSize = 100
		page, err := w.list(filter)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, view := range page.Items {
			if view.Asset.WorkspaceID != engineering || view.Overview == nil {
				t.Errorf("%s: the page holds %+v of the workspace %s, want only assets of engineering with their overview", name, view, view.Asset.WorkspaceID)
			}
		}
		if len(page.Items) != 10 {
			t.Errorf("%s: the page holds %d assets, want the ten of engineering", name, len(page.Items))
		}
		for _, asked := range w.assets.asked {
			if asked.All || !slices.Equal(asked.Workspaces, []domain.WorkspaceID{engineering}) {
				t.Errorf("%s: the Catalog was asked for assets of all workspaces %t, of %v; want only those of engineering", name, asked.All, asked.Workspaces)
			}
		}
		for _, asked := range w.overviews.asked {
			for _, place := range asked.places {
				if place.WorkspaceID != engineering {
					t.Errorf("%s: the overview of %s of the workspace %s was asked for", name, place.ID, place.WorkspaceID)
				}
			}
			if asked.product != someProduct {
				t.Errorf("%s: the overviews were asked of the product %s", name, asked.product)
			}
		}
	}

	// A workspace the caller reads no revisions in is not looked in because
	// the caller names it.
	for _, workspace := range []domain.WorkspaceID{operations, financeWorkspace} {
		w := newWorld(t, scope, assets, pending)
		page, err := w.list(ListAssets{ReleasePending: true, WorkspaceID: workspace})
		if err != nil || len(page.Items) != 0 || page.Next != nil || len(w.overviews.asked) != 0 {
			t.Errorf("asked for the workspace %s: %+v (%v) and %d questions, want an empty page and none asked", workspace, page, err, len(w.overviews.asked))
		}
	}
}

// A page that is not filtered holds the assets the caller reads, as it always
// did, and the assets of the Workspaces whose revisions the caller reads have
// where they stand: one question is asked of them, at most, and none if the
// page holds none of them, or if the caller reads the revisions of none.
func TestAnUnfilteredListAsksOnlyAboutTheAssetsOfTheWorkspacesTheCallerReadsRevisionsIn(t *testing.T) {
	workspaces := []domain.WorkspaceID{engineering, operations, financeWorkspace}
	assets := assetsOf(9, func(n int) domain.WorkspaceID { return workspaces[n%3] })
	ownsAndReads := ports.ProductScope{Owner: true, Workspaces: []domain.WorkspaceID{engineering, operations}, Readers: []domain.WorkspaceID{engineering}}

	w := newWorld(t, ownsAndReads, assets, pending)
	page, err := w.list(ListAssets{PageSize: 100})
	if err != nil || len(page.Items) != 9 {
		t.Fatalf("the page holds %d assets (%v), want the nine of the product", len(page.Items), err)
	}
	for _, view := range page.Items {
		reads := view.Asset.WorkspaceID == engineering
		if (view.Overview != nil) != reads {
			t.Errorf("the asset %s of %s has the overview %+v, want one only for the assets of engineering", view.Asset.ID, view.Asset.WorkspaceID, view.Overview)
		}
	}
	if len(w.overviews.asked) != 1 || w.overviews.asked[0].filter.Any() || len(w.overviews.asked[0].places) != 3 {
		t.Errorf("the overviews were asked %+v, want one question without a filter about the three assets of engineering", w.overviews.asked)
	}
	if len(w.assets.asked) != 1 || !w.assets.asked[0].All || w.assets.asked[0].Limit != 101 {
		t.Errorf("the Catalog was asked %+v, want one list of what the owner reads, a page and one more", w.assets.asked)
	}
	for _, view := range page.Items {
		if view.Overview != nil && !reflect.DeepEqual(*view.Overview, mustOverview(pending(view.Asset.ID))) {
			t.Errorf("the overview of %s is %+v, want what the overviews said of it", view.Asset.ID, *view.Overview)
		}
	}

	// A page of assets that none of the readers' Workspaces owns asks nothing,
	// and neither does a caller that reads the revisions of none.
	for name, c := range map[string]struct {
		scope     ports.ProductScope
		workspace domain.WorkspaceID
	}{
		"the page holds none of the assets that are read": {ownsAndReads, financeWorkspace},
		"the caller reads the revisions of no workspace":  {ports.ProductScope{Workspaces: []domain.WorkspaceID{engineering, operations}}, ""},
		"an owner that is a member of nothing":            {ports.ProductScope{Owner: true}, ""},
	} {
		w := newWorld(t, c.scope, assets, pending)
		page, err := w.list(ListAssets{PageSize: 100, WorkspaceID: c.workspace})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, view := range page.Items {
			if view.Overview != nil {
				t.Errorf("%s: the asset %s has the overview %+v", name, view.Asset.ID, *view.Overview)
			}
		}
		if len(w.overviews.asked) != 0 {
			t.Errorf("%s: the overviews were asked %+v, want nothing asked", name, w.overviews.asked)
		}
	}
}

func mustOverview(overview domain.Overview, _ bool) domain.Overview { return overview }

// An asset the overviews say nothing of has the empty overview, and no
// revision: what the caller that reads it is told is that it stands nowhere,
// not that it may not know.
func TestAnAssetTheOverviewsSayNothingOfHasTheEmptyOverview(t *testing.T) {
	assets := assetsOf(2, inWorkspace(engineering))
	scope := ports.ProductScope{Workspaces: []domain.WorkspaceID{engineering}, Readers: []domain.WorkspaceID{engineering}}
	for _, silent := range []bool{false, true} {
		w := newWorld(t, scope, assets, func(id domain.AssetID) (domain.Overview, bool) {
			if id == assetNumber(1) {
				return pending(id)
			}
			return domain.Overview{}, false
		})
		w.overviews.silent = silent
		page, err := w.list(ListAssets{})
		if err != nil || len(page.Items) != 2 {
			t.Fatalf("silent %t: the page holds %+v (%v), want two assets", silent, page.Items, err)
		}
		if first, second := page.Items[0].Overview, page.Items[1].Overview; first == nil || !first.ReleasePending ||
			second == nil || !reflect.DeepEqual(*second, domain.Overview{}) {
			t.Errorf("silent %t: the overviews are %+v and %+v, want that of the first and the empty one", silent, first, second)
		}
	}
}

// A caller that reads the revisions of no Workspace of the product is refused
// a filter on where assets stand, as it is for any list it may not read, and
// is not answered with an empty page: what a filter keeps is what it may not
// know. The refusal is on record as that of the list of assets, for no asset;
// nothing was looked in and nothing was asked. Without a filter the same
// caller reads its list, and leaves no record.
func TestACallerThatReadsNoRevisionsIsRefusedAFilterAsItIsForAnyListItMayNotRead(t *testing.T) {
	assets := assetsOf(5, inWorkspace(engineering))
	for name, scope := range map[string]ports.ProductScope{
		"a maintainer":                 {Workspaces: []domain.WorkspaceID{engineering}},
		"an owner that maintains":      {Owner: true, Workspaces: []domain.WorkspaceID{engineering}},
		"an owner that is no member":   {Owner: true},
		"readers of an empty set only": {Workspaces: []domain.WorkspaceID{engineering}, Readers: []domain.WorkspaceID{}},
	} {
		for filtering, query := range map[string]ListAssets{
			"a status": {CheckStatus: domain.CheckFail}, "a release": {ReleasePending: true},
			"both": {CheckStatus: domain.CheckFail, ReleasePending: true},
		} {
			w := newWorld(t, scope, assets, pending)
			page, err := w.list(query)
			if !errors.Is(err, ErrForbidden) || len(page.Items) != 0 || page.Next != nil {
				t.Errorf("%s, %s: %+v (%v), want no page and %v", name, filtering, page, err, ErrForbidden)
			}
			want := []ports.AuditEntry{{
				Actor: caller, ProductID: someProduct, ResourceID: "", CorrelationID: "correlation-1",
				Action: "asset.search", Rule: "DENY-03", Allowed: false,
			}}
			if !reflect.DeepEqual(w.audited, want) {
				t.Errorf("%s, %s: on record %+v, want %+v", name, filtering, w.audited, want)
			}
			if len(w.assets.asked) != 0 || len(w.overviews.asked) != 0 {
				t.Errorf("%s, %s: %d lists of assets and %d questions were asked, want none", name, filtering, len(w.assets.asked), len(w.overviews.asked))
			}
		}
		w := newWorld(t, scope, assets, pending)
		if page, err := w.list(ListAssets{}); err != nil || len(page.Items) != 5 || len(w.audited) != 0 {
			t.Errorf("%s: the list without a filter is %d assets (%v) and %d records, want five and none", name, len(page.Items), err, len(w.audited))
		}
	}

	// A caller that may not read the list at all is refused as before, and a
	// product it does not know is not there.
	w := newWorld(t, ports.ProductScope{}, assets, pending)
	if _, err := w.list(ListAssets{ReleasePending: true}); !errors.Is(err, ErrForbidden) || len(w.audited) != 1 {
		t.Errorf("a caller that reads nothing: %v and %d records, want %v and one", err, len(w.audited), ErrForbidden)
	}
	w = newWorld(t, ports.ProductScope{Readers: []domain.WorkspaceID{engineering}}, assets, pending)
	w.scopes.scope.Known = false
	if _, err := w.list(ListAssets{ReleasePending: true}); !errors.Is(err, ErrProductNotFound) {
		t.Errorf("a caller that does not know the product: %v, want %v", err, ErrProductNotFound)
	}
}

// batches are what a round of a list that is filtered takes: a page and one
// more, and then twice what the round before took, up to a thousand.
func batches(size, rounds int) []int {
	sizes := make([]int, 0, rounds)
	for next := size + 1; len(sizes) < rounds; next *= 2 {
		sizes = append(sizes, min(next, 1000))
	}
	return sizes
}

// A filter that keeps none of the assets reads them all and asks where each
// stands once, in rounds of a growing size, each from the position where the
// round before ended: as many rounds as it takes to read M assets, and the
// last of them is the one that reads fewer than it asked for. The same
// rounds are asked of the Catalog and of the overviews, and none is asked of
// the overviews for what the Catalog has no more of.
func TestAFilteredListThatKeepsNothingReadsTheAssetsInRoundsAndAsksAboutEachOnce(t *testing.T) {
	scope := ports.ProductScope{Workspaces: []domain.WorkspaceID{engineering}, Readers: []domain.WorkspaceID{engineering}}
	for _, c := range []struct {
		size, assets int
		rounds       int // of the Catalog
		questions    int // of the overviews
	}{
		{50, 0, 1, 0}, {50, 1, 1, 1}, {50, 50, 1, 1},
		// A round that reads as many as it asked for does not say that it was the last.
		{50, 51, 2, 1}, {50, 52, 2, 2}, {50, 153, 3, 2}, {50, 154, 3, 3}, {50, 1581, 6, 5},
		{50, 2000, 6, 6}, {50, 20000, 24, 24},
		{1, 1, 1, 1}, {1, 2, 2, 1}, {1, 6, 3, 2}, {1, 2000, 10, 10},
		{100, 20000, 23, 23},
	} {
		for _, filter := range []ListAssets{{ReleasePending: true}, {CheckStatus: domain.CheckFail}} {
			w := newWorld(t, scope, assetsOf(c.assets, inWorkspace(engineering)), never)
			filter.PageSize = c.size
			page, err := w.list(filter)
			if err != nil || len(page.Items) != 0 || page.Next != nil {
				t.Fatalf("a page of %d, %d assets, nothing kept: %+v (%v), want an empty page and no token", c.size, c.assets, page, err)
			}
			if len(w.assets.asked) != c.rounds || len(w.overviews.asked) != c.questions {
				t.Errorf("a page of %d, %d assets, nothing kept: %d lists of the Catalog and %d questions, want %d and %d",
					c.size, c.assets, len(w.assets.asked), len(w.overviews.asked), c.rounds, c.questions)
			}
			for round, asked := range w.assets.asked {
				if want := batches(c.size, c.rounds)[round]; asked.Limit != want {
					t.Errorf("a page of %d, %d assets: round %d took %d assets, want %d", c.size, c.assets, round+1, asked.Limit, want)
				}
				if (asked.After == nil) != (round == 0) {
					t.Errorf("a page of %d, %d assets: round %d starts behind %v", c.size, c.assets, round+1, asked.After)
				} else if round > 0 {
					behind := batches(c.size, c.rounds)
					read := 0
					for _, size := range behind[:round] {
						read += size
					}
					if want := assetNumber(read); asked.After.ID != want {
						t.Errorf("a page of %d, %d assets: round %d starts behind %s, want %s", c.size, c.assets, round+1, asked.After.ID, want)
					}
				}
			}
			// Each asset is asked about once, in the order of the list.
			var want []domain.AssetID
			for n := 1; n <= c.assets; n++ {
				want = append(want, assetNumber(n))
			}
			if got := w.askedAbout(); !slices.Equal(got, want) {
				t.Errorf("a page of %d, %d assets: %d assets were asked about, want each once in order", c.size, c.assets, len(got))
			}
		}
	}
}

// A page of a filtered list is full unless it is the last, and the position
// that follows it is that of its last asset; read to its end, the list holds
// every asset the filter keeps, once and in order. The assets that are kept are
// among those that are not.
func TestAFilteredPageIsFullUnlessItIsTheLastAndEveryAssetKeptIsListedOnce(t *testing.T) {
	scope := ports.ProductScope{Workspaces: []domain.WorkspaceID{engineering, operations}, Readers: []domain.WorkspaceID{engineering}}
	// Every third asset is released-pending, and they are those of engineering
	// and of operations in turn, so that half of them are not read.
	assets := assetsOf(120, func(n int) domain.WorkspaceID {
		if n%2 == 0 {
			return engineering
		}
		return operations
	})
	pendingEvery3 := func(id domain.AssetID) (domain.Overview, bool) {
		overview, _ := pending(id)
		var n int
		fmt.Sscanf(string(id), "a0000000-0000-4000-8000-%012d", &n)
		overview.ReleasePending = n%3 == 0
		return overview, true
	}
	var wanted []domain.AssetID
	for _, asset := range assets {
		if asset.WorkspaceID == engineering {
			if overview, _ := pendingEvery3(asset.ID); overview.ReleasePending {
				wanted = append(wanted, asset.ID)
			}
		}
	}
	if len(wanted) != 20 {
		t.Fatalf("the test keeps %d assets, want 20", len(wanted))
	}

	for _, size := range []int{1, 2, 3, 7, 19, 20, 21, 100} {
		w := newWorld(t, scope, assets, pendingEvery3)
		var listed []domain.AssetID
		var after *ports.Position
		for pages := 1; ; pages++ {
			page, err := w.list(ListAssets{ReleasePending: true, PageSize: size, After: after})
			if err != nil {
				t.Fatalf("a page of %d: %v", size, err)
			}
			listed = append(listed, identifiersOf(page.Items)...)
			if page.Next == nil {
				if len(page.Items) > size {
					t.Errorf("a page of %d: the last page holds %d", size, len(page.Items))
				}
				break
			}
			// A page that is followed by another is full, and the position is its last asset's.
			last := page.Items[len(page.Items)-1].Asset
			if len(page.Items) != size || *page.Next != (ports.Position{At: last.CreatedAt, ID: last.ID}) {
				t.Fatalf("a page of %d: a page that is followed by another holds %d assets and the position %+v, want %d and that of %s",
					size, len(page.Items), *page.Next, size, last.ID)
			}
			after = page.Next
			if pages > 200 {
				t.Fatalf("a page of %d: the list does not end", size)
			}
		}
		if !slices.Equal(listed, wanted) {
			t.Errorf("a page of %d: the list holds %v, want the %d assets kept, once and in order: %v", size, listed, len(wanted), wanted)
		}
		for _, asked := range w.overviews.asked {
			for _, place := range asked.places {
				if place.WorkspaceID != engineering {
					t.Errorf("a page of %d: the overview of an asset of %s was asked for", size, place.WorkspaceID)
				}
			}
		}
	}
}

// The filters of the list stay what they were in every round: the Workspace, the
// kind, the domain and the search of the caller, and the position the page
// token holds in the first.
func TestEveryRoundOfAFilteredListKeepsTheOtherFiltersAndStartsWhereTheTokenSaid(t *testing.T) {
	scope := ports.ProductScope{Owner: true, Workspaces: []domain.WorkspaceID{engineering}, Readers: []domain.WorkspaceID{engineering}}
	assets := assetsOf(300, inWorkspace(engineering))
	w := newWorld(t, scope, assets, never)
	token := &ports.Position{At: assets[9].CreatedAt, ID: assets[9].ID}
	_, err := w.list(ListAssets{
		ReleasePending: true, WorkspaceID: engineering, Kind: domain.KindHTTP, DomainKey: "shipping", Query: "asset", PageSize: 20, After: token,
	})
	if err != nil || len(w.assets.asked) < 2 {
		t.Fatalf("the list of 290 assets behind the token: %v and %d rounds, want more than one", err, len(w.assets.asked))
	}
	for round, asked := range w.assets.asked {
		if asked.All || !slices.Equal(asked.Workspaces, []domain.WorkspaceID{engineering}) || asked.Workspace != engineering ||
			asked.Kind != domain.KindHTTP || asked.DomainKey != "shipping" || asked.Query != "asset" {
			t.Errorf("round %d asks %+v", round+1, asked)
		}
	}
	if first := w.assets.asked[0]; first.After == nil || *first.After != *token {
		t.Errorf("the first round starts behind %+v, want the token %+v", first.After, *token)
	}
	if got := w.askedAbout(); len(got) != 290 || got[0] != assetNumber(11) {
		t.Errorf("%d assets were asked about, the first %v, want the 290 behind the token", len(got), got[0])
	}
}

// What a status or a release asked for and what the overviews say of the
// assets of a round is what the round keeps, and a failure of the Catalog or
// of the overviews is the failure of the list, whichever round it comes in.
func TestAFailureOfTheCatalogOrOfTheOverviewsIsTheFailureOfTheList(t *testing.T) {
	scope := ports.ProductScope{Workspaces: []domain.WorkspaceID{engineering}, Readers: []domain.WorkspaceID{engineering}}
	assets := assetsOf(10, inWorkspace(engineering))
	broken := errors.New("it is gone")
	for name, query := range map[string]ListAssets{"filtered": {ReleasePending: true}, "not filtered": {}} {
		w := newWorld(t, scope, assets, pending)
		w.assets.failure = broken
		if page, err := w.list(query); !errors.Is(err, broken) || len(page.Items) != 0 {
			t.Errorf("%s, assets that fail: %+v (%v), want no page and %v", name, page, err, broken)
		}
		w = newWorld(t, scope, assets, pending)
		w.overviews.failure = broken
		if page, err := w.list(query); !errors.Is(err, broken) || len(page.Items) != 0 {
			t.Errorf("%s, overviews that fail: %+v (%v), want no page and %v", name, page, err, broken)
		}
	}
}

// A status that no check concludes is refused, with the other things a
// request can say wrong, before anything is read: not even the scope of the
// caller. A filter on a status that is one is no mistake.
func TestAStatusNoCheckConcludesIsRefusedBeforeAnythingIsRead(t *testing.T) {
	scope := ports.ProductScope{Workspaces: []domain.WorkspaceID{engineering}, Readers: []domain.WorkspaceID{engineering}}
	for _, status := range []domain.CheckStatus{"fail", "NOT_RUN", "FAILED", " FAIL", "PENDING"} {
		w := newWorld(t, scope, assetsOf(3, inWorkspace(engineering)), pending)
		if _, err := w.list(ListAssets{CheckStatus: status}); !errors.Is(err, domain.ErrInvalid) || w.scopes.read != 0 ||
			len(w.assets.asked) != 0 || len(w.overviews.asked) != 0 || len(w.audited) != 0 {
			t.Errorf("the status %q: %v, %d reads of the scope, %d lists, %d questions, %d records; want %v and nothing",
				status, err, w.scopes.read, len(w.assets.asked), len(w.overviews.asked), len(w.audited), domain.ErrInvalid)
		}
	}
	for _, status := range domain.CheckStatuses() {
		w := newWorld(t, scope, assetsOf(3, inWorkspace(engineering)), pending)
		if _, err := w.list(ListAssets{CheckStatus: status}); err != nil {
			t.Errorf("the status %q: %v, want no mistake", status, err)
		}
	}
}

// The overview of one asset is read for a caller that reads the revisions of its
// Workspace, with one question, and not for one that does not. An asset the
// caller may not read is as missing as one that is not there, and is not asked
// about; GetAsset, the first step of every operation on the revisions of an
// asset, asks nothing of the overviews at all.
func TestTheOverviewOfOneAssetIsReadForWhoReadsItsRevisionsAndForNoOneElse(t *testing.T) {
	assets := assetsOf(3, func(n int) domain.WorkspaceID {
		return []domain.WorkspaceID{engineering, operations, financeWorkspace}[n-1]
	})
	read := func(w *world, n int) (AssetView, error) {
		w.t.Helper()

		return w.catalog.ReadAsset(w.t.Context(), GetAsset{Request: w.request(), AssetID: assetNumber(n)})
	}
	scope := ports.ProductScope{Workspaces: []domain.WorkspaceID{engineering, operations}, Readers: []domain.WorkspaceID{engineering}}

	w := newWorld(t, scope, assets, pending)
	view, err := read(w, 1)
	if err != nil || view.Asset.ID != assetNumber(1) || view.Overview == nil || !view.Overview.ReleasePending ||
		len(w.overviews.asked) != 1 || !reflect.DeepEqual(w.overviews.asked[0].places, []ports.AssetPlace{{ID: assetNumber(1), WorkspaceID: engineering}}) ||
		w.overviews.asked[0].filter.Any() {
		t.Errorf("an asset of a Workspace the caller reads revisions in: %+v (%v), asked %+v", view, err, w.overviews.asked)
	}

	w = newWorld(t, scope, assets, pending)
	if view, err := read(w, 2); err != nil || view.Overview != nil || len(w.overviews.asked) != 0 {
		t.Errorf("an asset of a Workspace the caller is a member of: %+v (%v), asked %+v, want it without an overview and nothing asked", view, err, w.overviews.asked)
	}

	// An asset the caller does not read is not there, and is on record as it was.
	w = newWorld(t, scope, assets, pending)
	if view, err := read(w, 3); !errors.Is(err, ErrAssetNotFound) || view.Overview != nil || len(w.overviews.asked) != 0 {
		t.Errorf("an asset of a Workspace the caller reads nothing in: %+v (%v), want %v and nothing asked", view, err, ErrAssetNotFound)
	}
	if want := []ports.AuditEntry{{
		Actor: caller, ProductID: someProduct, ResourceID: string(assetNumber(3)), CorrelationID: "correlation-1",
		Action: "asset.read", Rule: "DENY-03", Allowed: false,
	}}; !reflect.DeepEqual(w.audited, want) {
		t.Errorf("the refusal is on record as %+v, want %+v", w.audited, want)
	}
	if _, err := read(w, 99); !errors.Is(err, ErrAssetNotFound) || len(w.overviews.asked) != 0 {
		t.Errorf("an asset that is not there: %v, want %v and nothing asked", err, ErrAssetNotFound)
	}

	// A failure of the overviews is the failure of the read.
	w = newWorld(t, scope, assets, pending)
	broken := errors.New("it is gone")
	w.overviews.failure = broken
	if view, err := read(w, 1); !errors.Is(err, broken) || view.Overview != nil {
		t.Errorf("overviews that fail: %+v (%v), want no asset and %v", view, err, broken)
	}

	// GetAsset gives the asset and asks nothing else.
	w = newWorld(t, scope, assets, pending)
	if asset, err := w.catalog.GetAsset(t.Context(), GetAsset{Request: w.request(), AssetID: assetNumber(1)}); err != nil ||
		asset.ID != assetNumber(1) || len(w.overviews.asked) != 0 {
		t.Errorf("GetAsset: %+v (%v), %d questions of the overviews, want the asset and none", asset, err, len(w.overviews.asked))
	}
}

// A Catalog that does not read forward ends the list with a failure at the
// first round that shows it, and not at a later one, and not with rounds that
// never end: nothing is asked about what it answered out of its place, and the
// failure is no refusal, so that no page is given and nothing is put on
// record. These are what a loop that asks from the same place again is
// answered by a Catalog that does what it is asked, and what a Catalog that
// ignores the place it is asked to read from answers a loop that is right.
func TestAFilteredListFailsAtTheFirstRoundThatDoesNotReadForward(t *testing.T) {
	scope := ports.ProductScope{Workspaces: []domain.WorkspaceID{engineering}, Readers: []domain.WorkspaceID{engineering}}
	assets := assetsOf(40, inWorkspace(engineering))
	const size = 5

	for name, answering := range map[string]func(ports.Selection) []domain.Asset{
		"the same assets every time": func(ports.Selection) []domain.Asset { return assets[:size+1] },
		"the assets from the start of the list, as many as it was asked for": func(selection ports.Selection) []domain.Asset {
			return assets[:selection.Limit]
		},
		"the last assets of the list, as many as it was asked for": func(selection ports.Selection) []domain.Asset {
			return assets[len(assets)-selection.Limit:]
		},
	} {
		w := newWorld(t, scope, assets, never)
		w.assets.answering = answering
		page, err := w.list(ListAssets{ReleasePending: true, PageSize: size})
		if !errors.Is(err, ErrAssetsOutOfOrder) || len(page.Items) != 0 || page.Next != nil {
			t.Errorf("%s: %+v (%v), want no page and %v", name, page, err, ErrAssetsOutOfOrder)
		}
		// The first round is answered as it was asked. The second is the one
		// that does not read forward, and nothing is asked about its answer.
		if len(w.assets.asked) != 2 || len(w.overviews.asked) != 1 {
			t.Errorf("%s: the Catalog was asked %d times and the overviews %d, want two and one", name, len(w.assets.asked), len(w.overviews.asked))
		}
		if len(w.audited) != 0 {
			t.Errorf("%s: %+v is on record, want nothing: a Catalog that fails is no refusal", name, w.audited)
		}
	}
}

// The first round has to read from where the token says, and a Catalog that
// answers from before it, or from it, fails the list with nothing asked about
// the answer. What follows the token is read, also an asset of the instant of
// the token that has a greater identifier.
func TestAFilteredListFailsWhenTheFirstRoundDoesNotStartAfterTheToken(t *testing.T) {
	scope := ports.ProductScope{Workspaces: []domain.WorkspaceID{engineering}, Readers: []domain.WorkspaceID{engineering}}
	assets := assetsOf(20, inWorkspace(engineering))
	token := ports.Position{At: assets[9].CreatedAt, ID: assets[9].ID}
	atTheInstant := func(n int) domain.Asset {
		asset := assets[9]
		asset.ID = assetNumber(n)
		return asset
	}

	for name, answer := range map[string][]domain.Asset{
		"the asset of the token itself":                         assets[9:12],
		"the asset before it":                                   assets[8:11],
		"the start of the list":                                 assets[:6],
		"an asset of the same instant with a lesser identifier": {atTheInstant(8), assets[10]},
	} {
		w := newWorld(t, scope, assets, never)
		w.assets.answering = func(ports.Selection) []domain.Asset { return answer }
		if _, err := w.list(ListAssets{ReleasePending: true, PageSize: 5, After: &token}); !errors.Is(err, ErrAssetsOutOfOrder) ||
			len(w.assets.asked) != 1 || len(w.overviews.asked) != 0 {
			t.Errorf("%s: %v, the Catalog asked %d times and the overviews %d, want %v, once and never", name, err,
				len(w.assets.asked), len(w.overviews.asked), ErrAssetsOutOfOrder)
		}
	}
	for name, answer := range map[string][]domain.Asset{
		"the asset after it": assets[10:13],
		"an asset of the same instant with a greater identifier": {atTheInstant(12), assets[10]},
	} {
		w := newWorld(t, scope, assets, never)
		w.assets.answering = func(ports.Selection) []domain.Asset { return answer }
		if _, err := w.list(ListAssets{ReleasePending: true, PageSize: 5, After: &token}); err != nil ||
			len(w.assets.asked) != 1 || len(w.overviews.asked) != 1 || len(w.askedAbout()) != len(answer) {
			t.Errorf("%s: %v, the Catalog asked %d times and the overviews %d about %d assets, want no failure, once and about all %d",
				name, err, len(w.assets.asked), len(w.overviews.asked), len(w.askedAbout()), len(answer))
		}
	}
}

// What a round is answered with is in the order of the list, one asset after
// the other, strictly: the instant the assets were created at and then their
// identifiers. A Catalog that answers in another order, or twice with an asset,
// fails the list with nothing asked about the answer; assets of one instant
// are in order by their identifiers.
func TestAFilteredListFailsWhenTheCatalogAnswersOutOfTheOrderOfTheList(t *testing.T) {
	scope := ports.ProductScope{Workspaces: []domain.WorkspaceID{engineering}, Readers: []domain.WorkspaceID{engineering}}
	assets := assetsOf(10, inWorkspace(engineering))
	atOneInstant := func(numbers ...int) []domain.Asset {
		var made []domain.Asset
		for _, n := range numbers {
			asset := assets[0]
			asset.ID = assetNumber(n)
			made = append(made, asset)
		}
		return made
	}

	for name, answer := range map[string][]domain.Asset{
		"the newer asset first":                      {assets[1], assets[0]},
		"an asset twice in a row":                    {assets[0], assets[0]},
		"an asset that falls back in the middle":     {assets[0], assets[3], assets[2], assets[4]},
		"an asset again after others":                {assets[0], assets[1], assets[0]},
		"one instant, the identifiers the other way": atOneInstant(2, 1),
		"one instant, an identifier twice":           atOneInstant(1, 1),
		"one instant, an identifier that falls back": atOneInstant(1, 3, 2),
	} {
		w := newWorld(t, scope, assets, never)
		w.assets.answering = func(ports.Selection) []domain.Asset { return answer }
		page, err := w.list(ListAssets{ReleasePending: true, PageSize: 5})
		if !errors.Is(err, ErrAssetsOutOfOrder) || len(page.Items) != 0 || len(w.assets.asked) != 1 || len(w.overviews.asked) != 0 || len(w.audited) != 0 {
			t.Errorf("%s: %+v (%v), the Catalog asked %d times, the overviews %d, %d on record; want %v, once, never and nothing",
				name, page, err, len(w.assets.asked), len(w.overviews.asked), len(w.audited), ErrAssetsOutOfOrder)
		}
	}

	w := newWorld(t, scope, assets, never)
	answer := atOneInstant(1, 2, 3)
	w.assets.answering = func(ports.Selection) []domain.Asset { return answer }
	if _, err := w.list(ListAssets{ReleasePending: true, PageSize: 5}); err != nil || len(w.askedAbout()) != 3 {
		t.Errorf("assets of one instant in the order of their identifiers: %v, %d asked about, want no failure and all three", err, len(w.askedAbout()))
	}
}

// follows says where an answer reads to, and refuses it if it is not behind the
// place that was read to, or its assets are not one after the other.
func TestAnAnswerFollowsThePlaceItIsGivenAndReadsToItsLastAsset(t *testing.T) {
	assets := assetsOf(5, inWorkspace(engineering))
	place := func(asset domain.Asset) *ports.Position { return &ports.Position{At: asset.CreatedAt, ID: asset.ID} }
	atTheInstantOf := func(asset domain.Asset, n int) domain.Asset {
		asset.ID = assetNumber(n)
		return asset
	}

	for name, c := range map[string]struct {
		read   *ports.Position
		answer []domain.Asset
		to     *ports.Position // where it reads to; nil and a failure is what is wanted when fails
		fails  bool
	}{
		"no assets from the start":                          {nil, nil, nil, false},
		"one asset from the start":                          {nil, assets[:1], place(assets[0]), false},
		"assets from the start":                             {nil, assets[:3], place(assets[2]), false},
		"assets after a place":                              {place(assets[1]), assets[2:4], place(assets[3]), false},
		"no assets after a place leave it where it was":     {place(assets[1]), nil, place(assets[1]), false},
		"an asset of the instant with a greater identifier": {place(assets[1]), []domain.Asset{atTheInstantOf(assets[1], 9)}, place(atTheInstantOf(assets[1], 9)), false},
		"the asset of the place":                            {place(assets[1]), assets[1:3], nil, true},
		"an asset before the place":                         {place(assets[2]), assets[1:3], nil, true},
		"an asset of the instant with a lesser identifier":  {place(assets[1]), []domain.Asset{atTheInstantOf(assets[1], 1)}, nil, true},
		"an answer that falls back":                         {nil, []domain.Asset{assets[2], assets[1]}, nil, true},
		"an asset twice":                                    {nil, []domain.Asset{assets[1], assets[1]}, nil, true},
	} {
		to, err := follows(c.read, c.answer)
		switch {
		case c.fails && (!errors.Is(err, ErrAssetsOutOfOrder) || to != nil):
			t.Errorf("%s: %+v (%v), want no place and %v", name, to, err, ErrAssetsOutOfOrder)
		case !c.fails && (err != nil || !reflect.DeepEqual(to, c.to)):
			t.Errorf("%s: %+v (%v), want %+v and no failure", name, to, err, c.to)
		}
	}

	// What is logged of the failure is which asset does not come after which, and
	// the instants they were created at.
	_, err := follows(place(assets[2]), assets[1:2])
	for _, want := range []string{string(assets[1].ID), string(assets[2].ID), assets[1].CreatedAt.UTC().Format(time.RFC3339Nano)} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("the failure %v does not say %s", err, want)
		}
	}
}

// A fake that is asked without end stops the test at the limit, and no further:
// the test fails where the fake is asked the question after the limit, with the
// fake named, and it is not answered. It is what keeps a loop that does not
// stop from using the memory of the machine.
func TestAFakeThatIsAskedWithoutEndStopsTheTestAtTheLimit(t *testing.T) {
	scope := ports.ProductScope{Workspaces: []domain.WorkspaceID{engineering}, Readers: []domain.WorkspaceID{engineering}}
	for name, ask := range map[string]func(*world){
		"the Catalog": func(w *world) {
			_, _ = w.assets.List(t.Context(), someProduct, ports.Selection{All: true, Limit: 1})
		},
		"the overviews": func(w *world) {
			_, _ = w.overviews.Of(t.Context(), someProduct, nil, ports.OverviewFilter{})
		},
	} {
		w := newWorld(t, scope, assetsOf(3, inWorkspace(engineering)), pending)
		stopped := &stoppedTest{}
		w.assets.test, w.overviews.test = stopped, stopped
		answered := 0
		func() {
			defer func() {
				if recovered := recover(); recovered != nil && recovered != stopTheTest {
					panic(recovered)
				}
			}()
			for answered < 2*askedAtMost {
				ask(w)
				answered++
			}
		}()
		if answered != askedAtMost || len(stopped.told) != 1 || !strings.Contains(stopped.told[0], name) {
			t.Errorf("%s: answered %d times and told the test %q, want %d answers and one stop that names it", name, answered, stopped.told, askedAtMost)
		}
	}
}

// stopTheTest is what a stoppedTest panics with, as a test that is stopped
// ends the goroutine that runs it.
var stopTheTest = new(int)

// stoppedTest stands in for a test that a fake stops: it keeps what it was
// told and ends what asked.
type stoppedTest struct{ told []string }

func (s *stoppedTest) Helper() {}

func (s *stoppedTest) Fatalf(format string, args ...any) {
	s.told = append(s.told, fmt.Sprintf(format, args...))
	panic(stopTheTest)
}

func TestTheCatalogIsWiredWithOverviews(t *testing.T) {
	if catalog, err := New(Dependencies{
		Transactions: unusedTransactions{}, Scopes: unusedScopes{}, Assets: &assetStore{},
		Audit:  func(context.Context, ports.AuditEntry) error { return nil },
		Events: unusedEvents{}, Clock: unusedClock{}, Identifiers: unusedIdentifiers{},
	}); err == nil || catalog != nil {
		t.Errorf("a Catalog without overviews was wired: %v, %v", catalog, err)
	}
}
