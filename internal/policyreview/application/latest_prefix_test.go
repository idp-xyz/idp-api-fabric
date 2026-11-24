package application_test

import (
	"errors"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/policyreview/application"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// An identifier is the whole of what is given: something in front of it makes
// it none, as something behind it does, whichever of the four names it is the
// identifier of. A read that took the end of what it was given for an
// identifier would hand the front of it to the database, which answers with an
// error that is not that of a read that cannot be asked for.
func TestAnIdentifierWithSomethingBeforeItIsRefusedWhicheverItNames(t *testing.T) {
	for _, before := range []string{"0", "x", " ", "\n", "urn:uuid:"} {
		revision := revisionOf(latestProduct, 1)
		bad := func(change func(*domain.RevisionRef)) application.Expected {
			changed := revision
			change(&changed)
			return application.Expected{Revision: changed, InputDigest: latestDigest}
		}
		for name, c := range map[string]struct {
			product  string
			expected application.Expected
		}{
			"the product":   {before + latestProduct, expecting(latestProduct, 1)},
			"the revision":  {latestProduct, bad(func(r *domain.RevisionRef) { r.RevisionID = before + r.RevisionID })},
			"the asset":     {latestProduct, bad(func(r *domain.RevisionRef) { r.AssetID = before + r.AssetID })},
			"the Workspace": {latestProduct, bad(func(r *domain.RevisionRef) { r.WorkspaceID = before + r.WorkspaceID })},
		} {
			store := &lastChecks{}
			if got, err := latestOver(t, store).Of(t.Context(), c.product, []application.Expected{c.expected}, ""); !errors.Is(err, application.ErrInvalidRevisions) ||
				len(got) != 0 || len(store.asked) != 0 {
				t.Errorf("%s, with %q before it: %+v (%v), %d questions, want it refused and nothing asked",
					name, before, got, err, len(store.asked))
			}
		}
	}
}
