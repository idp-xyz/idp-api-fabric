package main

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	governance "go.idp.xyz/idp-api-fabric/internal/productgovernance/application"
	publication "go.idp.xyz/idp-api-fabric/internal/publication/application"
	publicationdomain "go.idp.xyz/idp-api-fabric/internal/publication/domain"
)

// slotsWorld is a product of two workspaces, engineering and operations, and
// the people of each: those of publicationFor, who read and publish in both,
// a reader of each workspace, who reads in that one only, and a reviewer of
// engineering.
type slotsWorld struct {
	*releaseFixture
	operations string
}

func slotsFor(t *testing.T) *slotsWorld {
	t.Helper()

	s := publicationFor(t)
	operations := s.createWorkspace(s.product, "owner", "operations")
	for subject, role := range map[string]string{
		"maintainer": "workspace_maintainer", "author": "internal_author", "reviewer": "internal_reviewer",
		"publisher": "internal_publisher", "ops-reader": "internal_author",
	} {
		s.join(s.product, operations, "owner", subject, role)
	}
	s.join(s.product, s.workspace, "owner", "eng-reader", "internal_author")
	s.join(s.product, s.workspace, "owner", "eng-reviewer", "internal_reviewer")
	return &slotsWorld{releaseFixture: s, operations: operations}
}

// assetIn registers an asset of the workspace of the product, as its
// maintainer, and returns the fixture that works on that asset.
func (s *stack) assetIn(product, workspace, key string) *releaseFixture {
	s.t.Helper()

	revisions := s.revisionAsset(product, workspace, "maintainer", key, "HTTP")
	return &releaseFixture{
		reviewFixture: &reviewFixture{stack: s, product: product, workspace: workspace, revisions: revisions},
		asset:         strings.TrimSuffix(revisions, "/revisions"),
	}
}

// publishing is what publishing an asset on a target made: where its slot is
// read, and the revision, the check and the release the slot rests on.
type publishing struct{ slot, revision, check, release string }

// publishedOn checks a revision of the asset, releases it and puts the
// release on the slot of the asset on the target, which is made with that.
func (s *releaseFixture) publishedOn(target string) publishing {
	s.t.Helper()

	revision, check, release := s.released(revisionSource, "", "1.0.0")
	slot := assetSlot(target, s.asset)
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, release, "", "first"), http.StatusOK, "")
	return publishing{slot, revision, check, release}
}

// identifier is the identifier of the asset of the fixture.
func (s *releaseFixture) identifier() string { return last(s.asset) }

// identifiers are those of the assets.
func identifiers(fixtures ...*releaseFixture) []string {
	listed := make([]string, 0, len(fixtures))
	for _, fixture := range fixtures {
		listed = append(listed, fixture.identifier())
	}
	return listed
}

// listed are the slots of the pages of a list, in the order they were read.
func listed(pages [][]map[string]any) []map[string]any { return slices.Concat(pages...) }

// slotAmong is the slot of the asset among those of a list.
func slotAmong(slots []map[string]any, asset string) map[string]any {
	for _, slot := range slots {
		if slot["api_asset_id"] == asset {
			return slot
		}
	}
	return nil
}

// pagesOf reads the list as the subject, a page of size slots at a time, and
// says which assets each page holds. It fails the test if a page that is not
// the last is not full.
func (s *stack) pagesOf(path, subject string, size int) [][]string {
	s.t.Helper()

	pages := s.whole(path, subject, size)
	held := make([][]string, 0, len(pages))
	for index, page := range pages {
		held = append(held, told(page, "api_asset_id"))
		if index < len(pages)-1 && len(page) != size {
			s.t.Errorf("%s reads %s in pages of %d: page %d holds %d slots and is not the last", subject, path, size, index+1, len(page))
		}
	}
	return held
}

// DC-020: a target holds the slots of several assets, each with a pointer and
// a generation of its own, and whoever reads it lists those of the workspaces
// where it reads. The slots of two workspaces are made in turn, so that a page
// that was cut from the slots of both and then lost those of the other
// workspace would not be full: a page of a reader holds as many slots as it
// asks for, and none of the other workspace's.
func TestTheSlotsOfATargetAreThoseOfTheWorkspacesTheCallerReads(t *testing.T) {
	w := slotsFor(t)
	target, _ := w.target(internalSandbox, "engineering")
	listing := target + "/assets"

	var engineering, operations []*releaseFixture
	for _, key := range []string{"e1", "e2", "e3"} {
		engineering = append(engineering, w.assetIn(w.product, w.workspace, key))
	}
	for _, key := range []string{"o1", "o2", "o3"} {
		operations = append(operations, w.assetIn(w.product, w.operations, key))
	}
	made := map[*releaseFixture]publishing{}
	for index := range engineering {
		made[engineering[index]] = engineering[index].publishedOn(target)
		made[operations[index]] = operations[index].publishedOn(target)
	}
	e1, e2, e3 := engineering[0], engineering[1], engineering[2]
	o1, o2, o3 := operations[0], operations[1], operations[2]

	for _, c := range []struct {
		subject string
		size    int
		pages   [][]string
	}{
		{"eng-reader", 2, [][]string{identifiers(e1, e2), identifiers(e3)}},
		{"eng-reader", 1, [][]string{identifiers(e1), identifiers(e2), identifiers(e3)}},
		{"eng-reader", 3, [][]string{identifiers(e1, e2, e3)}},
		{"ops-reader", 2, [][]string{identifiers(o1, o2), identifiers(o3)}},
		// A reviewer reads what it is to decide on, as an author does.
		{"eng-reviewer", 2, [][]string{identifiers(e1, e2), identifiers(e3)}},
		{"reviewer", 4, [][]string{identifiers(e1, o1, e2, o2), identifiers(e3, o3)}},
		{"publisher", 2, [][]string{identifiers(e1, o1), identifiers(e2, o2), identifiers(e3, o3)}},
		{"publisher", 4, [][]string{identifiers(e1, o1, e2, o2), identifiers(e3, o3)}},
		{"publisher", 100, [][]string{identifiers(e1, o1, e2, o2, e3, o3)}},
		{"author", 5, [][]string{identifiers(e1, o1, e2, o2, e3), identifiers(o3)}},
	} {
		if got := w.pagesOf(listing, c.subject, c.size); !reflect.DeepEqual(got, c.pages) {
			t.Errorf("%s reads the slots in pages of %d as\n %v\nwant\n %v", c.subject, c.size, got, c.pages)
		}
	}
	// The last page of a reader is full and hands out no token, though more
	// slots lie beyond it that it does not read.
	if items, next := w.page(listing+"?page_size=3", "eng-reader"); len(items) != 3 || next != "" {
		t.Errorf("a reader of three slots, in a page of three, reads %d and is handed the token %q", len(items), next)
	}

	// Each slot of the target has its own pointer and generation: another
	// release for the first asset of engineering changes that slot and no other.
	before := listed(w.whole(listing, "publisher", 100))
	second := e1.prepare(made[e1].revision, made[e1].check, "1.0.1", "publisher")
	w.require(second, http.StatusCreated, "")
	w.require(w.change(made[e1].slot, "publisher", "ACTIVATE", 1, second.id("release_id"), "", "replace"), http.StatusOK, "")
	after := listed(w.whole(listing, "publisher", 100))
	if !reflect.DeepEqual(told(after, "api_asset_id"), told(before, "api_asset_id")) {
		t.Fatalf("the replacement moved the slots: %v, were %v", told(after, "api_asset_id"), told(before, "api_asset_id"))
	}
	for index, slot := range after {
		if index == 0 {
			if slot["active_release_id"] != second.id("release_id") || slot["generation"] != float64(2) {
				t.Errorf("the slot that was replaced reads %v, want the new release at generation 2", slot)
			}
			continue
		}
		if !reflect.DeepEqual(slot, before[index]) {
			t.Errorf("the slot %d reads %v, was %v: it was none of those replaced", index, slot, before[index])
		}
	}
}

// A slot says what stands between its release and whoever asks for the
// artifact, and says it to whoever reads it, alone and in every list it is
// in: an access block of the target, of the asset or of the release, a slot
// withdrawn. A release on a check the tools have moved on from has nothing
// standing in its way. What a list says of a slot is what the read of that
// slot says, member for member.
func TestAListedSlotIsTheSlotAsItIsRead(t *testing.T) {
	w := slotsFor(t)
	first, configPath := w.target(internalSandbox, "engineering")
	preview := w.call(http.MethodPost, "/v1/products/"+w.product+"/publication-targets", "owner", jsonBody(map[string]string{
		"audience_config_id": last(configPath), "portal_key": "preview",
	}))
	w.require(preview, http.StatusCreated, "")
	second := preview.header.Get("Location")
	w.require(w.securityRole("security", "security_admin", "owner"), http.StatusCreated, "")

	asset := func(key string) *releaseFixture { return w.assetIn(w.product, w.workspace, key) }
	plain, assetBlocked, releaseBlocked := asset("plain"), asset("asset-blocked"), asset("release-blocked")
	earlier, withdrawn := asset("earlier"), asset("withdrawn")

	// The first asset is on both targets, and the second target is blocked
	// as a whole.
	onFirst := plain.publishedOn(first)
	onSecond := assetSlot(second, plain.asset)
	w.require(plain.change(onSecond, "publisher", "ACTIVATE", 0, onFirst.release, "", "second"), http.StatusOK, "")
	blockedAsset, blockedRelease := assetBlocked.publishedOn(first), releaseBlocked.publishedOn(first)
	// One rests on a check the tools have moved on from since.
	revision, check, current := earlier.released(revisionSource, "", "1.0.0")
	later := recordAnotherCheck(t, checkFixture{stack: w.stack}, checkRef(earlier, revision), check)
	earlierSlot := assetSlot(first, earlier.asset)
	w.require(earlier.change(earlierSlot, "publisher", "ACTIVATE", 0, earlier.preparedOn(current, later, "0.9.0"), "", "earlier"), http.StatusOK, "")
	taken := withdrawn.publishedOn(first)
	w.require(withdrawn.change(taken.slot, "publisher", "WITHDRAW", 1, "", "taken down", "down"), http.StatusOK, "")

	block := func(target string, body map[string]string) {
		t.Helper()
		w.require(w.call(http.MethodPost, target+"/access-blocks", "security", jsonBody(body)), http.StatusCreated, "")
	}
	block(second, map[string]string{"reason": "investigate the preview"})
	block(first, map[string]string{"api_asset_id": assetBlocked.identifier(), "reason": "investigate the asset"})
	block(first, map[string]string{"api_asset_id": releaseBlocked.identifier(), "release_id": blockedRelease.release, "reason": "investigate the release"})

	states := []struct {
		name, slot string
		visibility string
		blocked    bool
	}{
		{"a release that is served", onFirst.slot, "ACTIVE", false},
		{"the same release on a target that is blocked as a whole", onSecond, "ACTIVE", true},
		{"an asset that is blocked", blockedAsset.slot, "ACTIVE", true},
		{"a release that is blocked", blockedRelease.slot, "ACTIVE", true},
		{"a release on a check the tools have moved on from", earlierSlot, "ACTIVE", false},
		{"a slot that was withdrawn", taken.slot, "WITHDRAWN", false},
	}
	assets := "/v1/products/" + w.product + "/assets/"
	for _, reader := range []string{"publisher", "eng-reader"} {
		onFirstTarget, onPreview := listed(w.whole(first+"/assets", reader, 100)), listed(w.whole(second+"/assets", reader, 100))
		if len(onFirstTarget) != 5 || len(onPreview) != 1 {
			t.Fatalf("%s lists %d slots of the first target and %d of the second, want 5 and 1", reader, len(onFirstTarget), len(onPreview))
		}
		for _, state := range states {
			single := w.call(http.MethodGet, state.slot, reader, "")
			if single.status != http.StatusOK || single.body["visibility"] != state.visibility || single.body["blocked"] != state.blocked {
				t.Fatalf("%s reads %s as %d %v, want %s, blocked %t", reader, state.name, single.status, single.body,
					state.visibility, state.blocked)
			}
			assetID, targetID := single.id("api_asset_id"), single.id("publication_target_id")
			inTarget := slotAmong(onFirstTarget, assetID)
			if strings.HasPrefix(state.slot, second) {
				inTarget = slotAmong(onPreview, assetID)
			}
			var inAsset map[string]any
			for _, slot := range listed(w.whole(assets+assetID+"/publications", reader, 100)) {
				if slot["publication_target_id"] == targetID {
					inAsset = slot
				}
			}
			if !reflect.DeepEqual(inTarget, single.body) || !reflect.DeepEqual(inAsset, single.body) {
				t.Errorf("%s: %s is read as\n %v\nlisted among the slots of its target as\n %v\nand among the publications of its asset as\n %v",
					reader, state.name, single.body, inTarget, inAsset)
			}
		}
	}

	// The publications of the asset on both targets: the order is that of
	// the first operation of each slot.
	got := told(listed(w.whole(plain.asset+"/publications", "publisher", 100)), "publication_target_id")
	if want := []string{last(first), last(second)}; !reflect.DeepEqual(got, want) {
		t.Errorf("the publications of the asset are on %v, want the first target and then the second: %v", got, want)
	}
}

// A list is read oldest first by the first operation of a slot, whatever is
// done to a slot afterwards and whatever size the pages are. Every page but the
// last is full, and the last one hands out no token, also when it is full.
// What is published while a list is read comes at its end, once.
func TestTheSlotsAreListedOldestFirstAndTheListIsReadToItsEnd(t *testing.T) {
	w := slotsFor(t)
	target, _ := w.target(internalSandbox, "engineering")
	listing := target + "/assets"

	var assets []*releaseFixture
	made := map[*releaseFixture]publishing{}
	for _, key := range []string{"p1", "p2", "p3", "p4", "p5"} {
		asset := w.assetIn(w.product, w.workspace, key)
		made[asset] = asset.publishedOn(target)
		assets = append(assets, asset)
	}
	want := identifiers(assets...)

	read := func(when string) {
		t.Helper()
		for _, size := range []int{1, 2, 3, 5, 100} {
			var got []string
			for _, page := range w.pagesOf(listing, "publisher", size) {
				got = append(got, page...)
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s, in pages of %d: %v, want %v", when, size, got, want)
			}
		}
	}
	read("as published")

	// A slot that is withdrawn and returned to its release is where it was.
	oldest := made[assets[0]]
	w.require(w.change(oldest.slot, "publisher", "WITHDRAW", 1, "", "taken down", "down"), http.StatusOK, "")
	w.require(w.change(oldest.slot, "publisher", "RESTORE", 2, oldest.release, "back again", "up"), http.StatusOK, "")
	if got := slotAmong(listed(w.whole(listing, "publisher", 100)), assets[0].identifier()); got["generation"] != float64(3) {
		t.Fatalf("the slot that was withdrawn and returned reads %v, want generation 3", got)
	}
	read("after the oldest was withdrawn and returned")

	// A slot made while the list is read is read once, at its end.
	items, next := w.page(listing+"?page_size=2", "publisher")
	if !slices.Equal(told(items, "api_asset_id"), want[:2]) || next == "" {
		t.Fatalf("the first page holds %v and the token %q, want two slots and a token", told(items, "api_asset_id"), next)
	}
	late := w.assetIn(w.product, w.workspace, "late")
	late.publishedOn(target)
	var rest []string
	for next != "" {
		items, next = w.page(listing+"?page_size=2&page_token="+next, "publisher")
		rest = append(rest, told(items, "api_asset_id")...)
	}
	if wantRest := append(slices.Clone(want[2:]), late.identifier()); !slices.Equal(rest, wantRest) {
		t.Errorf("the rest of the list holds %v, want %v", rest, wantRest)
	}
}

// Whoever reads the slots of no workspace of the product is told so, and
// whoever may not know the product is told it does not exist. A reader of
// one workspace is told nothing of the slots of another, and every refusal
// is on record as one, under the action of the read and the rule of refusals.
// A list that is answered leaves no record and no event.
func TestWhoReadsNoSlotsIsListedNone(t *testing.T) {
	w := slotsFor(t)
	w.require(w.securityRole("auditor", "internal_auditor", "platform-admin"), http.StatusCreated, "")
	target, _ := w.target(internalSandbox, "engineering")
	listing := target + "/assets"
	engineering, operations := w.assetIn(w.product, w.workspace, "e1"), w.assetIn(w.product, w.operations, "o1")
	engineering.publishedOn(target)
	operations.publishedOn(target)
	publications := func(asset *releaseFixture) string {
		return "/v1/products/" + w.product + "/assets/" + asset.identifier() + "/publications"
	}

	refused := func(subject, path string, status int, code string) {
		t.Helper()
		before := w.on(subject, "publication.read", "DENY-03", false)
		got := w.call(http.MethodGet, path, subject, "")
		if got.status != status || got.code() != code || got.body["items"] != nil {
			t.Errorf("%s lists %s: %d %v, want %d %s and no slot", subject, path, got.status, got.body, status, code)
		}
		if after := w.on(subject, "publication.read", "DENY-03", false); after != before+1 {
			t.Errorf("%s lists %s: %d refusals are on record, want one more than %d", subject, path, after, before)
		}
		// The refusal is on record for the kind of resource a slot is, as the
		// refusal to read one is, and for no resource of its own: a list
		// names none.
		for record := range w.recorded(func(row audited) bool {
			return row.Subject == subject && row.Action == "publication.read" && row.Rule == "DENY-03" && row.Outcome != "SUCCEEDED"
		}) {
			if record.ResourceType != "PublicationSlot" || record.ResourceID != "" {
				t.Errorf("%s lists %s: the refusal is on record for the resource %q %q, want PublicationSlot and no identifier",
					subject, path, record.ResourceType, record.ResourceID)
			}
		}
	}
	for _, c := range []struct {
		subject     string
		slotsStatus int
		slotsCode   string
		assetStatus int
		assetCode   string
	}{
		// A product auditor holds the separate audit grant.
		{"auditor", http.StatusForbidden, "FORBIDDEN", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		// An administrator of the platform knows the product and reads none
		// of its assets: of one asset it is told what the read of the slot
		// tells it, and of the slots of a target that it reads none.
		{"platform-admin", http.StatusForbidden, "FORBIDDEN", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		{"stranger", http.StatusNotFound, "PRODUCT_NOT_FOUND", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	} {
		refused(c.subject, listing, c.slotsStatus, c.slotsCode)
		refused(c.subject, publications(engineering), c.assetStatus, c.assetCode)
		if single := w.call(http.MethodGet, assetSlot(target, engineering.asset), c.subject, ""); single.status != c.assetStatus || single.code() != c.assetCode {
			t.Errorf("%s reads the slot as %d %v, but is told %d %s of its asset", c.subject, single.status, single.body, c.assetStatus, c.assetCode)
		}
	}

	// A reader of one workspace lists the slots of that workspace, and the
	// publications of its assets. A list that is answered leaves no record
	// and no event.
	records, events := w.count("platformoperations.audit_record"), w.count("bento.outbox")
	if got := told(listed(w.whole(listing, "owner", 100)), "api_asset_id"); !slices.Equal(got, []string{engineering.identifier(), operations.identifier()}) {
		t.Errorf("the owner lists %v, want the slots of both workspaces", got)
	}
	w.require(w.call(http.MethodGet, publications(operations), "owner", ""), http.StatusOK, "")
	for _, c := range []struct {
		subject string
		own     *releaseFixture
	}{{"eng-reader", engineering}, {"ops-reader", operations}, {"eng-reviewer", engineering}} {
		if got := told(listed(w.whole(listing, c.subject, 100)), "api_asset_id"); !slices.Equal(got, []string{c.own.identifier()}) {
			t.Errorf("%s lists the slots %v, want only those of its workspace: %s", c.subject, got, c.own.identifier())
		}
		if got := w.call(http.MethodGet, publications(c.own), c.subject, ""); got.status != http.StatusOK || len(got.body["items"].([]any)) != 1 {
			t.Errorf("%s lists the publications of its own asset: %d %v", c.subject, got.status, got.body)
		}
	}
	if after, sent := w.count("platformoperations.audit_record"), w.count("bento.outbox"); after != records || sent != events {
		t.Errorf("the lists that were answered left %d audit records and %d events, want none", after-records, sent-events)
	}

	// A reader of a workspace that owns no asset reads no slot, and is
	// answered with a list that is empty: not refused, and not shown the
	// slots of the workspaces it does not read.
	empty := w.createWorkspace(w.product, "owner", "empty")
	w.join(w.product, empty, "owner", "empty-reader", "internal_author")
	records, events = w.count("platformoperations.audit_record"), w.count("bento.outbox")
	if items, next := w.page(listing, "empty-reader"); len(items) != 0 || next != "" {
		t.Errorf("a reader of a workspace without assets lists %v and the token %q, want an empty list", items, next)
	}
	if items, next := w.page(publications(engineering), "publisher"); len(items) != 1 || next != "" {
		t.Errorf("a reader of both workspaces lists %v and the token %q of the publications of an asset, want its one slot", items, next)
	}
	if after, sent := w.count("platformoperations.audit_record"), w.count("bento.outbox"); after != records || sent != events {
		t.Errorf("the lists that were answered left %d audit records and %d events, want none", after-records, sent-events)
	}

	// Of the asset of the other workspace a reader is told what the read of
	// its slot tells it.
	refused("eng-reader", publications(operations), http.StatusNotFound, "API_ASSET_NOT_FOUND")
	refused("ops-reader", publications(engineering), http.StatusNotFound, "API_ASSET_NOT_FOUND")
	// Whether a target is there is told to whoever reads, and to no one else.
	missing := "/v1/products/" + w.product + "/publication-targets/" + ids.NewUUID() + "/assets"
	refused("eng-reader", missing, http.StatusNotFound, "PUBLICATION_TARGET_NOT_FOUND")
	refused("owner", missing, http.StatusNotFound, "PUBLICATION_TARGET_NOT_FOUND")
	refused("stranger", missing, http.StatusNotFound, "PRODUCT_NOT_FOUND")
	// A product that is not there is not there for anyone, an administrator
	// included.
	absent := "/v1/products/" + ids.NewUUID() + "/publication-targets/" + last(target) + "/assets"
	for _, subject := range []string{"platform-admin", "eng-reader", "owner"} {
		refused(subject, absent, http.StatusNotFound, "PRODUCT_NOT_FOUND")
	}
}

// What another product has is no more to be found through this one than what
// is not there: a target or an asset of another product is answered as one
// that does not exist, to whoever reads here, and the slots of the other
// product are in none of the lists of this one.
func TestAnotherProductsTargetOrAssetIsAsMissingAsOneThatIsNot(t *testing.T) {
	w := slotsFor(t)
	target, _ := w.target(internalSandbox, "engineering")
	mine := w.assetIn(w.product, w.workspace, "mine")
	mine.publishedOn(target)

	parcel := w.register("platform-admin", "parcel", "parcel-owner")
	workspace := w.createWorkspace(parcel, "parcel-owner", "engineering")
	for subject, role := range map[string]string{
		"maintainer": "workspace_maintainer", "author": "internal_author", "reviewer": "internal_reviewer",
		"publisher": "internal_publisher", "parcel-reader": "internal_author",
	} {
		w.join(parcel, workspace, "parcel-owner", subject, role)
	}
	config := w.call(http.MethodPost, "/v1/products/"+parcel+"/audience-configs", "parcel-owner", jsonBody(map[string]string{
		"audience": "internal", "environment": "sandbox",
	}))
	w.require(config, http.StatusCreated, "")
	theirs := w.call(http.MethodPost, "/v1/products/"+parcel+"/publication-targets", "parcel-owner", jsonBody(map[string]string{
		"audience_config_id": config.id("audience_config_id"), "portal_key": "engineering",
	}))
	w.require(theirs, http.StatusCreated, "")
	parcelTarget := theirs.header.Get("Location")
	asset := w.assetIn(parcel, workspace, "parcel-api")
	asset.publishedOn(parcelTarget)

	// The identifiers of the other product, asked of this one, are the
	// identifiers of what is not there.
	products := "/v1/products/"
	for _, c := range []struct {
		name, subject, withheld, absent string
		code                            string
	}{
		{"a target of the other product", "author", products + w.product + "/publication-targets/" + last(parcelTarget) + "/assets",
			products + w.product + "/publication-targets/" + ids.NewUUID() + "/assets", "PUBLICATION_TARGET_NOT_FOUND"},
		{"an asset of the other product", "author", products + w.product + "/assets/" + asset.identifier() + "/publications",
			products + w.product + "/assets/" + ids.NewUUID() + "/publications", "API_ASSET_NOT_FOUND"},
		{"a target of this product, asked of the other", "parcel-reader", products + parcel + "/publication-targets/" + last(target) + "/assets",
			products + parcel + "/publication-targets/" + ids.NewUUID() + "/assets", "PUBLICATION_TARGET_NOT_FOUND"},
		{"an asset of this product, asked of the other", "parcel-reader", products + parcel + "/assets/" + mine.identifier() + "/publications",
			products + parcel + "/assets/" + ids.NewUUID() + "/publications", "API_ASSET_NOT_FOUND"},
	} {
		withheld, absent := w.call(http.MethodGet, c.withheld, c.subject, ""), w.call(http.MethodGet, c.absent, c.subject, "")
		if withheld.status != http.StatusNotFound || withheld.code() != c.code || !sameAnswer(withheld, absent) {
			t.Errorf("%s: %d %v differs from what is answered of one that does not exist: %d %v, want 404 %s",
				c.name, withheld.status, withheld.body, absent.status, absent.body, c.code)
		}
	}
	// Who does not know the other product is told that it is not there, and
	// who does know it reads what is there.
	for _, path := range []string{
		products + parcel + "/publication-targets/" + last(parcelTarget) + "/assets",
		products + parcel + "/assets/" + asset.identifier() + "/publications",
	} {
		w.require(w.call(http.MethodGet, path, "author", ""), http.StatusOK, "")
		w.require(w.call(http.MethodGet, path, "eng-reader", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	}

	// Each product lists its own slots and no others.
	if got := told(listed(w.whole(target+"/assets", "author", 100)), "api_asset_id"); !slices.Equal(got, []string{mine.identifier()}) {
		t.Errorf("the slots of the target are %v, want only those of the asset of this product: %s", got, mine.identifier())
	}
	if got := told(listed(w.whole(parcelTarget+"/assets", "parcel-reader", 100)), "api_asset_id"); !slices.Equal(got, []string{asset.identifier()}) {
		t.Errorf("the slots of the target of the other product are %v, want only its own: %s", got, asset.identifier())
	}
}

// DC-007: an asset that was published is found from the asset, on the targets
// it was published to, and from each slot on to the release and the revision
// it rests on. A slot that was withdrawn is still one of the publications of
// the asset, and what is done to a slot does not move it in the list.
func TestAnAssetIsListedOnTheTargetsItWasPublishedTo(t *testing.T) {
	w := slotsFor(t)
	first, configPath := w.target(internalSandbox, "engineering")
	preview := w.call(http.MethodPost, "/v1/products/"+w.product+"/publication-targets", "owner", jsonBody(map[string]string{
		"audience_config_id": last(configPath), "portal_key": "preview",
	}))
	w.require(preview, http.StatusCreated, "")
	second := preview.header.Get("Location")
	shipped, unshipped := w.assetIn(w.product, w.workspace, "shipped"), w.assetIn(w.product, w.workspace, "unshipped")

	onFirst := shipped.publishedOn(first)
	onSecond := assetSlot(second, shipped.asset)
	w.require(shipped.change(onSecond, "publisher", "ACTIVATE", 0, onFirst.release, "", "second"), http.StatusOK, "")
	w.require(shipped.change(onFirst.slot, "publisher", "WITHDRAW", 1, "", "taken down", "down"), http.StatusOK, "")

	listing := shipped.asset + "/publications"
	got, token := w.page(listing, "author")
	if token != "" || len(got) != 2 {
		t.Fatalf("the publications of the asset are %v with the token %q, want two and no token", got, token)
	}
	// Oldest first by the first operation of the slot: the first target was
	// published to before the second, though it was changed after it.
	if got[0]["publication_target_id"] != last(first) || got[0]["visibility"] != "WITHDRAWN" || got[0]["generation"] != float64(2) ||
		got[0]["active_release_id"] != nil || got[0]["blocked"] != false {
		t.Errorf("the first publication reads %v, want the slot on the first target, withdrawn at its second generation", got[0])
	}
	if got[1]["publication_target_id"] != last(second) || got[1]["visibility"] != "ACTIVE" || got[1]["generation"] != float64(1) ||
		got[1]["active_release_id"] != onFirst.release || got[1]["slot_id"] == got[0]["slot_id"] {
		t.Errorf("the second publication reads %v, want the slot on the second target, active at its first generation", got[1])
	}
	for index, slot := range []string{onFirst.slot, onSecond} {
		if single := w.call(http.MethodGet, slot, "author", ""); single.status != http.StatusOK || !reflect.DeepEqual(single.body, got[index]) {
			t.Errorf("publication %d is %v, the slot read alone is %d %v", index, got[index], single.status, single.body)
		}
	}
	for size, pages := range map[int]int{1: 2, 2: 1} {
		read := w.whole(listing, "publisher", size)
		if targets := told(listed(read), "publication_target_id"); len(read) != pages || !slices.Equal(targets, []string{last(first), last(second)}) {
			t.Errorf("in pages of %d the publications are on %v in %d pages, want the two targets in %d", size, targets, len(read), pages)
		}
	}

	// From the slot to what it rests on: the release it points at, and the
	// revision of the release.
	release := w.call(http.MethodGet, shipped.asset+"/releases/"+got[1]["active_release_id"].(string), "publisher", "")
	if release.status != http.StatusOK || release.id("revision_id") != onFirst.revision || release.id("check_run_id") != onFirst.check {
		t.Fatalf("the release of the slot reads %d %v, want the revision %s and the check %s", release.status, release.body, onFirst.revision, onFirst.check)
	}
	if revision := w.call(http.MethodGet, shipped.asset+"/revisions/"+release.id("revision_id"), "publisher", ""); revision.status != http.StatusOK {
		t.Errorf("the revision of the release reads %d %v", revision.status, revision.body)
	}

	// An asset published nowhere has none.
	if items, token := w.page(unshipped.asset+"/publications", "author"); len(items) != 0 || token != "" {
		t.Errorf("an asset that was never published lists %v and the token %q, want an empty list", items, token)
	}
}

// A token names a position and grants nothing. What a caller may read is
// decided with every page, from what it holds then: a token another caller
// was given opens nothing of what that caller reads, one that is of another
// list is no token, and a caller whose grant was revoked since the page before
// is a stranger to the next one.
func TestAPageTokenOfASlotListGrantsNothing(t *testing.T) {
	w := slotsFor(t)
	w.require(w.securityRole("auditor", "internal_auditor", "platform-admin"), http.StatusCreated, "")
	target, configPath := w.target(internalSandbox, "engineering")
	listing := target + "/assets"
	var engineering []*releaseFixture
	for _, key := range []string{"e1", "e2", "e3"} {
		asset := w.assetIn(w.product, w.workspace, key)
		asset.publishedOn(target)
		engineering = append(engineering, asset)
	}
	operations := w.assetIn(w.product, w.operations, "o1")
	operations.publishedOn(target)

	items, token := w.page(listing+"?page_size=1", "eng-reader")
	if len(items) != 1 || token == "" {
		t.Fatalf("the first page holds %v and the token %q", items, token)
	}
	// The token of a reader of engineering in the hands of others.
	w.require(w.call(http.MethodGet, listing+"?page_size=1&page_token="+token, "auditor", ""), http.StatusForbidden, "FORBIDDEN")
	w.require(w.call(http.MethodGet, listing+"?page_size=1&page_token="+token, "stranger", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	if got, _ := w.page(listing+"?page_size=100&page_token="+token, "ops-reader"); !slices.Equal(told(got, "api_asset_id"), []string{operations.identifier()}) {
		t.Errorf("a reader of operations, with the token of a reader of engineering, lists %v, want the slots it reads", told(got, "api_asset_id"))
	}
	// A token of one list is none of another.
	other := w.call(http.MethodPost, "/v1/products/"+w.product+"/publication-targets", "owner", jsonBody(map[string]string{
		"audience_config_id": last(configPath), "portal_key": "preview",
	}))
	w.require(other, http.StatusCreated, "")
	w.require(w.call(http.MethodGet, other.header.Get("Location")+"/assets?page_token="+token, "eng-reader", ""), http.StatusBadRequest, "PAGE_TOKEN_INVALID")
	w.require(w.call(http.MethodGet, engineering[0].asset+"/publications?page_token="+token, "eng-reader", ""), http.StatusBadRequest, "PAGE_TOKEN_INVALID")

	// The grant is revoked: the next page is the answer to a stranger.
	var membership string
	if err := w.owner.QueryRow(t.Context(), `SELECT membership_id::text FROM productgovernance.workspace_membership
WHERE product_id = $1 AND principal_subject = 'eng-reader' AND role = 'internal_author' AND revoked_at IS NULL`, w.product).Scan(&membership); err != nil {
		t.Fatal(err)
	}
	w.revoke("/v1/products/"+w.product+"/workspaces/"+w.workspace+"/memberships/"+membership, "owner")
	w.require(w.call(http.MethodGet, listing+"?page_size=1&page_token="+token, "eng-reader", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
}

// A list of the slots of a target locks nothing: it is no point where an
// access block has to be ordered with what is served, as the read of the
// artifact is, and a security administrator who is placing a block does not
// make a page wait. The read of one slot does hold the target and waits.
func TestAListOfSlotsDoesNotWaitForABlockThatIsBeingPlaced(t *testing.T) {
	w := slotsFor(t)
	target, _ := w.target(internalSandbox, "engineering")
	asset := w.assetIn(w.product, w.workspace, "e1")
	asset.publishedOn(target)

	// A block is being placed: the row of the target is locked for update,
	// as the placement of a block does until it commits.
	placing, err := w.owner.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = placing.Rollback(context.WithoutCancel(t.Context())) })
	if _, err := placing.Exec(t.Context(), `SELECT 1 FROM publication.target WHERE target_id = $1 FOR UPDATE`, last(target)); err != nil {
		t.Fatal(err)
	}

	asked := func(path string) (*http.Response, error) {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		t.Cleanup(cancel)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, w.server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+w.token("publisher"))
		return http.DefaultClient.Do(request)
	}
	response, err := asked(target + "/assets")
	if err != nil {
		t.Fatalf("the list waited for the placement of a block: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Errorf("the list answered %d while a block was being placed, want 200", response.StatusCode)
	}
	if response, err := asked(assetSlot(target, asset.asset)); err == nil {
		response.Body.Close()
		t.Errorf("the read of one slot answered %d while a block was being placed, want it to wait", response.StatusCode)
	}
}

// What the product governance cannot read the slots of is refused to whoever
// lists them, as any other who may not read is, and is not failed: a request
// that names no caller, or a product that is no identifier, is answered by
// the governance before it reads anything, and the authority turns that into
// the refusals the use case puts on record. The transport authenticates every
// caller and takes the identifier of a product from a path that names one, so
// no request through it comes this far, and the authority is asked directly.
func TestWhatTheGovernanceCannotReadTheSlotsOfIsRefusedAndNotFailed(t *testing.T) {
	authority := publicationAuthority{releaseAuthority: releaseAuthority{
		revisionScopes: revisionScopes{governance: &governance.ProductGovernance{}},
	}}
	caller := publicationdomain.Principal{Issuer: "https://issuer.example", Subject: "reader"}
	const product = "1c9a4e7b-52d0-4f8e-b6a1-90c3d2e4f5a6"

	for name, c := range map[string]struct {
		request publicationdomain.Request
		cause   error
	}{
		"no caller":                       {publicationdomain.Request{ProductID: product}, governance.ErrForbidden},
		"a caller without a subject":      {publicationdomain.Request{Caller: publicationdomain.Principal{Issuer: caller.Issuer}, ProductID: product}, governance.ErrForbidden},
		"a product that is a key":         {publicationdomain.Request{Caller: caller, ProductID: "cargopai"}, governance.ErrProductNotFound},
		"no product":                      {publicationdomain.Request{Caller: caller}, governance.ErrProductNotFound},
		"words that are no identifier":    {publicationdomain.Request{Caller: caller, ProductID: "not-an-identifier"}, governance.ErrProductNotFound},
		"no caller and no product at all": {publicationdomain.Request{}, governance.ErrForbidden},
	} {
		readable, err := authority.ReadableAssets(t.Context(), c.request)
		if !errors.Is(err, c.cause) || !errors.Is(err, publication.ErrForbidden) || len(readable) != 0 {
			t.Errorf("%s: %+v (%v), want no assets and a refusal that says %v", name, readable, err, c.cause)
		}
	}
}
