package application_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/ports"
)

const (
	someProduct   = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"
	otherProduct  = "2d0b5f8c-63e1-4a9f-87b2-a1d4e3f5a6b7"
	someWorkspace = "b0000000-0000-4000-8000-000000000001"
)

func assetOf(product string, n int) domain.AssetRef {
	return domain.AssetRef{ProductID: product, WorkspaceID: someWorkspace, AssetID: fmt.Sprintf("a0000000-0000-4000-8000-%012d", n)}
}

// headStore stands in for the storage of heads: it answers what the test says
// and keeps what it was asked. The real one is tested against PostgreSQL where
// it lives.
type headStore struct {
	heads   []ports.Head
	failure error
	asked   []question
}

type question struct {
	product string
	assets  []domain.AssetRef
}

func (s *headStore) Heads(_ context.Context, product string, assets []domain.AssetRef) ([]ports.Head, error) {
	s.asked = append(s.asked, question{product, append([]domain.AssetRef(nil), assets...)})
	return s.heads, s.failure
}

func headsOver(t *testing.T, store ports.Heads) *application.Heads {
	t.Helper()

	heads, err := application.NewHeads(store)
	if err != nil {
		t.Fatalf("wire the read of heads: %v", err)
	}
	return heads
}

// What the storage says of the assets of a product is what is answered, and a
// failure of the storage is the failure of the read.
func TestTheHeadsOfAssetsAreWhatTheStorageSaysOfTheProductAndTheAssetsNamed(t *testing.T) {
	said := []ports.Head{{Revision: domain.Revision{ID: "r1", Sequence: 2, Status: "APPROVED"}, Released: true}}
	store := &headStore{heads: said}
	assets := []domain.AssetRef{assetOf(someProduct, 1), assetOf(someProduct, 2)}

	got, err := headsOver(t, store).Of(t.Context(), someProduct, assets)
	if err != nil || !reflect.DeepEqual(got, said) {
		t.Errorf("the heads are %+v (%v), want %+v", got, err, said)
	}
	if want := []question{{someProduct, assets}}; !reflect.DeepEqual(store.asked, want) {
		t.Errorf("the storage was asked %+v, want %+v", store.asked, want)
	}

	broken := errors.New("the storage is gone")
	if got, err := headsOver(t, &headStore{failure: broken}).Of(t.Context(), someProduct, assets); !errors.Is(err, broken) || len(got) != 0 {
		t.Errorf("a storage that fails: %+v (%v), want no heads and %v", got, err, broken)
	}
}

// Nothing is read for no assets. A set that is not assets of one product named
// by their identifiers, or that is more than a read takes, is refused whole,
// and the storage is asked of none of it: what it would say of the part that
// is fit is not what the read answers.
func TestHeadsAreNotReadOfNoAssetsOrOfAssetsThatAreNotOfTheProductNamedByIdentifiers(t *testing.T) {
	many := make([]domain.AssetRef, 0, application.MaxHeadAssets+1)
	for n := range application.MaxHeadAssets + 1 {
		many = append(many, assetOf(someProduct, n))
	}
	asset := assetOf(someProduct, 1)
	with := func(change func(*domain.AssetRef)) []domain.AssetRef {
		changed := asset
		change(&changed)
		return []domain.AssetRef{assetOf(someProduct, 2), changed}
	}

	for name, c := range map[string]struct {
		product string
		assets  []domain.AssetRef
	}{
		"a key and not an identifier":            {"cargopai", []domain.AssetRef{asset}},
		"no product":                             {"", []domain.AssetRef{asset}},
		"an identifier in capitals":              {strings.ToUpper(someProduct), []domain.AssetRef{asset}},
		"an identifier and something more":       {someProduct + "0", []domain.AssetRef{asset}},
		"an identifier and a line":               {someProduct + "\n", []domain.AssetRef{asset}},
		"an asset and something more":            {someProduct, with(func(a *domain.AssetRef) { a.AssetID += "0" })},
		"a Workspace and something more":         {someProduct, with(func(a *domain.AssetRef) { a.WorkspaceID += "0" })},
		"no product and no assets":               {"", nil},
		"an asset that is no identifier":         {someProduct, with(func(a *domain.AssetRef) { a.AssetID = "shipping-api" })},
		"an asset without an identifier":         {someProduct, with(func(a *domain.AssetRef) { a.AssetID = "" })},
		"an asset in capitals":                   {someProduct, with(func(a *domain.AssetRef) { a.AssetID = strings.ToUpper(a.AssetID) })},
		"a Workspace that is no identifier":      {someProduct, with(func(a *domain.AssetRef) { a.WorkspaceID = "engineering" })},
		"an asset without a Workspace":           {someProduct, with(func(a *domain.AssetRef) { a.WorkspaceID = "" })},
		"an asset of another product":            {someProduct, with(func(a *domain.AssetRef) { a.ProductID = otherProduct })},
		"an asset without a product":             {someProduct, with(func(a *domain.AssetRef) { a.ProductID = "" })},
		"more assets than one read takes":        {someProduct, many},
		"the first asset is the one that is bad": {someProduct, []domain.AssetRef{{ProductID: someProduct, WorkspaceID: someWorkspace}, assetOf(someProduct, 2)}},
	} {
		store := &headStore{heads: []ports.Head{{Revision: domain.Revision{ID: "r1"}}}}
		got, err := headsOver(t, store).Of(t.Context(), c.product, c.assets)
		if !errors.Is(err, application.ErrInvalidAssets) || len(got) != 0 {
			t.Errorf("%s: %+v (%v), want no heads and %v", name, got, err, application.ErrInvalidAssets)
		}
		if len(store.asked) != 0 {
			t.Errorf("%s: the storage was asked %d times, want it never", name, len(store.asked))
		}
	}

	// No assets are no heads, and no question; and as many as a read takes are asked.
	store := &headStore{heads: []ports.Head{{Revision: domain.Revision{ID: "r1"}}}}
	for _, none := range [][]domain.AssetRef{nil, {}} {
		if got, err := headsOver(t, store).Of(t.Context(), someProduct, none); err != nil || len(got) != 0 || len(store.asked) != 0 {
			t.Errorf("the heads of no assets are %+v (%v) after %d questions, want none and none", got, err, len(store.asked))
		}
	}
	if _, err := headsOver(t, store).Of(t.Context(), someProduct, many[:application.MaxHeadAssets]); err != nil ||
		len(store.asked) != 1 || len(store.asked[0].assets) != application.MaxHeadAssets {
		t.Errorf("%d assets, as many as a read takes: %v, %d questions", application.MaxHeadAssets, err, len(store.asked))
	}
}

// A character missing from any of the five groups of an identifier makes it no
// identifier, whichever of the three names it is the identifier of.
func TestAnIdentifierThatLacksACharacterOfAGroupIsRefusedWhicheverItNames(t *testing.T) {
	for group, at := range []int{0, 9, 14, 19, 24} {
		cut := func(id string) string { return id[:at] + id[at+1:] }
		asset := assetOf(someProduct, 1)
		bad := func(change func(*domain.AssetRef)) domain.AssetRef {
			changed := asset
			change(&changed)
			return changed
		}
		for name, c := range map[string]struct {
			product string
			asset   domain.AssetRef
		}{
			"the product":   {cut(someProduct), asset},
			"the asset":     {someProduct, bad(func(a *domain.AssetRef) { a.AssetID = cut(a.AssetID) })},
			"the Workspace": {someProduct, bad(func(a *domain.AssetRef) { a.WorkspaceID = cut(a.WorkspaceID) })},
		} {
			store := &headStore{}
			if got, err := headsOver(t, store).Of(t.Context(), c.product, []domain.AssetRef{c.asset}); !errors.Is(err, application.ErrInvalidAssets) ||
				len(got) != 0 || len(store.asked) != 0 {
				t.Errorf("%s, with a character missing from group %d: %+v (%v), %d questions, want it refused and nothing asked",
					name, group+1, got, err, len(store.asked))
			}
		}
	}
}

func TestTheReadOfHeadsIsWiredToTheStorageOfHeads(t *testing.T) {
	if heads, err := application.NewHeads(nil); err == nil || heads != nil {
		t.Errorf("a read of heads without storage was wired: %v, %v", heads, err)
	}
}
