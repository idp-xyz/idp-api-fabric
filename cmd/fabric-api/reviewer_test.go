package main

import (
	"net/http"
	"reflect"
	"testing"
)

// Nobody is made a reviewer any more (CR-0001): no review is decided. Asking
// for the role is refused as a membership that grants no role of a
// workspace is, by an owner as by anyone, and nothing is recorded of it. The
// other roles are granted as before.
func TestNobodyIsMadeAReviewerAnyMore(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "owner")
	workspace := s.createWorkspace(product, "owner", "engineering")
	memberships := "/v1/products/" + product + "/workspaces/" + workspace + "/memberships"
	records, events, held := s.count("platformoperations.audit_record"), s.count("bento.outbox"), s.count("productgovernance.workspace_membership")

	got := s.call(http.MethodPost, memberships, "owner", s.membership("reviewer", "internal_reviewer"))
	if got.status != http.StatusUnprocessableEntity || got.code() != "REQUEST_UNPROCESSABLE" {
		t.Fatalf("an owner makes somebody a reviewer: %d %v, want 422 REQUEST_UNPROCESSABLE", got.status, got.body)
	}
	if s.count("productgovernance.workspace_membership") != held || s.count("platformoperations.audit_record") != records || s.count("bento.outbox") != events {
		t.Errorf("the refusal left %d memberships, %d audit records and %d events behind",
			s.count("productgovernance.workspace_membership")-held, s.count("platformoperations.audit_record")-records, s.count("bento.outbox")-events)
	}
	if items, _ := s.page(memberships, "owner"); len(items) != 0 {
		t.Errorf("the workspace lists the members %v, want none", items)
	}
	for _, role := range []string{"workspace_maintainer", "internal_author", "internal_publisher", "internal_reader"} {
		if got := s.call(http.MethodPost, memberships, "owner", s.membership("member", role)); got.status != http.StatusCreated {
			t.Errorf("an owner grants %s: %d %v", role, got.status, got.body)
		}
	}
}

// A reviewer made before reviewers stopped being made reads what it read:
// the revisions of the assets of the workspace, their files, their checks,
// how two of them compare and the reviews of before, answer for answer as an
// author reads them. It writes no revision and runs no check, and the grant
// counts in the workspace it was made in and in no other.
func TestAReviewerOfBeforeReadsTheRevisionsChecksAndReviewsOfItsWorkspaceAndWritesNone(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "owner")
	workspace := s.createWorkspace(product, "owner", "engineering")
	s.join(product, workspace, "owner", "maintainer", "workspace_maintainer")
	s.join(product, workspace, "owner", "author", "internal_author")
	membership := s.join(product, workspace, "owner", "reviewer", "internal_reviewer")
	other := s.createWorkspace(product, "owner", "operations")
	s.join(product, other, "owner", "elsewhere", "internal_reviewer")
	// Someone who sees the assets of engineering and reviews in operations.
	s.join(product, workspace, "owner", "between", "internal_reader")
	s.join(product, other, "owner", "between", "internal_reviewer")
	path := s.revisionAsset(product, workspace, "maintainer", "shipping-api", "HTTP")

	first := s.call(http.MethodPost, path, "author", uploadRevision("openapi.yaml", revisionSource))
	second := s.call(http.MethodPost, path, "author", uploadRevision("openapi.yaml", revisionSource+"# next\n"))
	if first.status != http.StatusCreated || second.status != http.StatusCreated {
		t.Fatalf("the imports: %d %v, %d %v", first.status, first.body, second.status, second.body)
	}
	revision := path + "/" + first.id("revision_id")
	checked := s.call(http.MethodPost, revision+"/check-runs", "author", "")
	if checked.status != http.StatusCreated {
		t.Fatalf("the check: %d %v", checked.status, checked.body)
	}
	history := &reviewFixture{stack: s, product: product, workspace: workspace, revisions: path}
	review := history.asked(revision, checked.body["check_run"].(map[string]any)["check_run_id"].(string), "author", partnerSandbox)
	history.concluded(review, "reviewer", "APPROVED", "")
	reads := []string{
		path, revision, revision + "/source", revision + "/check-runs", checked.header.Get("Location"),
		path + "/" + second.id("revision_id") + "/compatibility?baseline_revision_id=" + first.id("revision_id"),
		revision + "/reviews", review,
	}

	records, events := s.count("platformoperations.audit_record"), s.count("bento.outbox")
	for _, read := range reads {
		ofAuthor, ofReviewer := s.call(http.MethodGet, read, "author", ""), s.call(http.MethodGet, read, "reviewer", "")
		if ofAuthor.status != http.StatusOK || ofReviewer.status != http.StatusOK || !reflect.DeepEqual(ofReviewer.body, ofAuthor.body) {
			t.Errorf("GET %s: the reviewer reads %d %v, the author %d %v; want the same answer", read, ofReviewer.status, ofReviewer.body, ofAuthor.status, ofAuthor.body)
		}
	}
	if after, sent := s.count("platformoperations.audit_record"), s.count("bento.outbox"); after != records || sent != events {
		t.Errorf("the reads left %d audit records and %d events", after-records, sent-events)
	}

	// It writes no revision and runs no check. Each refusal is on record as
	// a refusal of what was asked.
	s.expect("a reviewer writes no revision and runs no check", "reviewer", []exchange{
		{http.MethodPost, path, uploadRevision("openapi.yaml", revisionSource+"# by the reviewer\n"), http.StatusForbidden, "FORBIDDEN"},
		{http.MethodPost, revision + "/check-runs", "", http.StatusForbidden, "FORBIDDEN"},
		{http.MethodPost, revision + "/check-runs?baseline_revision_id=" + second.id("revision_id"), "", http.StatusForbidden, "FORBIDDEN"},
	})
	refusals := s.recorded(func(row audited) bool { return row.Subject == "reviewer" })
	creation := occurrences(s.recorded(func(row audited) bool {
		return row.Subject == "reviewer" && row.Action == "revision.create" && row.Outcome != "SUCCEEDED" && row.Rule == "DENY-03"
	}))
	checking := occurrences(s.recorded(func(row audited) bool {
		return row.Subject == "reviewer" && row.Action == "revision.check" && row.Outcome != "SUCCEEDED" && row.Rule == "DENY-03"
	}))
	if creation != 1 || checking != 2 || occurrences(refusals) != 3 ||
		s.count("contractregistry.contract_revision") != 2 || s.count("policyreview.check_run") != 1 {
		t.Errorf("the refusals are on record as %v, want one of revision.create and two of revision.check; %d revisions, %d checks",
			refusals, s.count("contractregistry.contract_revision"), s.count("policyreview.check_run"))
	}

	// The grant counts where it was made. A reviewer of another workspace
	// does not see the asset; one who sees it for another reason and
	// holds a grant elsewhere cannot read its revisions. Owners of the
	// current product can read them without a membership.
	for _, read := range reads {
		for caller, want := range map[string]int{
			"elsewhere": http.StatusNotFound, "between": http.StatusForbidden,
			"maintainer": http.StatusOK, "owner": http.StatusOK,
		} {
			if got := s.call(http.MethodGet, read, caller, ""); got.status != want {
				t.Errorf("GET %s as %s: %d %v, want %d", read, caller, got.status, got.body, want)
			}
		}
	}

	// The grant is read with every request. It was all the reviewer held
	// in the product, so without it the product is one it does not know.
	s.revoke(membership, "owner")
	for _, read := range reads {
		if got := s.call(http.MethodGet, read, "reviewer", ""); got.status != http.StatusNotFound || got.code() != "PRODUCT_NOT_FOUND" {
			t.Errorf("GET %s once the grant is revoked: %d %v, want 404 PRODUCT_NOT_FOUND", read, got.status, got.body)
		}
	}
}
