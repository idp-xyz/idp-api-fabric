package postgres_test

import (
	"cmp"
	"net/url"
	"slices"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/apicatalog/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	"go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
)

// registeredAt is the instant register records every asset at.
var registeredAt = time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)

// listed are the identifiers of what the storage lists under the selection.
func (r registrar) listed(product domain.ProductID, selection ports.Selection) []domain.AssetID {
	r.t.Helper()

	assets, err := r.assets.List(r.t.Context(), product, selection)
	if err != nil {
		r.t.Fatalf("list the assets of %+v: %v", selection, err)
	}
	named := make([]domain.AssetID, 0, len(assets))
	for _, asset := range assets {
		named = append(named, asset.ID)
	}
	return named
}

func identifiersOf(places ...ports.AssetPlace) []domain.AssetID {
	named := make([]domain.AssetID, 0, len(places))
	for _, place := range places {
		named = append(named, place.ID)
	}
	slices.Sort(named)
	return named
}

// The assets that are listed are those of the Workspaces the selection names,
// and a selection that names none lists none: the set of Workspaces is the
// caller's to give, and an empty one is no Workspace, never every Workspace. A
// list of everything is asked for by saying so. Whatever the selection, the
// assets of another product are not listed.
func TestAListOfAssetsNamingNoWorkspaceListsNoneAndIsNeverTakenForEveryAsset(t *testing.T) {
	r := newRegistrar(t)
	product, other := domain.ProductID(ids.NewUUID()), domain.ProductID(ids.NewUUID())
	engineering, operations := domain.WorkspaceID(ids.NewUUID()), domain.WorkspaceID(ids.NewUUID())
	payments, billing := r.register(product, engineering, "payments"), r.register(product, engineering, "billing")
	shipping := r.register(product, operations, "shipping")
	r.register(other, engineering, "payments")

	for name, c := range map[string]struct {
		selection ports.Selection
		want      []domain.AssetID
	}{
		"every asset, asked for":                 {ports.Selection{All: true, Limit: 10}, identifiersOf(payments, billing, shipping)},
		"a Workspace":                            {ports.Selection{Workspaces: []domain.WorkspaceID{engineering}, Limit: 10}, identifiersOf(payments, billing)},
		"two Workspaces":                         {ports.Selection{Workspaces: []domain.WorkspaceID{operations, engineering}, Limit: 10}, identifiersOf(payments, billing, shipping)},
		"a Workspace, and another one asked for": {ports.Selection{Workspaces: []domain.WorkspaceID{engineering}, Workspace: operations, Limit: 10}, nil},
		"a Workspace, and the same asked for":    {ports.Selection{Workspaces: []domain.WorkspaceID{engineering}, Workspace: engineering, Limit: 10}, identifiersOf(payments, billing)},
		"no Workspace":                           {ports.Selection{Workspaces: []domain.WorkspaceID{}, Limit: 10}, nil},
		"no set of Workspaces":                   {ports.Selection{Limit: 10}, nil},
		"no Workspace, and one asked for":        {ports.Selection{Workspace: engineering, Limit: 10}, nil},
		"a Workspace that owns nothing":          {ports.Selection{Workspaces: []domain.WorkspaceID{domain.WorkspaceID(ids.NewUUID())}, Limit: 10}, nil},
	} {
		got := r.listed(product, c.selection)
		slices.Sort(got)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: listed %v, want %v", name, got, c.want)
		}
	}
}

// Assets created at the same instant are listed in the order of their
// identifiers, whatever the page and wherever it starts: a page that ends
// between two of them is followed by one that starts with the second, and the
// pages of the list hold each asset once.
func TestAssetsCreatedAtTheSameInstantAreListedInTheOrderOfTheirIdentifiers(t *testing.T) {
	listedInOrderOfIdentifiers(t, newRegistrar(t))
}

// The order is the one the statement asks for, and not the one an index
// happens to give: with the indexes of the database turned off the statement
// has to sort the assets itself, and the identifier is what it sorts the
// assets of one instant by.
func TestTheOrderOfAssetsCreatedAtTheSameInstantIsAskedOfTheStatementAndNotOfAnIndex(t *testing.T) {
	database := pgtest.MigratedDatabase(t)
	dsn, err := url.Parse(database.Application)
	if err != nil {
		t.Fatal(err)
	}
	query := dsn.Query()
	query.Set("options", "-c enable_indexscan=off -c enable_indexonlyscan=off -c enable_bitmapscan=off")
	dsn.RawQuery = query.Encode()
	fabric, err := db.Open(t.Context(), dsn.String())
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	t.Cleanup(fabric.Close)

	listedInOrderOfIdentifiers(t, registrar{t: t, fabric: fabric, assets: postgres.NewAssets(fabric.Framework)})
}

func listedInOrderOfIdentifiers(t *testing.T, r registrar) {
	t.Helper()

	product, workspace := domain.ProductID(ids.NewUUID()), domain.WorkspaceID(ids.NewUUID())
	var places []ports.AssetPlace
	for _, key := range []string{"first", "second", "third", "fourth", "fifth", "sixth", "seventh", "eighth"} {
		places = append(places, r.register(product, workspace, key))
	}
	want := identifiersOf(places...)
	selection := ports.Selection{Workspaces: []domain.WorkspaceID{workspace}}

	selection.Limit = 10
	if got := r.listed(product, selection); !slices.Equal(got, want) {
		t.Fatalf("the eight assets are listed as %v, want them in the order of their identifiers: %v", got, want)
	}
	selection.Limit = 2
	if got := r.listed(product, selection); !slices.Equal(got, want[:2]) {
		t.Errorf("a page of two is %v, want %v", got, want[:2])
	}
	// Behind any one of them, the rest follow in order.
	for index := range want {
		selection.Limit = 10
		selection.After = &ports.Position{At: registeredAt, ID: want[index]}
		if got := r.listed(product, selection); !slices.Equal(got, want[index+1:]) {
			t.Errorf("behind %d of the eight the list holds %v, want %v", index+1, got, want[index+1:])
		}
	}
	// Pages of two, each behind the last of the page before, hold each asset once.
	var walked []domain.AssetID
	selection.After = nil
	for range len(want) {
		selection.Limit = 2
		page := r.listed(product, selection)
		walked = append(walked, page...)
		if len(page) < 2 {
			break
		}
		selection.After = &ports.Position{At: registeredAt, ID: page[len(page)-1]}
	}
	if !slices.Equal(walked, want) {
		t.Errorf("pages of two hold %v, want %v", walked, want)
	}
	if !slices.IsSortedFunc(want, cmp.Compare[domain.AssetID]) {
		t.Errorf("the identifiers are not in order: %v", want)
	}
}
