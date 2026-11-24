package main

import (
	"net/http"
	"reflect"
	"slices"
	"testing"
)

func TestProductOwnershipAllowsReadingAndFilteringOverviewsInEveryWorkspace(t *testing.T) {
	w := slotsFor(t)
	grant := w.securityRole("owner-maintainer", "product_owner", "owner")
	w.require(grant, http.StatusCreated, "")
	w.require(w.securityRole("owner-mixed", "product_owner", "owner"), http.StatusCreated, "")
	w.join(w.product, w.operations, "owner", "owner-maintainer", "workspace_maintainer")
	w.join(w.product, w.workspace, "owner", "owner-mixed", "internal_author")
	w.join(w.product, w.operations, "owner", "owner-mixed", "workspace_maintainer")

	engineering, operations := w.releaseFixture, w.assetIn(w.product, w.operations, "ops-api")
	w.putInState(engineering, "released")
	w.putInState(operations, "pending-unknown")
	both := []string{engineering.identifier(), operations.identifier()}
	pending := []string{operations.identifier()}

	for _, subject := range []string{"owner", "owner-maintainer", "owner-mixed"} {
		listed, next := w.page(w.assets(), subject)
		if got := told(listed, "api_asset_id"); !slices.Equal(got, both) || next != "" {
			t.Errorf("%s lists %v with token %q, want %v", subject, got, next, both)
		}
		for _, item := range listed {
			id := item["api_asset_id"].(string)
			single := w.call(http.MethodGet, w.assets()+"/"+id, subject, "")
			if overviewOf(t, item) == nil || single.status != http.StatusOK || !reflect.DeepEqual(single.body, item) {
				t.Errorf("%s reads %s as %d %v, want an overview and the same detail as the list", subject, id, single.status, single.body)
			}
		}
		for _, filter := range []struct {
			query string
			want  []string
		}{
			{"release_pending=true", pending},
			{"check_status=UNKNOWN", both},
			{"check_status=FAIL", nil},
			{"release_pending=true&check_status=UNKNOWN", pending},
		} {
			before := len(w.audit())
			items, next := w.page(w.assets()+"?"+filter.query, subject)
			if got := told(items, "api_asset_id"); !slices.Equal(got, filter.want) || next != "" {
				t.Errorf("%s asks for %s: %v with token %q, want %v", subject, filter.query, got, next, filter.want)
			}
			if after := len(w.audit()); after != before {
				t.Errorf("%s asks for %s and leaves %d audit records, want none", subject, filter.query, after-before)
			}
		}
	}

	parcel, parcelWorkspace := w.otherProduct()
	parcelAsset := w.assetIn(parcel, parcelWorkspace, "parcel-api")
	w.require(w.call(http.MethodGet, parcelAsset.asset, "owner-maintainer", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	w.require(w.call(http.MethodGet, operations.asset, "parcel-owner", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	w.require(w.call(http.MethodGet, "/v1/products/"+w.product+"/audit-records", "owner-maintainer", ""), http.StatusForbidden, "FORBIDDEN")

	filtered := w.assets() + "?check_status=UNKNOWN&page_size=1"
	caller := w.token("owner-maintainer")
	first := w.callWithHeaders(http.MethodGet, filtered, caller, "", nil)
	w.require(first, http.StatusOK, "")
	cursor := first.id("next_page_token")

	w.revoke("/v1/products/"+w.product+"/role-assignments/"+grant.id("role_assignment_id"), "owner")
	w.require(w.callWithHeaders(http.MethodGet, filtered+"&page_token="+cursor, caller, "", nil), http.StatusForbidden, "FORBIDDEN")
	got := w.callWithHeaders(http.MethodGet, operations.asset, caller, "", nil)
	w.require(got, http.StatusOK, "")
	if overviewOf(t, got.body) != nil {
		t.Errorf("a former owner with a maintainer grant reads an overview: %v", got.body)
	}
	w.require(w.callWithHeaders(http.MethodGet, operations.revisions, caller, "", nil), http.StatusForbidden, "FORBIDDEN")
}
