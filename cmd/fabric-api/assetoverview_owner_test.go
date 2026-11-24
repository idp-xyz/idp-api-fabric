package main

import (
	"net/http"
	"reflect"
	"slices"
	"testing"
)

// An owner of the product that is also a member of a workspace is shown the
// overview of the assets of that workspace by what it holds there, as any
// member is, and not by owning the product. One that only maintains the
// workspace is shown none, in the list or alone, and a filter on the overview
// is refused it, with the refusal of any list it may not read, as it is to an
// owner that is a member of nothing. One that maintains a workspace and holds
// the author grant in another is shown the overview of the assets of the other
// only, and a filter keeps what that overview says and no asset of the
// workspace it maintains, though it reads the asset.
func TestAnOwnerOfTheProductIsShownTheOverviewOnlyOfTheWorkspacesWhereItsGrantsReadTheRevisions(t *testing.T) {
	w := slotsFor(t)
	w.require(w.securityRole("owner-maintainer", "product_owner", "owner"), http.StatusCreated, "")
	w.require(w.securityRole("owner-mixed", "product_owner", "owner"), http.StatusCreated, "")
	// One owner maintains operations; the other writes in engineering and
	// maintains operations.
	w.join(w.product, w.operations, "owner", "owner-maintainer", "workspace_maintainer")
	w.join(w.product, w.workspace, "owner", "owner-mixed", "internal_author")
	w.join(w.product, w.operations, "owner", "owner-mixed", "workspace_maintainer")

	// The asset of engineering was released, and the one of operations is
	// approved: a release is pending for it. Both were last checked as UNKNOWN.
	engineering, operations := w.releaseFixture, w.assetIn(w.product, w.operations, "ops-api")
	w.putInState(engineering, "released")
	w.putInState(operations, "pending-unknown")
	engID, opsID := engineering.identifier(), operations.identifier()
	both, engOnly := []string{engID, opsID}, []string{engID}

	for _, c := range []struct {
		subject  string
		overview []string // the assets it is shown the overview of
		pending  []string // what a release pending keeps for it
		unknown  []string // what a last check that concluded UNKNOWN keeps for it
	}{
		{"owner-maintainer", nil, nil, nil},
		{"owner-mixed", engOnly, nil, engOnly},
	} {
		// An owner reads every asset of the product, whatever it holds.
		listed, next := w.page(w.assets(), c.subject)
		if got := told(listed, "api_asset_id"); !slices.Equal(got, both) || next != "" {
			t.Errorf("%s lists the assets %v and a token %q, want %v", c.subject, got, next, both)
		}
		for _, item := range listed {
			id := item["api_asset_id"].(string)
			if overview := overviewOf(t, item); (overview != nil) != slices.Contains(c.overview, id) {
				t.Errorf("%s lists the asset %s with the overview %v, want one for %v", c.subject, id, overview, c.overview)
			}
			if single := w.call(http.MethodGet, w.assets()+"/"+id, c.subject, ""); single.status != http.StatusOK || !reflect.DeepEqual(single.body, item) {
				t.Errorf("%s reads the asset %s as %d %v, want what the list shows: %v", c.subject, id, single.status, single.body, item)
			}
		}

		for _, filter := range []struct {
			query string
			want  []string
		}{
			{"release_pending=true", c.pending},
			{"check_status=UNKNOWN", c.unknown},
			{"check_status=FAIL", nil},
			{"release_pending=true&check_status=UNKNOWN", c.pending},
		} {
			before := len(w.audit())
			if len(c.overview) == 0 {
				got := w.call(http.MethodGet, w.assets()+"?"+filter.query, c.subject, "")
				records := w.audit()
				refusal := audited{Subject: c.subject, Action: "asset.search", Outcome: "DENIED", Rule: "DENY-03", ProductID: w.product, ResourceType: "APIAsset"}
				if got.status != http.StatusForbidden || got.code() != "FORBIDDEN" || len(records) != before+1 || records[len(records)-1] != refusal {
					t.Errorf("%s asks for %s: %d %v with %d new audit records, want 403 FORBIDDEN and the one refusal %+v",
						c.subject, filter.query, got.status, got.body, len(records)-before, refusal)
				}
				continue
			}
			items, next := w.page(w.assets()+"?"+filter.query, c.subject)
			if got := told(items, "api_asset_id"); !slices.Equal(got, filter.want) || next != "" {
				t.Errorf("%s asks for %s: %v and a token %q, want %v", c.subject, filter.query, got, next, filter.want)
			}
			if after := len(w.audit()); after != before {
				t.Errorf("%s asks for %s and %d audit records are made: a read that is answered leaves none", c.subject, filter.query, after-before)
			}
		}
	}
}
