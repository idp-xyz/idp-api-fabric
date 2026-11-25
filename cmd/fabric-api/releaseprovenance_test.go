package main

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
)

// approveFrom imports a revision with the body as the author, has it
// checked, asks for the review of the scopes and has the reviewer decide as
// the decision says. It is approve for a source that is not an upload with
// the entrypoint openapi.yaml, and for an approval that is not the plain
// one.
func (s *releaseFixture) approveFrom(imported, decision string, scopes ...[2]string) (revisionID, reviewID string) {
	s.t.Helper()
	revision := s.call(http.MethodPost, s.revisions, "author", imported)
	s.require(revision, http.StatusCreated, "")
	revisionID = revision.id("revision_id")
	checked := s.call(http.MethodPost, s.revisions+"/"+revisionID+"/check-runs", "author", "")
	s.require(checked, http.StatusCreated, "")
	checkID := checked.body["check_run"].(map[string]any)["check_run_id"].(string)
	review := s.call(http.MethodPost, s.revisions+"/"+revisionID+"/reviews", "author", asking(checkID, scopes...))
	s.require(review, http.StatusCreated, "")
	reviewID = review.id("review_id")
	s.require(s.call(http.MethodPost, review.header.Get("Location")+"/decision", "reviewer", decision), http.StatusOK, "")
	return revisionID, reviewID
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

// A release says what it was prepared on, and that stays what it says when
// the revision is reviewed again. The revision is approved anew, on another
// check and by another reviewer, and a release prepared under that approval
// names it: the release that was prepared before still names the review and
// the check it was prepared on, and does not become the latest of either.
func TestTheProvenanceOfAReleaseStaysWhenTheRevisionIsReviewedAgain(t *testing.T) {
	s := publicationFor(t)
	s.join(s.product, s.workspace, "owner", "second", "internal_reviewer")
	id, first := s.approve(revisionSource, "", internalSandbox)
	revision := s.revisions + "/" + id
	released := s.prepare(id, first, "1.0.0", "publisher")
	s.require(released, http.StatusCreated, "")
	old := released.id("release_id")
	before := s.provenanceOf(old)
	firstCheck, _ := part(before, "check")["check_run_id"].(string)

	approval := part(before, "approval")
	approver, _ := approval["concluded_by"].(map[string]any)
	if approval["review_id"] != first || approval["supplementary"] != false || approver["subject"] != "reviewer" || approver["issuer"] != s.issuer.URL() ||
		!reflect.DeepEqual(approval["scope"], []any{map[string]any{"audience": "internal", "environment": "sandbox"}}) ||
		!reflect.DeepEqual(approval["waivers"], []any{}) || len(part(before, "check")["tool_manifest"].([]any)) != 1 {
		t.Fatalf("the provenance of the first release: %v", before.body)
	}

	// The revision is checked against another revision of the asset, and the
	// second review is asked on that check, and decided by another reviewer.
	baseline := s.call(http.MethodPost, s.revisions, "author", uploadRevision("openapi.yaml", revisionSource+"# to compare with\n"))
	s.require(baseline, http.StatusCreated, "")
	check := s.call(http.MethodPost, revision+"/check-runs?baseline_revision_id="+baseline.id("revision_id"), "author", "")
	s.require(check, http.StatusCreated, "")
	secondCheck := check.body["check_run"].(map[string]any)["check_run_id"].(string)
	asked := s.call(http.MethodPost, revision+"/reviews", "author", asking(secondCheck, internalSandbox))
	s.require(asked, http.StatusCreated, "")
	s.require(s.call(http.MethodPost, asked.header.Get("Location")+"/decision", "second", deciding("APPROVED", "", time.Now().Add(2*time.Hour))), http.StatusOK, "")
	renewed := s.prepare(id, asked.id("review_id"), "1.0.0-r2", "publisher")
	s.require(renewed, http.StatusCreated, "")

	// The second review and the second check are the latest of the revision.
	reviews, _ := s.page(revision+"/reviews", "author")
	checks, _ := s.page(revision+"/check-runs", "author")
	if len(reviews) != 2 || reviews[1]["review_id"] != asked.id("review_id") || len(checks) != 2 || checks[1]["check_run_id"] != secondCheck {
		t.Fatalf("the reviews %v and the checks %v of the revision, want the second of each last", reviews, checks)
	}

	after := s.provenanceOf(old)
	if !reflect.DeepEqual(after.body, before.body) {
		t.Errorf("the provenance of the first release is\n %v\nafter the revision was reviewed again, was\n %v", after.body, before.body)
	}
	now := s.provenanceOf(renewed.id("release_id"))
	renewedApproval, renewedCheck := part(now, "approval"), part(now, "check")
	renewedBy, _ := renewedApproval["concluded_by"].(map[string]any)
	concluded := func(of map[string]any) time.Time {
		at, err := time.Parse(time.RFC3339Nano, of["concluded_at"].(string))
		if err != nil {
			t.Fatal(err)
		}
		return at
	}
	if renewedApproval["review_id"] != asked.id("review_id") || renewedApproval["supplementary"] != true || renewedBy["subject"] != "second" ||
		!concluded(renewedApproval).After(concluded(approval)) || renewedCheck["check_run_id"] != secondCheck || firstCheck == secondCheck ||
		len(renewedCheck["tool_manifest"].([]any)) != 2 || !reflect.DeepEqual(part(now, "revision"), part(before, "revision")) ||
		part(now, "release")["release_id"] != renewed.id("release_id") || part(now, "release")["version"] != "1.0.0-r2" {
		t.Errorf("the provenance of the release prepared under the second approval is\n %v\nwant the second review, its reviewer and its check, and the revision as it was", now.body)
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
	plain := func() string { return deciding("APPROVED", "", time.Now().Add(time.Hour)) }
	onMain := gitOrigin(contractsRepository, commitOnMain, "refs/heads/main")

	fromGit, gitReview := s.approveFrom(importFromGit(entrypoint, revisionSource, onMain), plain(), internalSandbox)
	git := s.prepare(fromGit, gitReview, "1.0.0", "publisher")
	s.require(git, http.StatusCreated, "")
	uploaded, uploadReview := s.approve(revisionSource+"# uploaded\n", "", internalSandbox)
	upload := s.prepare(uploaded, uploadReview, "1.0.1", "publisher")
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
	movedOn, movedReview := s.approveFrom(importFromGit(entrypoint, revisionSource+"# moved on\n", unnamed), plain(), internalSandbox)
	next := s.prepare(movedOn, movedReview, "2.0.0", "publisher")
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

// What a release was prepared on says nothing of whether it still counts.
// When the reviewer of the approval loses the grant the slot says so, in
// approval_problem, and serves what it served; the provenance is what it
// was, and a new release under that approval is refused.
func TestTheProvenanceDoesNotSayWhetherTheApprovalHoldsNow(t *testing.T) {
	s := publicationFor(t)
	target, _ := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	revision, review, id := s.released(revisionSource, "", "1.0.0")
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, id, "", "first"), http.StatusOK, "")
	before := s.provenanceOf(id)
	if got := s.call(http.MethodGet, slot, "publisher", ""); got.body["approval_problem"] != nil {
		t.Fatalf("the slot says %v of an approval that holds", got.body["approval_problem"])
	}

	var membership string
	if err := s.owner.QueryRow(t.Context(), `SELECT membership_id FROM productgovernance.workspace_membership
WHERE product_id = $1 AND workspace_id = $2 AND principal_subject = 'reviewer'
  AND role = 'internal_reviewer' AND revoked_at IS NULL`, s.product, s.workspace).Scan(&membership); err != nil {
		t.Fatal(err)
	}
	s.revoke("/v1/products/"+s.product+"/workspaces/"+s.workspace+"/memberships/"+membership, "owner")
	if got := s.call(http.MethodGet, slot, "publisher", ""); got.body["approval_problem"] != "REVIEWER_AUTH_REVOKED" {
		t.Fatalf("the slot says %v, want REVIEWER_AUTH_REVOKED", got.body["approval_problem"])
	}
	s.require(s.call(http.MethodGet, slot+"/artifact", "author", ""), http.StatusOK, "")
	s.require(s.prepare(revision, review, "1.0.1", "publisher"), http.StatusConflict, "REVIEWER_AUTH_REVOKED")

	if after := s.provenanceOf(id); !reflect.DeepEqual(after.body, before.body) {
		t.Errorf("the provenance is\n %v\nonce the reviewer lost the grant, was\n %v", after.body, before.body)
	}
	for _, member := range []string{"approval_problem", "approval_holds", "holds", "status"} {
		for name, of := range map[string]map[string]any{"the provenance": before.body, "its approval": part(before, "approval"), "its revision": part(before, "revision")} {
			if _, said := of[member]; said {
				t.Errorf("%s says %s, which is a matter of now", name, member)
			}
		}
	}
}

// An approval that waived a rule says which rule and until when, and no more
// of it: why is for the review to say, to whoever may read the review. The
// check it rests on says what it concluded of the rules as the review reads
// it, and a check that did not pass is not shown as one that did.
func TestTheProvenanceListsTheWaiversOfItsApprovalByRuleAndExpiry(t *testing.T) {
	s := publicationFor(t)
	until := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	waived := waiving("F04-GOV-003", until.Add(-time.Hour))
	waived["compensating_control"] = "the gateway names every route"
	revision, review := s.approveFrom(uploadRevision("openapi.yaml", unnamedSource), deciding("APPROVED", "", until, waived), internalSandbox)
	released := s.prepare(revision, review, "1.0.0", "publisher")
	s.require(released, http.StatusCreated, "")

	got := s.download(s.releaseAt(released.id("release_id"))+"/provenance", "author")
	if got.status != http.StatusOK {
		t.Fatalf("the provenance: %d %s", got.status, got.body)
	}
	for _, words := range []string{"generated clients are not used yet", "the gateway names every route", "reason", "compensating"} {
		if strings.Contains(string(got.body), words) {
			t.Errorf("the provenance says %q, which a review says in words and a provenance does not: %s", words, got.body)
		}
	}
	var shown map[string]any
	if err := json.Unmarshal(got.body, &shown); err != nil {
		t.Fatal(err)
	}
	approval, _ := shown["approval"].(map[string]any)
	if want := []any{map[string]any{"rule_id": "F04-GOV-003", "expires_at": until.Add(-time.Hour).Format(time.RFC3339)}}; !reflect.DeepEqual(approval["waivers"], want) ||
		approval["valid_until"] != until.Format(time.RFC3339) {
		t.Errorf("the approval is %v, want the waiver of F04-GOV-003 until %s and the approval until %s", approval, until.Add(-time.Hour).Format(time.RFC3339), until.Format(time.RFC3339))
	}
	check, _ := shown["check"].(map[string]any)
	summary, _ := check["summary"].(map[string]any)
	if check["status"] != "FAIL" || summary["fail"] != float64(1) || summary["finding_count"] != float64(1) {
		t.Errorf("the check is %v, want it as it concluded: a rule that failed", check)
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
	until := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	revision, review := s.approveFrom(uploadRevision("openapi.yaml", unnamedTwice),
		deciding("APPROVED", "", until, waiving("F04-GOV-003", until.Add(-time.Hour))), internalSandbox)
	released := s.prepare(revision, review, "1.0.0", "publisher")
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

// An approval is for the targets it was asked for, every one of them, and
// the provenance of a release prepared under it says all of them in the
// order of the review, which is by audience and then by environment and not
// the order in which they were asked for.
func TestTheProvenanceNamesEveryTargetTheApprovalWasGivenFor(t *testing.T) {
	s := publicationFor(t)
	publicProduction := [2]string{"public", "production"}
	revision, review := s.approve(revisionSource, "", partnerSandbox, publicSandbox, internalProduction, publicProduction, internalSandbox, partnerProduction)
	released := s.prepare(revision, review, "1.0.0", "publisher")
	s.require(released, http.StatusCreated, "")

	want := []any{}
	for _, target := range [][2]string{internalProduction, internalSandbox, partnerProduction, partnerSandbox, publicProduction, publicSandbox} {
		want = append(want, map[string]any{"audience": target[0], "environment": target[1]})
	}
	read := s.call(http.MethodGet, s.revisions+"/"+revision+"/reviews/"+review, "author", "")
	s.require(read, http.StatusOK, "")
	if approval := part(s.provenanceOf(released.id("release_id")), "approval"); !reflect.DeepEqual(read.body["scope"], want) ||
		!reflect.DeepEqual(approval["scope"], want) {
		t.Errorf("the review is for\n %v\nand the approval in the provenance for\n %v\nwant\n %v", read.body["scope"], approval["scope"], want)
	}
}

// What a release says of where it came from is what the reads of the
// revision, of the check and of the review say, member for member: the
// provenance is those reads at once, as they were when the release was
// prepared, and not an account of its own. The release of a Git source
// that an approval waived a rule for is the example.
func TestTheProvenanceSaysWhatTheReadsOfItsEvidenceSay(t *testing.T) {
	s := publicationFor(t)
	until := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	origin := gitOrigin(contractsRepository, commitOnMain, "refs/heads/main")
	revisionID, reviewID := s.approveFrom(importFromGit("api/openapi.yaml", unnamedSource, origin),
		deciding("APPROVED", "", until, waiving("F04-GOV-003", until.Add(-time.Hour))), internalSandbox)
	released := s.prepare(revisionID, reviewID, "1.0.0", "publisher")
	s.require(released, http.StatusCreated, "")
	provenance := s.provenanceOf(released.id("release_id"))
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
	if want := read(s.releaseAt(released.id("release_id"))); !reflect.DeepEqual(part(provenance, "release"), want) {
		t.Errorf("the release in the provenance is\n %v\nwant what reading it says\n %v", part(provenance, "release"), want)
	}
	revision := read(s.revisions + "/" + revisionID)
	if want := only(revision, "revision_id", "revision_sequence", "source_kind", "git_origin", "entrypoint", "source_digest", "ref_closure_digest"); !reflect.DeepEqual(part(provenance, "revision"), want) {
		t.Errorf("the revision in the provenance is\n %v\nwant what reading it says\n %v", part(provenance, "revision"), want)
	}
	checkRun := read(s.revisions + "/" + revisionID + "/check-runs/" + part(provenance, "check")["check_run_id"].(string))["check_run"].(map[string]any)
	if want := only(checkRun, "check_run_id", "policy_set_id", "policy_digest", "tool_manifest", "status", "summary", "completed_at"); !reflect.DeepEqual(part(provenance, "check"), want) {
		t.Errorf("the check in the provenance is\n %v\nwant what reading it says\n %v", part(provenance, "check"), want)
	}
	review := read(s.revisions + "/" + revisionID + "/reviews/" + reviewID)
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

// A release whose revision or evidence is not what it recorded is a failure
// of the service. The answer says that and nothing of what is wrong, the
// log says what, under the identifier of the request, and what does not
// rest on what was damaged is read as before: the artifact does not rest on
// the evidence, nor the provenance on the bytes. Once the record is as it was
// recorded the answers are what they were.
func TestEvidenceThatIsNotWhatWasRecordedIsLoggedAndNotSent(t *testing.T) {
	s := publicationFor(t)
	_, _, id := s.released(revisionSource, "", "1.0.0")
	path := s.releaseAt(id)
	intact := s.provenanceOf(id)
	bytesOf := s.download(path+"/artifact", "author")
	if bytesOf.status != http.StatusOK {
		t.Fatalf("the artifact: %d %s", bytesOf.status, bytesOf.body)
	}
	release, check, closure := part(intact, "release"), part(intact, "check"), part(intact, "release")["ref_closure_digest"].(string)
	zero := "sha256:" + strings.Repeat("0", 64)
	var constraint, definition string
	if err := s.owner.QueryRow(t.Context(), `SELECT conname, pg_get_constraintdef(oid) FROM pg_constraint
WHERE conrelid = 'contractregistry.release_artifact'::regclass AND contype = 'c' AND pg_get_constraintdef(oid) LIKE '%sha256%'`).Scan(&constraint, &definition); err != nil {
		t.Fatal(err)
	}
	update := func(table, set, where string) string {
		return "UPDATE " + table + " SET " + set + " WHERE " + where
	}
	byRelease := "release_id = '" + id + "'"
	byCheck := "check_run_id = '" + check["check_run_id"].(string) + "'"
	byReview := "review_id = '" + release["review_id"].(string) + "'"
	byDigest := "artifact_digest = '" + release["artifact_digest"].(string) + "'"

	damage := []struct {
		name         string
		break_, mend []string
		provenance   int
		download     int
		cause        string
	}{
		{"the closure a check read was rewritten",
			[]string{update("policyreview.check_run", "input_digest = '"+zero+"'", byCheck)},
			[]string{update("policyreview.check_run", "input_digest = '"+closure+"'", byCheck)},
			http.StatusInternalServerError, http.StatusOK, "the stored check run is not the one that was recorded"},
		{"the closure a review was asked on was rewritten",
			[]string{update("policyreview.review_request", "input_digest = '"+zero+"'", byReview)},
			[]string{update("policyreview.review_request", "input_digest = '"+closure+"'", byReview)},
			http.StatusInternalServerError, http.StatusOK, "the stored review is not the one that was recorded"},
		{"the release names another review",
			[]string{update("contractregistry.contract_release", "review_id = '"+ids.NewUUID()+"'", byRelease)},
			[]string{update("contractregistry.contract_release", "review_id = '"+release["review_id"].(string)+"'", byRelease)},
			http.StatusInternalServerError, http.StatusOK, "the stored review is not the one that was recorded"},
		{"the release names another check",
			[]string{update("contractregistry.contract_release", "check_run_id = '"+ids.NewUUID()+"'", byRelease)},
			[]string{update("contractregistry.contract_release", "check_run_id = '"+check["check_run_id"].(string)+"'", byRelease)},
			http.StatusInternalServerError, http.StatusOK, "the stored review is not the one that was recorded"},
		{"the release names another policy",
			[]string{update("contractregistry.contract_release", "policy_digest = '"+zero+"'", byRelease)},
			[]string{update("contractregistry.contract_release", "policy_digest = '"+release["policy_digest"].(string)+"'", byRelease)},
			http.StatusInternalServerError, http.StatusOK, "the stored check run is not the one that was recorded"},
		{"the release names another snapshot of the policy",
			[]string{update("contractregistry.contract_release", "policy_snapshot_id = '"+ids.NewUUID()+"'", byRelease)},
			[]string{update("contractregistry.contract_release", "policy_snapshot_id = '"+release["policy_snapshot_id"].(string)+"'", byRelease)},
			http.StatusInternalServerError, http.StatusOK, "the stored check run is not the one that was recorded"},
		{"the release was prepared from another closure than its revision has",
			[]string{update("contractregistry.contract_release", "input_digest = '"+zero+"'", byRelease)},
			[]string{update("contractregistry.contract_release", "input_digest = '"+closure+"'", byRelease)},
			http.StatusInternalServerError, http.StatusInternalServerError, "the release artifact does not match its digest"},
		{"a byte was added to the artifact",
			[]string{"ALTER TABLE contractregistry.release_artifact DROP CONSTRAINT " + constraint,
				update("contractregistry.release_artifact", `payload = payload || '\x20'::bytea`, byDigest)},
			[]string{update("contractregistry.release_artifact", "payload = substring(payload FROM 1 FOR octet_length(payload) - 1)", byDigest),
				"ALTER TABLE contractregistry.release_artifact ADD CONSTRAINT " + constraint + " " + definition},
			http.StatusOK, http.StatusInternalServerError, "the release artifact does not match its digest"},
	}
	run := func(statements []string) {
		t.Helper()
		for _, statement := range statements {
			if _, err := s.owner.Exec(t.Context(), statement); err != nil {
				t.Fatalf("%s: %v", statement, err)
			}
		}
	}
	for _, d := range damage {
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
}
