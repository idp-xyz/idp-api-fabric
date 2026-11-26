package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	checkdomain "go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// checkedFrom imports a revision with the body as the author and has it
// checked. It is checkedRevision for a source that is not an upload with the
// entrypoint openapi.yaml.
func (s *releaseFixture) checkedFrom(imported string) (revisionID, checkID string) {
	s.t.Helper()
	revision := s.call(http.MethodPost, s.revisions, "author", imported)
	s.require(revision, http.StatusCreated, "")
	revisionID = revision.id("revision_id")
	checked := s.call(http.MethodPost, s.revisions+"/"+revisionID+"/check-runs", "author", "")
	s.require(checked, http.StatusCreated, "")
	return revisionID, checked.body["check_run"].(map[string]any)["check_run_id"].(string)
}

// provenanceOf reads where the release came from, as an author does.
func (s *releaseFixture) provenanceOf(release string) answer {
	s.t.Helper()
	got := s.call(http.MethodGet, s.releaseAt(release)+"/provenance", "author", "")
	s.require(got, http.StatusOK, "")
	return got
}

// part is a member of a provenance that is itself an object.
func part(of answer, name string) map[string]any {
	member, _ := of.body[name].(map[string]any)
	return member
}

// noted is the waiver of the rule no operationId breaks, and why.
var noted = waiver{"F04-GOV-003", "generated clients are not used yet"}

// approvedBefore records that the reviewer approved the revision, on its
// check and for the scope, waiving the rules, before reviews were retired,
// and a release of it under that approval with the bytes of the release
// that is named. It returns the review and the release.
func (s *releaseFixture) approvedBefore(revision, check, sameBytes, version string, scope [][2]string, waived ...string) (review, release string) {
	s.t.Helper()
	review = s.asked(s.pathOf(revision), check, "author", scope...)
	s.concluded(review, "reviewer", "APPROVED", "", waived...)
	return review, s.releasedUnderApproval(review, sameBytes, version, "publisher")
}

// A release says what it was prepared on, and that stays what it says when
// the revision is checked again. A release prepared on the later check names
// that one: the release that was prepared before still names the check it
// was prepared on, and does not become the latest of anything. Neither names
// an approval: a release is prepared on a check alone.
func TestTheProvenanceOfAReleaseStaysWhenTheRevisionIsCheckedAgain(t *testing.T) {
	s := publicationFor(t)
	id, first := s.checkedRevision(revisionSource, "")
	revision := s.revisions + "/" + id
	released := s.prepare(id, first, "1.0.0", "publisher")
	s.require(released, http.StatusCreated, "")
	old := released.id("release_id")
	before := s.provenanceOf(old)
	if _, said := before.body["approval"]; said || part(before, "check")["check_run_id"] != first ||
		!reflect.DeepEqual(part(before, "release")["waivers"], []any{}) || part(before, "release")["review_id"] != nil ||
		len(part(before, "check")["tool_manifest"].([]any)) != 1 {
		t.Fatalf("the provenance of the first release: %v", before.body)
	}

	// The revision is checked against another revision of the asset, and a
	// release is prepared on that check.
	baseline := s.call(http.MethodPost, s.revisions, "author", uploadRevision("openapi.yaml", revisionSource+"# to compare with\n"))
	s.require(baseline, http.StatusCreated, "")
	check := s.call(http.MethodPost, revision+"/check-runs?baseline_revision_id="+baseline.id("revision_id"), "author", "")
	s.require(check, http.StatusCreated, "")
	secondCheck := check.body["check_run"].(map[string]any)["check_run_id"].(string)
	renewed := s.prepare(id, secondCheck, "1.0.0-r2", "publisher")
	s.require(renewed, http.StatusCreated, "")

	checks, _ := s.page(revision+"/check-runs", "author")
	if len(checks) != 2 || checks[1]["check_run_id"] != secondCheck {
		t.Fatalf("the checks %v of the revision, want the second last", checks)
	}
	if after := s.provenanceOf(old); !reflect.DeepEqual(after.body, before.body) {
		t.Errorf("the provenance of the first release is\n %v\nafter the revision was checked again, was\n %v", after.body, before.body)
	}
	now := s.provenanceOf(renewed.id("release_id"))
	renewedCheck := part(now, "check")
	if _, said := now.body["approval"]; said || renewedCheck["check_run_id"] != secondCheck || first == secondCheck ||
		len(renewedCheck["tool_manifest"].([]any)) != 2 || !reflect.DeepEqual(part(now, "revision"), part(before, "revision")) ||
		part(now, "release")["release_id"] != renewed.id("release_id") || part(now, "release")["version"] != "1.0.0-r2" {
		t.Errorf("the provenance of the release prepared on the second check is\n %v\nwant that check and the revision as it was", now.body)
	}
}

// A release names the Git origin of its revision as it was declared when the
// revision was recorded, and an upload names none. The branch moving on
// changes neither: the same files at a later commit are the revision that
// was recorded first, other files at a later commit are another revision, and
// each release names the commit of its own.
func TestTheProvenanceNamesTheGitOriginOfItsRevisionAndNotTheBranch(t *testing.T) {
	s := publicationFor(t)
	const entrypoint = "api/openapi.yaml"
	onMain := gitOrigin(contractsRepository, commitOnMain, "refs/heads/main")

	fromGit, gitCheck := s.checkedFrom(importFromGit(entrypoint, revisionSource, onMain))
	git := s.prepare(fromGit, gitCheck, "1.0.0", "publisher")
	s.require(git, http.StatusCreated, "")
	uploaded, uploadCheck := s.checkedRevision(revisionSource+"# uploaded\n", "")
	upload := s.prepare(uploaded, uploadCheck, "1.0.1", "publisher")
	s.require(upload, http.StatusCreated, "")

	onGit, onUpload := s.provenanceOf(git.id("release_id")), s.provenanceOf(upload.id("release_id"))
	if got := part(onGit, "revision"); got["revision_id"] != fromGit || got["revision_sequence"] != float64(1) || got["source_kind"] != "GIT" ||
		!reflect.DeepEqual(got["git_origin"], onMain) || got["entrypoint"] != entrypoint {
		t.Fatalf("the revision of the release from Git: %v, want it from %v", got, onMain)
	}
	got := part(onUpload, "revision")
	if _, said := got["git_origin"]; said || got["revision_id"] != uploaded || got["revision_sequence"] != float64(2) ||
		got["source_kind"] != "UPLOAD" || got["entrypoint"] != "openapi.yaml" {
		t.Fatalf("the revision of the release from an upload: %v, want no origin", got)
	}

	// The branch moves on, without changing the files: the revision is the
	// one that was recorded first, at the commit it was recorded at.
	later := s.call(http.MethodPost, s.revisions, "author",
		importFromGit(entrypoint, revisionSource, gitOrigin(contractsRepository, commitTagged, "refs/tags/v1.0.0")))
	if later.status != http.StatusOK || later.id("revision_id") != fromGit {
		t.Fatalf("the same files at a later commit: %d %v, want the revision recorded first", later.status, later.body)
	}
	// And then it moves on with other files, which is another revision, under
	// a commit that names no reference.
	unnamed := gitOrigin(contractsRepository, commitRevised, "")
	movedOn, movedCheck := s.checkedFrom(importFromGit(entrypoint, revisionSource+"# moved on\n", unnamed))
	next := s.prepare(movedOn, movedCheck, "2.0.0", "publisher")
	s.require(next, http.StatusCreated, "")

	if got := part(s.provenanceOf(next.id("release_id")), "revision"); got["revision_id"] != movedOn || got["revision_sequence"] != float64(3) ||
		got["source_kind"] != "GIT" || !reflect.DeepEqual(got["git_origin"], unnamed) {
		t.Errorf("the revision of the release after the branch moved on: %v, want it from %v", got, unnamed)
	}
	if again := s.provenanceOf(git.id("release_id")); !reflect.DeepEqual(again.body, onGit.body) {
		t.Errorf("the provenance of the release from Git is\n %v\nonce the branch moved on, was\n %v", again.body, onGit.body)
	}
	if again := s.provenanceOf(upload.id("release_id")); !reflect.DeepEqual(again.body, onUpload.body) {
		t.Errorf("the provenance of the release from an upload is\n %v\nonce the branch moved on, was\n %v", again.body, onUpload.body)
	}
}

// What a release was prepared on says nothing of whether it would be
// prepared today. Once the tools have moved on from its check the provenance
// is what it was, and says nothing of now; the slot goes on serving the
// release.
func TestTheProvenanceDoesNotSayWhetherTheCheckWouldPassToday(t *testing.T) {
	s := publicationFor(t)
	target, _ := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	revision, check, id := s.released(revisionSource, "", "1.0.0")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, id, "", "first"), http.StatusOK, "")
	before := s.provenanceOf(id)

	// The same revision as a later version of the tool checked it.
	recordAnotherCheck(t, checkFixture{stack: s.stack}, checkRef(s, revision), check)
	s.require(s.call(http.MethodGet, slot+"/artifact", "author", ""), http.StatusOK, "")
	if after := s.provenanceOf(id); !reflect.DeepEqual(after.body, before.body) {
		t.Errorf("the provenance is\n %v\nonce the revision was checked by another tool, was\n %v", after.body, before.body)
	}
	for _, member := range []string{"approval", "approval_problem", "current", "holds"} {
		for name, of := range map[string]map[string]any{"the provenance": before.body, "its release": part(before, "release"), "its revision": part(before, "revision")} {
			if _, said := of[member]; said {
				t.Errorf("%s says %s, which is a matter of now or of reviews", name, member)
			}
		}
	}
}

// The waivers a release was prepared with are part of the release, rule by
// rule and with the reason whoever prepared it gave. The check it rests on
// says what it concluded of the rules, and a check that did not pass is not
// shown as one that did.
func TestTheProvenanceShowsTheWaiversOfItsReleaseAndTheCheckAsItConcluded(t *testing.T) {
	s := publicationFor(t)
	revision, check := s.checkedRevision(unnamedSource, "")
	released := s.prepare(revision, check, "1.0.0", "publisher", noted)
	s.require(released, http.StatusCreated, "")

	provenance := s.provenanceOf(released.id("release_id"))
	if want := []any{map[string]any{"rule_id": "F04-GOV-003", "reason": "generated clients are not used yet"}}; !reflect.DeepEqual(part(provenance, "release")["waivers"], want) {
		t.Errorf("the release in the provenance waives %v, want %v", part(provenance, "release")["waivers"], want)
	}
	if _, said := provenance.body["approval"]; said {
		t.Errorf("the provenance of a release prepared on a check names an approval: %v", provenance.body)
	}
	shown := part(provenance, "check")
	summary, _ := shown["summary"].(map[string]any)
	if shown["status"] != "FAIL" || summary["fail"] != float64(1) || summary["finding_count"] != float64(1) {
		t.Errorf("the check is %v, want it as it concluded: a rule that failed", shown)
	}
}

// unnamedTwice is a contract of two operations that have no operationId: the
// one rule that fails has a finding for each of them. Nothing else fails,
// and the rule may be waived.
const unnamedTwice = `openapi: 3.1.2
info: {title: Shipments, version: v1}
security: [{token: []}]
paths:
  /shipments:
    get:
      responses:
        '200': {description: OK}
  /parcels:
    get:
      responses:
        '200': {description: OK}
components:
  securitySchemes:
    token: {type: http, scheme: bearer}
`

// A check is counted by the rules and by the findings, which are not the
// same: a rule that failed is one, and it may have several findings. What
// the provenance says of the check it rests on is the count of each, as
// reading the check says them.
func TestTheProvenanceCountsTheFindingsOfTheCheckApartFromTheRulesThatFailed(t *testing.T) {
	s := publicationFor(t)
	revision, checkID := s.checkedRevision(unnamedTwice, "")
	released := s.prepare(revision, checkID, "1.0.0", "publisher", noted)
	s.require(released, http.StatusCreated, "")

	check := part(s.provenanceOf(released.id("release_id")), "check")
	summary, _ := check["summary"].(map[string]any)
	if check["status"] != "FAIL" || summary["fail"] != float64(1) || summary["finding_count"] != float64(2) {
		t.Errorf("the check is %v, want one rule that failed, with two findings", check)
	}
	read := s.call(http.MethodGet, s.revisions+"/"+revision+"/check-runs/"+check["check_run_id"].(string), "author", "")
	s.require(read, http.StatusOK, "")
	if want := read.body["check_run"].(map[string]any)["summary"]; !reflect.DeepEqual(summary, want) {
		t.Errorf("the summary of the check in the provenance is\n %v\nwant what reading the check says\n %v", summary, want)
	}
}

// A release prepared under an approval, before releases were prepared on a
// check alone, keeps it: the provenance names the review, who approved and
// when, for every target it was given for, in the order of the review, which
// is by audience and then by environment, until when it held and the rules it
// let stand. The release itself waives nothing: its waivers were the
// approval's.
func TestTheProvenanceOfAReleasePreparedUnderAnApprovalShowsThatApproval(t *testing.T) {
	s := publicationFor(t)
	publicProduction := [2]string{"public", "production"}
	revision, check := s.checkedRevision(unnamedSource, "")
	current := s.prepare(revision, check, "1.0.0", "publisher", noted)
	s.require(current, http.StatusCreated, "")
	review, id := s.approvedBefore(revision, check, current.id("release_id"), "0.9.0",
		[][2]string{partnerSandbox, publicSandbox, internalProduction, publicProduction, internalSandbox, partnerProduction}, "F04-GOV-003")

	want := []any{}
	for _, target := range [][2]string{internalProduction, internalSandbox, partnerProduction, partnerSandbox, publicProduction, publicSandbox} {
		want = append(want, map[string]any{"audience": target[0], "environment": target[1]})
	}
	read := s.call(http.MethodGet, review, "author", "")
	s.require(read, http.StatusOK, "")
	provenance := s.provenanceOf(id)
	approval := part(provenance, "approval")
	approver, _ := approval["concluded_by"].(map[string]any)
	if !reflect.DeepEqual(read.body["scope"], want) || !reflect.DeepEqual(approval["scope"], want) || approval["review_id"] != last(review) ||
		approval["supplementary"] != false || approver["subject"] != "reviewer" || approval["valid_until"] == nil {
		t.Errorf("the review is for\n %v\nand the approval in the provenance is\n %v\nwant both for\n %v", read.body["scope"], approval, want)
	}
	waivers, _ := approval["waivers"].([]any)
	if len(waivers) != 1 || waivers[0].(map[string]any)["rule_id"] != "F04-GOV-003" || len(waivers[0].(map[string]any)) != 2 {
		t.Errorf("the approval waives %v, want F04-GOV-003 until a time and no more of it", approval["waivers"])
	}
	if release := part(provenance, "release"); release["review_id"] != last(review) || !reflect.DeepEqual(release["waivers"], []any{}) ||
		part(provenance, "check")["check_run_id"] != check {
		t.Errorf("the release prepared under the approval is %v, want it to name the review and its check and to waive nothing itself", release)
	}
}

// What a release says of where it came from is what the reads of the
// revision, of the check and of the review say, member for member: the
// provenance is those reads at once, as they were when the release was
// prepared, and not an account of its own. The release of a Git source that
// an approval waived a rule for, prepared before releases were prepared on a
// check alone, is the example.
func TestTheProvenanceSaysWhatTheReadsOfItsEvidenceSay(t *testing.T) {
	s := publicationFor(t)
	origin := gitOrigin(contractsRepository, commitOnMain, "refs/heads/main")
	revisionID, check := s.checkedFrom(importFromGit("api/openapi.yaml", unnamedSource, origin))
	current := s.prepare(revisionID, check, "1.0.0", "publisher", noted)
	s.require(current, http.StatusCreated, "")
	reviewPath, released := s.approvedBefore(revisionID, check, current.id("release_id"), "0.9.0", [][2]string{internalSandbox}, "F04-GOV-003")
	provenance := s.provenanceOf(released)
	pretty, err := json.MarshalIndent(provenance.body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("the provenance of a release of a revision from Git whose approval waived a rule:\n%s", pretty)

	read := func(path string) map[string]any {
		t.Helper()
		got := s.call(http.MethodGet, path, "author", "")
		s.require(got, http.StatusOK, "")
		return got.body
	}
	only := func(from map[string]any, members ...string) map[string]any {
		kept := map[string]any{}
		for _, member := range members {
			if value, said := from[member]; said {
				kept[member] = value
			}
		}
		return kept
	}
	for _, id := range []string{released, current.id("release_id")} {
		if want := read(s.releaseAt(id)); !reflect.DeepEqual(part(s.provenanceOf(id), "release"), want) {
			t.Errorf("the release in the provenance is\n %v\nwant what reading it says\n %v", part(s.provenanceOf(id), "release"), want)
		}
	}
	revision := read(s.revisions + "/" + revisionID)
	if want := only(revision, "revision_id", "revision_sequence", "source_kind", "git_origin", "entrypoint", "source_digest", "ref_closure_digest"); !reflect.DeepEqual(part(provenance, "revision"), want) {
		t.Errorf("the revision in the provenance is\n %v\nwant what reading it says\n %v", part(provenance, "revision"), want)
	}
	checkRun := read(s.revisions + "/" + revisionID + "/check-runs/" + part(provenance, "check")["check_run_id"].(string))["check_run"].(map[string]any)
	if want := only(checkRun, "check_run_id", "policy_set_id", "policy_digest", "tool_manifest", "status", "summary", "completed_at"); !reflect.DeepEqual(part(provenance, "check"), want) {
		t.Errorf("the check in the provenance is\n %v\nwant what reading it says\n %v", part(provenance, "check"), want)
	}
	review := read(reviewPath)
	conclusion, _ := review["conclusion"].(map[string]any)
	want := only(review, "review_id", "supplementary", "scope")
	want["concluded_by"], want["concluded_at"], want["valid_until"] = conclusion["concluded_by"], conclusion["concluded_at"], conclusion["valid_until"]
	want["waivers"] = []any{}
	for _, waiver := range conclusion["waivers"].([]any) {
		want["waivers"] = append(want["waivers"].([]any), only(waiver.(map[string]any), "rule_id", "expires_at"))
	}
	if !reflect.DeepEqual(part(provenance, "approval"), want) {
		t.Errorf("the approval in the provenance is\n %v\nwant what reading the review says\n %v", part(provenance, "approval"), want)
	}
	if part(provenance, "revision")["source_kind"] != "GIT" || len(part(provenance, "approval")["waivers"].([]any)) != 1 {
		t.Errorf("the example is of %v, want a Git source and a waiver", provenance.body)
	}
}

// checkRef names the revision of the fixture in the words of Policy & Review.
func checkRef(s *releaseFixture, revision string) checkdomain.RevisionRef {
	return checkdomain.RevisionRef{ProductID: s.product, WorkspaceID: s.workspace, AssetID: last(s.asset), RevisionID: revision}
}

// A release whose revision or evidence is not what it recorded is a failure
// of the service. The answer says that and nothing of what is wrong, the
// log says what, under the identifier of the request, and what does not
// rest on what was damaged is read as before: the artifact does not rest on
// the evidence, nor the provenance on the bytes. Once the record is as it was
// recorded the answers are what they were. That holds of a release prepared
// on a check and of one prepared under an approval, whose review is evidence
// besides.
func TestEvidenceThatIsNotWhatWasRecordedIsLoggedAndNotSent(t *testing.T) {
	s := publicationFor(t)
	revision, checkID, onCheck := s.released(revisionSource, "", "1.0.0")
	_, underApproval := s.approvedBefore(revision, checkID, onCheck, "0.9.0", [][2]string{internalSandbox})
	zero := "sha256:" + strings.Repeat("0", 64)
	var constraint, definition string
	if err := s.owner.QueryRow(t.Context(), `SELECT conname, pg_get_constraintdef(oid) FROM pg_constraint
WHERE conrelid = 'contractregistry.release_artifact'::regclass AND contype = 'c' AND pg_get_constraintdef(oid) LIKE '%sha256%'`).Scan(&constraint, &definition); err != nil {
		t.Fatal(err)
	}
	update := func(table, set, where string) string {
		return "UPDATE " + table + " SET " + set + " WHERE " + where
	}
	run := func(statements []string) {
		t.Helper()
		for _, statement := range statements {
			if _, err := s.owner.Exec(t.Context(), statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
	}
	const (
		damagedCheck  = "the stored check run is not the one that was recorded"
		damagedReview = "the stored review is not the one that was recorded"
		damagedBytes  = "the release artifact does not match its digest"
	)
	type damage struct {
		name         string
		break_, mend []string
		provenance   int
		download     int
		cause        string
	}

	for kind, id := range map[string]string{"a release on a check": onCheck, "a release under an approval": underApproval} {
		t.Run(kind, func(t *testing.T) {
			path := s.releaseAt(id)
			intact := s.provenanceOf(id)
			bytesOf := s.download(path+"/artifact", "author")
			if bytesOf.status != http.StatusOK {
				t.Fatalf("the artifact: %d %s", bytesOf.status, bytesOf.body)
			}
			release, check, closure := part(intact, "release"), part(intact, "check"), part(intact, "release")["ref_closure_digest"].(string)
			byRelease := "release_id = '" + id + "'"
			byCheck := "check_run_id = '" + check["check_run_id"].(string) + "'"
			byDigest := "artifact_digest = '" + release["artifact_digest"].(string) + "'"
			review, approved := release["review_id"].(string)
			// The review of an approval names the check it rests on, so a
			// release that names another check than its review is no
			// approval the release was prepared under.
			otherCheck := damagedCheck
			if approved {
				otherCheck = damagedReview
			}

			cases := []damage{
				{"the closure a check read was rewritten",
					[]string{update("policyreview.check_run", "input_digest = '"+zero+"'", byCheck)},
					[]string{update("policyreview.check_run", "input_digest = '"+closure+"'", byCheck)},
					http.StatusInternalServerError, http.StatusOK, damagedCheck},
				{"the release names another check",
					[]string{update("contractregistry.contract_release", "check_run_id = '"+ids.NewUUID()+"'", byRelease)},
					[]string{update("contractregistry.contract_release", "check_run_id = '"+check["check_run_id"].(string)+"'", byRelease)},
					http.StatusInternalServerError, http.StatusOK, otherCheck},
				{"the release names another policy",
					[]string{update("contractregistry.contract_release", "policy_digest = '"+zero+"'", byRelease)},
					[]string{update("contractregistry.contract_release", "policy_digest = '"+release["policy_digest"].(string)+"'", byRelease)},
					http.StatusInternalServerError, http.StatusOK, damagedCheck},
				{"the release names another snapshot of the policy",
					[]string{update("contractregistry.contract_release", "policy_snapshot_id = '"+ids.NewUUID()+"'", byRelease)},
					[]string{update("contractregistry.contract_release", "policy_snapshot_id = '"+release["policy_snapshot_id"].(string)+"'", byRelease)},
					http.StatusInternalServerError, http.StatusOK, damagedCheck},
				{"the release was prepared from another closure than its revision has",
					[]string{update("contractregistry.contract_release", "input_digest = '"+zero+"'", byRelease)},
					[]string{update("contractregistry.contract_release", "input_digest = '"+closure+"'", byRelease)},
					http.StatusInternalServerError, http.StatusInternalServerError, damagedBytes},
				{"a byte was added to the artifact",
					[]string{"ALTER TABLE contractregistry.release_artifact DROP CONSTRAINT " + constraint,
						update("contractregistry.release_artifact", `payload = payload || '\x20'::bytea`, byDigest)},
					[]string{update("contractregistry.release_artifact", "payload = substring(payload FROM 1 FOR octet_length(payload) - 1)", byDigest),
						"ALTER TABLE contractregistry.release_artifact ADD CONSTRAINT " + constraint + " " + definition},
					http.StatusOK, http.StatusInternalServerError, damagedBytes},
			}
			if approved {
				byReview := "review_id = '" + review + "'"
				cases = append(cases,
					damage{"the closure a review was asked on was rewritten",
						[]string{update("policyreview.review_request", "input_digest = '"+zero+"'", byReview)},
						[]string{update("policyreview.review_request", "input_digest = '"+closure+"'", byReview)},
						http.StatusInternalServerError, http.StatusOK, damagedReview},
					damage{"the release names another review",
						[]string{update("contractregistry.contract_release", "review_id = '"+ids.NewUUID()+"'", byRelease)},
						[]string{update("contractregistry.contract_release", "review_id = '"+review+"'", byRelease)},
						http.StatusInternalServerError, http.StatusOK, damagedReview})
			}
			for _, d := range cases {
				t.Run(d.name, func(t *testing.T) {
					run(d.break_)
					for what, c := range map[string]struct {
						path   string
						status int
					}{"the provenance": {path + "/provenance", d.provenance}, "the artifact": {path + "/artifact", d.download}} {
						logged := len(s.log.String())
						got := s.download(c.path, "author")
						if got.status != c.status {
							t.Errorf("%s: %d %s, want %d", what, got.status, got.body, c.status)
						}
						if c.status != http.StatusInternalServerError {
							continue
						}
						var problem map[string]any
						if err := json.Unmarshal(got.body, &problem); err != nil || len(problem) != 5 || problem["error_code"] != "INTERNAL_ERROR" {
							t.Errorf("%s: %s, want 500 INTERNAL_ERROR and nothing of what is wrong", what, got.body)
						}
						trace, _ := problem["trace_id"].(string)
						if entry := s.log.String()[logged:]; trace == "" || !strings.Contains(entry, d.cause) || !strings.Contains(entry, "correlation_id="+trace) ||
							strings.Contains(string(got.body), "recorded") || strings.Contains(string(got.body), "digest") {
							t.Errorf("%s: the log does not say %q under the identifier %s, or the answer does: %s\n%s", what, d.cause, trace, got.body, entry)
						}
					}
					run(d.mend)
					if got := s.provenanceOf(id); !reflect.DeepEqual(got.body, intact.body) {
						t.Errorf("the provenance with the record as it was recorded: %v, want it as it was", got.body)
					}
					if got := s.download(path+"/artifact", "author"); got.status != http.StatusOK || string(got.body) != string(bytesOf.body) {
						t.Errorf("the artifact with the record as it was recorded: %d %q, want it as it was", got.status, got.body)
					}
				})
			}
		})
	}
}
