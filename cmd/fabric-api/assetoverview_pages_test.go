package main

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
)

// madeAsset is an asset a test made: where, and in which state.
type madeAsset struct{ id, workspace, state string }

// The assets a filter keeps are the assets of the list that satisfy it, and
// the pages are cut from those: the filter is applied before the page, so a
// page is as full as the filter lets it be unless it is the last, and a page
// that follows a token is never empty. The assets that satisfy it and those
// that do not, those of another workspace of the product and those of another
// product follow one another, and the list is read in pages of 1, 2, 3, 5
// and all of them: every asset that is kept is listed once, in the order of
// the list. What a caller reads in a workspace is what it reads there, and
// not what lies in the workspaces it does not.
func TestAFilteredListIsCutIntoPagesAfterItIsFiltered(t *testing.T) {
	w := slotsFor(t)
	parcel, parcelSpace := w.otherProduct()
	made := []madeAsset{{w.identifier(), "engineering", "plain"}}
	create := func(workspace, state string) {
		t.Helper()

		space := w.workspace
		if workspace == "operations" {
			space = w.operations
		}
		asset := w.assetIn(w.product, space, fmt.Sprintf("a%02d", len(made)))
		w.putInState(asset, state)
		made = append(made, madeAsset{asset.identifier(), workspace, state})
	}
	// The other product has assets that satisfy every filter, which no list
	// of this product holds.
	elsewhere := []string{"pending-unknown", "pending-fail", "released", "draft-fail"}
	for index, spec := range [][2]string{
		{"engineering", "pending-unknown"}, {"operations", "pending-unknown"}, {"engineering", "plain"}, {"engineering", "released"},
		{"operations", "draft-fail"}, {"engineering", "pending-fail"}, {"operations", "plain"}, {"engineering", "draft-unknown"},
		{"operations", "pending-fail"}, {"engineering", "pending-unknown"}, {"engineering", "draft-fail"}, {"operations", "released"},
		{"engineering", "draft-unchecked"}, {"engineering", "pending-unknown"}, {"operations", "pending-unknown"}, {"engineering", "plain"},
	} {
		create(spec[0], spec[1])
		if index%3 == 2 {
			w.putInState(w.assetIn(parcel, parcelSpace, fmt.Sprintf("p%02d", index)), elsewhere[index/3%len(elsewhere)])
		}
	}

	for _, reader := range []struct {
		subject string
		reads   []string
	}{
		{"author", []string{"engineering", "operations"}},
		{"eng-reader", []string{"engineering"}},
	} {
		for _, filter := range []struct {
			query   string
			pending bool
			status  string
		}{
			{"", false, ""},
			{"release_pending=true", true, ""},
			{"check_status=UNKNOWN", false, "UNKNOWN"},
			{"check_status=FAIL", false, "FAIL"},
			{"check_status=PASS", false, "PASS"},
			{"release_pending=true&check_status=FAIL", true, "FAIL"},
			{"release_pending=true&check_status=UNKNOWN", true, "UNKNOWN"},
		} {
			var want []string
			for _, asset := range made {
				facts := stateFacts[asset.state]
				if slices.Contains(reader.reads, asset.workspace) && (!filter.pending || facts.pending) && (filter.status == "" || facts.check == filter.status) {
					want = append(want, asset.id)
				}
			}
			if filter.query == "release_pending=true" && len(want) < 4 {
				t.Fatalf("%s: %d of the assets are made to be pending: the test does not cut the pages of a list that is long", reader.subject, len(want))
			}
			for _, size := range []int{1, 2, 3, 5, 100} {
				if got := slices.Concat(w.walk(filter.query, reader.subject, size)...); !slices.Equal(got, want) {
					t.Errorf("%s reads %q in pages of %d and is listed\n %v\nwant\n %v", reader.subject, filter.query, size, got, want)
				}
			}
		}
	}
}

// A list is read while the assets change under it: what has been listed is
// not listed again, and an asset is where the list is when it is looked at.
// An asset that stops satisfying the filter before the page that would hold it
// is not listed; one that comes to satisfy it ahead of the page is; one behind
// the page is not looked at again.
func TestAFilteredListReadWhileTheAssetsChangeListsNoAssetTwice(t *testing.T) {
	w := slotsFor(t)
	asset := func(key, state string) (fixture *releaseFixture, revision, review string) {
		fixture = w.assetIn(w.product, w.workspace, key)
		revision, review = w.putInState(fixture, state)
		return fixture, revision, review
	}
	// The asset the world starts with comes first, and has no revision.
	p1, revision1, review1 := asset("p1", "pending-unknown")
	p2, _, _ := asset("p2", "pending-unknown")
	q, _, _ := asset("q-plain", "plain")
	p3, _, _ := asset("p3", "pending-unknown")
	p4, revision4, review4 := asset("p4", "pending-unknown")
	p5, _, _ := asset("p5", "pending-unknown")

	filter := w.assets() + "?release_pending=true&page_size=2"
	first, token := w.page(filter, "author")
	if got, want := told(first, "api_asset_id"), identifiers(p1, p2); !slices.Equal(got, want) || token == "" {
		t.Fatalf("the first page holds %v and a token %q, want %v and a token", got, token, want)
	}
	listed := told(first, "api_asset_id")

	// Behind the page: one is released, one comes to be pending. Ahead of it,
	// where it was: one is released and one comes to be pending.
	w.require(p1.prepare(revision1, review1, "1.0.0", "publisher"), http.StatusCreated, "")
	w.require(p4.prepare(revision4, review4, "1.0.0", "publisher"), http.StatusCreated, "")
	w.putInState(q, "pending-unknown")
	w.putInState(w.releaseFixture, "pending-unknown")

	for token != "" {
		var items []map[string]any
		items, token = w.page(filter+"&page_token="+token, "author")
		listed = append(listed, told(items, "api_asset_id")...)
	}
	if want := identifiers(p1, p2, q, p3, p5); !slices.Equal(listed, want) {
		t.Errorf("the list read while the assets change holds\n %v\nwant\n %v", listed, want)
	}
}

// The overview is shown for as long as the grant that reads the revisions is
// held, and not a request longer: a caller that held it and holds it no more
// is shown the assets it still reads without one, is refused the filters, and
// a token it was handed carries no more than it did.
func TestTheOverviewIsShownForAsLongAsTheGrantThatReadsItIsHeld(t *testing.T) {
	w := slotsFor(t)
	w.putInState(w.releaseFixture, "released")
	w.putInState(w.assetIn(w.product, w.workspace, "second"), "pending-unknown")
	grant := w.join(w.product, w.workspace, "owner", "temp", "internal_author")
	w.join(w.product, w.workspace, "owner", "temp", "internal_reader")

	filtered := w.assets() + "?check_status=UNKNOWN&page_size=1"
	listed, filteredToken := w.page(filtered, "temp")
	plain, plainToken := w.page(w.assets()+"?page_size=1", "temp")
	if len(listed) != 1 || overviewOf(t, listed[0]) == nil || filteredToken == "" || len(plain) != 1 || overviewOf(t, plain[0]) == nil || plainToken == "" {
		t.Fatalf("a member that reads the revisions lists %v and %v with the tokens %q and %q, want one asset with an overview, and a token, each",
			listed, plain, filteredToken, plainToken)
	}
	if got := w.call(http.MethodGet, w.asset, "temp", ""); got.status != http.StatusOK || got.body["overview"] == nil {
		t.Fatalf("a member that reads the revisions reads the asset as %d %v, want it with the overview", got.status, got.body)
	}

	w.revoke(grant, "owner")

	// Its remaining membership reads published material and the asset metadata.
	if got := w.call(http.MethodGet, w.asset, "temp", ""); got.status != http.StatusOK || got.body["overview"] != nil {
		t.Errorf("the member reads the asset as %d %v, want it without an overview", got.status, got.body)
	}
	items, next := w.page(w.assets(), "temp")
	if len(items) != 2 || next != "" || overviewOf(t, items[0]) != nil || overviewOf(t, items[1]) != nil {
		t.Errorf("the member lists %v and a token %q, want the two assets and no overview", items, next)
	}
	items, next = w.page(w.assets()+"?page_size=1&page_token="+plainToken, "temp")
	if len(items) != 1 || next != "" || overviewOf(t, items[0]) != nil {
		t.Errorf("the member follows the token of the list and reads %v and a token %q, want the asset without an overview", items, next)
	}
	for name, target := range map[string]string{
		"a filter":                              w.assets() + "?release_pending=true",
		"the first page of a filtered list":     filtered,
		"the token of a list that was filtered": filtered + "&page_token=" + filteredToken,
	} {
		if got := w.call(http.MethodGet, target, "temp", ""); got.status != http.StatusForbidden || got.code() != "FORBIDDEN" {
			t.Errorf("%s: the member is answered %d %v, want 403 FORBIDDEN", name, got.status, got.body)
		}
	}
}

// A suspended product takes no new work, and it is still read: whoever read
// where its assets stand reads it as before, alone, in the list and under
// the filters.
func TestTheOverviewOfASuspendedProductIsStillRead(t *testing.T) {
	w := slotsFor(t)
	w.putInState(w.releaseFixture, "released")
	w.putInState(w.assetIn(w.product, w.workspace, "second"), "pending-unknown")

	read := func() map[string]any {
		t.Helper()

		reads := map[string]any{"asset": w.call(http.MethodGet, w.asset, "author", "").body}
		for _, filter := range []string{"", "release_pending=true", "check_status=UNKNOWN", "check_status=FAIL"} {
			reads[filter] = w.pagesRead(filter, "author", 1)
		}
		return reads
	}
	before := read()
	if overviewOf(t, before["asset"].(map[string]any)) == nil || len(before["release_pending=true"].([]pageRead)[0].Items) != 1 {
		t.Fatalf("the reads before the product is suspended are %v", before)
	}
	w.suspend("SUSPENDED")
	after := read()
	w.suspend("ACTIVE")

	for name, was := range before {
		if fmt.Sprint(after[name]) != fmt.Sprint(was) {
			t.Errorf("the read of %q changes when the product is suspended\n %v\nwas\n %v", name, after[name], was)
		}
	}
}

// A check that is not what the model recorded fails the overview as it fails
// the history of the checks of its revision: its status is not what its
// outcomes add up to. The failure is the failure of the service, whoever asks
// and however the asset is read; a caller that reads no overview is not told
// of it, and what the storage holds is read as it was once it is repaired.
func TestACheckThatIsNotWhatWasRecordedFailsTheOverviewAsItFailsTheHistory(t *testing.T) {
	w := slotsFor(t)
	asset := w.releaseFixture
	revision, _ := w.putInState(asset, "pending-unknown")
	checks := asset.revisions + "/" + revision + "/check-runs"
	recorded, _ := w.page(checks, "author")
	if len(recorded) != 1 {
		t.Fatalf("the revision has the checks %v, want one", recorded)
	}
	run := recorded[0]["check_run_id"].(string)
	good := w.call(http.MethodGet, asset.asset, "author", "")
	w.require(good, http.StatusOK, "")

	setStatus := func(status string) {
		t.Helper()

		if _, err := w.owner.Exec(t.Context(), `UPDATE policyreview.check_run SET status = $2 WHERE check_run_id = $1`, run, status); err != nil {
			t.Fatal(err)
		}
	}
	setStatus("PASS")

	history := w.call(http.MethodGet, checks, "author", "")
	if history.status != http.StatusInternalServerError || history.code() != "INTERNAL_ERROR" {
		t.Fatalf("the history of the checks answers %d %v, want a failure of the service", history.status, history.body)
	}
	for name, target := range map[string]string{
		"the asset":                              asset.asset,
		"the list":                               w.assets(),
		"the list of those that await a release": w.assets() + "?release_pending=true",
		"the list of the checks that passed":     w.assets() + "?check_status=PASS",
	} {
		if got := w.call(http.MethodGet, target, "author", ""); got.status != history.status || got.code() != history.code() {
			t.Errorf("%s: the author is answered %d %v, want what the history answers: %d %s", name, got.status, got.body, history.status, history.code())
		}
	}
	// The status that is stored is PASS now: a check that the status keeps out
	// is not read further.
	if kept, _ := w.page(w.assets()+"?check_status=UNKNOWN", "author"); len(kept) != 0 {
		t.Errorf("the list of the checks that could not judge holds %v, want none: the stored status of the check is PASS", kept)
	}
	w.require(w.call(http.MethodGet, asset.asset, "owner", ""), history.status, history.code())
	w.require(w.call(http.MethodGet, asset.asset, "maintainer", ""), history.status, history.code())
	w.join(w.product, w.workspace, "owner", "reader", "internal_reader")
	if got := w.call(http.MethodGet, asset.asset, "reader", ""); got.status != http.StatusOK || got.body["overview"] != nil {
		t.Errorf("the published reader reads the asset as %d %v, want it without an overview", got.status, got.body)
	}

	setStatus("UNKNOWN")
	if got := w.call(http.MethodGet, asset.asset, "author", ""); got.status != http.StatusOK || fmt.Sprint(got.body) != fmt.Sprint(good.body) {
		t.Errorf("once repaired the asset is read as %d %v, want %v", got.status, got.body, good.body)
	}
}
