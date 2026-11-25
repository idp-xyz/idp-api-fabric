package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// lapse is a way the approval of a published release stops holding.
type lapse struct {
	// source is the contract that is published. waived says that its check
	// did not pass a rule a reviewer may waive, and that the approval
	// waives it.
	source string
	waived bool
	// happen makes the approval of the review stop holding.
	happen func(s *releaseFixture, review string)
	// code is what the slot says of it, and what an activation is refused
	// with.
	code string
}

// lapses are the ways an approval stops holding that the service can be
// brought to from outside. That the policy and the tools moved on from the
// check is held where the approval is verified, in policyreview.
var lapses = map[string]lapse{
	"the approval runs out": {
		source: revisionSource, code: "APPROVAL_EXPIRED",
		happen: func(s *releaseFixture, review string) {
			s.t.Helper()
			tag, err := s.owner.Exec(s.t.Context(), `UPDATE policyreview.review_conclusion
SET valid_until = concluded_at + interval '1 millisecond' WHERE review_id = $1`, review)
			if err != nil || tag.RowsAffected() != 1 {
				s.t.Fatalf("end the approval: %v, %d rows", err, tag.RowsAffected())
			}
		},
	},
	"a waiver of the approval runs out": {
		source: unnamedSource, waived: true, code: "WAIVER_EXPIRED",
		happen: func(s *releaseFixture, review string) {
			s.t.Helper()
			// A waiver outlives the decision and not the approval: it
			// ends a moment after it was given, inside the approval.
			tag, err := s.owner.Exec(s.t.Context(), `UPDATE policyreview.review_waiver AS w
SET expires_at = c.concluded_at + interval '1 millisecond'
FROM policyreview.review_conclusion c
WHERE w.review_id = c.review_id AND w.review_id = $1`, review)
			if err != nil || tag.RowsAffected() != 1 {
				s.t.Fatalf("end the waiver: %v, %d rows", err, tag.RowsAffected())
			}
		},
	},
	"the reviewer of the approval loses the grant": {
		source: revisionSource, code: "REVIEWER_AUTH_REVOKED",
		happen: func(s *releaseFixture, _ string) {
			s.t.Helper()
			var membership string
			if err := s.owner.QueryRow(s.t.Context(), `SELECT membership_id FROM productgovernance.workspace_membership
WHERE product_id = $1 AND workspace_id = $2 AND principal_subject = 'reviewer'
  AND role = 'internal_reviewer' AND revoked_at IS NULL`, s.product, s.workspace).Scan(&membership); err != nil {
				s.t.Fatal(err)
			}
			s.revoke("/v1/products/"+s.product+"/workspaces/"+s.workspace+"/memberships/"+membership, "owner")
		},
	},
}

// An approval decides what is put on a slot, and not what stays on it.
// However the approval of a published release stops holding, the slot goes
// on serving the release, byte for byte and to whoever read it, and refuses
// nobody; it names what became of the approval. Nothing new goes out under
// that approval: no other release is prepared under it, and the release is
// put on no slot it was not on, nor on its own slot as a new one. The slot
// is taken down by whoever publishes and returns to the release it served
// at the hands of a publisher who had no part in the revision. An access
// block stops the read and the return as it does under an approval that
// holds.
func TestAnApprovalThatStopsHoldingTakesNothingDownAndPutsNothingUp(t *testing.T) {
	for name, c := range lapses {
		t.Run(name, func(t *testing.T) {
			s := publicationFor(t)
			// The author publishes too, which gives it no say over the
			// revision it recorded.
			s.join(s.product, s.workspace, "owner", "author", "internal_publisher")
			target, configPath := s.target(internalSandbox, "engineering")
			slot, artifact := assetSlot(target, s.asset), assetSlot(target, s.asset)+"/artifact"
			// Another portal of the same audience, which the release
			// was never on.
			preview := s.call(http.MethodPost, "/v1/products/"+s.product+"/publication-targets", "owner", jsonBody(map[string]string{
				"audience_config_id": last(configPath), "portal_key": "preview",
			}))
			s.require(preview, http.StatusCreated, "")
			elsewhere := assetSlot(preview.header.Get("Location"), s.asset)

			until := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
			decision := deciding("APPROVED", "", until)
			if c.waived {
				decision = deciding("APPROVED", "", until, waiving("F04-GOV-003", until.Add(-time.Hour)))
			}
			revision, review := s.approveFrom(uploadRevision("openapi.yaml", c.source), decision, internalSandbox)
			released := s.prepare(revision, review, "1.0.0", "publisher")
			s.require(released, http.StatusCreated, "")
			id, digest := released.id("release_id"), released.id("artifact_digest")
			s.require(s.change(slot, "publisher", "ACTIVATE", 0, id, "", "first"), http.StatusOK, "")
			s.requireArtifact(slot, digest, c.source)
			if got := s.call(http.MethodGet, slot, "publisher", ""); got.body["approval_problem"] != nil {
				t.Fatalf("the slot says %v of an approval that holds", got.body["approval_problem"])
			}

			// shown fails the test unless the slot points at the release
			// at the generation and names what became of its approval.
			shown := func(when string, generation float64) {
				t.Helper()
				got := s.call(http.MethodGet, slot, "publisher", "")
				if got.status != http.StatusOK || got.body["active_release_id"] != id || got.body["generation"] != generation ||
					got.body["visibility"] != "ACTIVE" || got.body["approval_problem"] != c.code {
					t.Fatalf("%s the slot reads %d %v, want it to point at %s at generation %v and to say %s", when, got.status, got.body, id, generation, c.code)
				}
			}

			c.happen(s, review)
			records, events := s.count("platformoperations.audit_record"), s.count("bento.outbox")

			// What the slot serves it goes on serving, and it says why
			// the release would not be put there today. Nobody was
			// refused, so nothing is on record.
			shown("once the approval stopped holding", 1)
			s.requireArtifact(slot, digest, c.source)
			for _, reader := range []string{"coauthor", "publisher", "owner"} {
				s.require(s.call(http.MethodGet, artifact, reader, ""), http.StatusOK, "")
			}
			if after, sent := s.count("platformoperations.audit_record"), s.count("bento.outbox"); after != records || sent != events {
				t.Errorf("the reads left %d audit records and %d events, want none: nothing was changed and nobody refused", after-records, sent-events)
			}

			// Nothing new goes out under the approval: no other release,
			// and the release to no slot it was not on.
			s.require(s.prepare(revision, review, "1.0.1", "publisher"), http.StatusConflict, c.code)
			s.require(s.change(elsewhere, "publisher", "ACTIVATE", 0, id, "", "elsewhere"), http.StatusConflict, c.code)

			// Whoever publishes takes the slot down. It does not take the
			// release as a new one, and returns to it as the one it
			// served, at the hands of a publisher who had no part in the
			// revision.
			s.require(s.change(slot, "author", "WITHDRAW", 1, "", "due another review", "down"), http.StatusOK, "")
			s.require(s.call(http.MethodGet, artifact, "coauthor", ""), http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
			s.require(s.change(slot, "publisher", "ACTIVATE", 2, id, "", "anew"), http.StatusConflict, c.code)
			s.require(s.change(slot, "author", "RESTORE", 2, id, "nothing else to serve", "back-by-the-author"), http.StatusForbidden, "PUBLISHER_NOT_INDEPENDENT")
			back := s.change(slot, "publisher", "RESTORE", 2, id, "nothing else to serve", "back")
			s.require(back, http.StatusOK, "")
			if back.body["generation"] != float64(3) || back.body["action"] != "RESTORE" || back.body["release_id"] != id ||
				back.body["previous_release_id"] != nil || back.body["reason"] != "nothing else to serve" {
				t.Fatalf("the return to the release: %v", back.body)
			}
			shown("after the return to the release", 3)
			s.requireArtifact(slot, digest, c.source)

			// An access block stops the read and the return, whatever
			// became of the approval, and lets both through once it is
			// cleared.
			s.require(s.securityRole("security", "security_admin", "owner"), http.StatusCreated, "")
			s.require(s.securityRole("clearer", "security_reviewer", "owner"), http.StatusCreated, "")
			block := s.call(http.MethodPost, target+"/access-blocks", "security", `{"reason":"investigate"}`)
			s.require(block, http.StatusCreated, "")
			s.require(s.call(http.MethodGet, artifact, "coauthor", ""), http.StatusNotFound, "PUBLISHED_ARTIFACT_NOT_FOUND")
			s.require(s.change(slot, "publisher", "WITHDRAW", 3, "", "under investigation", "down-again"), http.StatusOK, "")
			s.require(s.change(slot, "publisher", "RESTORE", 4, id, "too early", "blocked"), http.StatusConflict, "PUBLICATION_BLOCKED")
			s.require(s.call(http.MethodPost, block.header.Get("Location")+"/clearance", "clearer", `{"reason":"nothing found"}`), http.StatusOK, "")
			s.require(s.change(slot, "publisher", "RESTORE", 4, id, "nothing was found", "cleared"), http.StatusOK, "")
			shown("after the block was cleared and the slot returned", 5)
			s.requireArtifact(slot, digest, c.source)

			// A return is on record as a return under the rule of
			// publishing, and each refusal for who the caller is or for a
			// block as one. What was refused for the approval alone is no
			// refusal of the caller.
			for _, want := range []struct {
				subject, action, rule string
				done                  bool
				count                 int
			}{
				{"publisher", "publication.restore", "AC-04", true, 2},
				{"author", "publication.withdraw", "AC-05", true, 1},
				{"author", "publication.restore", "DENY-03", false, 1},
				{"publisher", "publication.restore", "DENY-03", false, 1},
				{"publisher", "publication.activate", "DENY-03", false, 0},
				{"publisher", "release.prepare", "DENY-03", false, 0},
			} {
				if got := s.on(want.subject, want.action, want.rule, want.done); got != want.count {
					t.Errorf("%d records of %s by %s under %s, want %d", got, want.action, want.subject, want.rule, want.count)
				}
			}
			if got := s.announced("fabric.publication.slot.restored"); got != 2 {
				t.Errorf("%d events of a return, want 2", got)
			}
		})
	}
}

// A read through a slot verifies the release it serves and does not read
// the review. Bytes that are not the ones the release names, and a release
// that was prepared from another closure than its revision has, are a
// failure of the service and nothing is served. Evidence of the approval
// that is not what was recorded is no matter of the read: the artifact is
// served, and it is the slot, which says what became of the approval, that
// fails. A return to a release is held to both, the bytes and the review
// being the approval the release was prepared on, and changes nothing while
// either is not what was recorded. The answer says nothing of what is
// wrong; the log does.
func TestAReadThroughASlotVerifiesTheReleaseAndNotItsReview(t *testing.T) {
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
		damagedBytes  = "the release artifact does not match its digest"
		damagedReview = "the stored review is not the one that was recorded"
	)
	// damage is something of a release that is not what was recorded: how
	// it is made so and put right again, and what the log says of it.
	type damage struct {
		break_, mend []string
		cause        string
	}
	// bytesOf adds a byte to the artifact of the release, closureOf has
	// the release prepared from another closure than its revision has,
	// requestOf rewrites the closure the review of the release was asked
	// on, and policyOf has the release name another policy than its check
	// was held to.
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
	rewritten := func(table, column, key string, release map[string]any, cause string) damage {
		set := func(value string) []string {
			return []string{"UPDATE " + table + " SET " + column + " = '" + value + "' WHERE " + key + " = '" + release[key].(string) + "'"}
		}
		recorded := release[column]
		if column == "input_digest" {
			recorded = release["ref_closure_digest"]
		}
		return damage{set(zero), set(recorded.(string)), cause}
	}
	closureOf := func(release map[string]any) damage {
		return rewritten("contractregistry.contract_release", "input_digest", "release_id", release, damagedBytes)
	}
	requestOf := func(release map[string]any) damage {
		return rewritten("policyreview.review_request", "input_digest", "review_id", release, damagedReview)
	}
	policyOf := func(release map[string]any) damage {
		return rewritten("contractregistry.contract_release", "policy_digest", "release_id", release, damagedReview)
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
			got.body["generation"] != generation || got.body["approval_problem"] != nil {
			t.Errorf("%s the slot reads %d %v, want it to point at %s at generation %v and to say nothing of the approval", when, got.status, got.body, release, generation)
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
		"the closure the review was asked on was rewritten":      {requestOf(served), true},
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
			// The slot verifies the release and the review, and fails for
			// either.
			logged = len(s.log.String())
			failed("the slot", s.call(http.MethodGet, slot, "publisher", ""), logged, c.damage.cause)
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
		{"the closure the review was asked on was rewritten", requestOf(before)},
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
