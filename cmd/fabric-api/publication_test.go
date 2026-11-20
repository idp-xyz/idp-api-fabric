package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

var internalSandbox = [2]string{"internal", "sandbox"}

type releaseFixture struct {
	*reviewFixture
	asset string
}

func publicationFor(t *testing.T) *releaseFixture {
	t.Helper()
	s := reviewsFor(t)
	s.join(s.product, s.workspace, "owner", "publisher", "internal_publisher")
	return &releaseFixture{reviewFixture: s, asset: strings.TrimSuffix(s.revisions, "/revisions")}
}

func (s *releaseFixture) approve(source, baselineID string, scopes ...[2]string) (revisionID, reviewID string) {
	s.t.Helper()
	imported := s.call(http.MethodPost, s.revisions, "author", uploadRevision("openapi.yaml", source))
	s.require(imported, http.StatusCreated, "")
	revisionID = imported.id("revision_id")
	checkPath := s.revisions + "/" + revisionID + "/check-runs"
	if baselineID != "" {
		checkPath += "?baseline_revision_id=" + baselineID
	}
	checked := s.call(http.MethodPost, checkPath, "author", "")
	s.require(checked, http.StatusCreated, "")
	checkID := checked.body["check_run"].(map[string]any)["check_run_id"].(string)
	review := s.call(http.MethodPost, s.revisions+"/"+revisionID+"/reviews", "author", asking(checkID, scopes...))
	s.require(review, http.StatusCreated, "")
	reviewID = review.id("review_id")
	approved := s.call(http.MethodPost, review.header.Get("Location")+"/decision", "reviewer",
		deciding("APPROVED", "", time.Now().Add(time.Hour)))
	s.require(approved, http.StatusOK, "")
	return revisionID, reviewID
}

func (s *releaseFixture) prepare(revision, review, version, caller string) answer {
	s.t.Helper()
	return s.call(http.MethodPost, s.asset+"/releases", caller, jsonBody(map[string]string{
		"revision_id": revision, "review_id": review, "version": version,
	}))
}

func (s *releaseFixture) require(got answer, status int, code string) {
	s.t.Helper()
	if got.status != status || got.code() != code {
		s.t.Fatalf("got %d %v, want status %d error code %q\n%s", got.status, got.body, status, code, s.log.String())
	}
}

func jsonBody(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
