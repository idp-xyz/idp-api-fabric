package main

import (
	"net/http"
	"reflect"
	"testing"
)

func TestAnAuthorComparesTwoRecordedRevisionsOfTheSameAsset(t *testing.T) {
	s := checksFor(t)
	_, baseline := s.checkPath(checkSource)
	_, candidate := s.checkPath("openapi: 3.1.2\ninfo: {title: Shipments, version: v2}\nsecurity: []\npaths: {}\n")
	path := s.revisions + "/" + candidate.id("revision_id") + "/compatibility"
	query := "?baseline_revision_id=" + baseline.id("revision_id")
	compare := s.call(http.MethodGet, path+query, "author", "")
	if compare.status != http.StatusOK || compare.header.Get("Cache-Control") != "no-store" ||
		compare.body["classification"] != "BREAKING" || compare.body["truncated"] != false {
		t.Fatalf("compare recorded revisions: status %d, result %v", compare.status, compare.body)
	}
	for name, revision := range map[string]answer{"baseline": baseline, "candidate": candidate} {
		want := map[string]any{"revision_id": revision.id("revision_id"), "input_digest": revision.body["ref_closure_digest"]}
		if !reflect.DeepEqual(compare.body[name], want) {
			t.Errorf("%s evidence = %v, want %v", name, compare.body[name], want)
		}
	}
	changes := []any{map[string]any{"classification": "BREAKING", "code": "PATH_REMOVED", "pointer": "/paths/~1shipments"}}
	tool := map[string]any{"name": "fabric-openapi-compatibility", "version": "1"}
	if !reflect.DeepEqual(compare.body["changes"], changes) || !reflect.DeepEqual(compare.body["tool"], tool) {
		t.Fatalf("compare evidence: changes %v, tool %v", compare.body["changes"], compare.body["tool"])
	}
	reversed := s.call(http.MethodGet, s.revisions+"/"+baseline.id("revision_id")+"/compatibility?baseline_revision_id="+candidate.id("revision_id"), "author", "")
	if reversed.status != http.StatusOK || reversed.body["classification"] != "COMPATIBLE" || s.count("policyreview.check_run") != 0 {
		t.Fatalf("reverse the comparison: status %d, result %v", reversed.status, reversed.body)
	}

	other := s.revisionAsset(s.product, s.workspace, "maintainer", "another-api", "HTTP")
	otherSource := s.call(http.MethodPost, other, "author", uploadRevision("openapi.yaml", checkSource))
	if otherSource.status != http.StatusCreated {
		t.Fatalf("record another asset's revision: %d %v", otherSource.status, otherSource.body)
	}
	s.expect("a baseline must be a revision of the same asset", "author", []exchange{
		{http.MethodGet, path, "", http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodGet, path + "?baseline_revision_id=" + otherSource.id("revision_id"), "", http.StatusNotFound, "REVISION_NOT_FOUND"},
	})
	if owner := s.call(http.MethodGet, path+query, "owner", ""); owner.status != http.StatusOK || !reflect.DeepEqual(owner.body, compare.body) {
		t.Errorf("the owner's comparison: status %d, result %v", owner.status, owner.body)
	}
	s.revoke(s.membership, "owner")
	s.expect("the author was revoked", "author", []exchange{
		{http.MethodGet, path + query, "", http.StatusForbidden, "FORBIDDEN"},
	})
}
