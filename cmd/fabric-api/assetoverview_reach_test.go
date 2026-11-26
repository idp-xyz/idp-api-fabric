package main

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"
)

// otherProduct is a second product, staffed as the first one is, with a
// workspace of its own.
func (w *slotsWorld) otherProduct() (product, workspace string) {
	w.t.Helper()

	product = w.register("platform-admin", "parcel", "parcel-owner")
	workspace = w.createWorkspace(product, "parcel-owner", "engineering")
	for subject, role := range map[string]string{
		"maintainer": "workspace_maintainer", "author": "internal_author", "reviewer": "internal_reviewer", "publisher": "internal_publisher",
	} {
		w.join(product, workspace, "parcel-owner", subject, role)
	}
	return product, workspace
}

// The overview of an asset is shown to whoever reads the revisions of the
// workspace that owns it now: owners of the product, and authors, reviewers
// and publishers of the workspace. The same caller is shown the
// overview of the assets of the workspaces where it reads and the asset
// without one of those where it does not, in the list and alone.
//
// A filter keeps what the overview says and only among what the caller reads
// the overview of. A caller that reads none anywhere is refused it, with the
// refusal of any list it may not read, and the refusal is recorded; it is not
// answered with an empty page.
func TestTheOverviewIsShownToWhoReadsTheRevisionsOfTheWorkspaceAndToNoOneElse(t *testing.T) {
	w := slotsFor(t)
	w.require(w.securityRole("auditor", "internal_auditor", "platform-admin"), http.StatusCreated, "")
	w.require(w.securityRole("security", "security_admin", "owner"), http.StatusCreated, "")
	w.join(w.product, w.workspace, "owner", "mixed", "internal_author")
	w.join(w.product, w.operations, "owner", "mixed", "internal_reader")
	w.join(w.product, w.workspace, "owner", "double", "workspace_maintainer")
	w.join(w.product, w.workspace, "owner", "double", "internal_publisher")

	// The asset of engineering was released, and the one of operations is
	// checked: a release is pending for it. Both were last checked as UNKNOWN.
	engineering, operations := w.releaseFixture, w.assetIn(w.product, w.operations, "ops-api")
	w.putInState(engineering, "released")
	w.putInState(operations, "pending-unknown")
	engID, opsID := engineering.identifier(), operations.identifier()
	both, engOnly := []string{engID, opsID}, []string{engID}

	for _, c := range []struct {
		subject  string
		sees     []string // the assets it reads, in the order of the list
		overview []string // those among them it is shown the overview of
	}{
		{"author", both, both},
		{"reviewer", both, both},
		{"publisher", both, both},
		{"eng-reader", engOnly, engOnly},
		{"eng-reviewer", engOnly, engOnly},
		// Holds the publisher grant next to the maintainer's.
		{"double", engOnly, engOnly},
		// Reads engineering evidence and the published material of operations.
		{"mixed", both, engOnly},
		{"maintainer", both, both},
		{"security", both, nil},
		{"owner", both, both},
	} {
		listed, next := w.page(w.assets(), c.subject)
		if got := told(listed, "api_asset_id"); !slices.Equal(got, c.sees) || next != "" {
			t.Errorf("%s lists the assets %v and a token %q, want %v", c.subject, got, next, c.sees)
		}
		shown := map[string]map[string]any{}
		for _, item := range listed {
			id := item["api_asset_id"].(string)
			overview := overviewOf(t, item)
			if (overview != nil) != slices.Contains(c.overview, id) {
				t.Errorf("%s lists the asset %s with the overview %v, want one for %v", c.subject, id, overview, c.overview)
			}
			if single := w.call(http.MethodGet, w.assets()+"/"+id, c.subject, ""); single.status != http.StatusOK || !reflect.DeepEqual(single.body, item) {
				t.Errorf("%s reads the asset %s as %d %v, want what the list shows: %v", c.subject, id, single.status, single.body, item)
			}
			shown[id] = item
		}
		for _, id := range both {
			if slices.Contains(c.sees, id) {
				continue
			}
			if got := w.call(http.MethodGet, w.assets()+"/"+id, c.subject, ""); got.status != http.StatusNotFound || got.code() != "API_ASSET_NOT_FOUND" {
				t.Errorf("%s reads an asset of a workspace it is no member of as %d %v, want 404 API_ASSET_NOT_FOUND", c.subject, got.status, got.body)
			}
		}

		var pending []string
		if slices.Contains(c.overview, opsID) {
			pending = []string{opsID}
		}
		for _, filter := range []struct {
			query string
			want  []string
		}{
			{"release_pending=true", pending},
			{"check_status=UNKNOWN", c.overview},
			{"check_status=FAIL", nil},
			{"check_status=PASS", nil},
			{"release_pending=true&check_status=UNKNOWN", pending},
		} {
			before := len(w.audit())
			if len(c.overview) == 0 {
				got := w.call(http.MethodGet, w.assets()+"?"+filter.query, c.subject, "")
				records := w.audit()
				refusal := audited{Subject: c.subject, Action: "asset.search", Outcome: "DENIED", Rule: "DENY-03", ProductID: w.product, ResourceType: "APIAsset"}
				if got.status != http.StatusForbidden || got.code() != "FORBIDDEN" || len(records) != before+1 || records[len(records)-1] != refusal {
					t.Errorf("%s asks for %s: %d %v with %d new audit records (the last %+v), want 403 FORBIDDEN and the one refusal %+v",
						c.subject, filter.query, got.status, got.body, len(records)-before, records[len(records)-1], refusal)
				}
				continue
			}
			items, next := w.page(w.assets()+"?"+filter.query, c.subject)
			if got := told(items, "api_asset_id"); !slices.Equal(got, filter.want) || next != "" {
				t.Errorf("%s asks for %s: %v and a token %q, want %v", c.subject, filter.query, got, next, filter.want)
			}
			for _, item := range items {
				if !reflect.DeepEqual(item, shown[item["api_asset_id"].(string)]) {
					t.Errorf("%s asks for %s and is shown %v, want the asset as the list shows it: %v", c.subject, filter.query, item, shown[item["api_asset_id"].(string)])
				}
			}
			if after := len(w.audit()); after != before {
				t.Errorf("%s asks for %s and %d audit records are made: a read that is answered leaves none", c.subject, filter.query, after-before)
			}
		}
	}

	// Whoever reads no asset of the product is answered as it was before there
	// was a filter: the filter says nothing more than the list does.
	for _, c := range []struct {
		subject, single, list string
	}{
		{"platform-admin", "API_ASSET_NOT_FOUND", "FORBIDDEN"},
		{"stranger", "PRODUCT_NOT_FOUND", "PRODUCT_NOT_FOUND"},
		{"auditor", "", ""},
	} {
		single := w.call(http.MethodGet, engineering.asset, c.subject, "")
		start := len(w.audit())
		list := w.call(http.MethodGet, w.assets(), c.subject, "")
		refusals := len(w.audit()) - start
		for _, query := range []string{"release_pending=true", "check_status=UNKNOWN", "check_status=FAIL&release_pending=true"} {
			start = len(w.audit())
			filtered := w.call(http.MethodGet, w.assets()+"?"+query, c.subject, "")
			if !sameAnswer(list, filtered) || len(w.audit())-start != refusals {
				t.Errorf("%s: the list answers %d %v with %d audit records, and with the filter %s %d %v with %d",
					c.subject, list.status, list.body, refusals, query, filtered.status, filtered.body, len(w.audit())-start)
			}
		}
		for name, got := range map[string]answer{"the asset": single, "the list": list} {
			if _, said := got.body["overview"]; said || got.status == http.StatusOK {
				t.Errorf("%s reads %s as %d %v, want it refused", c.subject, name, got.status, got.body)
			}
		}
		if c.single != "" && (single.code() != c.single || list.code() != c.list) {
			t.Errorf("%s reads the asset as %s and the list as %s, want %s and %s", c.subject, single.code(), list.code(), c.single, c.list)
		}
	}

	// Registering an asset answers with the asset, as it always did: it has no
	// overview, for whoever registers it.
	created := w.call(http.MethodPost, w.assets(), "maintainer", assetRegistration(w.workspace, "fresh", "Fresh", "HTTP"))
	w.require(created, http.StatusCreated, "")
	if _, said := created.body["overview"]; said {
		t.Errorf("registering an asset answers %v, want no overview", created.body)
	}
}

// Where an asset stands is nobody's business who does not read the revisions
// of its workspace, and nothing of it reaches the pages of such a caller:
// what happens to the assets of a workspace the caller reads no overview of
// changes none of their pages, filtered or not, nor the tokens of them. The
// asset is imported, checked and released there, and the caller
// that reads only the other workspace, or reads published material of this one, reads the same
// pages as before.
func TestWhatHappensToAnAssetInAWorkspaceTheCallerReadsNoOverviewOfChangesNoPageOfTheirs(t *testing.T) {
	w := slotsFor(t)
	w.join(w.product, w.workspace, "owner", "mixed", "internal_author")
	w.join(w.product, w.operations, "owner", "mixed", "internal_reader")
	asset := func(workspace, key, state string) (fixture *releaseFixture, revision, check string) {
		fixture = w.assetIn(w.product, workspace, key)
		revision, check = w.putInState(fixture, state)
		return fixture, revision, check
	}
	asset(w.workspace, "e1", "pending-unknown")
	asset(w.workspace, "e2", "released")
	asset(w.workspace, "e3", "released-fail")
	asset(w.workspace, "e4", "pending-fail")
	o1, revision, check := asset(w.operations, "o1", "pending-unknown")
	o2, _, _ := asset(w.operations, "o2", "plain")
	o3, _, _ := asset(w.operations, "o3", "released-fail")
	asset(w.operations, "o4", "released")
	w.join(w.product, w.workspace, "owner", "metadata-reader", "internal_reader")
	w.join(w.product, w.operations, "owner", "metadata-reader", "internal_reader")

	filters := []string{
		"", "release_pending=true", "check_status=UNKNOWN", "check_status=FAIL", "check_status=PASS",
		"release_pending=true&check_status=UNKNOWN", "release_pending=true&check_status=FAIL",
	}
	read := func() map[string][]pageRead {
		all := map[string][]pageRead{}
		for _, subject := range []string{"eng-reader", "mixed", "metadata-reader"} {
			for _, filter := range filters {
				for _, size := range []int{1, 2, 3, 100} {
					if subject == "metadata-reader" && filter != "" {
						continue
					}
					all[fmt.Sprintf("%s reads %q in pages of %d", subject, filter, size)] = w.pagesRead(filter, subject, size)
				}
			}
		}
		return all
	}
	pendingOfAuthor := func() []string {
		return slices.Concat(w.walk("release_pending=true", "author", 100)...)
	}
	before, pendingBefore := read(), pendingOfAuthor()

	// Operations changes: one asset is released, one is checked, and the
	// last revision of another is a newer one that is checked and fails.
	w.require(o1.prepare(revision, check, "1.0.0", "publisher"), http.StatusCreated, "")
	w.putInState(o2, "pending-unknown")
	o3.checkedRevision(variant(unnamedSource, 2), "")

	if pendingAfter := pendingOfAuthor(); slices.Equal(pendingAfter, pendingBefore) || len(pendingAfter) != len(pendingBefore)+1 {
		t.Fatalf("the author of both workspaces reads %v pending after the changes of operations, and read %v before: they changed nothing", pendingAfter, pendingBefore)
	}
	for key, pages := range read() {
		if !reflect.DeepEqual(pages, before[key]) {
			t.Errorf("%s: the pages changed with what happened in operations\n %v\nwere\n %v", key, pages, before[key])
		}
	}
}
