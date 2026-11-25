package main

import (
	"net/http"
	"reflect"
	"testing"
)

func TestContractRevisionHistoryIsScopedToTheAssetAndCurrentAuthor(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "owner")
	s.register("platform-admin", "parcel", "parcel-owner")
	workspace := s.createWorkspace(product, "owner", "engineering")
	s.join(product, workspace, "owner", "maintainer", "workspace_maintainer")
	s.join(product, workspace, "owner", "author", "internal_reader")
	membership := s.join(product, workspace, "owner", "author", "internal_author")
	path := s.revisionAsset(product, workspace, "maintainer", "shipping-api", "HTTP")
	other := s.revisionAsset(product, workspace, "maintainer", "tracking-api", "HTTP")

	if items, token := s.page(path, "author"); len(items) != 0 || token != "" {
		t.Fatalf("new asset history: %+v, %q", items, token)
	}
	add := func(content string) answer {
		t.Helper()
		created := s.call(http.MethodPost, path, "author", uploadRevision("openapi.yaml", content))
		if created.status != http.StatusCreated || created.header.Get("Location") != path+"/"+created.id("revision_id") {
			t.Fatalf("imported revision: %d %v %v", created.status, created.body, created.header)
		}
		return created
	}
	first := add(revisionSource)
	second := add(revisionSource + "# revised\n")
	detail := first.header.Get("Location")
	got := s.call(http.MethodGet, detail, "author", "")
	if got.status != http.StatusOK || got.header.Get("Cache-Control") != "no-store" || !reflect.DeepEqual(got.body, first.body) {
		t.Fatalf("revision detail: %d %v, want %v", got.status, got.body, first.body)
	}

	items, cursor := s.page(path+"?page_size=1", "author")
	if len(items) != 1 || !reflect.DeepEqual(items[0], first.body) || cursor == "" {
		t.Fatalf("first page: %v, %q", items, cursor)
	}
	items, tail := s.page(path+"?page_size=1&page_token="+cursor, "author")
	if len(items) != 1 || !reflect.DeepEqual(items[0], second.body) || tail != "" {
		t.Fatalf("second page: %v, %q", items, tail)
	}
	s.expect("history is scoped to its asset", "author", []exchange{
		{http.MethodGet, other + "/" + first.id("revision_id"), "", http.StatusNotFound, "REVISION_NOT_FOUND"},
		{http.MethodGet, other + "?page_token=" + cursor, "", http.StatusBadRequest, "PAGE_TOKEN_INVALID"},
	})
	s.expect("unrelated products cannot see source history", "parcel-owner", []exchange{
		{http.MethodGet, detail, "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	})
	s.revoke(membership, "owner")
	s.expect("a visible asset and an issued cursor do not grant revision access", "author", []exchange{
		{http.MethodGet, detail, "", http.StatusForbidden, "FORBIDDEN"},
		{http.MethodGet, path + "?page_token=" + cursor, "", http.StatusForbidden, "FORBIDDEN"},
	})
}
