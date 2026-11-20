package main

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
)

// on counts the audit records of the action by the subject under the rule,
// of what was done or of what was refused.
func (s *stack) on(subject, action, rule string, done bool) int {
	s.t.Helper()
	return occurrences(s.recorded(func(row audited) bool {
		return row.Subject == subject && row.Action == action && row.Rule == rule && (row.Outcome == "SUCCEEDED") == done
	}))
}

// announced counts the events of the type that wait in the outbox.
func (s *stack) announced(eventType string) int {
	s.t.Helper()
	var events int
	if err := s.owner.QueryRow(s.t.Context(), `SELECT count(*) FROM bento.outbox WHERE event_type = $1`, eventType).Scan(&events); err != nil {
		s.t.Fatal(err)
	}
	return events
}

// released approves a revision of the source, checked against the baseline
// if one is named, and prepares a release of it under the version.
func (s *releaseFixture) released(source, baseline, version string) (revision, review, release string) {
	s.t.Helper()
	revision, review = s.approve(source, baseline, internalSandbox)
	prepared := s.prepare(revision, review, version, "publisher")
	s.require(prepared, http.StatusCreated, "")
	return revision, review, prepared.id("release_id")
}

func (s *releaseFixture) suspend(status string) {
	s.t.Helper()
	if _, err := s.owner.Exec(s.t.Context(), `UPDATE productgovernance.product SET status = $2 WHERE product_id = $1`, s.product, status); err != nil {
		s.t.Fatal(err)
	}
}

// A publisher of the workspace changes what a slot serves, and whoever
// reads the revisions of the workspace reads the slot, its history and what
// it serves. Whoever had a part in a revision does not put a release of it
// on a slot, publisher or not, and takes it down like any publisher: a
// withdrawal asks for the role and for nothing else. Every change says what
// it does, to which generation, under which key.
func TestASlotIsChangedByAPublisherWhoHadNoPartInTheRevision(t *testing.T) {
	s := publicationFor(t)
	target, _ := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	operations, artifact := slot+"/operations", slot+"/artifact"
	_, _, id := s.released(revisionSource, "", "1.0.0")

	// Reading the revisions of the workspace, maintaining it or owning the
	// product is not publishing.
	for _, caller := range []string{"author", "coauthor", "reviewer", "maintainer", "owner"} {
		s.require(s.change(slot, caller, "ACTIVATE", 0, id, "", "first"), http.StatusForbidden, "FORBIDDEN")
	}
	s.require(s.change(slot, "stranger", "ACTIVATE", 0, id, "", "first"), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	s.join(s.product, s.workspace, "owner", "author", "internal_publisher")
	s.join(s.product, s.workspace, "owner", "reviewer", "internal_publisher")
	for _, caller := range []string{"author", "reviewer"} {
		s.require(s.change(slot, caller, "ACTIVATE", 0, id, "", "first"), http.StatusForbidden, "PUBLISHER_NOT_INDEPENDENT")
	}
	if rows := s.count("publication.slot") + s.count("publication.operation"); rows != 0 {
		t.Fatalf("the refusals left %d rows of slots and operations", rows)
	}

	first := s.change(slot, "publisher", "ACTIVATE", 0, id, "", "first")
	s.require(first, http.StatusOK, "")
	slotID := first.id("slot_id")
	if first.body["generation"] != float64(1) || first.body["action"] != "ACTIVATE" || first.body["release_id"] != id ||
		first.body["previous_release_id"] != nil || first.body["reason"] != nil || slotID == "" ||
		first.body["performed_by"].(map[string]any)["subject"] != "publisher" || first.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the first activation: %v %v", first.body, first.header)
	}
	s.expectChanges("what is active is not activated again, and a release is one of the asset", slot, 1, []change{
		{"ACTIVATE", id, "", http.StatusConflict, "RELEASE_ALREADY_ACTIVE"},
		{"RESTORE", id, "again", http.StatusConflict, "RELEASE_ALREADY_ACTIVE"},
		{"ACTIVATE", ids.NewUUID(), "", http.StatusNotFound, "RELEASE_NOT_FOUND"},
	})

	// Whoever had a part in the revision takes it down, and does not put
	// it back.
	down := s.change(slot, "author", "WITHDRAW", 1, "", "wrong audience", "down")
	s.require(down, http.StatusOK, "")
	if down.body["generation"] != float64(2) || down.body["action"] != "WITHDRAW" || down.body["release_id"] != nil ||
		down.body["previous_release_id"] != id || down.body["reason"] != "wrong audience" ||
		down.body["performed_by"].(map[string]any)["subject"] != "author" {
		t.Fatalf("the withdrawal: %v", down.body)
	}
	withdrawn := s.call(http.MethodGet, slot, "publisher", "")
	if withdrawn.status != http.StatusOK || withdrawn.body["visibility"] != "WITHDRAWN" || withdrawn.body["generation"] != float64(2) ||
		withdrawn.body["active_release_id"] != nil || withdrawn.body["slot_id"] != slotID || withdrawn.body["blocked"] != false {
		t.Fatalf("the slot after the withdrawal: %d %v", withdrawn.status, withdrawn.body)
	}
	s.expectChanges("nothing is withdrawn twice", slot, 2, []change{{"WITHDRAW", "", "again", http.StatusConflict, "NO_ACTIVE_RELEASE"}})
	s.require(s.change(slot, "author", "RESTORE", 2, id, "back again", "up"), http.StatusForbidden, "PUBLISHER_NOT_INDEPENDENT")
	up := s.change(slot, "publisher", "RESTORE", 2, id, "back again", "up")
	s.require(up, http.StatusOK, "")
	if up.body["generation"] != float64(3) || up.body["action"] != "RESTORE" || up.body["release_id"] != id ||
		up.body["previous_release_id"] != nil || up.body["reason"] != "back again" {
		t.Fatalf("the return to the release: %v", up.body)
	}

	// A change says what it does: a release to activate or to return to,
	// a reason to withdraw or to return, and nothing its action has no
	// use for. What the contract states is refused as it; the rest by the
	// model.
	s.expectChanges("a change says what it does", slot, 3, []change{
		{"PAUSE", id, "", http.StatusBadRequest, "REQUEST_INVALID"},
		{"WITHDRAW", "", strings.Repeat("r", 501), http.StatusBadRequest, "REQUEST_INVALID"},
		{"ACTIVATE", "", "", http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{"WITHDRAW", "", "", http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{"WITHDRAW", id, "wrong audience", http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{"RESTORE", id, "", http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{"RESTORE", "", "back again", http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{"WITHDRAW", "", "two\nlines", http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{"WITHDRAW", "", " padded ", http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
	})
	withdraw := jsonBody(map[string]any{"action": "WITHDRAW", "expected_generation": 3, "reason": "wrong audience"})
	for name, headers := range map[string]http.Header{
		"no key":                        nil,
		"a key with a space":            {"Idempotency-Key": {"a key"}},
		"a key longer than the longest": {"Idempotency-Key": {strings.Repeat("k", 129)}},
	} {
		if got := s.callWithHeaders(http.MethodPost, operations, s.token("publisher"), withdraw, headers); got.status != http.StatusBadRequest || got.code() != "REQUEST_INVALID" {
			t.Errorf("a change under %s: %d %v, want 400 REQUEST_INVALID", name, got.status, got.body)
		}
	}
	s.require(s.change(slot, "publisher", "WITHDRAW", -1, "", "wrong audience", "shape"), http.StatusBadRequest, "REQUEST_INVALID")
	if got := s.count("publication.operation"); got != 3 {
		t.Fatalf("%d operations are recorded after the refusals, want the three that were made", got)
	}

	// Whoever reads the revisions of the workspace reads the slot, its
	// history and what it serves. Maintaining the workspace or owning the
	// product is not reading them.
	for _, path := range []string{slot, operations, artifact} {
		for _, caller := range []string{"author", "coauthor", "reviewer", "publisher"} {
			if got := s.call(http.MethodGet, path, caller, ""); got.status != http.StatusOK || got.header.Get("Cache-Control") != "no-store" {
				t.Errorf("%s reads %s: %d %v", caller, path, got.status, got.body)
			}
		}
		for _, caller := range []string{"maintainer", "owner"} {
			s.require(s.call(http.MethodGet, path, caller, ""), http.StatusForbidden, "FORBIDDEN")
		}
		s.require(s.call(http.MethodGet, path, "stranger", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	}
	history := s.call(http.MethodGet, operations, "reviewer", "")
	var steps []string
	for _, item := range history.body["items"].([]any) {
		step := item.(map[string]any)
		steps = append(steps, fmt.Sprintf("%v %v by %v", step["generation"], step["action"], step["performed_by"].(map[string]any)["subject"]))
	}
	if want := []string{"1 ACTIVATE by publisher", "2 WITHDRAW by author", "3 RESTORE by publisher"}; !reflect.DeepEqual(steps, want) ||
		history.body["next_after_generation"] != nil {
		t.Errorf("the history of the slot is %v, want %v and nothing after it", steps, want)
	}

	// Every change is on record under its action and its rule and left an
	// event; every refusal of the right to change or to read is on record
	// as one.
	for _, c := range []struct {
		subject, action, rule string
		done                  bool
		want                  int
	}{
		{"publisher", "publication.activate", "AC-04", true, 1},
		{"author", "publication.withdraw", "AC-05", true, 1},
		{"publisher", "publication.restore", "AC-04", true, 1},
		{"author", "publication.activate", "DENY-03", false, 2},
		{"reviewer", "publication.activate", "DENY-03", false, 2},
		{"coauthor", "publication.activate", "DENY-03", false, 1},
		{"maintainer", "publication.activate", "DENY-03", false, 1},
		{"owner", "publication.activate", "DENY-03", false, 1},
		{"author", "publication.restore", "DENY-03", false, 1},
		{"owner", "publication.read", "DENY-03", false, 1},
		{"maintainer", "publication.history", "DENY-03", false, 1},
		{"owner", "publication.download", "DENY-03", false, 1},
	} {
		if got := s.on(c.subject, c.action, c.rule, c.done); got != c.want {
			t.Errorf("%d records of %s by %s under %s, want %d", got, c.action, c.subject, c.rule, c.want)
		}
	}
	changed := s.recorded(func(row audited) bool {
		return row.Outcome == "SUCCEEDED" && strings.HasPrefix(row.Action, "publication.") && row.Action != "publication.target.create"
	})
	for row := range changed {
		if row.ResourceType != "PublicationSlot" || row.ResourceID != slotID || row.ProductID != s.product {
			t.Errorf("the record of %s is of %s %s in product %s, want the slot %s", row.Action, row.ResourceType, row.ResourceID, row.ProductID, slotID)
		}
	}
	for event, want := range map[string]int{"slot.activated": 1, "slot.withdrawn": 1, "slot.restored": 1} {
		if got := s.announced("fabric.publication." + event); got != want {
			t.Errorf("%d events %s, want %d", got, event, want)
		}
	}
}

// change is one change of a slot by the publisher and what it is answered.
type change struct {
	action, release, reason string
	status                  int
	code                    string
}

// expectChanges sends each change at the generation as the publisher, each
// under a key of its own, and checks what it is answered.
func (s *releaseFixture) expectChanges(when, slot string, generation int64, changes []change) {
	s.t.Helper()
	for i, c := range changes {
		key := fmt.Sprintf("%s-%d-%d", strings.ToLower(c.action), generation, i)
		if got := s.change(slot, "publisher", c.action, generation, c.release, c.reason, key); got.status != c.status || got.code() != c.code {
			s.t.Errorf("%s: %s of %q for %q answered %d %v, want %d %s", when, c.action, c.release, c.reason, got.status, got.body, c.status, c.code)
		}
	}
}

// A slot takes a release of another revision when the check that revision
// was approved on compared it with what the slot served last, withdrawn or
// not; another release of the same revision needs no comparison. A slot
// returns to any release it served before, and to no other.
func TestASlotTakesWhatWasComparedWithItsLastReleaseAndReturnsToWhatItServed(t *testing.T) {
	s := publicationFor(t)
	target, configPath := s.target(internalSandbox, "engineering")
	second := s.call(http.MethodPost, "/v1/products/"+s.product+"/publication-targets", "owner", jsonBody(map[string]string{
		"audience_config_id": last(configPath), "portal_key": "preview",
	}))
	s.require(second, http.StatusCreated, "")
	slot, elsewhere := assetSlot(target, s.asset), assetSlot(second.header.Get("Location"), s.asset)
	source := func(version string) string { return strings.ReplaceAll(revisionSource, "v1", version) }

	firstRevision, firstReview, first := s.released(revisionSource, "", "1.0.0")
	patch := s.prepare(firstRevision, firstReview, "1.0.1", "publisher")
	s.require(patch, http.StatusCreated, "")
	secondRevision, _, next := s.released(source("v2"), firstRevision, "2.0.0")
	_, _, third := s.released(source("v3"), secondRevision, "3.0.0")
	_, _, branch := s.released(source("v4"), firstRevision, "4.0.0")

	s.require(s.change(slot, "publisher", "ACTIVATE", 0, first, "", "first"), http.StatusOK, "")
	s.expectChanges("after the first release", slot, 1, []change{
		// Compared with the second revision, which the slot does not serve.
		{"ACTIVATE", third, "", http.StatusConflict, "COMPATIBILITY_BASELINE_MISMATCH"},
		// The same revision under another version: nothing to compare.
		{"ACTIVATE", patch.id("release_id"), "", http.StatusOK, ""},
	})
	s.expectChanges("after the patch of the first revision", slot, 2, []change{
		{"ACTIVATE", third, "", http.StatusConflict, "COMPATIBILITY_BASELINE_MISMATCH"},
		{"ACTIVATE", next, "", http.StatusOK, ""},
	})
	s.expectChanges("after the second revision", slot, 3, []change{
		// Compared with the first revision, which is not the last one.
		{"ACTIVATE", branch, "", http.StatusConflict, "COMPATIBILITY_BASELINE_MISMATCH"},
		{"ACTIVATE", third, "", http.StatusOK, ""},
	})

	// The branch was never on this slot. It is on the other one, which
	// does not make it part of the history of this one.
	s.require(s.change(slot, "publisher", "RESTORE", 4, branch, "try the branch", "branch"), http.StatusConflict, "RELEASE_NOT_IN_SLOT_HISTORY")
	s.require(s.change(elsewhere, "publisher", "ACTIVATE", 0, first, "", "first"), http.StatusOK, "")
	s.require(s.change(elsewhere, "publisher", "ACTIVATE", 1, branch, "", "branch"), http.StatusOK, "")
	s.require(s.change(slot, "publisher", "RESTORE", 4, branch, "try the branch", "branch"), http.StatusConflict, "RELEASE_NOT_IN_SLOT_HISTORY")
	s.require(s.change(slot, "publisher", "RESTORE", 4, ids.NewUUID(), "try anything", "anything"), http.StatusConflict, "RELEASE_NOT_IN_SLOT_HISTORY")

	// A return goes any number of releases back and asks for no comparison.
	back := s.change(slot, "publisher", "RESTORE", 4, first, "the third breaks clients", "back")
	s.require(back, http.StatusOK, "")
	if back.body["generation"] != float64(5) || back.body["release_id"] != first || back.body["previous_release_id"] != third {
		t.Fatalf("the return to the first release: %v", back.body)
	}
	if again := s.change(slot, "publisher", "RESTORE", 4, first, "the third breaks clients", "back"); again.status != http.StatusOK ||
		!reflect.DeepEqual(again.body, back.body) {
		t.Errorf("the return sent again: %d %v, want the operation as it was answered", again.status, again.body)
	}

	// What a slot served last is what a new release is compared with,
	// also when it was taken down since.
	s.require(s.change(slot, "publisher", "WITHDRAW", 5, "", "maintenance", "pause"), http.StatusOK, "")
	s.expectChanges("after a withdrawal", slot, 6, []change{
		{"ACTIVATE", third, "", http.StatusConflict, "COMPATIBILITY_BASELINE_MISMATCH"},
		{"ACTIVATE", branch, "", http.StatusOK, ""},
	})

	history := s.call(http.MethodGet, slot+"/operations", "publisher", "")
	var steps []string
	for _, item := range history.body["items"].([]any) {
		step := item.(map[string]any)
		steps = append(steps, fmt.Sprintf("%v %v %v after %v", step["generation"], step["action"], step["release_id"], step["previous_release_id"]))
	}
	want := []string{
		fmt.Sprintf("1 ACTIVATE %s after <nil>", first),
		fmt.Sprintf("2 ACTIVATE %s after %s", patch.id("release_id"), first),
		fmt.Sprintf("3 ACTIVATE %s after %s", next, patch.id("release_id")),
		fmt.Sprintf("4 ACTIVATE %s after %s", third, next),
		fmt.Sprintf("5 RESTORE %s after %s", first, third),
		fmt.Sprintf("6 WITHDRAW <nil> after %s", first),
		fmt.Sprintf("7 ACTIVATE %s after <nil>", branch),
	}
	if !reflect.DeepEqual(steps, want) {
		t.Errorf("the history of the slot is\n%s\nwant\n%s", strings.Join(steps, "\n"), strings.Join(want, "\n"))
	}
}

// A suspended product publishes nothing new and serves nothing: it takes no
// target, no activation and no return to a release, and what its slots
// point at is not served. What is published can still be taken down, and
// everything is as it was when the product takes work again.
func TestASuspendedProductPublishesAndServesNothing(t *testing.T) {
	s := publicationFor(t)
	target, configPath := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	firstRevision, _, first := s.released(revisionSource, "", "1.0.0")
	_, _, next := s.released(strings.ReplaceAll(revisionSource, "v1", "v2"), firstRevision, "2.0.0")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, first, "", "first"), http.StatusOK, "")
	s.requireArtifact(slot, s.call(http.MethodGet, s.asset+"/releases/"+first, "publisher", "").id("artifact_digest"), revisionSource)
	registering := func(portal string) string {
		return jsonBody(map[string]string{"audience_config_id": last(configPath), "portal_key": portal})
	}
	targets := "/v1/products/" + s.product + "/publication-targets"

	s.suspend("SUSPENDED")
	s.expect("a suspended product takes no target", "owner", []exchange{
		{http.MethodPost, targets, registering("preview"), http.StatusConflict, "PRODUCT_NOT_ACTIVE"},
		{http.MethodPost, targets, registering("engineering"), http.StatusConflict, "PRODUCT_NOT_ACTIVE"},
		{http.MethodGet, target, "", http.StatusOK, ""},
	})
	s.expectChanges("a suspended product", slot, 1, []change{{"ACTIVATE", next, "", http.StatusConflict, "PRODUCT_NOT_ACTIVE"}})
	s.require(s.call(http.MethodGet, slot+"/artifact", "author", ""), http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	if shown := s.call(http.MethodGet, slot, "author", ""); shown.status != http.StatusOK || shown.body["visibility"] != "ACTIVE" || shown.body["active_release_id"] != first {
		t.Errorf("the slot of a suspended product reads %d %v, want it as it was", shown.status, shown.body)
	}
	s.expectChanges("a suspended product", slot, 1, []change{{"WITHDRAW", "", "the product is suspended", http.StatusOK, ""}})
	s.expectChanges("a suspended product", slot, 2, []change{{"RESTORE", first, "too early", http.StatusConflict, "PRODUCT_NOT_ACTIVE"}})
	if got := s.count("publication.target") + s.count("publication.operation"); got != 3 {
		t.Fatalf("%d targets and operations are recorded, want the target, the activation and the withdrawal", got)
	}

	s.suspend("ACTIVE")
	s.expectChanges("a product that takes work again", slot, 2, []change{{"RESTORE", first, "the product is back", http.StatusOK, ""}})
	s.require(s.call(http.MethodGet, slot+"/artifact", "author", ""), http.StatusOK, "")
	s.require(s.call(http.MethodPost, targets, "owner", registering("preview")), http.StatusCreated, "")
}

// An owner of the product registers its publication targets: a portal over
// an audience configuration of the product. Registering one that is there
// answers it and changes nothing. Whoever knows the product reads its
// targets; to anyone else there is no such product.
func TestAPublicationTargetIsRegisteredByAnOwnerOverAnAudienceConfiguration(t *testing.T) {
	s := publicationFor(t)
	products := "/v1/products/"
	targets := products + s.product + "/publication-targets"
	configured := func(product, owner string, scope [2]string) string {
		config := s.call(http.MethodPost, products+product+"/audience-configs", owner, jsonBody(map[string]string{
			"audience": scope[0], "environment": scope[1],
		}))
		s.require(config, http.StatusCreated, "")
		return config.id("audience_config_id")
	}
	registering := func(config, portal string) string {
		return jsonBody(map[string]string{"audience_config_id": config, "portal_key": portal})
	}
	config := configured(s.product, "owner", internalSandbox)
	parcel := s.register("platform-admin", "parcel", "parcel-owner")
	foreign := configured(parcel, "parcel-owner", internalSandbox)
	s.require(s.securityRole("security", "security_admin", "owner"), http.StatusCreated, "")
	s.require(s.securityRole("clearer", "security_reviewer", "owner"), http.StatusCreated, "")

	// Only an owner registers a target: no other role of the product or
	// its workspaces does, and neither does administering the platform.
	refusedCallers := []string{"maintainer", "publisher", "author", "reviewer", "security", "clearer", "platform-admin"}
	for _, caller := range refusedCallers {
		s.require(s.call(http.MethodPost, targets, caller, registering(config, "engineering")), http.StatusForbidden, "FORBIDDEN")
	}
	s.require(s.call(http.MethodPost, targets, "stranger", registering(config, "engineering")), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	s.require(s.call(http.MethodPost, targets, "parcel-owner", registering(foreign, "engineering")), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	if got := s.count("publication.target"); got != 0 {
		t.Fatalf("the refusals left %d targets", got)
	}

	created := s.call(http.MethodPost, targets, "owner", registering(config, "engineering"))
	s.require(created, http.StatusCreated, "")
	id := created.id("publication_target_id")
	if created.body["product_id"] != s.product || created.body["audience_config_id"] != config || created.body["portal_key"] != "engineering" ||
		created.body["audience"] != "internal" || created.body["environment"] != "sandbox" || created.body["block_generation"] != float64(0) ||
		created.body["created_at"] == nil || created.header.Get("Location") != targets+"/"+id || created.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the target: %v %v", created.body, created.header)
	}
	again := s.call(http.MethodPost, targets, "owner", registering(config, "engineering"))
	if again.status != http.StatusOK || !reflect.DeepEqual(again.body, created.body) || again.header.Get("Location") != "" {
		t.Errorf("the target registered again: %d %v, want 200 and the target as it was answered", again.status, again.body)
	}
	other := s.call(http.MethodPost, targets, "owner", registering(config, "preview"))
	s.require(other, http.StatusCreated, "")
	s.expect("a target is a portal over a configuration of the product", "owner", []exchange{
		{http.MethodPost, targets, registering(ids.NewUUID(), "engineering"), http.StatusNotFound, "AUDIENCE_CONFIG_NOT_FOUND"},
		{http.MethodPost, targets, registering(foreign, "engineering"), http.StatusNotFound, "AUDIENCE_CONFIG_NOT_FOUND"},
		{http.MethodPost, targets, registering(config, "Engineering"), http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, targets, registering(config, ""), http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, targets, `{"portal_key":"engineering"}`, http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodGet, targets + "/" + ids.NewUUID(), "", http.StatusNotFound, "PUBLICATION_TARGET_NOT_FOUND"},
		{http.MethodGet, products + parcel + "/publication-targets/" + id, "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	})
	s.require(s.call(http.MethodGet, products+parcel+"/publication-targets/"+id, "parcel-owner", ""), http.StatusNotFound, "PUBLICATION_TARGET_NOT_FOUND")
	if listed, _ := s.page(products+parcel+"/publication-targets", "parcel-owner"); len(listed) != 0 {
		t.Errorf("another product lists %d targets, want none", len(listed))
	}

	// Whoever knows the product reads its targets.
	for _, caller := range append([]string{"owner"}, refusedCallers...) {
		read := s.call(http.MethodGet, targets+"/"+id, caller, "")
		listed, next := s.page(targets, caller)
		if read.status != http.StatusOK || !reflect.DeepEqual(read.body, created.body) || len(listed) != 2 || next != "" ||
			!reflect.DeepEqual(listed[0], created.body) || !reflect.DeepEqual(listed[1], other.body) {
			t.Errorf("%s reads the target as %d %v and lists %v", caller, read.status, read.body, listed)
		}
	}
	for _, path := range []string{targets, targets + "/" + id} {
		s.require(s.call(http.MethodGet, path, "stranger", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
		s.require(s.call(http.MethodGet, path, "parcel-owner", ""), http.StatusNotFound, "PRODUCT_NOT_FOUND")
	}

	// A configuration that was removed takes no new target.
	s.require(s.call(http.MethodDelete, products+s.product+"/audience-configs/"+config, "owner", ""), http.StatusNoContent, "")
	s.require(s.call(http.MethodPost, targets, "owner", registering(config, "later")), http.StatusNotFound, "AUDIENCE_CONFIG_NOT_FOUND")

	// The two targets are on record under the rule of the owners and left
	// an event each; the one that was answered again left neither, and
	// every refusal for lack of the role is on record as one.
	if done, events := s.on("owner", "publication.target.create", "AC-09", true), s.announced("fabric.publication.target.created"); done != 2 || events != 2 {
		t.Errorf("%d targets are on record and %d events were written, want 2 of each", done, events)
	}
	for _, caller := range refusedCallers {
		if got := s.on(caller, "publication.target.create", "DENY-03", false); got != 1 {
			t.Errorf("%d refusals of %s are on record, want 1", got, caller)
		}
	}
	recorded := s.recorded(func(row audited) bool { return row.Action == "publication.target.create" && row.Outcome == "SUCCEEDED" })
	for row := range recorded {
		if row.ResourceType != "PublicationTarget" || (row.ResourceID != id && row.ResourceID != other.id("publication_target_id")) {
			t.Errorf("the record of a target is of %s %s", row.ResourceType, row.ResourceID)
		}
	}
}
