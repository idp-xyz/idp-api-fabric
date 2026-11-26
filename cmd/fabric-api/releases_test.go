package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// preparing is the body a release is asked for with: a revision, a check of
// it and a version, and the waivers if there are any.
func preparing(revision, check, version string, waivers ...map[string]string) string {
	body := map[string]any{"revision_id": revision, "check_run_id": check, "version": version}
	if waivers != nil {
		body["waivers"] = waivers
	}
	return jsonBody(body)
}

func waived(rule, reason string) map[string]string {
	return map[string]string{"rule_id": rule, "reason": reason}
}

// A publisher of the workspace makes a checked revision a release: the files
// as they were recorded, under a version of the asset, bound to the check it
// rests on. Nobody approves it first, and whatever status the revision has is
// not asked. The release is written once. Asking again for the same version
// of the same revision on the same check answers the release that is there,
// and the version names no other one. Whoever imported and checked the
// revision releases it once it is a publisher.
func TestAPublisherPreparesAnImmutableReleaseOfACheckedRevision(t *testing.T) {
	s := publicationFor(t)
	releases := s.asset + "/releases"
	revision, check := s.checkedRevision(revisionSource, "")
	other, otherCheck := s.checkedRevision(revisionSource+"# next\n", "")

	// Reading the revision is not releasing it, and neither is owning the
	// product or maintaining the workspace.
	for _, caller := range []string{"author", "reviewer", "owner", "maintainer"} {
		s.require(s.prepare(revision, check, "1.0.0", caller), http.StatusForbidden, "FORBIDDEN")
	}
	if rows := s.count("contractregistry.contract_release") + s.count("contractregistry.release_artifact"); rows != 0 {
		t.Fatalf("the refusals left %d rows of releases and artifacts", rows)
	}

	first := s.prepare(revision, check, "1.0.0", "publisher")
	s.require(first, http.StatusCreated, "")
	id := first.id("release_id")
	imported := s.call(http.MethodGet, s.revisions+"/"+revision, "author", "")
	if imported.body["status"] != "DRAFT" {
		t.Fatalf("the released revision is %v, want the draft it was recorded as", imported.body["status"])
	}
	if first.body["status"] != "READY" || first.body["version"] != "1.0.0" || first.body["revision_id"] != revision ||
		first.body["check_run_id"] != check || first.body["review_id"] != nil || !reflect.DeepEqual(first.body["waivers"], []any{}) ||
		first.body["artifact_format"] != "fabric-openapi-source/v1" || first.body["ref_closure_digest"] != imported.body["ref_closure_digest"] ||
		first.body["prepared_by"].(map[string]any)["subject"] != "publisher" ||
		first.header.Get("Location") != releases+"/"+id || first.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the release: %v %v", first.body, first.header)
	}
	again := s.prepare(revision, check, "1.0.0", "publisher")
	if again.status != http.StatusOK || !reflect.DeepEqual(again.body, first.body) || again.header.Get("Location") != "" {
		t.Fatalf("the same release asked again: %d %v, want 200 and the release as it was answered", again.status, again.body)
	}
	s.expect("a version names one release", "publisher", []exchange{
		{http.MethodPost, releases, preparing(other, otherCheck, "1.0.0"), http.StatusConflict, "RELEASE_VERSION_CONFLICT"},
		{http.MethodPost, releases, preparing(revision, otherCheck, "1.0.0"), http.StatusConflict, "RELEASE_VERSION_CONFLICT"},
		{http.MethodPost, releases, preparing(other, check, "1.0.0"), http.StatusConflict, "RELEASE_VERSION_CONFLICT"},
		{http.MethodPost, releases, preparing(revision, check, "1.0.0", waived("F04-GOV-003", "not asked for")), http.StatusConflict, "RELEASE_VERSION_CONFLICT"},
	})
	s.expect("a release rests on a recorded check of its revision", "publisher", []exchange{
		{http.MethodPost, releases, preparing(other, check, "2.0.0"), http.StatusNotFound, "CHECK_RUN_NOT_FOUND"},
		{http.MethodPost, releases, preparing(revision, "00000000-0000-4000-8000-000000000000", "2.0.0"), http.StatusNotFound, "CHECK_RUN_NOT_FOUND"},
		{http.MethodPost, releases, preparing("00000000-0000-4000-8000-000000000000", check, "2.0.0"), http.StatusNotFound, "REVISION_NOT_FOUND"},
		{http.MethodPost, releases, preparing(revision, check, "two point zero"), http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, releases, preparing(revision, check, ""), http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, releases, jsonBody(map[string]string{"revision_id": revision, "review_id": check, "version": "2.0.0"}), http.StatusBadRequest, "REQUEST_INVALID"},
	})

	// The same files under another version are another release of the same
	// bytes: the artifact is kept once.
	second := s.prepare(revision, check, "1.0.1", "publisher")
	s.require(second, http.StatusCreated, "")
	if second.id("release_id") == id || second.body["artifact_digest"] != first.body["artifact_digest"] ||
		s.count("contractregistry.contract_release") != 2 || s.count("contractregistry.release_artifact") != 1 {
		t.Fatalf("a second version of the same revision: %v; %d releases, %d artifacts", second.body,
			s.count("contractregistry.contract_release"), s.count("contractregistry.release_artifact"))
	}

	// Whoever reads the revisions of the workspace reads its releases.
	for _, caller := range []string{"author", "reviewer", "publisher"} {
		if read := s.call(http.MethodGet, releases+"/"+id, caller, ""); read.status != http.StatusOK || !reflect.DeepEqual(read.body, first.body) {
			t.Errorf("%s reads the release: %d %v, want it as it was answered", caller, read.status, read.body)
		}
	}
	page := s.call(http.MethodGet, releases+"?page_size=1", "publisher", "")
	s.require(page, http.StatusOK, "")
	rest := s.call(http.MethodGet, releases+"?page_size=1&page_token="+page.id("next_page_token"), "publisher", "")
	if items := page.body["items"].([]any); len(items) != 1 || !reflect.DeepEqual(items[0], first.body) || rest.status != http.StatusOK ||
		!reflect.DeepEqual(rest.body["items"], []any{second.body}) || rest.body["next_page_token"] != nil {
		t.Errorf("the releases, a page at a time: %v then %v", page.body, rest.body)
	}
	tracking := strings.TrimSuffix(s.revisionAsset(s.product, s.workspace, "maintainer", "tracking-api", "HTTP"), "/revisions")
	s.expect("a release is read under its asset", "publisher", []exchange{
		{http.MethodGet, tracking + "/releases/" + id, "", http.StatusNotFound, "RELEASE_NOT_FOUND"},
		{http.MethodGet, releases + "/00000000-0000-4000-8000-000000000000", "", http.StatusNotFound, "RELEASE_NOT_FOUND"},
		{http.MethodGet, releases + "/latest", "", http.StatusBadRequest, "REQUEST_INVALID"},
	})
	s.expect("owning the product authorizes reading its releases", "owner", []exchange{
		{http.MethodGet, releases + "/" + id, "", http.StatusOK, ""},
		{http.MethodGet, releases, "", http.StatusOK, ""},
	})

	// Whoever imported and checked a revision releases it once it is a
	// publisher: nobody has to be independent of what is released.
	s.join(s.product, s.workspace, "owner", "author", "internal_publisher")
	own := s.prepare(other, otherCheck, "2.0.0", "author")
	s.require(own, http.StatusCreated, "")
	if own.body["prepared_by"].(map[string]any)["subject"] != "author" {
		t.Errorf("the release of the author: %v", own.body)
	}

	// The artifact is the files as they were recorded, under its digest.
	var payload []byte
	digest, _ := first.body["artifact_digest"].(string)
	if err := s.owner.QueryRow(t.Context(), `SELECT payload FROM contractregistry.release_artifact WHERE artifact_digest = $1`, digest).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		Format     string `json:"format"`
		Entrypoint string `json:"entrypoint"`
		Closure    string `json:"ref_closure_digest"`
		Files      []struct {
			Path    string `json:"path"`
			Content []byte `json:"content_base64"`
		} `json:"files"`
	}
	if err := json.Unmarshal(payload, &bundle); err != nil || fmt.Sprintf("sha256:%x", sha256.Sum256(payload)) != digest ||
		bundle.Format != "fabric-openapi-source/v1" || bundle.Entrypoint != "openapi.yaml" || bundle.Closure != first.body["ref_closure_digest"] ||
		len(bundle.Files) != 1 || bundle.Files[0].Path != "openapi.yaml" || string(bundle.Files[0].Content) != revisionSource {
		t.Fatalf("the artifact is %s (%v), want the recorded files under %s", payload, err, digest)
	}

	// The process can add a release and read it. It cannot change or remove one.
	app := pgtest.Connect(t, s.database.Application)
	for _, statement := range []string{
		`UPDATE contractregistry.contract_release SET release_version = 'other'`,
		`UPDATE contractregistry.contract_release SET waivers = '[]'`,
		`DELETE FROM contractregistry.contract_release`,
		`UPDATE contractregistry.release_artifact SET payload = 'x'`,
		`DELETE FROM contractregistry.release_artifact`,
	} {
		if _, err := app.Exec(t.Context(), statement); err == nil || !strings.Contains(err.Error(), "42501") {
			t.Errorf("%s as the application role: %v, want it refused for lack of privilege", statement, err)
		}
	}

	// Each release that was recorded left one audit record and one event;
	// the one that was answered again left neither, and every refusal of
	// the right to release is on record as one.
	recorded := occurrences(s.recorded(func(row audited) bool {
		return row.Action == "release.prepare" && row.Outcome == "SUCCEEDED" && row.Rule == "AC-04" &&
			(row.Subject == "publisher" || row.Subject == "author") && row.ResourceType == "ContractRelease" && row.ResourceID != ""
	}))
	refused := occurrences(s.recorded(func(row audited) bool {
		return row.Action == "release.prepare" && row.Outcome != "SUCCEEDED" && row.Rule == "DENY-03"
	}))
	var events int
	if err := s.owner.QueryRow(t.Context(), `SELECT count(*) FROM bento.outbox
WHERE event_type = 'fabric.contractregistry.release.prepared' AND scope = $1`, s.product).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if recorded != 3 || refused != 4 || events != 3 {
		t.Errorf("%d releases and %d refusals are on record and %d events were written, want 3, 4 and 3: %v", recorded, refused, events,
			s.recorded(func(row audited) bool { return strings.HasPrefix(row.Action, "release.") }))
	}
}

// A release rests on a check of today: one made under the policy and by the
// tools a check is made with now. Every rule that failed or could not be
// judged in it is waived by name, for a reason, by whoever prepares the
// release, and nothing else is: not a rule that passed, not one the policy
// does not have, not one twice, and never a rule that nothing waives, which
// keeps any release from being prepared when it did not pass. The waivers are
// part of the release and are read with it.
func TestAReleaseRestsOnACurrentCheckWithEveryRuleThatDidNotPassWaived(t *testing.T) {
	s := publicationFor(t)
	releases := s.asset + "/releases"
	clean, cleanCheck := s.checkedRevision(revisionSource, "")
	// The same revision as a later version of the tool checked it.
	stale := recordAnotherCheck(t, checkFixture{stack: s.stack}, domain.RevisionRef{
		ProductID: s.product, WorkspaceID: s.workspace, AssetID: last(s.asset), RevisionID: clean,
	}, cleanCheck)
	// A contract whose operation has no operationId, a rule that can be
	// waived; and one that states no security either, a rule that cannot.
	unnamed, unnamedCheck := s.checkedRevision(unnamedSource, "")
	flawed, flawedCheck := s.checkedRevision(checkSource, "")
	because := waived("F04-GOV-003", "generated clients are not used yet")

	s.expect("a check a release cannot rest on", "publisher", []exchange{
		{http.MethodPost, releases, preparing(clean, stale, "1.0.0"), http.StatusConflict, "POLICY_STALE"},
		{http.MethodPost, releases, preparing(flawed, flawedCheck, "1.0.0"), http.StatusConflict, "RELEASE_BLOCKED"},
		{http.MethodPost, releases, preparing(flawed, flawedCheck, "1.0.0", because), http.StatusConflict, "RELEASE_BLOCKED"},
		{http.MethodPost, releases, preparing(flawed, flawedCheck, "1.0.0", because, waived("F04-GOV-006", "security comes later")), http.StatusUnprocessableEntity, "WAIVER_FORBIDDEN"},
	})
	s.expect("waivers that do not cover what did not pass, exactly", "publisher", []exchange{
		{http.MethodPost, releases, preparing(unnamed, unnamedCheck, "1.0.0"), http.StatusConflict, "WAIVER_REQUIRED"},
		{http.MethodPost, releases, preparing(unnamed, unnamedCheck, "1.0.0", []map[string]string{}...), http.StatusConflict, "WAIVER_REQUIRED"},
		{http.MethodPost, releases, preparing(unnamed, unnamedCheck, "1.0.0", because, waived("F04-GOV-002", "it passed")), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{http.MethodPost, releases, preparing(unnamed, unnamedCheck, "1.0.0", because, waived("F04-GOV-999", "no such rule")), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{http.MethodPost, releases, preparing(unnamed, unnamedCheck, "1.0.0", because, because), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{http.MethodPost, releases, preparing(unnamed, unnamedCheck, "1.0.0", waived("F04-GOV-003", " padded ")), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
		{http.MethodPost, releases, preparing(unnamed, unnamedCheck, "1.0.0", because, waived("F04-GOV-006", "it passed")), http.StatusUnprocessableEntity, "WAIVER_FORBIDDEN"},
		{http.MethodPost, releases, preparing(unnamed, unnamedCheck, "1.0.0", waived("F04-GOV-003", "two\nlines")), http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, releases, preparing(unnamed, unnamedCheck, "1.0.0", map[string]string{"rule_id": "F04-GOV-003"}), http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, releases, preparing(clean, cleanCheck, "1.0.0", because), http.StatusUnprocessableEntity, "REQUEST_UNPROCESSABLE"},
	})
	if got := s.count("contractregistry.contract_release"); got != 0 {
		t.Fatalf("the refusals left %d releases", got)
	}

	released := s.prepare(unnamed, unnamedCheck, "1.0.0", "publisher", waiver{"F04-GOV-003", "generated clients are not used yet"})
	s.require(released, http.StatusCreated, "")
	want := []any{map[string]any{"rule_id": "F04-GOV-003", "reason": "generated clients are not used yet"}}
	if !reflect.DeepEqual(released.body["waivers"], want) || released.body["check_run_id"] != unnamedCheck || released.body["review_id"] != nil {
		t.Fatalf("the release with its waiver: %v", released.body)
	}
	read := s.call(http.MethodGet, released.header.Get("Location"), "author", "")
	listed, _ := s.page(releases, "reviewer")
	if read.status != http.StatusOK || !reflect.DeepEqual(read.body, released.body) || len(listed) != 1 || !reflect.DeepEqual(listed[0], released.body) {
		t.Errorf("the release is read as %d %v and listed as %v, want it as it was answered", read.status, read.body, listed)
	}
	var stored string
	if err := s.owner.QueryRow(t.Context(), `SELECT waivers::text FROM contractregistry.contract_release WHERE release_id = $1`,
		released.id("release_id")).Scan(&stored); err != nil || stored != `[{"reason": "generated clients are not used yet", "rule_id": "F04-GOV-003"}]` {
		t.Errorf("the table holds the waivers %s (%v)", stored, err)
	}
	// The same release is asked for again with the same waivers, and with
	// others it is another release, which the version does not name.
	again := s.prepare(unnamed, unnamedCheck, "1.0.0", "publisher", waiver{"F04-GOV-003", "generated clients are not used yet"})
	if again.status != http.StatusOK || !reflect.DeepEqual(again.body, released.body) {
		t.Errorf("the release asked again with its waivers: %d %v", again.status, again.body)
	}
	s.expect("another waiver under the version", "publisher", []exchange{
		{http.MethodPost, releases, preparing(unnamed, unnamedCheck, "1.0.0", waived("F04-GOV-003", "another reason")), http.StatusConflict, "RELEASE_VERSION_CONFLICT"},
		{http.MethodPost, releases, preparing(unnamed, unnamedCheck, "1.0.0"), http.StatusConflict, "RELEASE_VERSION_CONFLICT"},
	})
	if got := s.count("contractregistry.contract_release"); got != 1 {
		t.Errorf("%d releases are recorded, want the one", got)
	}
}

// A release is prepared in a product that takes new work, and wholly or not
// at all.
func TestAReleaseIsRecordedWhollyOrNotAtAll(t *testing.T) {
	s := publicationFor(t)
	releases := s.asset + "/releases"
	revision, check := s.checkedRevision(revisionSource, "")

	// Nothing is left of a release whose event could not be written.
	if _, err := s.owner.Exec(t.Context(), `ALTER TABLE bento.outbox ADD CONSTRAINT test_reject_release
CHECK (event_type <> 'fabric.contractregistry.release.prepared')`); err != nil {
		t.Fatal(err)
	}
	records := s.count("platformoperations.audit_record")
	s.require(s.prepare(revision, check, "1.0.0", "publisher"), http.StatusInternalServerError, "INTERNAL_ERROR")
	if s.count("contractregistry.contract_release") != 0 || s.count("contractregistry.release_artifact") != 0 ||
		s.count("platformoperations.audit_record") != records {
		t.Fatal("the release that failed left a release, an artifact or an audit record")
	}
	if _, err := s.owner.Exec(t.Context(), `ALTER TABLE bento.outbox DROP CONSTRAINT test_reject_release`); err != nil {
		t.Fatal(err)
	}

	// A suspended product takes no release and still shows the ones it has.
	first := s.prepare(revision, check, "1.0.0", "publisher")
	s.require(first, http.StatusCreated, "")
	s.suspend("SUSPENDED")
	s.require(s.prepare(revision, check, "1.0.1", "publisher"), http.StatusConflict, "PRODUCT_NOT_ACTIVE")
	s.require(s.call(http.MethodGet, releases+"/"+first.id("release_id"), "author", ""), http.StatusOK, "")
	s.suspend("ACTIVE")
	s.require(s.prepare(revision, check, "1.0.1", "publisher"), http.StatusCreated, "")
	if s.count("contractregistry.contract_release") != 2 {
		t.Fatalf("%d releases are recorded, want the two", s.count("contractregistry.contract_release"))
	}
}
