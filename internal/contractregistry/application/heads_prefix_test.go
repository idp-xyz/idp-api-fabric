package application_test

import (
	"errors"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/contractregistry/application"
	"go.idp.xyz/idp-api-fabric/internal/contractregistry/domain"
)

// An identifier is the whole of what is given: something in front of it makes
// it none, as something behind it does, whichever of the three names it is the
// identifier of. A read that took the end of what it was given for an
// identifier would hand the front of it to the database, which answers with an
// error that is not that of a read that cannot be asked for.
func TestAnIdentifierWithSomethingBeforeItIsRefusedWhicheverItNames(t *testing.T) {
	for _, before := range []string{"0", "x", " ", "\n", "urn:uuid:"} {
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
			"the product":   {before + someProduct, asset},
			"the asset":     {someProduct, bad(func(a *domain.AssetRef) { a.AssetID = before + a.AssetID })},
			"the Workspace": {someProduct, bad(func(a *domain.AssetRef) { a.WorkspaceID = before + a.WorkspaceID })},
		} {
			store := &headStore{}
			if got, err := headsOver(t, store).Of(t.Context(), c.product, []domain.AssetRef{c.asset}); !errors.Is(err, application.ErrInvalidAssets) ||
				len(got) != 0 || len(store.asked) != 0 {
				t.Errorf("%s, with %q before it: %+v (%v), %d questions, want it refused and nothing asked",
					name, before, got, err, len(store.asked))
			}
		}
	}
}
