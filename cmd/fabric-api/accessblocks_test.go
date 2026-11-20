package main

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
)

// A security administrator of the product blocks access to a target, to one
// asset on it or to one release of an asset. What a block covers is neither
// served nor activated, whatever the slot points at, and nothing else is
// touched. A security reviewer other than whoever placed the block clears
// it, once nothing it covers is published: clearing a block makes nothing
// visible. Only the two security roles read blocks.
func TestAnAccessBlockDeniesWhatItNamesUntilAnotherReviewerClearsIt(t *testing.T) {
	s := publicationFor(t)
	target, _ := s.target(internalSandbox, "engineering")
	blocks := target + "/access-blocks"
	shipping, slot := last(s.asset), assetSlot(target, s.asset)
	firstRevision, _, first := s.released(revisionSource, "", "1.0.0")
	_, _, next := s.released(strings.ReplaceAll(revisionSource, "v1", "v2"), firstRevision, "2.0.0")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, first, "", "first"), http.StatusOK, "")

	// Another asset of the workspace, published on the same target.
	other := *s
	otherReviews := *s.reviewFixture
	other.reviewFixture = &otherReviews
	other.revisions = s.revisionAsset(s.product, s.workspace, "maintainer", "tracking-api", "HTTP")
	other.asset = strings.TrimSuffix(other.revisions, "/revisions")
	tracking, trackingSlot := last(other.asset), assetSlot(target, other.asset)
	_, _, tracked := other.released(revisionSource, "", "1.0.0")
	s.require(s.change(trackingSlot, "publisher", "ACTIVATE", 0, tracked, "", "first"), http.StatusOK, "")

	for subject, roles := range map[string][]string{
		"security": {"security_admin"}, "clearer": {"security_reviewer"}, "both": {"security_admin", "security_reviewer"},
	} {
		for _, role := range roles {
			s.require(s.securityRole(subject, role, "owner"), http.StatusCreated, "")
		}
	}
	placing := func(asset, release, reason string) string {
		body := map[string]string{"reason": reason}
		if asset != "" {
			body["api_asset_id"] = asset
		}
		if release != "" {
			body["release_id"] = release
		}
		return jsonBody(body)
	}
	clearing := func(reason string) string { return jsonBody(map[string]string{"reason": reason}) }
	served := func(slot string, status int, code string) {
		t.Helper()
		s.require(s.call(http.MethodGet, slot+"/artifact", "author", ""), status, code)
	}
	blocked := func(slot string) any { return s.call(http.MethodGet, slot, "publisher", "").body["blocked"] }

	// Only a security administrator places a block: not an owner, not a
	// publisher, not who clears blocks, not who administers the platform.
	const leak = "the first release names an internal host"
	refused := []string{"owner", "publisher", "maintainer", "clearer", "platform-admin"}
	for _, caller := range refused {
		s.require(s.call(http.MethodPost, blocks, caller, placing(shipping, first, leak)), http.StatusForbidden, "FORBIDDEN")
	}
	s.require(s.call(http.MethodPost, blocks, "stranger", placing(shipping, first, leak)), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	s.expect("a block names a target, an asset or a release that the product has", "security", []exchange{
		{http.MethodPost, blocks, placing("", first, leak), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{http.MethodPost, blocks, placing(ids.NewUUID(), "", leak), http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		{http.MethodPost, blocks, placing(tracking, first, leak), http.StatusNotFound, "RELEASE_NOT_FOUND"},
		{http.MethodPost, blocks, placing(shipping, ids.NewUUID(), leak), http.StatusNotFound, "RELEASE_NOT_FOUND"},
		{http.MethodPost, strings.Replace(blocks, last(target), ids.NewUUID(), 1), placing(shipping, first, leak), http.StatusNotFound, "PUBLICATION_TARGET_NOT_FOUND"},
		{http.MethodPost, blocks, placing(shipping, first, ""), http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, blocks, placing(shipping, first, "two\nlines"), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{http.MethodPost, blocks, `{"api_asset_id":"` + shipping + `"}`, http.StatusBadRequest, "REQUEST_INVALID"},
	})
	if got := s.count("publication.access_block"); got != 0 {
		t.Fatalf("the refusals left %d blocks", got)
	}

	// A block of one release.
	placed := s.call(http.MethodPost, blocks, "security", placing(shipping, first, leak))
	s.require(placed, http.StatusCreated, "")
	id := placed.id("access_block_id")
	block := blocks + "/" + id
	if placed.body["status"] != "BLOCK_ACTIVE" || placed.body["api_asset_id"] != shipping || placed.body["release_id"] != first ||
		placed.body["generation"] != float64(1) || placed.body["reason"] != leak || placed.body["edge_status"] != "UNKNOWN" ||
		placed.body["created_by"].(map[string]any)["subject"] != "security" || placed.body["cleared_at"] != nil ||
		placed.header.Get("Location") != block || placed.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the block: %v %v", placed.body, placed.header)
	}
	// The same block asked again is the block that is there; anything else
	// of the same scope is told that it is.
	if again := s.call(http.MethodPost, blocks, "security", placing(shipping, first, leak)); again.status != http.StatusOK ||
		!reflect.DeepEqual(again.body, placed.body) || again.header.Get("Location") != "" {
		t.Errorf("the block asked again: %d %v, want 200 and the block as it was answered", again.status, again.body)
	}
	s.require(s.call(http.MethodPost, blocks, "security", placing(shipping, first, "another reason")), http.StatusConflict, "ACCESS_BLOCK_EXISTS")
	s.require(s.call(http.MethodPost, blocks, "both", placing(shipping, first, leak)), http.StatusConflict, "ACCESS_BLOCK_EXISTS")

	// The release is not served and the slot says so; the other asset is
	// untouched.
	served(slot, http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	served(trackingSlot, http.StatusOK, "")
	if blocked(slot) != true || blocked(trackingSlot) != false {
		t.Errorf("the slot of the blocked release says blocked %v and the other %v, want true and false", blocked(slot), blocked(trackingSlot))
	}
	// It is not cleared while it is published.
	const safe = "the host was renamed"
	s.require(s.call(http.MethodPost, block+"/clearance", "clearer", clearing(safe)), http.StatusConflict, "WITHDRAW_BEFORE_CLEARING")
	// Another release of the asset is not covered: it replaces the blocked
	// one and is served, and the slot does not return to the blocked one.
	s.require(s.change(slot, "publisher", "ACTIVATE", 1, next, "", "next"), http.StatusOK, "")
	served(slot, http.StatusOK, "")
	if blocked(slot) != false {
		t.Error("the slot says it is blocked while it serves a release no block covers")
	}
	s.require(s.change(slot, "publisher", "RESTORE", 2, first, "the second breaks clients", "back"), http.StatusConflict, "PUBLICATION_BLOCKED")

	// Only a security reviewer clears, and the release it covers is not
	// published any more: that another release of the asset is published
	// does not hold the clearance up.
	for _, caller := range []string{"security", "owner", "publisher"} {
		s.require(s.call(http.MethodPost, block+"/clearance", caller, clearing(safe)), http.StatusForbidden, "FORBIDDEN")
	}
	s.expect("a clearance says why, of a block the target has", "clearer", []exchange{
		{http.MethodPost, block + "/clearance", clearing(""), http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, block + "/clearance", clearing("two\nlines"), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{http.MethodPost, blocks + "/" + ids.NewUUID() + "/clearance", clearing(safe), http.StatusNotFound, "ACCESS_BLOCK_NOT_FOUND"},
	})
	cleared := s.call(http.MethodPost, block+"/clearance", "clearer", clearing(safe))
	s.require(cleared, http.StatusOK, "")
	if cleared.body["status"] != "CLEARED" || cleared.body["clear_generation"] != float64(2) || cleared.body["clear_reason"] != safe ||
		cleared.body["cleared_by"].(map[string]any)["subject"] != "clearer" || cleared.body["cleared_at"] == nil ||
		cleared.body["reason"] != leak || cleared.body["generation"] != float64(1) || cleared.body["release_id"] != first {
		t.Fatalf("the cleared block: %v", cleared.body)
	}
	if again := s.call(http.MethodPost, block+"/clearance", "clearer", clearing(safe)); again.status != http.StatusOK || !reflect.DeepEqual(again.body, cleared.body) {
		t.Errorf("the clearance asked again: %d %v, want 200 and the block as it was answered", again.status, again.body)
	}
	s.require(s.call(http.MethodPost, block+"/clearance", "clearer", clearing("another reason")), http.StatusConflict, "ACCESS_BLOCK_CLEARED")
	s.require(s.call(http.MethodPost, block+"/clearance", "both", clearing(safe)), http.StatusConflict, "ACCESS_BLOCK_CLEARED")
	// Clearing made nothing visible: the slot serves what it served, and
	// returning to the release is a change of its own.
	if shown := s.call(http.MethodGet, slot, "publisher", ""); shown.body["active_release_id"] != next || shown.body["generation"] != float64(2) {
		t.Errorf("the slot after the clearance: %v, want it as it was", shown.body)
	}
	s.require(s.change(slot, "publisher", "RESTORE", 2, first, "the second breaks clients", "back"), http.StatusOK, "")

	// A block of an asset, placed by someone who holds both roles: every
	// release of the asset is covered, on this target, and the other asset
	// is not.
	const audit = "the asset is under audit"
	whole := s.call(http.MethodPost, blocks, "both", placing(shipping, "", audit))
	s.require(whole, http.StatusCreated, "")
	wholeBlock := blocks + "/" + whole.id("access_block_id")
	if whole.body["generation"] != float64(3) || whole.body["api_asset_id"] != shipping || whole.body["release_id"] != nil {
		t.Fatalf("the block of the asset: %v", whole.body)
	}
	served(slot, http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	served(trackingSlot, http.StatusOK, "")
	s.require(s.change(slot, "publisher", "ACTIVATE", 3, next, "", "forward"), http.StatusConflict, "PUBLICATION_BLOCKED")
	// Whoever placed it does not clear it, reviewer or not; another
	// reviewer does, once the asset is taken down. The other asset stays
	// published throughout.
	s.require(s.call(http.MethodPost, wholeBlock+"/clearance", "both", clearing("audit done")), http.StatusForbidden, "SECURITY_REVIEWER_NOT_INDEPENDENT")
	s.require(s.call(http.MethodPost, wholeBlock+"/clearance", "clearer", clearing("audit done")), http.StatusConflict, "WITHDRAW_BEFORE_CLEARING")
	s.require(s.change(slot, "publisher", "WITHDRAW", 3, "", "under audit", "down"), http.StatusOK, "")
	served(slot, http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	lifted := s.call(http.MethodPost, wholeBlock+"/clearance", "clearer", clearing("audit done"))
	s.require(lifted, http.StatusOK, "")
	if lifted.body["clear_generation"] != float64(4) || lifted.body["status"] != "CLEARED" {
		t.Fatalf("the cleared block of the asset: %v", lifted.body)
	}
	served(trackingSlot, http.StatusOK, "")
	served(slot, http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	if shown := s.call(http.MethodGet, target, "publisher", ""); shown.body["block_generation"] != float64(4) {
		t.Errorf("the target counts %v changes of its blocks, want 4", shown.body["block_generation"])
	}

	// The two security roles read the blocks of a target, and nobody else.
	for _, caller := range []string{"security", "clearer", "both"} {
		read := s.call(http.MethodGet, block, caller, "")
		page := s.call(http.MethodGet, blocks+"?page_size=1", caller, "")
		rest := s.call(http.MethodGet, fmt.Sprintf("%s?after_generation=%v", blocks, page.body["next_after_generation"]), caller, "")
		if read.status != http.StatusOK || !reflect.DeepEqual(read.body, cleared.body) || page.status != http.StatusOK ||
			!reflect.DeepEqual(page.body["items"], []any{cleared.body}) || page.body["next_after_generation"] != float64(1) ||
			!reflect.DeepEqual(rest.body["items"], []any{lifted.body}) || rest.body["next_after_generation"] != nil {
			t.Errorf("%s reads the block as %d %v, a page of one as %v and the rest as %v", caller, read.status, read.body, page.body, rest.body)
		}
	}
	for _, caller := range []string{"owner", "publisher", "author", "maintainer", "platform-admin"} {
		for _, path := range []string{block, blocks} {
			s.require(s.call(http.MethodGet, path, caller, ""), http.StatusForbidden, "FORBIDDEN")
		}
	}
	s.expect("a block is read under its target", "security", []exchange{
		{http.MethodGet, blocks + "/" + ids.NewUUID(), "", http.StatusNotFound, "ACCESS_BLOCK_NOT_FOUND"},
		{http.MethodGet, strings.Replace(block, last(target), ids.NewUUID(), 1), "", http.StatusNotFound, "PUBLICATION_TARGET_NOT_FOUND"},
	})
	s.require(s.call(http.MethodGet, block, "stranger", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")

	// Every block and every clearance is on record under its rule and left
	// an event; what was asked again left neither. Every refusal is on
	// record: for lack of the role, for clearing what one placed, for
	// publishing under a block and for an artifact that was not served.
	for _, c := range []struct {
		subject, action, rule string
		done                  bool
		want                  int
	}{
		{"security", "security.block", "AC-06", true, 1},
		{"both", "security.block", "AC-06", true, 1},
		{"clearer", "security.unblock", "AC-07", true, 2},
		{"both", "security.unblock", "DENY-06", false, 1},
		{"both", "security.unblock", "DENY-03", false, 0},
		{"owner", "security.block", "DENY-03", false, 1},
		{"clearer", "security.block", "DENY-03", false, 1},
		{"platform-admin", "security.block", "DENY-03", false, 1},
		{"security", "security.unblock", "DENY-03", false, 1},
		{"owner", "security.unblock", "DENY-03", false, 1},
		{"owner", "security.block.read", "DENY-03", false, 2},
		{"publisher", "publication.restore", "DENY-03", false, 1},
		{"publisher", "publication.activate", "DENY-03", false, 1},
		{"author", "publication.download", "DENY-03", false, 4},
	} {
		if got := s.on(c.subject, c.action, c.rule, c.done); got != c.want {
			t.Errorf("%d records of %s by %s under %s, want %d", got, c.action, c.subject, c.rule, c.want)
		}
	}
	recorded := s.recorded(func(row audited) bool {
		return strings.HasPrefix(row.Action, "security.") && row.Outcome == "SUCCEEDED"
	})
	for row := range recorded {
		if row.ResourceType != "AccessBlock" || (row.ResourceID != id && row.ResourceID != whole.id("access_block_id")) {
			t.Errorf("the record of %s is of %s %s, want one of the two blocks", row.Action, row.ResourceType, row.ResourceID)
		}
	}
	for event, want := range map[string]int{"access_block.created": 2, "access_block.cleared": 2} {
		if got := s.announced("fabric.publication." + event); got != want {
			t.Errorf("%d events %s, want %d", got, event, want)
		}
	}
}
