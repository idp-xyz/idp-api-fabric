package application

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/apicatalog/domain"
	"go.idp.xyz/idp-api-fabric/internal/apicatalog/ports"
)

const (
	someProduct = domain.ProductID("1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6")
	engineering = domain.WorkspaceID("b0000000-0000-4000-8000-000000000001")
	operations  = domain.WorkspaceID("b0000000-0000-4000-8000-000000000002")
)

// The rest of what the Catalog is wired with has nothing to do with placing
// assets: a use of any of it fails the test.
type (
	unusedTransactions struct{ ports.Transactor }
	unusedScopes       struct{ ports.Scopes }
	unusedEvents       struct{ ports.Events }
	unusedClock        struct{ ports.Clock }
	unusedIdentifiers  struct{ ports.Identifiers }
)

// storage stands in for the assets the Catalog keeps: it answers what the
// test says and keeps what it was asked. The real one is tested against
// PostgreSQL where it lives.
type storage struct {
	ports.Assets
	placed  []ports.AssetPlace
	failure error
	asked   []question
}

type question struct {
	product    domain.ProductID
	workspaces []domain.WorkspaceID
}

func (s *storage) InWorkspaces(_ context.Context, product domain.ProductID, workspaces []domain.WorkspaceID) ([]ports.AssetPlace, error) {
	s.asked = append(s.asked, question{product, slices.Clone(workspaces)})
	return s.placed, s.failure
}

func catalogOver(t *testing.T, assets ports.Assets) *Catalog {
	t.Helper()

	catalog, err := New(Dependencies{
		Transactions: unusedTransactions{}, Scopes: unusedScopes{}, Assets: assets,
		Audit: func(context.Context, ports.AuditEntry) error {
			t.Error("the placing of assets was put on record")
			return nil
		},
		Events: unusedEvents{}, Clock: unusedClock{}, Identifiers: unusedIdentifiers{},
	})
	if err != nil {
		t.Fatalf("wire the use cases: %v", err)
	}
	return catalog
}

// The assets of some Workspaces are what the storage says of the product and
// of those Workspaces, as it says it, and a failure of the storage is the
// failure of the read. Nothing is recorded: the read grants nothing.
func TestTheAssetsOfWorkspacesAreWhatTheStorageSaysOfTheProductAndTheWorkspacesNamed(t *testing.T) {
	placed := []ports.AssetPlace{
		{ID: "a0000000-0000-4000-8000-000000000001", WorkspaceID: engineering},
		{ID: "a0000000-0000-4000-8000-000000000002", WorkspaceID: operations},
	}
	store := &storage{placed: placed}
	workspaces := []domain.WorkspaceID{engineering, operations}

	got, err := catalogOver(t, store).AssetsInWorkspaces(t.Context(), someProduct, workspaces)
	if err != nil || !reflect.DeepEqual(got, placed) {
		t.Errorf("the assets are %+v (%v), want %+v", got, err, placed)
	}
	if want := []question{{someProduct, workspaces}}; !reflect.DeepEqual(store.asked, want) {
		t.Errorf("the storage was asked %+v, want %+v", store.asked, want)
	}

	broken := errors.New("the storage is gone")
	if got, err := catalogOver(t, &storage{failure: broken}).AssetsInWorkspaces(t.Context(), someProduct, workspaces); !errors.Is(err, broken) || len(got) != 0 {
		t.Errorf("a storage that fails: %+v (%v), want no assets and %v", got, err, broken)
	}
}

// A product or a Workspace that is no identifier names nothing the storage
// holds. The first is a product that is not there, the second is a request
// that cannot be answered, and the storage is asked of neither: what it would
// say of one of them is not what the read answers.
func TestAnIdentifierThatIsNoneIsRefusedBeforeTheStorageIsAsked(t *testing.T) {
	const capitals = domain.ProductID("1C9A4E7B-52D0-4F8E-B6A1-90C3D2E4F5A6")

	for name, c := range map[string]struct {
		product    domain.ProductID
		workspaces []domain.WorkspaceID
		want       error
	}{
		"a key and not an identifier":                     {"cargopai", []domain.WorkspaceID{engineering}, ErrProductNotFound},
		"no product":                                      {"", []domain.WorkspaceID{engineering}, ErrProductNotFound},
		"an identifier in capitals":                       {capitals, []domain.WorkspaceID{engineering}, ErrProductNotFound},
		"an identifier and something more":                {someProduct + "0", []domain.WorkspaceID{engineering}, ErrProductNotFound},
		"no product and no Workspace":                     {"", nil, ErrProductNotFound},
		"a Workspace that is a key":                       {someProduct, []domain.WorkspaceID{"engineering"}, domain.ErrInvalid},
		"no Workspace, among others":                      {someProduct, []domain.WorkspaceID{engineering, ""}, domain.ErrInvalid},
		"an identifier in capitals, among Workspaces":     {someProduct, []domain.WorkspaceID{engineering, "B0000000-0000-4000-8000-000000000002"}, domain.ErrInvalid},
		"a Workspace that is the first of the Workspaces": {someProduct, []domain.WorkspaceID{"not-an-identifier", operations}, domain.ErrInvalid},
	} {
		store := &storage{placed: []ports.AssetPlace{{ID: "a0000000-0000-4000-8000-000000000001", WorkspaceID: engineering}}}
		got, err := catalogOver(t, store).AssetsInWorkspaces(t.Context(), c.product, c.workspaces)
		if !errors.Is(err, c.want) || len(got) != 0 {
			t.Errorf("%s: %+v (%v), want no assets and %v", name, got, err, c.want)
		}
		if len(store.asked) != 0 {
			t.Errorf("%s: the storage was asked %+v", name, store.asked)
		}
	}
}
