package main

import (
	"net/http"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// preparedOn records a release of the same revision and bytes as the release
// that is named, under the version, as if it had been prepared on the check
// that is named: a check of that revision that the policy and the tools of
// today have moved on from. That is what every release becomes once a build
// with another version of the tools starts. It returns the identifier of the
// release.
func (s *releaseFixture) preparedOn(release, check, version string) string {
	s.t.Helper()
	id := ids.NewUUID()
	tag, err := s.owner.Exec(s.t.Context(), `
INSERT INTO contractregistry.contract_release (
    release_id, product_id, workspace_id, api_asset_id, revision_id, check_run_id, policy_snapshot_id, policy_digest,
    input_digest, artifact_digest, release_version, prepared_at, prepared_by_issuer, prepared_by_subject
)
SELECT $1, product_id, workspace_id, api_asset_id, revision_id, $3, policy_snapshot_id, policy_digest,
       input_digest, artifact_digest, $4, clock_timestamp(), prepared_by_issuer, prepared_by_subject
FROM contractregistry.contract_release WHERE release_id = $2`, id, release, check, version)
	if err != nil || tag.RowsAffected() != 1 {
		s.t.Fatalf("record a release on the check %s: %v, %d rows", check, err, tag.RowsAffected())
	}
	return id
}

// A release rests on the check it was prepared on, for good. Once the policy
// and the tools of today have moved on from that check, no release is
// prepared on it any more, and the release that was is still put on a slot,
// served, taken down, put up again and returned to: by any publisher, the one
// who imported and checked the revision included, on a slot it was on and on
// one it never was. An access block stops the read and the return as it does
// for any release, and lets both through once it is cleared.
func TestAReleaseIsPublishedOnTheCheckItWasPreparedOnWhateverCameAfter(t *testing.T) {
	s := publicationFor(t)
	// The author publishes too, and imported and checked the revision.
	s.join(s.product, s.workspace, "owner", "author", "internal_publisher")
	target, configPath := s.target(internalSandbox, "engineering")
	slot, artifact := assetSlot(target, s.asset), assetSlot(target, s.asset)+"/artifact"
	// Another portal of the same audience, which the release was never on.
	preview := s.call(http.MethodPost, "/v1/products/"+s.product+"/publication-targets", "owner", jsonBody(map[string]string{
		"audience_config_id": last(configPath), "portal_key": "preview",
	}))
	s.require(preview, http.StatusCreated, "")
	elsewhere := assetSlot(preview.header.Get("Location"), s.asset)

	revision, check, current := s.released(revisionSource, "", "1.0.0")
	// The same revision as a later version of the tool checked it: to that
	// version, the check the release was prepared on is one of before.
	later := recordAnotherCheck(t, checkFixture{stack: s.stack}, domain.RevisionRef{
		ProductID: s.product, WorkspaceID: s.workspace, AssetID: last(s.asset), RevisionID: revision,
	}, check)
	id := s.preparedOn(current, later, "0.9.0")
	digest := s.call(http.MethodGet, s.asset+"/releases/"+id, "publisher", "").id("artifact_digest")
	s.require(s.prepare(revision, later, "0.9.1", "publisher"), http.StatusConflict, "POLICY_STALE")

	s.require(s.change(slot, "author", "ACTIVATE", 0, id, "", "first"), http.StatusOK, "")
	s.requireArtifact(slot, digest, revisionSource)
	for _, reader := range []string{"coauthor", "reviewer", "publisher", "owner"} {
		s.require(s.call(http.MethodGet, artifact, reader, ""), http.StatusOK, "")
	}
	if got := s.call(http.MethodGet, slot, "publisher", ""); got.status != http.StatusOK || got.body["active_release_id"] != id ||
		got.body["visibility"] != "ACTIVE" || len(got.body) != 8 {
		t.Fatalf("the slot reads %d %v, want it to point at %s and to say nothing else of the release", got.status, got.body, id)
	}
	s.require(s.change(elsewhere, "publisher", "ACTIVATE", 0, id, "", "elsewhere"), http.StatusOK, "")

	s.require(s.change(slot, "author", "WITHDRAW", 1, "", "taken down for a while", "down"), http.StatusOK, "")
	s.require(s.call(http.MethodGet, artifact, "coauthor", ""), http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	s.require(s.change(slot, "publisher", "ACTIVATE", 2, id, "", "anew"), http.StatusOK, "")
	s.require(s.change(slot, "author", "WITHDRAW", 3, "", "taken down again", "down-again"), http.StatusOK, "")
	back := s.change(slot, "author", "RESTORE", 4, id, "nothing else to serve", "back")
	s.require(back, http.StatusOK, "")
	if back.body["generation"] != float64(5) || back.body["action"] != "RESTORE" || back.body["release_id"] != id ||
		back.body["previous_release_id"] != nil || back.body["reason"] != "nothing else to serve" ||
		back.body["performed_by"].(map[string]any)["subject"] != "author" {
		t.Fatalf("the return to the release: %v", back.body)
	}
	s.requireArtifact(slot, digest, revisionSource)

	s.require(s.securityRole("security", "security_admin", "owner"), http.StatusCreated, "")
	s.require(s.securityRole("clearer", "security_reviewer", "owner"), http.StatusCreated, "")
	block := s.call(http.MethodPost, target+"/access-blocks", "security", `{"reason":"investigate"}`)
	s.require(block, http.StatusCreated, "")
	s.require(s.call(http.MethodGet, artifact, "coauthor", ""), http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
	s.require(s.change(slot, "publisher", "WITHDRAW", 5, "", "under investigation", "blocked-down"), http.StatusOK, "")
	s.require(s.change(slot, "publisher", "RESTORE", 6, id, "too early", "blocked"), http.StatusConflict, "PUBLICATION_BLOCKED")
	s.require(s.call(http.MethodPost, block.header.Get("Location")+"/clearance", "clearer", `{"reason":"nothing found"}`), http.StatusOK, "")
	s.require(s.change(slot, "publisher", "RESTORE", 6, id, "nothing was found", "cleared"), http.StatusOK, "")
	s.requireArtifact(slot, digest, revisionSource)

	// Every change is on record under the rule of publishing, and the one
	// refused for the block as a refusal of it.
	for _, want := range []struct {
		subject, action, rule string
		done                  bool
		count                 int
	}{
		{"author", "publication.activate", "AC-04", true, 1},
		{"publisher", "publication.activate", "AC-04", true, 2},
		{"author", "publication.withdraw", "AC-05", true, 2},
		{"author", "publication.restore", "AC-04", true, 1},
		{"publisher", "publication.restore", "AC-04", true, 1},
		{"publisher", "publication.restore", "DENY-03", false, 1},
		{"publisher", "release.prepare", "DENY-03", false, 0},
	} {
		if got := s.on(want.subject, want.action, want.rule, want.done); got != want.count {
			t.Errorf("%d records of %s by %s under %s, want %d", got, want.action, want.subject, want.rule, want.count)
		}
	}
	if got := s.announced("fabric.publication.slot.restored"); got != 2 {
		t.Errorf("%d events of a return, want 2", got)
	}
}

// A read through a slot verifies the release it serves and does not read
// the check. Bytes that are not the ones the release names, and a release
// that was prepared from another closure than its revision has, are a
// failure of the service and nothing is served. Evidence of the check that
// is not what was recorded is no matter of the read: the artifact is served.
// Reading the slot verifies nothing of either. A return to a release is held
// to both, the bytes and the check being the one the release was prepared
// on, and changes nothing while either is not what was recorded. The answer
// says nothing of what is wrong; the log does.
func TestAReadThroughASlotVerifiesTheReleaseAndNotItsCheck(t *testing.T) {
	s := publicationFor(t)
	target, _ := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	second := strings.ReplaceAll(revisionSource, "v1", "v2")
	firstRevision, _, first := s.released(revisionSource, "", "1.0.0")
	_, _, next := s.released(second, firstRevision, "2.0.0")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, first, "", "first"), http.StatusOK, "")
	s.require(s.change(slot, "publisher", "ACTIVATE", 1, next, "", "next"), http.StatusOK, "")
	// served is the release the slot points at, and before the one it
	// served first.
	served, before := part(s.provenanceOf(next), "release"), part(s.provenanceOf(first), "release")
	s.requireArtifact(slot, served["artifact_digest"].(string), second)

	var constraint, definition string
	if err := s.owner.QueryRow(t.Context(), `SELECT conname, pg_get_constraintdef(oid) FROM pg_constraint
WHERE conrelid = 'contractregistry.release_artifact'::regclass AND contype = 'c' AND pg_get_constraintdef(oid) LIKE '%sha256%'`).Scan(&constraint, &definition); err != nil {
		t.Fatal(err)
	}
	run := func(statements ...string) {
		t.Helper()
		for _, statement := range statements {
			if _, err := s.owner.Exec(t.Context(), statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
	}
	zero := "sha256:" + strings.Repeat("0", 64)
	const (
		damagedBytes = "the release artifact does not match its digest"
		damagedCheck = "the stored check run is not the one that was recorded"
	)
	// damage is something of a release that is not what was recorded: how
	// it is made so and put right again, and what the log says of it.
	type damage struct {
		break_, mend []string
		cause        string
	}
	// bytesOf adds a byte to the artifact of the release, closureOf has
	// the release prepared from another closure than its revision has,
	// checkOf rewrites the closure the check of the release read, and
	// policyOf has the release name another policy than its check was held
	// to.
	bytesOf := func(release map[string]any) damage {
		byDigest := " WHERE artifact_digest = '" + release["artifact_digest"].(string) + "'"
		return damage{
			[]string{"ALTER TABLE contractregistry.release_artifact DROP CONSTRAINT " + constraint,
				`UPDATE contractregistry.release_artifact SET payload = payload || '\x20'::bytea` + byDigest},
			[]string{"UPDATE contractregistry.release_artifact SET payload = substring(payload FROM 1 FOR octet_length(payload) - 1)" + byDigest,
				"ALTER TABLE contractregistry.release_artifact ADD CONSTRAINT " + constraint + " " + definition},
			damagedBytes,
		}
	}
	rewritten := func(table, column, key, keyColumn string, release map[string]any, recorded, cause string) damage {
		set := func(value string) []string {
			return []string{"UPDATE " + table + " SET " + column + " = '" + value + "' WHERE " + keyColumn + " = '" + release[key].(string) + "'"}
		}
		return damage{set(zero), set(recorded), cause}
	}
	closureOf := func(release map[string]any) damage {
		return rewritten("contractregistry.contract_release", "input_digest", "release_id", "release_id", release,
			release["ref_closure_digest"].(string), damagedBytes)
	}
	checkOf := func(release map[string]any) damage {
		return rewritten("policyreview.check_run", "input_digest", "check_run_id", "check_run_id", release,
			release["ref_closure_digest"].(string), damagedCheck)
	}
	policyOf := func(release map[string]any) damage {
		return rewritten("contractregistry.contract_release", "policy_digest", "release_id", "release_id", release,
			release["policy_digest"].(string), damagedCheck)
	}
	// failed fails the test unless the answer is a failure of the service
	// that says nothing of what is wrong, and the log says what is.
	failed := func(what string, got answer, logged int, cause string) {
		t.Helper()
		entry := s.log.String()[logged:]
		trace, _ := got.body["trace_id"].(string)
		if got.status != http.StatusInternalServerError || got.code() != "INTERNAL_ERROR" || len(got.body) != 5 || trace == "" ||
			!strings.Contains(entry, cause) || !strings.Contains(entry, "correlation_id="+trace) {
			t.Errorf("%s: %d %v, want 500 INTERNAL_ERROR and nothing of what is wrong, and the log to say %q under the identifier of the request\n%s", what, got.status, got.body, cause, entry)
		}
	}
	pointing := func(when, release string, generation float64) {
		t.Helper()
		if got := s.call(http.MethodGet, slot, "publisher", ""); got.status != http.StatusOK || got.body["active_release_id"] != release ||
			got.body["generation"] != generation {
			t.Errorf("%s the slot reads %d %v, want it to point at %s at generation %v", when, got.status, got.body, release, generation)
		}
	}

	// What the slot serves.
	for name, c := range map[string]struct {
		damage damage
		// served says that the artifact is served all the same.
		served bool
	}{
		"a byte was added to the artifact":                       {bytesOf(served), false},
		"the release was prepared from another closure":          {closureOf(served), false},
		"the closure the check read was rewritten":               {checkOf(served), true},
		"the release names another policy than its check was on": {policyOf(served), true},
	} {
		t.Run("what is served: "+name, func(t *testing.T) {
			run(c.damage.break_...)
			logged := len(s.log.String())
			if got := s.call(http.MethodGet, slot+"/artifact", "author", ""); c.served {
				s.require(got, http.StatusOK, "")
			} else {
				failed("the artifact", got, logged, c.damage.cause)
			}
			pointing("with the record damaged", next, 2)
			run(c.damage.mend...)
			pointing("with the record as it was recorded", next, 2)
			s.requireArtifact(slot, served["artifact_digest"].(string), second)
		})
	}

	// What the slot returns to. Each return is asked under a key of its
	// own.
	for i, c := range []struct {
		name   string
		damage damage
	}{
		{"a byte was added to the artifact", bytesOf(before)},
		{"the release was prepared from another closure", closureOf(before)},
		{"the closure the check read was rewritten", checkOf(before)},
		{"the release names another policy than its check was on", policyOf(before)},
	} {
		t.Run("what is returned to: "+c.name, func(t *testing.T) {
			run(c.damage.break_...)
			logged := len(s.log.String())
			key := "back-" + string(rune('a'+i))
			failed("the return", s.change(slot, "publisher", "RESTORE", 2, first, "the second breaks clients", key), logged, c.damage.cause)
			pointing("after the return that failed", next, 2)
			run(c.damage.mend...)
		})
	}
	if got := s.count("publication.operation"); got != 2 {
		t.Fatalf("%d operations are recorded, want the two activations", got)
	}
	s.require(s.change(slot, "publisher", "RESTORE", 2, first, "the second breaks clients", "back"), http.StatusOK, "")
	s.requireArtifact(slot, before["artifact_digest"].(string), revisionSource)
}
