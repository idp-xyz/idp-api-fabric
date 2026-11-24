package main

import (
	"net/http"
	"reflect"
	"testing"
)

// A reviewer of a workspace reads what it is to decide on: the revisions of
// the assets of the workspace, their files, their checks and how two of them
// compare, answer for answer as an author reads them. It writes no revision
// and runs no check, and the grant counts in the workspace it was made in
// and in no other.
func TestAReviewerReadsTheRevisionsAndChecksOfItsWorkspaceAndWritesNone(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "owner")
	workspace := s.createWorkspace(product, "owner", "engineering")
	s.join(product, workspace, "owner", "maintainer", "workspace_maintainer")
	s.join(product, workspace, "owner", "author", "internal_author")
	membership := s.join(product, workspace, "owner", "reviewer", "internal_reviewer")
	other := s.createWorkspace(product, "owner", "operations")
	s.join(product, other, "owner", "elsewhere", "internal_reviewer")
	// Someone who sees the assets of engineering and reviews in operations.
	s.join(product, workspace, "owner", "between", "workspace_maintainer")
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
	reads := []string{
		path, revision, revision + "/source", revision + "/check-runs", checked.header.Get("Location"),
		path + "/" + second.id("revision_id") + "/compatibility?baseline_revision_id=" + first.id("revision_id"),
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
			"maintainer": http.StatusForbidden, "owner": http.StatusOK,
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
