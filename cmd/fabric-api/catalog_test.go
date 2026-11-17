package main

import (
	"fmt"
	"net/http"
	"testing"

	"go.idp.xyz/idp-api-fabric/internal/platform/db/pgtest"
)

func assetRegistration(workspace, key, name, kind string) string {
	return fmt.Sprintf(`{"workspace_id":%q,"asset_key":%q,"display_name":%q,"domain_key":"shipping","api_kind":%q}`,
		workspace, key, name, kind)
}

func TestTheCatalogRegistersAndReadsScopedAssets(t *testing.T) {
	s := start(t, "platform-admin")
	cargo := s.register("platform-admin", "cargopai", "alice")
	parcel := s.register("platform-admin", "parcel", "bob")
	engineering := s.createWorkspace(cargo, "alice", "engineering")
	operations := s.createWorkspace(cargo, "alice", "operations")
	parcelWorkspace := s.createWorkspace(parcel, "bob", "engineering")
	s.join(cargo, engineering, "alice", "dana", "workspace_maintainer")
	s.join(cargo, operations, "alice", "dana", "workspace_maintainer")
	carolsMembership := s.join(cargo, engineering, "alice", "carol", "internal_author")
	s.join(parcel, parcelWorkspace, "bob", "bob", "workspace_maintainer")

	path := "/v1/products/" + cargo + "/assets"
	otherPath := "/v1/products/" + parcel + "/assets"
	registration := assetRegistration(engineering, "shipment-api", "Shipment API", "HTTP")
	s.expect("the owning product role does not replace a maintainer membership", "alice", []exchange{
		{http.MethodPost, path, registration, http.StatusForbidden, "FORBIDDEN"},
	})
	s.expect("an author cannot register an asset", "carol", []exchange{
		{http.MethodPost, path, registration, http.StatusForbidden, "FORBIDDEN"},
	})
	s.expect("a Workspace under another product is missing", "dana", []exchange{
		{http.MethodPost, path, assetRegistration(parcelWorkspace, "foreign-api", "Foreign API", "HTTP"), http.StatusNotFound, "WORKSPACE_NOT_FOUND"},
	})
	register := func(path, subject, body string) answer {
		t.Helper()
		created := s.call(http.MethodPost, path, subject, body)
		if id := created.id("api_asset_id"); created.status != http.StatusCreated || id == "" ||
			created.header.Get("Location") != path+"/"+id || created.header.Get("Cache-Control") != "no-store" ||
			created.body["lifecycle"] != "EXPERIMENTAL" {
			t.Fatalf("asset registration: status %d, headers %v, body %v", created.status, created.header, created.body)
		}
		return created
	}
	publicToWorkspace := register(path, "dana", registration)
	otherProduct := register(otherPath, "bob", assetRegistration(parcelWorkspace, "shipment-api", "Shipment API", "HTTP"))
	if publicToWorkspace.id("api_asset_id") == otherProduct.id("api_asset_id") {
		t.Fatal("the same key in two products resolved to one identity")
	}
	s.expect("an asset key is reserved for the whole product", "dana", []exchange{
		{http.MethodPost, path, assetRegistration(operations, "shipment-api", "Duplicate API", "HTTP"), http.StatusConflict, "API_ASSET_KEY_TAKEN"},
	})
	hidden := register(path, "dana", assetRegistration(operations, "shipment-quotes", "Shipment confidential quotes", "HTTP"))
	events := register(path, "dana", assetRegistration(engineering, "shipment-events", "Shipment events", "EVENT"))

	visibleAt, hiddenAt := publicToWorkspace.header.Get("Location"), hidden.header.Get("Location")
	read := s.call(http.MethodGet, visibleAt, "carol", "")
	if read.status != http.StatusOK || read.id("product_id") != cargo || read.id("workspace_id") != engineering ||
		read.id("api_asset_id") != publicToWorkspace.id("api_asset_id") ||
		read.body["domain_key"] != "shipping" || read.body["api_kind"] != "HTTP" {
		t.Fatalf("read registered asset: status %d, body %v", read.status, read.body)
	}
	const absentID = "00000000-0000-4000-8000-000000000000"
	withheld := s.call(http.MethodGet, hiddenAt, "carol", "")
	absent := s.call(http.MethodGet, path+"/"+absentID, "carol", "")
	if withheld.status != http.StatusNotFound || withheld.code() != "API_ASSET_NOT_FOUND" || !sameAnswer(withheld, absent) {
		t.Fatalf("withheld asset %d %v differs from absent asset %d %v", withheld.status, withheld.body, absent.status, absent.body)
	}
	s.expect("platform administration conveys no catalog access", "platform-admin", []exchange{
		{http.MethodGet, visibleAt, "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
		{http.MethodGet, path, "", http.StatusForbidden, "FORBIDDEN"},
	})
	s.expect("anyone outside the product cannot discover assets", "bob", []exchange{
		{http.MethodGet, visibleAt, "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
		{http.MethodGet, path + "?q=shipment", "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
		{http.MethodGet, otherPath + "/" + publicToWorkspace.id("api_asset_id"), "", http.StatusNotFound, "API_ASSET_NOT_FOUND"},
	})

	first, next := s.page(path+"?q=SHIPMENT&page_size=1", "carol")
	if len(first) != 1 || first[0]["api_asset_id"] != publicToWorkspace.id("api_asset_id") || next == "" {
		t.Fatalf("first visible page: %v, token %q", first, next)
	}
	second, last := s.page(path+"?q=SHIPMENT&page_size=1&page_token="+next, "carol")
	if len(second) != 1 || second[0]["api_asset_id"] != events.id("api_asset_id") || last != "" {
		t.Fatalf("second visible page: %v, token %q", second, last)
	}
	for _, filter := range []string{"?workspace_id=" + operations, "?q=confidential", "?q=%25"} {
		if items, token := s.page(path+filter, "carol"); len(items) != 0 || token != "" {
			t.Fatalf("filtered read %s leaked items %v or token %q", filter, items, token)
		}
	}
	if items, token := s.page(path+"?api_kind=EVENT&domain_key=shipping", "alice"); len(items) != 1 ||
		items[0]["api_asset_id"] != events.id("api_asset_id") || token != "" {
		t.Fatalf("filter by type and domain: %v, token %q", items, token)
	}
	if items, _ := s.page(path, "alice"); len(items) != 3 {
		t.Fatalf("product owner saw %d assets, want 3", len(items))
	}
	s.revoke(carolsMembership, "alice")
	s.expect("a cursor conveys no access after revocation", "carol", []exchange{
		{http.MethodGet, path + "?page_size=1&page_token=" + next, "", http.StatusNotFound, "PRODUCT_NOT_FOUND"},
	})

	var persisted, outbox int
	if err := s.owner.QueryRow(t.Context(), `SELECT count(*) FROM apicatalog.api_asset WHERE product_id = $1`, cargo).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if err := s.owner.QueryRow(t.Context(), `SELECT count(*) FROM bento.outbox
		WHERE event_type = 'fabric.apicatalog.asset.registered' AND scope = $1
		  AND payload->>'product_id' = $1 AND payload ? 'workspace_id'`, cargo).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	trail := s.recorded(func(row audited) bool {
		return row.ProductID == cargo && row.Action == "asset.create" && row.Outcome == "SUCCEEDED" && row.Rule == "AC-11"
	})
	if persisted != 3 || outbox != 3 || len(trail) != 3 {
		t.Fatalf("asset rows, events and successful audit facts = %d, %d, %d, want 3 each", persisted, outbox, len(trail))
	}
}

func TestACatalogRegistrationWaitsForMembershipRevocation(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "alice")
	workspace := s.createWorkspace(product, "alice", "engineering")
	membership := s.join(product, workspace, "alice", "dana", "workspace_maintainer")
	s.join(product, workspace, "alice", "dana", "internal_author")

	ctx := t.Context()
	conn := pgtest.Connect(t, s.database.Migrator)
	revoking, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = revoking.Rollback(ctx) }()
	updated, err := revoking.Exec(ctx, `UPDATE productgovernance.workspace_membership
		SET revoked_at = greatest(granted_at, clock_timestamp()), revoked_by_issuer = $2,
		    revoked_by_subject = 'alice', membership_version = membership_version + 1
		WHERE membership_id = $1 AND revoked_at IS NULL`, last(membership), s.issuer.URL())
	if err != nil || updated.RowsAffected() != 1 {
		t.Fatalf("stage a maintainer revocation: %v (%v)", updated, err)
	}
	path, token := "/v1/products/"+product+"/assets", s.token("dana")
	registration := assetRegistration(workspace, "shipment-api", "Shipment API", "HTTP")
	waiting := make(chan answer, 1)
	go func() {
		defer close(waiting)
		waiting <- s.callWith(http.MethodPost, path, token, registration)
	}()
	s.waiting(1)
	if err := revoking.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	got := <-waiting
	if got.status != http.StatusForbidden || got.code() != "FORBIDDEN" {
		t.Fatalf("register after losing maintainer membership: status %d, body %v", got.status, got.body)
	}
	if items, _ := s.page(path, "alice"); len(items) != 0 {
		t.Fatalf("the refused registration left assets %v", items)
	}
}

func TestAnOutboxFailureRollsBackCatalogRegistration(t *testing.T) {
	s := start(t, "platform-admin")
	product := s.register("platform-admin", "cargopai", "alice")
	workspace := s.createWorkspace(product, "alice", "engineering")
	s.join(product, workspace, "alice", "alice", "workspace_maintainer")
	if _, err := s.owner.Exec(t.Context(), `ALTER TABLE bento.outbox
		ADD CONSTRAINT test_reject_catalog_events CHECK (event_type <> 'fabric.apicatalog.asset.registered')`); err != nil {
		t.Fatal(err)
	}
	path := "/v1/products/" + product + "/assets"
	s.expect("the event cannot be stored", "alice", []exchange{
		{http.MethodPost, path, assetRegistration(workspace, "shipment-api", "Shipment API", "HTTP"), http.StatusInternalServerError, "INTERNAL_ERROR"},
	})
	if items, _ := s.page(path, "alice"); len(items) != 0 {
		t.Fatalf("a failed transaction left assets %v", items)
	}
	if trail := s.recorded(func(row audited) bool { return row.Action == "asset.create" && row.Outcome == "SUCCEEDED" }); len(trail) != 0 {
		t.Fatalf("a failed transaction left success audit facts %v", trail)
	}
}
