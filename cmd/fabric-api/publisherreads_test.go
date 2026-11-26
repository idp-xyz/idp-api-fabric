package main

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// A publisher of a workspace reads what it is to release: the revisions of
// the assets of the workspace, their files, their checks, how two of them
// compare and the reviews of before, answer for answer as an author reads
// them. That is where it finds the revision and the check a release rests
// on. It writes no revision and runs no check, and the grant counts in the
// workspace it was made in and in no other.
func TestAPublisherReadsWhatItIsToReleaseAndWritesNoneOfIt(t *testing.T) {
	members := reviewsFor(t)
	membership := members.join(members.product, members.workspace, "owner", "publisher", "internal_publisher")
	s := &releaseFixture{reviewFixture: members, asset: strings.TrimSuffix(members.revisions, "/revisions")}
	other := s.createWorkspace(s.product, "owner", "operations")
	s.join(s.product, other, "owner", "elsewhere", "internal_publisher")
	// Someone who sees the assets of engineering and publishes in operations.
	s.join(s.product, s.workspace, "owner", "between", "internal_reader")
	s.join(s.product, other, "owner", "between", "internal_publisher")

	first := s.inReview(revisionSource, "author", "author", "author")
	s.concluded(first.review, "reviewer", "APPROVED", "")
	second := s.call(http.MethodPost, s.revisions, "author", uploadRevision("openapi.yaml", revisionSource+"# next\n"))
	s.require(second, http.StatusCreated, "")
	draft := s.revisions + "/" + second.id("revision_id")
	reads := []string{
		s.revisions, first.revision, first.revision + "/source", first.revision + "/check-runs",
		first.revision + "/check-runs/" + first.check, first.revision + "/reviews", first.review,
		draft + "/compatibility?baseline_revision_id=" + last(first.revision),
	}

	records, events := s.count("platformoperations.audit_record"), s.count("bento.outbox")
	for _, read := range reads {
		ofAuthor, ofPublisher := s.call(http.MethodGet, read, "author", ""), s.call(http.MethodGet, read, "publisher", "")
		if ofAuthor.status != http.StatusOK || ofPublisher.status != http.StatusOK || !reflect.DeepEqual(ofPublisher.body, ofAuthor.body) {
			t.Errorf("GET %s: the publisher reads %d %v, the author %d %v; want the same answer", read, ofPublisher.status, ofPublisher.body, ofAuthor.status, ofAuthor.body)
		}
	}
	if after, sent := s.count("platformoperations.audit_record"), s.count("bento.outbox"); after != records || sent != events {
		t.Errorf("the reads left %d audit records and %d events", after-records, sent-events)
	}

	// What it read is what a release rests on: the revision and its check.
	listed, _ := s.page(first.revision+"/check-runs", "publisher")
	if len(listed) != 1 || listed[0]["check_run_id"] != first.check {
		t.Fatalf("the publisher lists the checks %v, want the one of the revision", listed)
	}
	s.require(s.prepare(last(first.revision), first.check, "1.0.0", "publisher"), http.StatusCreated, "")

	// It writes no revision and runs no check. Each refusal is on record as
	// a refusal of what was asked.
	s.expect("a publisher writes no revision and runs no check", "publisher", []exchange{
		{http.MethodPost, s.revisions, uploadRevision("openapi.yaml", revisionSource+"# by the publisher\n"), http.StatusForbidden, "FORBIDDEN"},
		{http.MethodPost, draft + "/check-runs", "", http.StatusForbidden, "FORBIDDEN"},
	})
	refusals := s.recorded(func(row audited) bool { return row.Subject == "publisher" && row.Outcome != "SUCCEEDED" })
	for action, want := range map[string]int{"revision.create": 1, "revision.check": 1} {
		if got := s.on("publisher", action, "DENY-03", false); got != want {
			t.Errorf("%d refusals of %s are on record, want %d: %v", got, action, want, refusals)
		}
	}
	if occurrences(refusals) != 2 || s.count("contractregistry.contract_revision") != 2 || s.count("policyreview.check_run") != 1 ||
		s.count("policyreview.review_request") != 1 || s.count("policyreview.review_conclusion") != 1 {
		t.Errorf("the refusals are on record as %v; %d revisions, %d checks, %d reviews and %d conclusions are stored",
			refusals, s.count("contractregistry.contract_revision"), s.count("policyreview.check_run"),
			s.count("policyreview.review_request"), s.count("policyreview.review_conclusion"))
	}

	// The grant counts where it was made. A publisher of another workspace
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

	// The grant is read with every request. It was all the publisher held
	// in the product, so without it the product is one it does not know.
	s.revoke(membership, "owner")
	for _, read := range reads {
		if got := s.call(http.MethodGet, read, "publisher", ""); got.status != http.StatusNotFound || got.code() != "PRODUCT_NOT_FOUND" {
			t.Errorf("GET %s once the grant is revoked: %d %v, want 404 PRODUCT_NOT_FOUND", read, got.status, got.body)
		}
	}
}
