package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAsyncAPIContractCanBeCheckedReleasedAndPublished(t *testing.T) {
	const asyncSource = `asyncapi: '3.1.0'
info: {title: Shipment Events, version: '1'}
servers:
  broker:
    host: broker.example.test:9092
    protocol: kafka
    security: [{type: userPassword}]
channels:
  arrivals:
    address: shipment.arrived
    messages:
      shipmentArrived:
        payload: {type: string}
operations:
  observe:
    action: receive
    channel: {$ref: '#/channels/arrivals'}
`
	s := publicationFor(t)
	const entrypoint = "asyncapi.yaml"
	body := uploadRevision(entrypoint, asyncSource)
	s.require(s.call(http.MethodPost, s.revisions, "author", body), http.StatusUnprocessableEntity, "UNSUPPORTED_SPEC_VERSION")
	s.revisions = s.revisionAsset(s.product, s.workspace, "maintainer", "shipment-events", "EVENT")
	s.asset = strings.TrimSuffix(s.revisions, "/revisions")
	s.require(s.call(http.MethodPost, s.revisions, "author", uploadRevision(entrypoint,
		strings.Replace(asyncSource, "3.1.0", "3.0.0", 1))), http.StatusUnprocessableEntity, "UNSUPPORTED_SPEC_VERSION")

	revisionID, checkID := s.checkedFrom(body)
	revision := s.call(http.MethodGet, s.revisions+"/"+revisionID, "author", "")
	s.require(revision, http.StatusOK, "")
	if revision.body["specification"] != "ASYNCAPI" || revision.body["spec_version"] != "3.1.0" {
		t.Fatalf("unexpected revision specification: %v", revision.body)
	}
	release := s.prepare(revisionID, checkID, "1.0.0", "publisher")
	s.require(release, http.StatusCreated, "")
	const format = "fabric-asyncapi-source/v1"
	reread := s.call(http.MethodGet, release.header.Get("Location"), "author", "")
	s.require(reread, http.StatusOK, "")
	if release.body["artifact_format"] != format || reread.body["artifact_format"] != format {
		t.Fatalf("unexpected release formats: %v, %v", release.body, reread.body)
	}

	target, _ := s.target(internalSandbox, "engineering")
	slot := assetSlot(target, s.asset)
	s.require(s.change(slot, "publisher", "ACTIVATE", 0, release.id("release_id"), "", "first"), http.StatusOK, "")
	artifact := s.download(slot+"/artifact", "author")
	var content bundle
	if err := json.Unmarshal(artifact.body, &content); artifact.status != http.StatusOK || err != nil ||
		artifact.header.Get("X-Artifact-Digest") != release.id("artifact_digest") ||
		sha256Of(artifact.body) != release.id("artifact_digest") ||
		content.Format != format || content.Entrypoint != entrypoint || len(content.Files) != 1 ||
		content.Files[0].Path != entrypoint || string(content.Files[0].Content) != asyncSource {
		t.Fatalf("unexpected artifact: %d %s (%v)", artifact.status, artifact.body, err)
	}

	candidate := s.call(http.MethodPost, s.revisions, "author",
		uploadRevision(entrypoint, strings.Replace(asyncSource, "action: receive", "action: send", 1)))
	s.require(candidate, http.StatusCreated, "")
	candidateID := candidate.id("revision_id")
	checked := s.call(http.MethodPost,
		s.revisions+"/"+candidateID+"/check-runs?baseline_revision_id="+revisionID, "author", "")
	s.require(checked, http.StatusCreated, "")
	if comparison := part(checked, "compatibility"); comparison["classification"] != "BREAKING" {
		t.Fatalf("a changed event direction was classified as %v", comparison)
	}
	checkID = part(checked, "check_run")["check_run_id"].(string)
	s.require(s.prepare(candidateID, checkID, "2.0.0", "publisher"), http.StatusConflict, "WAIVER_REQUIRED")
}
