package main

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"go.idp.xyz/idp-api-fabric/internal/platform/db"
	"go.idp.xyz/idp-api-fabric/internal/platform/ids"
	checkdb "go.idp.xyz/idp-api-fabric/internal/policyreview/adapters/postgres"
	"go.idp.xyz/idp-api-fabric/internal/policyreview/domain"
)

// recordAnotherCheck represents the same revision checked by a newer tool.
func recordAnotherCheck(t *testing.T, s checkFixture, revision domain.RevisionRef, firstID string) string {
	t.Helper()
	database, err := db.Open(t.Context(), s.database.Application)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	store := checkdb.NewCheckRuns(database.Framework)
	run, found, err := store.Find(t.Context(), revision, firstID)
	if err != nil || !found {
		t.Fatalf("read the first check: found %v, error %v", found, err)
	}
	run.ID = ids.NewUUID()
	run.Tools[0].Version += "-next-test"
	run.StartedAt = run.StartedAt.Add(time.Second)
	run.CompletedAt = run.CompletedAt.Add(time.Second)
	for i, outcome := range run.Outcomes {
		if outcome.Tool.Name != "" {
			run.Outcomes[i].Tool = run.Tools[0]
		}
	}
	if err := database.WithinTransaction(t.Context(), func(ctx context.Context) error {
		return store.Record(ctx, run)
	}); err != nil {
		t.Fatal(err)
	}
	return run.ID
}

func TestARevisionCheckHistoryRechecksScope(t *testing.T) {
	s := checksFor(t)
	path, revision := s.checkPath(revisionSource)
	if items, next := s.page(path, "author"); len(items) != 0 || next != "" {
		t.Fatalf("new revision history: %v, token %q", items, next)
	}
	created := s.call(http.MethodPost, path, "author", "")
	if created.status != http.StatusCreated {
		t.Fatalf("execute the check: %d %v", created.status, created.body)
	}
	overview := created.body["check_run"].(map[string]any)
	firstID := overview["check_run_id"].(string)
	detail := created.header.Get("Location")
	if detail != path+"/"+firstID || overview["status"] != "UNKNOWN" {
		t.Fatalf("recorded check: %v, location %q", overview, detail)
	}
	got := s.call(http.MethodGet, detail, "author", "")
	if got.status != http.StatusOK || got.header.Get("Cache-Control") != "no-store" || !reflect.DeepEqual(got.body, created.body) {
		t.Fatalf("check detail: %d %v", got.status, got.body)
	}
	secondID := recordAnotherCheck(t, s, domain.RevisionRef{
		ProductID: s.product, WorkspaceID: s.workspace, AssetID: revision.id("api_asset_id"), RevisionID: revision.id("revision_id"),
	}, firstID)
	first, next := s.page(path+"?page_size=1", "author")
	if len(first) != 1 || !reflect.DeepEqual(first[0], overview) || next == "" {
		t.Fatalf("first page: %v, token %q", first, next)
	}
	second, tail := s.page(path+"?page_size=1&page_token="+next, "author")
	if len(second) != 1 || second[0]["check_run_id"] != secondID || tail != "" {
		t.Fatalf("last page: %v, token %q", second, tail)
	}

	other, _ := s.checkPath(revisionSource + "# next draft\n")
	s.expect("the read belongs to the revision in the URL", "author", []exchange{
		{http.MethodGet, other + "/" + firstID, "", http.StatusNotFound, "CHECK_RUN_NOT_FOUND"},
		{http.MethodGet, other + "?page_token=" + next, "", http.StatusBadRequest, "PAGE_TOKEN_INVALID"},
	})

	s.register("platform-admin", "parcel", "parcel-owner")
	s.expect("an unrelated product cannot see check evidence", "parcel-owner", []exchange{
		{http.MethodGet, detail, "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
		{http.MethodGet, path, "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	})
	s.revoke(s.membership, "owner")
	s.expect("an existing page token conveys no authorization", "author", []exchange{
		{http.MethodGet, detail, "", http.StatusForbidden, "FORBIDDEN"},
		{http.MethodGet, path + "?page_token=" + next, "", http.StatusForbidden, "FORBIDDEN"},
	})
}
