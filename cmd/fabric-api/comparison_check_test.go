package main

import (
	"net/http"
	"reflect"
	"testing"
)

func TestCheckRunsKeepTheChosenBaselineAndItsCompatibilityEvidence(t *testing.T) {
	s := checksFor(t)
	_, original := s.checkPath(checkSource)
	_, compatible := s.checkPath("openapi: 3.1.2\ninfo: {title: Shipments, version: previous}\nsecurity: []\npaths: {}\n")
	path, candidate := s.checkPath("openapi: 3.1.2\ninfo: {title: Shipments, version: current}\nsecurity: []\npaths: {}\n")
	withBaseline := func(id string) string { return path + "?baseline_revision_id=" + id }
	expectedHistory := map[string]any{}
	for _, tc := range []struct {
		baseline       answer
		classification string
		ruleStatus     string
	}{
		{original, "BREAKING", "FAIL"},
		{compatible, "COMPATIBLE", "PASS"},
	} {
		endpoint := withBaseline(tc.baseline.id("revision_id"))
		run := s.call(http.MethodPost, endpoint, "author", "")
		if run.status != http.StatusCreated {
			t.Fatalf("record a %s comparison: %d %v", tc.classification, run.status, run.body)
		}
		detail, ok := run.body["compatibility"].(map[string]any)
		if !ok || detail["classification"] != tc.classification {
			t.Fatalf("comparison of the recorded pair: %v", run.body)
		}
		wantBaseline := map[string]any{"revision_id": tc.baseline.id("revision_id"), "input_digest": tc.baseline.body["ref_closure_digest"]}
		wantCandidate := map[string]any{"revision_id": candidate.id("revision_id"), "input_digest": candidate.body["ref_closure_digest"]}
		if !reflect.DeepEqual(detail["baseline"], wantBaseline) || !reflect.DeepEqual(detail["candidate"], wantCandidate) {
			t.Fatalf("comparison digests and identities: %v", detail)
		}
		var outcome map[string]any
		for _, raw := range run.body["outcomes"].([]any) {
			item := raw.(map[string]any)
			if item["rule_id"] == "F04-GOV-013" {
				outcome = item
			}
		}
		if outcome["status"] != tc.ruleStatus || !reflect.DeepEqual(outcome["tool"], detail["tool"]) {
			t.Fatalf("compatibility rule outcome: %v, comparison %v", outcome, detail)
		}
		if tc.classification == "BREAKING" {
			changes := []any{map[string]any{"classification": "BREAKING", "code": "PATH_REMOVED", "pointer": "/paths/~1shipments"}}
			if !reflect.DeepEqual(detail["changes"], changes) {
				t.Fatalf("breaking comparison changes: %v", detail["changes"])
			}
		}
		retry := s.call(http.MethodPost, endpoint, "author", "")
		get := s.call(http.MethodGet, run.header.Get("Location"), "author", "")
		if retry.status != http.StatusOK || get.status != http.StatusOK ||
			!reflect.DeepEqual(retry.body, run.body) || !reflect.DeepEqual(get.body, run.body) {
			t.Fatalf("retry or read back: retry %d %v, detail %d %v", retry.status, retry.body, get.status, get.body)
		}
		overview := run.body["check_run"].(map[string]any)
		expectedHistory[overview["check_run_id"].(string)] = overview["compatibility"]
	}
	uncompared := s.call(http.MethodPost, path, "author", "")
	if uncompared.status != http.StatusCreated || uncompared.body["compatibility"] != nil {
		t.Fatalf("check without a baseline: %d %v", uncompared.status, uncompared.body)
	}
	overview := uncompared.body["check_run"].(map[string]any)
	expectedHistory[overview["check_run_id"].(string)] = nil
	page := s.call(http.MethodGet, path, "author", "")
	if page.status != http.StatusOK || len(page.body["items"].([]any)) != 3 || len(expectedHistory) != 3 {
		t.Fatalf("the checks of the same candidate: %d %v", page.status, page.body)
	}
	for _, item := range page.body["items"].([]any) {
		summary := item.(map[string]any)
		id := summary["check_run_id"].(string)
		want, exists := expectedHistory[id]
		if !exists || !reflect.DeepEqual(summary["compatibility"], want) {
			t.Fatalf("unexpected history entry %v", summary)
		}
	}

	anotherAsset := s.revisionAsset(s.product, s.workspace, "maintainer", "other-shipping-api", "HTTP")
	outside := s.call(http.MethodPost, anotherAsset, "author", uploadRevision("openapi.yaml", checkSource))
	if outside.status != http.StatusCreated {
		t.Fatalf("prepare another asset's revision: %d %v", outside.status, outside.body)
	}
	s.expect("a check's baseline must be a different revision of its asset", "author", []exchange{
		{http.MethodPost, withBaseline(candidate.id("revision_id")), "", http.StatusBadRequest, "REQUEST_INVALID"},
		{http.MethodPost, withBaseline(outside.id("revision_id")), "", http.StatusNotFound, "REVISION_NOT_FOUND"},
	})
}
