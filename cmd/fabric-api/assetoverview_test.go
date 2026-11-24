package main

import (
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	checkdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// These tests run the overview of an asset through the whole slice, with the
// reads it is made of and the reads it is compared with on the same database.
//
// Two sources stand for what a check concludes today: the check of
// revisionSource cannot judge it (UNKNOWN), and that of unnamedSource fails.
// Appending a comment makes another revision of the same contract.

// variant is another revision of the contract the source states.
func variant(source string, n int) string { return fmt.Sprintf("%s# revision %d\n", source, n) }

func (w *slotsWorld) assets() string { return "/v1/products/" + w.product + "/assets" }

// overviewOf is where the item says the asset stands, or nil if it says nothing.
func overviewOf(t *testing.T, item map[string]any) map[string]any {
	t.Helper()

	raw, said := item["overview"]
	if !said {
		return nil
	}
	overview, isObject := raw.(map[string]any)
	if !isObject {
		t.Fatalf("the overview is %v, want an object", raw)
	}
	return overview
}

// after says whether the record is later than the other: by the member that
// holds a time, and for the same time by the one that holds an identifier.
func after(a, b map[string]any, when, id string) bool {
	x, _ := time.Parse(time.RFC3339Nano, a[when].(string))
	y, _ := time.Parse(time.RFC3339Nano, b[when].(string))
	if !x.Equal(y) {
		return x.After(y)
	}
	return a[id].(string) > b[id].(string)
}

// pageRead is a page of a list as it was read: the items and the token of the page that follows.
type pageRead struct {
	Items []map[string]any
	Next  string
}

// pagesRead reads the assets of the product as the subject, a page of size at
// a time, under the filter, which is the query of the list and may be empty.
func (w *slotsWorld) pagesRead(filter, subject string, size int) []pageRead {
	w.t.Helper()

	var pages []pageRead
	token := ""
	for len(pages) < 400 {
		target := fmt.Sprintf("%s?page_size=%d", w.assets(), size)
		if filter != "" {
			target += "&" + filter
		}
		if token != "" {
			target += "&page_token=" + token
		}
		items, next := w.page(target, subject)
		pages = append(pages, pageRead{items, next})
		if next == "" {
			return pages
		}
		token = next
	}
	w.t.Fatalf("%s lists the assets under %q: the list does not end", subject, filter)
	return nil
}

// walk reads the assets as pagesRead does and says which assets each page
// holds. A page that is not the last must be full, and one that follows a
// token must not be empty: a token says that more follow.
func (w *slotsWorld) walk(filter, subject string, size int) [][]string {
	w.t.Helper()

	pages := w.pagesRead(filter, subject, size)
	held := make([][]string, 0, len(pages))
	for index, page := range pages {
		held = append(held, told(page.Items, "api_asset_id"))
		if page.Next != "" && len(page.Items) != size {
			w.t.Errorf("%s reads %q in pages of %d: page %d holds %d assets and is not the last", subject, filter, size, index+1, len(page.Items))
		}
		if index > 0 && len(page.Items) == 0 {
			w.t.Errorf("%s reads %q in pages of %d: page %d is empty, though a token said more follow", subject, filter, size, index+1)
		}
	}
	return held
}

// stateFacts are the states an asset is put in, and what the overview of one must
// say: the status of its last check, and whether a release is pending.
var stateFacts = map[string]struct {
	check   string
	pending bool
}{
	"plain":           {"", false},
	"draft-unchecked": {"", false},
	"draft-unknown":   {"UNKNOWN", false},
	"draft-fail":      {"FAIL", false},
	"pending-unknown": {"UNKNOWN", true},
	"pending-fail":    {"FAIL", true},
	"released":        {"UNKNOWN", false},
}

// putInState brings the asset, which has no revision, to the state. A state
// that is approved returns the revision and the review it rests on.
func (w *slotsWorld) putInState(asset *releaseFixture, state string) (revision, review string) {
	w.t.Helper()

	until := time.Now().UTC().Add(90 * 24 * time.Hour).Truncate(time.Second)
	switch state {
	case "plain":
	case "draft-unchecked":
		w.require(w.call(http.MethodPost, asset.revisions, "author", uploadRevision("openapi.yaml", revisionSource)), http.StatusCreated, "")
	case "draft-unknown":
		asset.checked(revisionSource)
	case "draft-fail":
		asset.checked(unnamedSource)
	case "pending-unknown":
		return asset.approve(revisionSource, "", internalSandbox)
	case "pending-fail":
		return asset.approveFrom(uploadRevision("openapi.yaml", unnamedSource),
			deciding("APPROVED", "", until, waiving("F04-GOV-003", until.Add(-time.Hour))), internalSandbox)
	case "released":
		revision, review, _ = asset.released(revisionSource, "", "1.0.0")
		return revision, review
	default:
		w.t.Fatalf("there is no state %q", state)
	}
	return "", ""
}

// climbers are the members of the product who read the revisions of the
// workspace the asset of the climb is in.
var climbers = []string{"author", "coauthor", "reviewer", "publisher", "eng-reader", "eng-reviewer"}

// climb follows an asset from where it has no revision to where it was
// released more than once, and knows after every step what its overview must
// say: from what the answers to its own writes said, and not from the reads
// the overview is compared with.
type climb struct {
	w  *slotsWorld
	of *releaseFixture
	// What the writes answered, of the last revision, of the check of it that
	// concluded last, and of the release prepared last.
	revision, check, release map[string]any
	// status is the status of the last revision.
	status  string
	pending bool
}

func (c *climb) want() map[string]any {
	want := map[string]any{"release_pending": c.pending}
	if c.revision != nil {
		want["latest_revision"] = map[string]any{
			"revision_id": c.revision["revision_id"], "revision_sequence": c.revision["revision_sequence"],
			"status": c.status, "created_at": c.revision["created_at"],
		}
	}
	if c.check != nil {
		want["latest_check"] = map[string]any{
			"check_run_id": c.check["check_run_id"], "status": c.check["status"], "completed_at": c.check["completed_at"],
		}
	}
	if c.release != nil {
		want["latest_release"] = map[string]any{
			"release_id": c.release["release_id"], "revision_id": c.release["revision_id"],
			"version": c.release["version"], "prepared_at": c.release["prepared_at"],
		}
	}
	return want
}

func (c *climb) lastRevision() string {
	return c.of.revisions + "/" + c.revision["revision_id"].(string)
}

// imported imports a revision of the source as the author, and returns where
// it is read. It is the last revision, and has no check.
func (c *climb) imported(source string) string {
	c.w.t.Helper()

	got := c.w.call(http.MethodPost, c.of.revisions, "author", uploadRevision("openapi.yaml", source))
	c.w.require(got, http.StatusCreated, "")
	c.revision, c.status, c.check, c.pending = got.body, "DRAFT", nil, false
	return c.of.revisions + "/" + got.id("revision_id")
}

// checked checks the revision, which was not checked, as the author.
func (c *climb) checked(revision, status string) {
	c.w.t.Helper()

	got := c.w.call(http.MethodPost, revision+"/check-runs", "author", "")
	c.w.require(got, http.StatusCreated, "")
	run := got.body["check_run"].(map[string]any)
	if run["status"] != status {
		c.w.t.Fatalf("the check of %s concluded %v, want %s", revision, run["status"], status)
	}
	if revision == c.lastRevision() {
		c.check = run
	}
}

// asked asks for a review of the last revision on the check of today, as the
// author, and returns where the review is read. An approved revision stays
// approved.
func (c *climb) asked() string {
	c.w.t.Helper()

	revision := c.lastRevision()
	got := c.w.call(http.MethodPost, revision+"/reviews", "author", asking(c.w.checkOf(revision), internalSandbox))
	c.w.require(got, http.StatusCreated, "")
	if c.status != "APPROVED" {
		c.status = "IN_REVIEW"
	}
	return got.header.Get("Location")
}

func (c *climb) withdrawn(review string) {
	c.w.t.Helper()

	c.w.require(c.w.call(http.MethodPost, review+"/withdrawal", "author", `{"reason":"the baseline was the wrong one"}`), http.StatusOK, "")
	c.status = "WITHDRAWN"
}

// decided has the reviewer decide the review of the last revision. A revision
// that is approved has no release yet, which a release from it ends.
func (c *climb) decided(review, reviewer, body, status string) {
	c.w.t.Helper()

	c.w.require(c.w.call(http.MethodPost, review+"/decision", reviewer, body), http.StatusOK, "")
	if status != "APPROVED" || c.status != "APPROVED" {
		c.status = status
	}
	c.pending = status == "APPROVED"
}

// released prepares a release of the revision under the review, as the
// publisher. It is the release prepared last, and ends what was pending for
// the revision if it is the last.
func (c *climb) released(revision, review, version string) {
	c.w.t.Helper()

	got := c.of.prepare(revision, review, version, "publisher")
	c.w.require(got, http.StatusCreated, "")
	c.release = got.body
	if revision == c.revision["revision_id"] {
		c.pending = false
	}
}

// separately says where the asset stands from the reads of its revisions, of
// the checks of the last one and of the releases, as the author reads them:
// the last revision by its sequence, the last check by when it concluded, the
// last release by when it was prepared.
func (c *climb) separately() map[string]any {
	c.w.t.Helper()

	read := func(path string) []map[string]any { return slices.Concat(c.w.whole(path, "author", 100)...) }
	standing := map[string]any{}

	var last map[string]any
	for _, revision := range read(c.of.revisions) {
		if last == nil || revision["revision_sequence"].(float64) > last["revision_sequence"].(float64) {
			last = revision
		}
	}
	releases := read(c.of.asset + "/releases")
	var newest map[string]any
	for _, release := range releases {
		if newest == nil || after(release, newest, "prepared_at", "release_id") {
			newest = release
		}
	}
	pending := false
	if last != nil {
		id := last["revision_id"].(string)
		single := c.w.call(http.MethodGet, c.of.revisions+"/"+id, "author", "")
		c.w.require(single, http.StatusOK, "")
		standing["latest_revision"] = map[string]any{
			"revision_id": single.body["revision_id"], "revision_sequence": single.body["revision_sequence"],
			"status": single.body["status"], "created_at": single.body["created_at"],
		}
		pending = single.body["status"] == "APPROVED" &&
			!slices.ContainsFunc(releases, func(release map[string]any) bool { return release["revision_id"] == id })
		var latest map[string]any
		for _, check := range read(c.of.revisions + "/" + id + "/check-runs") {
			if latest == nil || after(check, latest, "completed_at", "check_run_id") {
				latest = check
			}
		}
		if latest != nil {
			standing["latest_check"] = map[string]any{
				"check_run_id": latest["check_run_id"], "status": latest["status"], "completed_at": latest["completed_at"],
			}
		}
	}
	if newest != nil {
		standing["latest_release"] = map[string]any{
			"release_id": newest["release_id"], "revision_id": newest["revision_id"],
			"version": newest["version"], "prepared_at": newest["prepared_at"],
		}
	}
	standing["release_pending"] = pending
	return standing
}

// kept fails the test unless the list under the filter holds the asset of the
// climb, and no other, exactly when it must.
func (c *climb) kept(when, filter string, keeps bool) {
	c.w.t.Helper()

	items, _ := c.w.page(c.w.assets()+"?"+filter, "author")
	if got := told(items, "api_asset_id"); keeps && !slices.Equal(got, []string{c.of.identifier()}) || !keeps && len(got) != 0 {
		c.w.t.Errorf("%s: the list of %s holds %v, want it to hold the asset: %v", when, filter, got, keeps)
	}
}

// now says that the overview of the asset is what the steps made it, however
// it is read: alone, in the list, from the reads it is made of, and under the
// filters.
func (c *climb) now(when string) {
	c.w.t.Helper()

	want := c.want()
	for _, reader := range climbers {
		got := c.w.call(http.MethodGet, c.of.asset, reader, "")
		if got.status != http.StatusOK || !reflect.DeepEqual(got.body["overview"], want) {
			c.w.t.Errorf("%s: %s reads the asset with the overview\n %v\nwant\n %v", when, reader, got.body["overview"], want)
		}
	}
	items, _ := c.w.page(c.w.assets(), "author")
	if len(items) != 1 || !reflect.DeepEqual(items[0]["overview"], want) {
		c.w.t.Errorf("%s: the list shows the asset with the overview\n %v\nwant\n %v", when, items, want)
	}
	if got := c.separately(); !reflect.DeepEqual(got, want) {
		c.w.t.Errorf("%s: the reads of the revisions, the checks and the releases say\n %v\nthe overview must say\n %v", when, got, want)
	}

	check := ""
	if c.check != nil {
		check, _ = c.check["status"].(string)
	}
	for _, status := range []string{"PASS", "WARN", "FAIL", "UNKNOWN"} {
		c.kept(when, "check_status="+status, check == status)
		c.kept(when, "release_pending=true&check_status="+status, c.pending && check == status)
	}
	c.kept(when, "release_pending=true", c.pending)
}

// The overview of an asset is where it stands on its way from a revision to
// a release, and it says at every step what the reads of the revisions, the
// checks and the releases of the asset say, member for member. Every step
// that moves the asset moves it: a revision imported, one that is not the
// last checked, the last one checked, a check that is later, a review asked
// and withdrawn and asked again and rejected, a revision approved, the same
// reviewed again, a release, a second release, a newer revision, a release of
// a revision that is not the last. Everything is the same whoever reads it,
// alone or in the list.
func TestWhereAnAssetStandsIsWhatTheReadsOfItsRevisionsChecksAndReleasesSay(t *testing.T) {
	w := slotsFor(t)
	c := &climb{w: w, of: w.releaseFixture}
	until := time.Now().UTC().Add(90 * 24 * time.Hour).Truncate(time.Second)
	approval := func() string { return deciding("APPROVED", "", time.Now().Add(time.Hour)) }

	c.now("before there is a revision")
	if got := c.want(); !reflect.DeepEqual(got, map[string]any{"release_pending": false}) {
		t.Fatalf("an asset without a revision stands as %v", got)
	}

	// The first revision is not the last for long, and a check of it is of no
	// concern to the last.
	first := c.imported(revisionSource)
	c.now("a revision is imported")
	if got := c.w.call(http.MethodGet, first, "author", ""); got.body["revision_sequence"] != float64(1) {
		t.Fatalf("the first revision has the sequence %v", got.body["revision_sequence"])
	}
	second := c.imported(unnamedSource)
	c.now("a second revision is imported")
	c.checked(first, "UNKNOWN")
	c.now("the first revision is checked: the last has no check")
	c.checked(second, "FAIL")
	c.now("the second revision is checked and fails")
	revision := checkdomain.RevisionRef{
		ProductID: w.product, WorkspaceID: w.workspace, AssetID: w.identifier(), RevisionID: c.revision["revision_id"].(string),
	}
	next := recordAnotherCheck(t, checkFixture{stack: w.stack, product: w.product, workspace: w.workspace}, revision, c.check["check_run_id"].(string))
	recorded := c.w.call(http.MethodGet, second+"/check-runs/"+next, "author", "")
	c.w.require(recorded, http.StatusOK, "")
	if c.check["check_run_id"] == next || !after(recorded.body["check_run"].(map[string]any), c.check, "completed_at", "check_run_id") {
		t.Fatalf("the check recorded by the newer tool is %v, want one that concluded after %v", recorded.body, c.check)
	}
	c.check = recorded.body["check_run"].(map[string]any)
	c.now("a check is recorded after it, by a newer tool: the last check is the one that concluded last")

	// A review is asked for, withdrawn, asked for again and rejected.
	review := c.asked()
	c.now("a review is asked for")
	c.withdrawn(review)
	c.now("the review is withdrawn")
	review = c.asked()
	c.now("the review is asked for again")
	c.decided(review, "reviewer", deciding("REJECTED", "not ready", time.Time{}), "REJECTED")
	c.now("the review is rejected")

	// A third revision is approved: a release is pending for it. That it is
	// reviewed again, twice, changes nothing of where it stands.
	c.imported(variant(revisionSource, 3))
	c.now("a third revision is imported")
	c.checked(c.lastRevision(), "UNKNOWN")
	c.now("the third revision is checked")
	reviewed := c.asked()
	c.now("the third revision is submitted for review")
	c.decided(reviewed, "reviewer", approval(), "APPROVED")
	c.now("the third revision is approved: a release is pending")
	again := c.asked()
	c.now("it is submitted for review again, and stays approved")
	c.decided(again, "eng-reviewer", approval(), "APPROVED")
	c.now("it is approved again")
	thrice := c.asked()
	c.decided(thrice, "reviewer", approval(), "APPROVED")
	c.now("it is approved a third time")

	// Two releases of it: the one prepared later is the last.
	approved := c.revision["revision_id"].(string)
	c.released(approved, last(reviewed), "1.0.0")
	c.now("a release is prepared of the third revision, and none is pending")
	c.released(approved, last(again), "1.0.1")
	c.now("another is prepared of it: the one prepared later is the last release")

	// A fourth revision is the last, and has no release, whatever the asset has.
	c.imported(variant(unnamedSource, 4))
	c.now("a fourth revision is imported: the asset has releases, its last revision has none")
	c.checked(c.lastRevision(), "FAIL")
	c.now("the fourth revision is checked and fails")
	waived := c.asked()
	c.decided(waived, "reviewer", deciding("APPROVED", "", until, waiving("F04-GOV-003", until.Add(-time.Hour))), "APPROVED")
	c.now("it is approved with a waiver for what failed: its check failed, and a release is pending")

	// A release of the third revision, which is approved still, is the last
	// release. The last revision has none: a release is pending still.
	c.released(approved, last(thrice), "1.0.2")
	c.now("a release is prepared of an earlier revision: none is prepared of the last")
	c.released(c.revision["revision_id"].(string), last(waived), "1.1.0")
	c.now("a release is prepared of the last revision")
}
