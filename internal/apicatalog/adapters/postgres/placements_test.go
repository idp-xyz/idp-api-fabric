package postgres_test

import (
	"cmp"
	"context"
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

// registrar is a migrated database opened the way a Fabric process opens it,
// as the application role, with the assets of the Catalog on it.
type registrar struct {
	t      *testing.T
	fabric *db.Database
	assets *postgres.Assets
}

func newRegistrar(t *testing.T) registrar {
	t.Helper()

	database := pgtest.MigratedDatabase(t)
	fabric, err := db.Open(t.Context(), database.Application)
	if err != nil {
		t.Fatalf("open the database: %v", err)
	}
	t.Cleanup(fabric.Close)
	return registrar{t: t, fabric: fabric, assets: postgres.NewAssets(fabric.Framework)}
}

// register keeps an asset of the Workspace under the product, as the Catalog
// does, and says what it is placed as.
func (r registrar) register(product domain.ProductID, workspace domain.WorkspaceID, key string) ports.AssetPlace {
	r.t.Helper()

	asset, err := domain.Register(domain.AssetID(ids.NewUUID()), product, workspace, key, "API "+key, "payments", domain.KindHTTP,
		domain.Principal{Issuer: "https://issuer.example", Subject: "maintainer"}, time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC))
	if err != nil {
		r.t.Fatalf("make the asset %s: %v", key, err)
	}
	if err := r.fabric.WithinTransaction(r.t.Context(), func(ctx context.Context) error { return r.assets.Add(ctx, asset) }); err != nil {
		r.t.Fatalf("keep the asset %s: %v", key, err)
	}
	return ports.AssetPlace{ID: asset.ID, WorkspaceID: workspace}
}

// placed is what the Workspaces of the product own, in the order of the
// assets, which the storage does not promise.
func (r registrar) placed(product domain.ProductID, workspaces ...domain.WorkspaceID) []ports.AssetPlace {
	r.t.Helper()

	places, err := r.assets.InWorkspaces(r.t.Context(), product, workspaces)
	if err != nil {
		r.t.Fatalf("place the assets of %v: %v", workspaces, err)
	}
	slices.SortFunc(places, func(a, b ports.AssetPlace) int { return cmp.Compare(a.ID, b.ID) })
	return places
}

// inOrder is the assets as placed, in the order of their identifiers.
func inOrder(places ...ports.AssetPlace) []ports.AssetPlace {
	slices.SortFunc(places, func(a, b ports.AssetPlace) int { return cmp.Compare(a.ID, b.ID) })
	return places
}

// The assets that some Workspaces own are placed with the Workspace that owns
// each: those of the Workspaces named, of the product named, each once. The
// assets of other Workspaces are not, and neither are those another product
// keeps in a Workspace that has the same identifier. A set of no Workspace
// owns no asset, however many the product has, and a Workspace or a product
// that holds nothing places nothing.
func TestTheAssetsOfSomeWorkspacesAreThoseOfTheProductThatTheyNameAndOfNoOther(t *testing.T) {
	r := newRegistrar(t)
	product, other := domain.ProductID(ids.NewUUID()), domain.ProductID(ids.NewUUID())
	engineering, operations, sorting := domain.WorkspaceID(ids.NewUUID()), domain.WorkspaceID(ids.NewUUID()), domain.WorkspaceID(ids.NewUUID())

	payments, billing := r.register(product, engineering, "payments"), r.register(product, engineering, "billing")
	shipping := r.register(product, operations, "shipping")
	r.register(product, sorting, "sorting")
	// The other product has a Workspace that is named like one of this one.
	theirs := r.register(other, engineering, "payments")
	r.register(other, domain.WorkspaceID(ids.NewUUID()), "ledger")

	for name, c := range map[string]struct {
		product    domain.ProductID
		workspaces []domain.WorkspaceID
		want       []ports.AssetPlace
	}{
		"a Workspace":                        {product, []domain.WorkspaceID{engineering}, inOrder(payments, billing)},
		"another Workspace":                  {product, []domain.WorkspaceID{operations}, inOrder(shipping)},
		"two Workspaces":                     {product, []domain.WorkspaceID{operations, engineering}, inOrder(payments, billing, shipping)},
		"a Workspace twice":                  {product, []domain.WorkspaceID{engineering, engineering}, inOrder(payments, billing)},
		"a Workspace of the other product":   {other, []domain.WorkspaceID{engineering}, inOrder(theirs)},
		"a Workspace that owns no asset":     {product, []domain.WorkspaceID{domain.WorkspaceID(ids.NewUUID())}, nil},
		"a Workspace the product has not":    {other, []domain.WorkspaceID{operations}, nil},
		"a product that has none":            {domain.ProductID(ids.NewUUID()), []domain.WorkspaceID{engineering, operations, sorting}, nil},
		"no Workspace":                       {product, []domain.WorkspaceID{}, nil},
		"no set of Workspaces":               {product, nil, nil},
		"no set of Workspaces, of the other": {other, nil, nil},
	} {
		got := r.placed(c.product, c.workspaces...)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: placed as\n %+v\nwant\n %+v", name, got, c.want)
		}
	}
}
